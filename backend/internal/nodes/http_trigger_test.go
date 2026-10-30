package nodes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// startTrigger initializes and starts an HTTPTrigger on a fresh mux whose
// emit records every payload. It returns the mux and a pointer to the
// recorded payloads.
func startTrigger(t *testing.T, config map[string]any, emitErr error) (*engine.HTTPMux, *[]engine.Payload) {
	t.Helper()
	svc := engine.NewEngineServices()
	tr := &HTTPTrigger{}
	if err := tr.Init(config, svc); err != nil {
		t.Fatalf("Init(%v): %v", config, err)
	}
	var got []engine.Payload
	emit := func(p engine.Payload) error {
		got = append(got, p)
		return emitErr
	}
	if err := tr.Start(context.Background(), emit); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = tr.Stop() })
	return svc.HTTP, &got
}

// do sends a request through mux and returns the recorded response.
func do(mux http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHTTPTrigger_Type(t *testing.T) {
	if got := (&HTTPTrigger{}).Type(); got != "http_trigger" {
		t.Errorf("Type() = %q, want http_trigger", got)
	}
}

func TestHTTPTrigger_InitErrors(t *testing.T) {
	svc := engine.NewEngineServices()
	cases := []struct {
		name    string
		config  map[string]any
		svc     *engine.EngineServices
		wantErr string
	}{
		{"missing path", map[string]any{}, svc, `"path" is required`},
		{"relative path", map[string]any{"path": "webhook"}, svc, "must start with /"},
		{"non-string method", map[string]any{"path": "/a", "method": 1}, svc, "must be a string"},
		{"empty method", map[string]any{"path": "/a", "method": ""}, svc, "must not be empty"},
		{"nil services", map[string]any{"path": "/a"}, nil, "no HTTP mux"},
		{"no mux", map[string]any{"path": "/a"}, &engine.EngineServices{}, "no HTTP mux"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&HTTPTrigger{}).Init(tc.config, tc.svc)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestHTTPTrigger_JSONRequest(t *testing.T) {
	mux, got := startTrigger(t, map[string]any{"path": "/webhook"}, nil)

	req := httptest.NewRequest("POST", "/webhook?a=1&a=2&b=x", strings.NewReader(`{"name":"ada"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("X-Tag", "one")
	req.Header.Add("X-Tag", "two")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %q)", rec.Code, rec.Body)
	}
	if len(*got) != 1 {
		t.Fatalf("emitted %d payloads, want 1", len(*got))
	}
	p := (*got)[0]
	if !reflect.DeepEqual(p["body"], map[string]any{"name": "ada"}) {
		t.Errorf("body = %v, want {name: ada}", p["body"])
	}
	if !reflect.DeepEqual(p["query"], map[string]any{"a": "1", "b": "x"}) {
		t.Errorf("query = %v, want first values", p["query"])
	}
	if h := p["headers"].(map[string]any); h["X-Tag"] != "one, two" {
		t.Errorf("X-Tag header = %v, want %q", h["X-Tag"], "one, two")
	}
}

func TestHTTPTrigger_Bodies(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        any
	}{
		{"plain text", "text/plain", "hi", "hi"},
		{"no content type", "", `{"a":1}`, `{"a":1}`},
		{"+json type", "application/vnd.api+json", `[1,2]`, []any{1.0, 2.0}},
		{"empty", "application/json", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, got := startTrigger(t, map[string]any{"path": "/a"}, nil)
			if rec := do(mux, "POST", "/a", tc.contentType, tc.body); rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			if b := (*got)[0]["body"]; !reflect.DeepEqual(b, tc.want) {
				t.Errorf("body = %#v, want %#v", b, tc.want)
			}
		})
	}
}

func TestHTTPTrigger_MethodConfig(t *testing.T) {
	mux, got := startTrigger(t, map[string]any{"path": "/a", "method": "get"}, nil)

	if rec := do(mux, "GET", "/a", "", ""); rec.Code != http.StatusAccepted {
		t.Errorf("GET status = %d, want 202", rec.Code)
	}
	if rec := do(mux, "POST", "/a", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
	if len(*got) != 1 {
		t.Errorf("emitted %d payloads, want 1", len(*got))
	}
}

func TestHTTPTrigger_InvalidJSON(t *testing.T) {
	mux, got := startTrigger(t, map[string]any{"path": "/a"}, nil)

	if rec := do(mux, "POST", "/a", "application/json", "{nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if len(*got) != 0 {
		t.Errorf("emitted %d payloads for invalid JSON, want 0", len(*got))
	}
}

func TestHTTPTrigger_BodyTooLarge(t *testing.T) {
	mux, got := startTrigger(t, map[string]any{"path": "/a"}, nil)

	big := strings.Repeat("x", maxBodyBytes+1)
	if rec := do(mux, "POST", "/a", "text/plain", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if len(*got) != 0 {
		t.Errorf("emitted %d payloads for oversized body, want 0", len(*got))
	}
}

func TestHTTPTrigger_EmitError(t *testing.T) {
	mux, _ := startTrigger(t, map[string]any{"path": "/a"}, errors.New("engine stopped"))

	rec := do(mux, "POST", "/a", "", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "engine stopped") {
		t.Errorf("body = %q, want the emit error", rec.Body)
	}
}

func TestHTTPTrigger_StopRemovesRoute(t *testing.T) {
	svc := engine.NewEngineServices()
	tr := &HTTPTrigger{}
	if err := tr.Init(map[string]any{"path": "/a"}, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := tr.Start(context.Background(), func(engine.Payload) error { return nil }); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if rec := do(svc.HTTP, "POST", "/a", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status after Stop = %d, want 404", rec.Code)
	}
}

func TestHTTPTrigger_DuplicateRoute(t *testing.T) {
	svc := engine.NewEngineServices()
	noop := func(engine.Payload) error { return nil }
	for i := range 2 {
		tr := &HTTPTrigger{}
		if err := tr.Init(map[string]any{"path": "/a"}, svc); err != nil {
			t.Fatalf("Init %d: %v", i, err)
		}
		err := tr.Start(context.Background(), noop)
		if i == 0 && err != nil {
			t.Fatalf("first Start: %v", err)
		}
		if i == 1 && err == nil {
			t.Error("second Start on the same route succeeded, want error")
		}
	}
}
