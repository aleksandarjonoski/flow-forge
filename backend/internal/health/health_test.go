package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(h http.Handler, method, path string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestProbes(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	p := New(func() Counts { return Counts{Running: 2, Failed: 1} })
	h := p.Handler(next)

	check := func(path string, wantCode int, wantStatus string) {
		t.Helper()
		code, body := get(h, "GET", path)
		if code != wantCode || body["status"] != wantStatus {
			t.Errorf("GET %s = %d %v, want %d %q", path, code, body, wantCode, wantStatus)
		}
	}

	if !p.Starting() {
		t.Error("new Probes not starting")
	}
	check(HealthPath, 200, "ok")
	check(ReadyPath, 503, "starting")

	p.SetReady()
	if p.Starting() {
		t.Error("ready Probes still starting")
	}
	check(ReadyPath, 200, "ready")
	if _, body := get(h, "GET", ReadyPath); body["flows"].(map[string]any)["failed"] != 1.0 {
		t.Errorf("ready body = %v, want the counts", body)
	}

	p.SetStopping()
	p.SetReady() // stopping is final
	check(ReadyPath, 503, "stopping")
	check(HealthPath, 200, "ok")

	if code, _ := get(h, "POST", ReadyPath); code != http.StatusMethodNotAllowed {
		t.Errorf("POST ready = %d, want 405", code)
	}
	if code, _ := get(h, "HEAD", HealthPath); code != 200 {
		t.Errorf("HEAD health = %d, want 200", code)
	}
	if code, _ := get(h, "GET", "/_flowforge/other"); code != 404 {
		t.Errorf("unknown reserved path = %d, want 404", code)
	}
	if code, _ := get(h, "POST", "/orders"); code != http.StatusTeapot {
		t.Errorf("flow route = %d, want it passed on", code)
	}
}
