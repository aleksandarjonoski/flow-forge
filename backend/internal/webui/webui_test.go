package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHandler_ServesBuild(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":          {Data: []byte("<ff-app></ff-app>")},
		"assets/index-ab.js":  {Data: []byte("console.log(1)")},
		"assets/index-ab.css": {Data: []byte("body{}")},
	})

	page := get(h, "/")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<ff-app>") {
		t.Fatalf("GET / = %d %q", page.Code, page.Body.String())
	}
	if cc := page.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("page Cache-Control = %q, want no-cache", cc)
	}

	js := get(h, "/assets/index-ab.js")
	if js.Code != http.StatusOK || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Errorf("GET asset = %d, Content-Type %q", js.Code, js.Header().Get("Content-Type"))
	}
	if cc := js.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}

	if rec := get(h, "/missing.js"); rec.Code != http.StatusNotFound {
		t.Errorf("GET missing = %d, want 404", rec.Code)
	}
}

func TestHandler_NotBuilt(t *testing.T) {
	h := newHandler(fstest.MapFS{".gitkeep": {}})

	page := get(h, "/")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "make build") {
		t.Errorf("GET / = %d %q, want the not-built page", page.Code, page.Body.String())
	}
	if rec := get(h, "/assets/x.js"); rec.Code != http.StatusNotFound {
		t.Errorf("GET asset = %d, want 404", rec.Code)
	}
}

func TestHandler_Embedded(t *testing.T) {
	// Whatever is embedded right now, / answers with a page.
	if rec := get(Handler(), "/"); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d", rec.Code)
	}
}
