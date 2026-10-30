package nodes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// capturedRequest is what the test server saw.
type capturedRequest struct {
	method string
	header http.Header
	body   string
}

// newServer starts a test server that records the request into *got and
// responds with contentType, status and body.
func newServer(t *testing.T, got *capturedRequest, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = capturedRequest{method: r.Method, header: r.Header, body: string(b)}
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newHTTPRequest(t *testing.T, config map[string]any) *HTTPRequest {
	t.Helper()
	h := &HTTPRequest{}
	if err := h.Init(config, nil); err != nil {
		t.Fatalf("Init(%v): %v", config, err)
	}
	return h
}

func execCtx() *engine.ExecutionContext {
	return &engine.ExecutionContext{GoCtx: context.Background()}
}

func TestHTTPRequest_Type(t *testing.T) {
	if got := (&HTTPRequest{}).Type(); got != "http_request" {
		t.Errorf("Type() = %q, want http_request", got)
	}
}

func TestHTTPRequest_InitErrors(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{"missing url", map[string]any{}, `"url" is required`},
		{"relative url", map[string]any{"url": "/path"}, "absolute http(s) URL"},
		{"bad scheme", map[string]any{"url": "ftp://x"}, "absolute http(s) URL"},
		{"empty method", map[string]any{"url": "http://x", "method": ""}, "must not be empty"},
		{"headers not object", map[string]any{"url": "http://x", "headers": "a"}, "must be an object"},
		{"header value not string", map[string]any{"url": "http://x", "headers": map[string]any{"A": 1}}, `value of "A" must be a string`},
		{"non-string bodyFrom", map[string]any{"url": "http://x", "bodyFrom": 1}, "must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&HTTPRequest{}).Init(tc.config, nil)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestHTTPRequest_GetJSON(t *testing.T) {
	var got capturedRequest
	srv := newServer(t, &got, http.StatusOK, "application/json", `{"id":7}`)
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})

	out, err := h.Execute(execCtx(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.method != "GET" || got.body != "" {
		t.Errorf("request = %s with body %q, want GET with no body", got.method, got.body)
	}
	if out["status"] != 200 {
		t.Errorf("status = %v, want 200", out["status"])
	}
	if !reflect.DeepEqual(out["body"], map[string]any{"id": 7.0}) {
		t.Errorf("body = %v, want {id: 7}", out["body"])
	}
	if h := out["headers"].(map[string]any); h["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %v, want application/json", h["Content-Type"])
	}
}

func TestHTTPRequest_PostBodyFromAndHeaders(t *testing.T) {
	var got capturedRequest
	srv := newServer(t, &got, http.StatusCreated, "", "")
	h := newHTTPRequest(t, map[string]any{
		"url":      srv.URL,
		"method":   "post",
		"headers":  map[string]any{"Authorization": "Bearer t"},
		"bodyFrom": "body.user",
	})
	input := engine.Payload{"body": map[string]any{"user": map[string]any{"name": "ada"}}}

	out, err := h.Execute(execCtx(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.method != "POST" {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.body != `{"name":"ada"}` {
		t.Errorf("body = %q, want the JSON-encoded user", got.body)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.header.Get("Content-Type"))
	}
	if got.header.Get("Authorization") != "Bearer t" {
		t.Errorf("Authorization = %q, want configured header", got.header.Get("Authorization"))
	}
	if out["status"] != 201 || out["body"] != nil {
		t.Errorf("output = %v, want status 201 and nil body", out)
	}
}

func TestHTTPRequest_ConfiguredContentTypeWins(t *testing.T) {
	var got capturedRequest
	srv := newServer(t, &got, http.StatusOK, "", "")
	h := newHTTPRequest(t, map[string]any{
		"url":      srv.URL,
		"method":   "PUT",
		"headers":  map[string]any{"Content-Type": "application/vnd.api+json"},
		"bodyFrom": "x",
	})

	if _, err := h.Execute(execCtx(), engine.Payload{"x": 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/vnd.api+json" {
		t.Errorf("Content-Type = %q, want the configured one", ct)
	}
}

func TestHTTPRequest_BodyFromMissing(t *testing.T) {
	srv := newServer(t, nil, http.StatusOK, "", "")
	cases := map[string]engine.Payload{
		"missing key":   {"body": map[string]any{}},
		"not an object": {"body": "text"},
		"nil input":     nil,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHTTPRequest(t, map[string]any{"url": srv.URL, "bodyFrom": "body.user"})
			_, err := h.Execute(execCtx(), input)
			if err == nil || !strings.Contains(err.Error(), "bodyFrom") {
				t.Errorf("error = %v, want a bodyFrom error", err)
			}
		})
	}
}

func TestHTTPRequest_Non2xxIsData(t *testing.T) {
	srv := newServer(t, nil, http.StatusNotFound, "text/plain", "nope")
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})

	out, err := h.Execute(execCtx(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["status"] != 404 || out["body"] != "nope" {
		t.Errorf("output = %v, want status 404 and body nope", out)
	}
}

func TestHTTPRequest_InvalidJSONResponse(t *testing.T) {
	srv := newServer(t, nil, http.StatusOK, "application/json", "{bad")
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})

	_, err := h.Execute(execCtx(), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %v, want invalid JSON error", err)
	}
}

func TestHTTPRequest_ResponseTooLarge(t *testing.T) {
	srv := newServer(t, nil, http.StatusOK, "text/plain", strings.Repeat("x", maxResponseBytes+1))
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})

	_, err := h.Execute(execCtx(), nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want size limit error", err)
	}
}

func TestHTTPRequest_ConnectionError(t *testing.T) {
	srv := newServer(t, nil, http.StatusOK, "", "")
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})
	srv.Close()

	if _, err := h.Execute(execCtx(), nil); err == nil {
		t.Error("Execute succeeded against a closed server, want error")
	}
}

func TestHTTPRequest_ContextCancelled(t *testing.T) {
	srv := newServer(t, nil, http.StatusOK, "", "")
	h := newHTTPRequest(t, map[string]any{"url": srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.Execute(&engine.ExecutionContext{GoCtx: ctx}, nil)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want context canceled", err)
	}
}
