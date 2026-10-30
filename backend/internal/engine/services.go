package engine

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/robfig/cron/v3"
)

// EngineServices holds shared infrastructure that some nodes need at runtime:
// the HTTP mux (for http_trigger), the cron scheduler (for cron_trigger),
// etc. See docs/engine-v1.md §12.
//
// Nodes only register on these services; whoever runs the engine serves the
// HTTP mux and starts/stops the cron scheduler.
type EngineServices struct {
	HTTP *HTTPMux
	Cron *cron.Cron
}

// NewEngineServices returns services with every field initialized. The cron
// scheduler is created stopped.
func NewEngineServices() *EngineServices {
	return &EngineServices{HTTP: NewHTTPMux(), Cron: cron.New()}
}

// HTTPMux is the HTTP handler shared by every http_trigger in a flow. Routes
// are keyed by exact method + path. Unlike http.ServeMux it supports removing
// routes, so a trigger can deregister itself on Stop.
//
// The mux does not listen itself; whoever runs the engine mounts it in an
// http.Server. Safe for concurrent use.
type HTTPMux struct {
	mu sync.RWMutex
	// routes maps path → method → handler.
	routes map[string]map[string]http.Handler
}

// NewHTTPMux returns an empty HTTPMux.
func NewHTTPMux() *HTTPMux {
	return &HTTPMux{routes: make(map[string]map[string]http.Handler)}
}

// Handle registers h for method + path. The method is case-insensitive.
// Registering an already-taken method + path is an error, since two triggers
// competing for one route is a flow bug.
func (m *HTTPMux) Handle(method, path string, h http.Handler) error {
	method = strings.ToUpper(method)
	m.mu.Lock()
	defer m.mu.Unlock()
	byMethod, ok := m.routes[path]
	if !ok {
		byMethod = make(map[string]http.Handler)
		m.routes[path] = byMethod
	}
	if _, taken := byMethod[method]; taken {
		return fmt.Errorf("route %s %s is already registered", method, path)
	}
	byMethod[method] = h
	return nil
}

// Remove deregisters method + path. Removing an unknown route is a no-op.
func (m *HTTPMux) Remove(method, path string) {
	method = strings.ToUpper(method)
	m.mu.Lock()
	defer m.mu.Unlock()
	byMethod, ok := m.routes[path]
	if !ok {
		return
	}
	delete(byMethod, method)
	if len(byMethod) == 0 {
		delete(m.routes, path)
	}
}

// ServeHTTP dispatches to the handler registered for the request's method and
// path: 404 for an unknown path, 405 (with an Allow header) for a known path
// with the wrong method.
func (m *HTTPMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	byMethod, ok := m.routes[r.URL.Path]
	var h http.Handler
	var allowed []string
	if ok {
		h = byMethod[r.Method]
		if h == nil {
			for method := range byMethod {
				allowed = append(allowed, method)
			}
		}
	}
	m.mu.RUnlock()

	switch {
	case !ok:
		http.NotFound(w, r)
	case h == nil:
		slices.Sort(allowed)
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	default:
		h.ServeHTTP(w, r)
	}
}
