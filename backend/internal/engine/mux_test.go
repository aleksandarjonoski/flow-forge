package engine

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRouteParams(t *testing.T) {
	ok := map[string][]string{
		"/":                         nil,
		"/orders":                   nil,
		"/orders/{id}":              {"id"},
		"/c/{customer}/o/{order_2}": {"customer", "order_2"},
		"/{_x}/":                    {"_x"},
	}
	for pattern, want := range ok {
		got, err := RouteParams(pattern)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("RouteParams(%q) = %v, %v; want %v", pattern, got, err, want)
		}
	}
	bad := map[string]string{
		"orders":          "must start with /",
		"/orders/id-{id}": "whole parameter",
		"/orders/{id":     "whole parameter",
		"/orders/{}":      "whole parameter",
		"/orders/{1id}":   "whole parameter",
		"/orders/{a-b}":   "whole parameter",
		"/a/{id}/b/{id}":  "appears twice",
	}
	for pattern, want := range bad {
		if _, err := RouteParams(pattern); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("RouteParams(%q) = %v, want %q", pattern, err, want)
		}
	}
}

// paramsHandler answers with the route name and its path values.
func paramsHandler(name string, params ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := name
		for _, p := range params {
			out += " " + p + "=" + r.PathValue(p)
		}
		_, _ = w.Write([]byte(out))
	})
}

func TestHTTPMux_Params(t *testing.T) {
	m := NewHTTPMux()
	routes := []struct {
		method, pattern string
		h               http.Handler
	}{
		{"GET", "/orders/{id}", paramsHandler("order", "id")},
		{"GET", "/orders/new", paramsHandler("new")},
		{"GET", "/orders/{id}/items/{item}", paramsHandler("item", "id", "item")},
		{"GET", "/orders/new/items/x", paramsHandler("literal-x")},
		{"POST", "/orders/{id}", paramsHandler("post-order", "id")},
		{"GET", "/{any}/b", paramsHandler("any-b", "any")},
	}
	for _, rt := range routes {
		if err := m.Handle(rt.method, rt.pattern, rt.h); err != nil {
			t.Fatalf("Handle %s %s: %v", rt.method, rt.pattern, err)
		}
	}

	cases := []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/orders/7", 200, "order id=7"},
		// A literal beats a parameter.
		{"GET", "/orders/new", 200, "new"},
		{"GET", "/orders/7/items/3", 200, "item id=7 item=3"},
		{"GET", "/orders/new/items/x", 200, "literal-x"},
		// Backtracking: the literal "new" branch has no items/{item}.
		{"GET", "/orders/new/items/9", 200, "item id=new item=9"},
		// Values are decoded; an encoded "/" stays in its segment.
		{"GET", "/orders/a%2Fb%20c", 200, "order id=a/b c"},
		{"GET", "/orders/b", 200, "order id=b"},
		{"GET", "/x/b", 200, "any-b any=x"},
		// The method picks between shapes: POST has only {id}.
		{"POST", "/orders/new", 200, "post-order id=new"},
		// Parameters never match an empty segment; trailing slashes count.
		{"GET", "/orders/", 404, ""},
		{"GET", "/orders/7/", 404, ""},
		{"GET", "/orders", 404, ""},
		{"DELETE", "/orders/7", 405, ""},
	}
	for _, tc := range cases {
		rec := serve(m, tc.method, tc.path)
		if rec.Code != tc.code || (tc.body != "" && rec.Body.String() != tc.body) {
			t.Errorf("%s %s = %d %q, want %d %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code, tc.body)
		}
	}
	if allow := serve(m, "DELETE", "/orders/7").Header().Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow = %q, want GET, POST", allow)
	}
}

func TestHTTPMux_ShapeConflicts(t *testing.T) {
	root := NewHTTPMux()
	a, b := root.Owned("flow-a"), root.Owned("flow-b")
	if err := a.Handle("GET", "/orders/{id}", okHandler("a")); err != nil {
		t.Fatal(err)
	}

	err := b.Handle("GET", "/orders/{orderId}", okHandler("b"))
	if err == nil || err.Error() != "route GET /orders/{orderId} conflicts with GET /orders/{id} of flow flow-a" {
		t.Errorf("other owner, same shape: %v", err)
	}
	err = a.Handle("GET", "/orders/{orderId}", okHandler("a"))
	if err == nil || err.Error() != "route GET /orders/{orderId} conflicts with GET /orders/{id}" {
		t.Errorf("same owner, same shape: %v", err)
	}
	// Different shapes or methods do not conflict.
	for _, r := range []struct{ method, pattern string }{
		{"GET", "/orders/new"}, {"POST", "/orders/{x}"}, {"GET", "/orders/{id}/items"},
	} {
		if err := b.Handle(r.method, r.pattern, okHandler("b")); err != nil {
			t.Errorf("Handle %s %s: %v", r.method, r.pattern, err)
		}
	}
	if err := b.Handle("GET", "/orders/{id}x", okHandler("b")); err == nil {
		t.Error("partial parameter segment accepted")
	}

	// Remove takes the pattern as registered, and prunes the trie.
	a.Remove("GET", "/orders/{other}")
	if rec := serve(root, "GET", "/orders/7"); rec.Code != 200 {
		t.Errorf("after removing another pattern: %d, want 200", rec.Code)
	}
	a.Remove("GET", "/orders/{id}")
	if rec := serve(root, "GET", "/orders/7"); rec.Code != 405 {
		t.Errorf("after Remove: %d, want 405 (POST /orders/{x} remains)", rec.Code)
	}
	b.Remove("POST", "/orders/{x}")
	b.Remove("GET", "/orders/{id}/items")
	if rec := serve(root, "GET", "/orders/7"); rec.Code != 404 {
		t.Errorf("after removing all {x} routes: %d, want 404", rec.Code)
	}
	if root.t.root.literals["orders"].param != nil {
		t.Error("empty parameter node not pruned")
	}
}
