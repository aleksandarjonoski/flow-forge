package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
)

func helloFlow(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../../examples/hello.flow.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// post sends body to h at POST /api/flows/run and decodes the JSON response
// into out (if non-nil). It returns the status code.
func post(t *testing.T, h http.Handler, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/flows/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

func runBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// twoTriggerFlow has an http_trigger (h) and a cron_trigger (c), each
// feeding its own log node.
const twoTriggerFlow = `{
  "id": "f", "version": "1.0",
  "nodes": [
    {"id": "h", "type": "http_trigger", "config": {"path": "/x"}},
    {"id": "c", "type": "cron_trigger", "config": {"expression": "@hourly"}},
    {"id": "l1", "type": "log"},
    {"id": "l2", "type": "log"}
  ],
  "edges": [
    {"id": "e1", "source": "h", "target": "l1"},
    {"id": "e2", "source": "c", "target": "l2"}
  ]
}`

func TestRun_HelloCompletes(t *testing.T) {
	h := NewHandler(engine.DefaultRegistry)
	var resp runResponse
	code := post(t, h, runBody(t, map[string]any{
		"flow":    helloFlow(t),
		"payload": map[string]any{"body": map[string]any{"name": "ada"}},
	}), &resp)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Status != "completed" || resp.Error != "" {
		t.Errorf("status/error = %q/%q, want completed and no error", resp.Status, resp.Error)
	}
	if resp.ExecutionID == "" {
		t.Error("executionId is empty")
	}
	var kinds []engine.TraceKind
	var greeting engine.Payload
	for _, ev := range resp.Trace {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == engine.TraceNodeCompleted && ev.NodeID == "node-2" {
			greeting = ev.Output
		}
		if ev.ExecutionID != resp.ExecutionID {
			t.Errorf("event %s has executionId %q, want %q", ev.Kind, ev.ExecutionID, resp.ExecutionID)
		}
	}
	want := []engine.TraceKind{
		engine.TraceExecutionStarted,
		engine.TraceNodeStarted, engine.TraceNodeCompleted,
		engine.TraceNodeStarted, engine.TraceNodeCompleted,
		engine.TraceExecutionCompleted,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("trace kinds = %v, want %v", kinds, want)
	}
	if greeting["greeting"] != "hello ada" {
		t.Errorf("transform output = %v, want greeting hello ada", greeting)
	}
}

func TestRun_NodeFailureIsA200(t *testing.T) {
	var resp runResponse
	// No payload: "'hello ' + input.body.name" fails in the transform.
	code := post(t, NewHandler(engine.DefaultRegistry), runBody(t, map[string]any{"flow": helloFlow(t)}), &resp)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Status != "failed" || !strings.Contains(resp.Error, "node node-2 (transform)") {
		t.Errorf("status/error = %q/%q, want failed at node-2", resp.Status, resp.Error)
	}
	if last := resp.Trace[len(resp.Trace)-1]; last.Kind != engine.TraceExecutionFailed {
		t.Errorf("last event = %s, want execution_failed", last.Kind)
	}
}

func TestRun_TriggerChoice(t *testing.T) {
	h := NewHandler(engine.DefaultRegistry)
	cases := []struct {
		name    string
		trigger string
		code    int
		wantErr string
	}{
		{"ambiguous", "", http.StatusBadRequest, `flow has 2 triggers (c, h); say which one to run with "trigger"`},
		{"unknown", "nope", http.StatusBadRequest, `"nope" is not a trigger node of this flow`},
		{"action node", "l1", http.StatusBadRequest, `"l1" is not a trigger node of this flow`},
		{"cron", "c", http.StatusOK, ""},
		{"http", "h", http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Either a runResponse or an errorResponse, depending on code.
			var resp struct {
				Error string              `json:"error"`
				Trace []engine.TraceEvent `json:"trace"`
			}
			code := post(t, h, runBody(t, map[string]any{
				"flow":    json.RawMessage(twoTriggerFlow),
				"trigger": tc.trigger,
			}), &resp)
			if code != tc.code {
				t.Fatalf("status = %d, want %d (%+v)", code, tc.code, resp)
			}
			if resp.Error != tc.wantErr {
				t.Errorf("error = %q, want %q", resp.Error, tc.wantErr)
			}
			if tc.code == http.StatusOK && resp.Trace[0].NodeID != tc.trigger {
				t.Errorf("execution started at %q, want %q", resp.Trace[0].NodeID, tc.trigger)
			}
		})
	}
}

func TestRun_BadRequests(t *testing.T) {
	h := NewHandler(engine.DefaultRegistry)
	badConfig := `{"id": "f", "version": "1.0", "nodes": [
	  {"id": "t", "type": "http_trigger"},
	  {"id": "x", "type": "no_such_type"}
	]}`
	cases := []struct {
		name         string
		body         string
		code         int
		wantErr      string
		wantProblems int
	}{
		{"not JSON", "{", http.StatusBadRequest, "invalid request body", 0},
		{"unknown field", `{"flow": {}, "extra": 1}`, http.StatusBadRequest, `unknown field "extra"`, 0},
		{"trailing data", `{"flow": null} {}`, http.StatusBadRequest, "unexpected data", 0},
		{"no flow", `{}`, http.StatusBadRequest, `"flow" is required`, 0},
		{"unparsable flow", `{"flow": {"id": "f", "version": "2.0"}}`, http.StatusBadRequest, "unsupported version", 0},
		{"does not compile", `{"flow": ` + badConfig + `}`, http.StatusBadRequest, "flow does not compile", 1},
		{"too large", `{"flow": "` + strings.Repeat("x", maxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge, "exceeds", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp errorResponse
			code := post(t, h, tc.body, &resp)
			if code != tc.code {
				t.Fatalf("status = %d, want %d (%+v)", code, tc.code, resp)
			}
			if !strings.Contains(resp.Error, tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", resp.Error, tc.wantErr)
			}
			if len(resp.Problems) < tc.wantProblems {
				t.Errorf("problems = %q, want at least %d", resp.Problems, tc.wantProblems)
			}
		})
	}
}

func TestRun_CompileProblemsAreListed(t *testing.T) {
	// Unknown type is caught by Validate before any Init runs.
	var resp errorResponse
	post(t, NewHandler(engine.DefaultRegistry), `{"flow": {"id": "f", "version": "1.0", "nodes": [
	  {"id": "t", "type": "http_trigger", "config": {"path": "/x"}},
	  {"id": "a", "type": "nope1"},
	  {"id": "b", "type": "nope2"}
	]}}`, &resp)
	if len(resp.Problems) != 2 {
		t.Fatalf("problems = %q, want one per unknown type", resp.Problems)
	}
	for i, want := range []string{`"nope1"`, `"nope2"`} {
		if !strings.Contains(resp.Problems[i], want) {
			t.Errorf("problems[%d] = %q, want it to mention %s", i, resp.Problems[i], want)
		}
	}
}

func TestRun_Timeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	flowJSON := `{"id": "f", "version": "1.0",
	  "nodes": [
	    {"id": "t", "type": "http_trigger", "config": {"path": "/x"}},
	    {"id": "r", "type": "http_request", "config": {"url": "` + slow.URL + `"}}
	  ],
	  "edges": [{"id": "e", "source": "t", "target": "r"}]}`
	h := &handler{reg: engine.DefaultRegistry, runTimeout: 50 * time.Millisecond}

	var resp runResponse
	start := time.Now()
	code := post(t, http.HandlerFunc(h.run), `{"flow": `+flowJSON+`}`, &resp)
	if code != http.StatusOK || resp.Status != "failed" {
		t.Fatalf("status = %d/%q, want 200/failed", code, resp.Status)
	}
	if !strings.Contains(resp.Error, "context deadline exceeded") {
		t.Errorf("error = %q, want a deadline error", resp.Error)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("run took %v; the timeout did not cut it short", time.Since(start))
	}
}

func TestRun_OnlyPOST(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(engine.DefaultRegistry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flows/run", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}
