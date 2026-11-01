package engine

import (
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

// ReservedPathPrefix is the path prefix FlowForge keeps for itself on the
// flows listener (health and readiness probes, docs/prod-v1.md §4); no
// http_trigger may use it.
const ReservedPathPrefix = "/_flowforge/"

// NewEngineServices returns services with every field initialized. The cron
// scheduler is created stopped.
func NewEngineServices() *EngineServices {
	return &EngineServices{HTTP: NewHTTPMux(), Cron: cron.New()}
}
