// Package health serves the liveness and readiness probes on the flows
// listener: /_flowforge/health and /_flowforge/ready (docs/prod-v1.md §4).
package health

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// The probe paths, under the prefix reserved for FlowForge.
const (
	HealthPath = engine.ReservedPathPrefix + "health"
	ReadyPath  = engine.ReservedPathPrefix + "ready"
)

// Counts is how many deployed flows run, and how many failed to start.
type Counts struct {
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

// phase is where the process is in its life.
type phase int32

const (
	starting phase = iota
	ready
	stopping
)

// Probes answers the health and readiness probes. It starts out starting:
// call SetReady once every deployed flow has started or failed, and
// SetStopping when shutdown begins. Safe for concurrent use.
type Probes struct {
	phase  atomic.Int32
	counts func() Counts
}

// New returns Probes that report counts once ready.
func New(counts func() Counts) *Probes {
	return &Probes{counts: counts}
}

// SetReady marks the boot done.
func (p *Probes) SetReady() { p.phase.CompareAndSwap(int32(starting), int32(ready)) }

// SetStopping marks the shutdown begun. It is final.
func (p *Probes) SetStopping() { p.phase.Store(int32(stopping)) }

// Starting reports whether the boot is still going on.
func (p *Probes) Starting() bool { return phase(p.phase.Load()) == starting }

// Handler serves the probes in front of next: GET (or HEAD) on the probe
// paths, 404 for anything else under the reserved prefix, everything else
// goes to next.
func (p *Probes) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, engine.ReservedPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != HealthPath && r.URL.Path != ReadyPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == HealthPath {
			// Liveness: answering at all is the answer.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		switch phase(p.phase.Load()) {
		case starting:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "starting"})
		case stopping:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "stopping"})
		default:
			writeJSON(w, http.StatusOK, struct {
				Status string `json:"status"`
				Flows  Counts `json:"flows"`
			}{"ready", p.counts()})
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
