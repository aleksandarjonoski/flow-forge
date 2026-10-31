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
// A server running many flows shares one route table between them: Owned
// returns a view of the mux that tags its routes with an owner (the flow ID),
// so a clash names the flow that holds the route.
//
// The mux does not listen itself; whoever runs the engine mounts it in an
// http.Server. Safe for concurrent use.
type HTTPMux struct {
	t     *routeTable
	owner string
}

type routeTable struct {
	mu sync.RWMutex
	// routes maps path → method → route.
	routes map[string]map[string]route
}

type route struct {
	h     http.Handler
	owner string
}

// NewHTTPMux returns an empty HTTPMux with no owner.
func NewHTTPMux() *HTTPMux {
	return &HTTPMux{t: &routeTable{routes: make(map[string]map[string]route)}}
}

// Owned returns a view of m that shares its routes and registers new ones as
// owner's. Serving either one serves every route.
func (m *HTTPMux) Owned(owner string) *HTTPMux {
	return &HTTPMux{t: m.t, owner: owner}
}

// Handle registers h for method + path. The method is case-insensitive.
// Registering an already-taken method + path is an error, since two triggers
// competing for one route is a flow bug; when another owner holds the route,
// the error names it.
func (m *HTTPMux) Handle(method, path string, h http.Handler) error {
	method = strings.ToUpper(method)
	m.t.mu.Lock()
	defer m.t.mu.Unlock()
	byMethod, ok := m.t.routes[path]
	if !ok {
		byMethod = make(map[string]route)
		m.t.routes[path] = byMethod
	}
	if taken, ok := byMethod[method]; ok {
		if taken.owner != "" && taken.owner != m.owner {
			return fmt.Errorf("route %s %s is used by flow %s", method, path, taken.owner)
		}
		return fmt.Errorf("route %s %s is already registered", method, path)
	}
	byMethod[method] = route{h: h, owner: m.owner}
	return nil
}

// Remove deregisters method + path if this view's owner holds it. Removing an
// unknown route, or another owner's, is a no-op.
func (m *HTTPMux) Remove(method, path string) {
	method = strings.ToUpper(method)
	m.t.mu.Lock()
	defer m.t.mu.Unlock()
	byMethod, ok := m.t.routes[path]
	if !ok || byMethod[method].owner != m.owner {
		return
	}
	delete(byMethod, method)
	if len(byMethod) == 0 {
		delete(m.t.routes, path)
	}
}

// ServeHTTP dispatches to the handler registered for the request's method and
// path: 404 for an unknown path, 405 (with an Allow header) for a known path
// with the wrong method.
func (m *HTTPMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.t.mu.RLock()
	byMethod, ok := m.t.routes[r.URL.Path]
	var h http.Handler
	var allowed []string
	if ok {
		h = byMethod[r.Method].h
		if h == nil {
			for method := range byMethod {
				allowed = append(allowed, method)
			}
		}
	}
	m.t.mu.RUnlock()

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
