package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler responds 200 with the given body.
func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

// serve sends method + path to m and returns the recorded response.
func serve(m *HTTPMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestNewEngineServices(t *testing.T) {
	svc := NewEngineServices()
	if svc.HTTP == nil {
		t.Error("HTTP mux is nil")
	}
	if svc.Cron == nil {
		t.Error("cron scheduler is nil")
	}
}

func TestHTTPMux_Dispatch(t *testing.T) {
	m := NewHTTPMux()
	if err := m.Handle("post", "/a", okHandler("post-a")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := m.Handle("GET", "/a", okHandler("get-a")); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	for method, want := range map[string]string{"POST": "post-a", "GET": "get-a"} {
		rec := serve(m, method, "/a")
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s /a = %d %q, want 200 %q", method, rec.Code, rec.Body, want)
		}
	}
}

func TestHTTPMux_NotFound(t *testing.T) {
	m := NewHTTPMux()
	_ = m.Handle("POST", "/a", okHandler(""))

	if rec := serve(m, "POST", "/b"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHTTPMux_MethodNotAllowed(t *testing.T) {
	m := NewHTTPMux()
	_ = m.Handle("PUT", "/a", okHandler(""))
	_ = m.Handle("POST", "/a", okHandler(""))

	rec := serve(m, "GET", "/a")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "POST, PUT" {
		t.Errorf("Allow = %q, want %q", got, "POST, PUT")
	}
}

func TestHTTPMux_DuplicateRoute(t *testing.T) {
	m := NewHTTPMux()
	if err := m.Handle("POST", "/a", okHandler("")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := m.Handle("post", "/a", okHandler("")); err == nil {
		t.Error("duplicate Handle succeeded, want error")
	}
}

func TestHTTPMux_Remove(t *testing.T) {
	m := NewHTTPMux()
	_ = m.Handle("POST", "/a", okHandler(""))
	_ = m.Handle("GET", "/a", okHandler(""))

	m.Remove("post", "/a")
	if rec := serve(m, "POST", "/a"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("after removing POST: status = %d, want 405", rec.Code)
	}
	m.Remove("GET", "/a")
	if rec := serve(m, "GET", "/a"); rec.Code != http.StatusNotFound {
		t.Errorf("after removing all: status = %d, want 404", rec.Code)
	}
	m.Remove("GET", "/missing") // no-op, must not panic

	if err := m.Handle("POST", "/a", okHandler("")); err != nil {
		t.Errorf("re-Handle after Remove: %v", err)
	}
}
