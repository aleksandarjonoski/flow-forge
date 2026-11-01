package engine

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// HTTPMux is the HTTP handler shared by every http_trigger in a flow. A route
// is a method and a path pattern whose segments are literals or parameters,
// "/orders/{id}" (docs/prod-v1.md §5). Unlike http.ServeMux it supports
// removing routes, so a trigger can deregister itself on Stop.
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
	mu   sync.RWMutex
	root *segmentNode
}

// segmentNode is one path segment in the route trie. Routes of the same
// shape (equal literals, parameters in the same positions) end at the same
// node.
type segmentNode struct {
	literals map[string]*segmentNode
	param    *segmentNode
	// routes maps method → route ending here.
	routes map[string]route
}

type route struct {
	h       http.Handler
	owner   string
	pattern string
	params  []string // parameter names, in path order
}

// NewHTTPMux returns an empty HTTPMux with no owner.
func NewHTTPMux() *HTTPMux {
	return &HTTPMux{t: &routeTable{root: &segmentNode{}}}
}

// Owned returns a view of m that shares its routes and registers new ones as
// owner's. Serving either one serves every route.
func (m *HTTPMux) Owned(owner string) *HTTPMux {
	return &HTTPMux{t: m.t, owner: owner}
}

var paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RouteParams checks a route path pattern and returns its parameter names
// in path order. The pattern starts with "/"; a parameter is a whole
// segment "{name}", unique within the pattern.
func RouteParams(pattern string) ([]string, error) {
	if !strings.HasPrefix(pattern, "/") {
		return nil, fmt.Errorf("path %q must start with /", pattern)
	}
	var names []string
	for _, seg := range strings.Split(pattern[1:], "/") {
		if !strings.ContainsAny(seg, "{}") {
			continue
		}
		name, ok := strings.CutPrefix(seg, "{")
		if ok {
			name, ok = strings.CutSuffix(name, "}")
		}
		if !ok || !paramName.MatchString(name) {
			return nil, fmt.Errorf("path %q: segment %q must be a literal or a whole parameter {name}, the name made of letters, digits and _", pattern, seg)
		}
		if slices.Contains(names, name) {
			return nil, fmt.Errorf("path %q: parameter {%s} appears twice", pattern, name)
		}
		names = append(names, name)
	}
	return names, nil
}

// segments splits a pattern or a request path into its segments: "/" has
// one empty segment, "/a/" has "a" and "".
func segments(path string) []string {
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

func isParam(seg string) bool { return strings.HasPrefix(seg, "{") }

// Handle registers h for method + pattern. The method is case-insensitive.
// Registering a route whose method and shape are taken is an error, since
// two triggers competing for one route is a flow bug; when another owner
// holds the route, the error names it.
func (m *HTTPMux) Handle(method, pattern string, h http.Handler) error {
	params, err := RouteParams(pattern)
	if err != nil {
		return err
	}
	method = strings.ToUpper(method)
	m.t.mu.Lock()
	defer m.t.mu.Unlock()
	n := m.t.root
	for _, seg := range segments(pattern) {
		n = n.child(seg)
	}
	if taken, ok := n.routes[method]; ok {
		other := taken.owner != "" && taken.owner != m.owner
		switch {
		case taken.pattern != pattern && other:
			return fmt.Errorf("route %s %s conflicts with %s %s of flow %s", method, pattern, method, taken.pattern, taken.owner)
		case taken.pattern != pattern:
			return fmt.Errorf("route %s %s conflicts with %s %s", method, pattern, method, taken.pattern)
		case other:
			return fmt.Errorf("route %s %s is used by flow %s", method, pattern, taken.owner)
		default:
			return fmt.Errorf("route %s %s is already registered", method, pattern)
		}
	}
	if n.routes == nil {
		n.routes = make(map[string]route)
	}
	n.routes[method] = route{h: h, owner: m.owner, pattern: pattern, params: params}
	return nil
}

// child returns the node for seg below n, creating it.
func (n *segmentNode) child(seg string) *segmentNode {
	if isParam(seg) {
		if n.param == nil {
			n.param = &segmentNode{}
		}
		return n.param
	}
	if n.literals == nil {
		n.literals = make(map[string]*segmentNode)
	}
	c, ok := n.literals[seg]
	if !ok {
		c = &segmentNode{}
		n.literals[seg] = c
	}
	return c
}

// Remove deregisters method + pattern if this view's owner holds it.
// Removing an unknown route, or another owner's, is a no-op.
func (m *HTTPMux) Remove(method, pattern string) {
	method = strings.ToUpper(method)
	m.t.mu.Lock()
	defer m.t.mu.Unlock()
	m.t.root.remove(segments(pattern), method, pattern, m.owner)
}

// remove deletes the route below n and reports whether n is left empty, so
// the caller can prune it.
func (n *segmentNode) remove(segs []string, method, pattern, owner string) bool {
	if len(segs) == 0 {
		if r, ok := n.routes[method]; ok && r.owner == owner && r.pattern == pattern {
			delete(n.routes, method)
		}
	} else if seg := segs[0]; isParam(seg) {
		if n.param != nil && n.param.remove(segs[1:], method, pattern, owner) {
			n.param = nil
		}
	} else if c := n.literals[seg]; c != nil && c.remove(segs[1:], method, pattern, owner) {
		delete(n.literals, seg)
	}
	return len(n.routes) == 0 && len(n.literals) == 0 && n.param == nil
}

// match appends to out every node with routes that matches segs, most
// specific first: at each segment a literal is tried before a parameter.
func (n *segmentNode) match(segs []string, out []*segmentNode) []*segmentNode {
	if len(segs) == 0 {
		if len(n.routes) > 0 {
			out = append(out, n)
		}
		return out
	}
	if c := n.literals[segs[0]]; c != nil {
		out = c.match(segs[1:], out)
	}
	// A parameter matches any segment but an empty one.
	if n.param != nil && segs[0] != "" {
		out = n.param.match(segs[1:], out)
	}
	return out
}

// ServeHTTP dispatches to the most specific route matching the request's
// path that has its method, with the parameter values set as path values
// (r.PathValue). 404 when no route matches the path; 405 (with an Allow
// header) when routes match it but none with this method.
func (m *HTTPMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Match on the escaped path, so an encoded "/" stays inside its
	// segment; each segment is then decoded.
	raw := segments(r.URL.EscapedPath())
	segs := make([]string, len(raw))
	for i, s := range raw {
		dec, err := url.PathUnescape(s)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		segs[i] = dec
	}

	m.t.mu.RLock()
	nodes := m.t.root.match(segs, nil)
	var found route
	var allowed []string
	for _, n := range nodes {
		if rt, ok := n.routes[r.Method]; ok {
			found = rt
			break
		}
		for method := range n.routes {
			if !slices.Contains(allowed, method) {
				allowed = append(allowed, method)
			}
		}
	}
	m.t.mu.RUnlock()

	switch {
	case found.h != nil:
		if len(found.params) > 0 {
			i := 0
			for j, seg := range segments(found.pattern) {
				if isParam(seg) {
					r.SetPathValue(found.params[i], segs[j])
					i++
				}
			}
		}
		found.h.ServeHTTP(w, r)
	case len(nodes) == 0:
		http.NotFound(w, r)
	default:
		slices.Sort(allowed)
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}
