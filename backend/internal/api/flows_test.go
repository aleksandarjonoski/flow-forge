package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/host"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

type libFixture struct {
	api   http.Handler
	host  *host.Host
	store *store.Store
}

func newLib(t *testing.T) *libFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	h := host.New(st, engine.DefaultRegistry, host.Options{})
	t.Cleanup(func() {
		h.Shutdown()
		st.Close()
	})
	return &libFixture{api: NewHandler(engine.DefaultRegistry, Options{Store: st, Host: h}), host: h, store: st}
}

// do sends a request to the API and decodes a JSON answer into out (if
// non-nil). It returns the status code.
func (fx *libFixture) do(t *testing.T, method, path, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.api.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

// flowDoc is a flow document: an http_trigger on POST path, then a transform.
func flowDoc(id, path, expression string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "name": "Flow %s",
  "version": "1.0",
  "nodes": [
    {"id": "t", "type": "http_trigger", "position": {"x": 0, "y": 0}, "config": {"path": %q}},
    {"id": "x", "type": "transform", "position": {"x": 0, "y": 0}, "config": {"expression": %q}}
  ],
  "edges": [{"id": "e", "source": "t", "target": "x"}]
}
`, id, id, path, expression)
}

func createBody(doc string) string { return `{"flow": ` + doc + `}` }

func saveBody(doc string, revision int64) string {
	return fmt.Sprintf(`{"flow": %s, "revision": %d}`, doc, revision)
}

func (fx *libFixture) create(t *testing.T, doc string) {
	t.Helper()
	if code := fx.do(t, "POST", "/api/flows", createBody(doc), nil); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
}

func (fx *libFixture) trigger(path string) int {
	rec := httptest.NewRecorder()
	fx.host.Handler().ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader("")))
	return rec.Code
}

func TestLibrary_Health(t *testing.T) {
	fx := newLib(t)
	var got map[string]string
	if code := fx.do(t, "GET", "/api/health", "", &got); code != http.StatusOK || got["status"] != "ok" {
		t.Errorf("health = %d %v", code, got)
	}
}

func TestLibrary_NotMountedWithoutStore(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(engine.DefaultRegistry, Options{}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/flows", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/flows without a store = %d, want 404", rec.Code)
	}
}

func TestLibrary_CreateAndGet(t *testing.T) {
	fx := newLib(t)
	doc := flowDoc("orders", "/orders", "{ok: true}")

	var created flowInfo
	if code := fx.do(t, "POST", "/api/flows", createBody(doc), &created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	if created.ID != "orders" || created.Name != "Flow orders" || created.Revision != 1 ||
		created.State != host.Stopped || created.CreatedAt.IsZero() {
		t.Errorf("created = %+v", created)
	}

	var got flowInfo
	if code := fx.do(t, "GET", "/api/flows/orders", "", &got); code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	// The store keeps the document byte for byte; the response carries the
	// same JSON value.
	stored, _ := fx.store.GetFlow(context.Background(), "orders")
	if string(stored.Document) != doc {
		t.Errorf("stored document changed:\n%s\nwant\n%s", stored.Document, doc)
	}
	var compact bytes.Buffer
	json.Compact(&compact, []byte(doc))
	if !bytes.Equal(got.Flow, compact.Bytes()) {
		t.Errorf("response flow = %s, want %s", got.Flow, compact.Bytes())
	}

	if code := fx.do(t, "GET", "/api/flows/nope", "", nil); code != http.StatusNotFound {
		t.Errorf("get unknown = %d, want 404", code)
	}
}

func TestLibrary_CreateErrors(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("orders", "/orders", "{ok: true}"))

	cases := map[string]struct {
		body string
		want int
	}{
		"existing id":     {createBody(flowDoc("orders", "/x", "1")), http.StatusConflict},
		"id not URL-safe": {createBody(flowDoc("my flow", "/x", "1")), http.StatusBadRequest},
		"id too long":     {createBody(flowDoc(strings.Repeat("a", 65), "/x", "1")), http.StatusBadRequest},
		"no flow":         {`{}`, http.StatusBadRequest},
		"does not parse":  {`{"flow": {"id": "f", "version": "9.9", "nodes": []}}`, http.StatusBadRequest},
		"unknown field":   {`{"flow": ` + flowDoc("g", "/g", "1") + `, "extra": 1}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		var e errorResponse
		if code := fx.do(t, "POST", "/api/flows", tc.body, &e); code != tc.want || e.Error == "" {
			t.Errorf("%s: %d %+v, want %d with an error", name, code, e, tc.want)
		}
	}
}

func TestLibrary_SaveDoesNotNeedToCompile(t *testing.T) {
	fx := newLib(t)
	// Work in progress: the transform has no expression yet.
	broken := flowDoc("wip", "/wip", "")
	if code := fx.do(t, "POST", "/api/flows", createBody(broken), nil); code != http.StatusCreated {
		t.Errorf("create a flow that does not compile = %d, want 201", code)
	}
}

func TestLibrary_List(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("b", "/b", "1"))
	fx.create(t, flowDoc("a", "/a", "1"))

	var got struct {
		Flows []map[string]any `json:"flows"`
	}
	if code := fx.do(t, "GET", "/api/flows", "", &got); code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	if len(got.Flows) != 2 || got.Flows[0]["id"] != "a" || got.Flows[1]["id"] != "b" {
		t.Fatalf("flows = %v, want a then b", got.Flows)
	}
	if _, ok := got.Flows[0]["flow"]; ok {
		t.Error("the list includes documents")
	}
	if got.Flows[0]["state"] != "stopped" {
		t.Errorf("state = %v", got.Flows[0]["state"])
	}

	// An empty library is an empty array, not null.
	empty := newLib(t)
	var raw map[string]json.RawMessage
	empty.do(t, "GET", "/api/flows", "", &raw)
	if string(raw["flows"]) != "[]" {
		t.Errorf("empty list = %s, want []", raw["flows"])
	}
}

func TestLibrary_Save(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("orders", "/orders", "1"))

	var saved flowInfo
	if code := fx.do(t, "PUT", "/api/flows/orders", saveBody(flowDoc("orders", "/v2", "2"), 1), &saved); code != http.StatusOK {
		t.Fatalf("save = %d", code)
	}
	if saved.Revision != 2 || !strings.Contains(string(saved.Flow), "/v2") {
		t.Errorf("saved = rev %d %s", saved.Revision, saved.Flow)
	}

	// A second editor still at revision 1 gets the current flow back.
	var conflict conflictResponse
	if code := fx.do(t, "PUT", "/api/flows/orders", saveBody(flowDoc("orders", "/stale", "3"), 1), &conflict); code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409", code)
	}
	if conflict.Current.Revision != 2 || !strings.Contains(string(conflict.Current.Flow), "/v2") || conflict.Error == "" {
		t.Errorf("conflict = %+v", conflict)
	}

	cases := map[string]struct {
		path, body string
		want       int
	}{
		"id changed":       {"/api/flows/orders", saveBody(flowDoc("other", "/x", "1"), 2), http.StatusBadRequest},
		"no revision":      {"/api/flows/orders", createBody(flowDoc("orders", "/x", "1")), http.StatusBadRequest},
		"unknown flow":     {"/api/flows/nope", saveBody(flowDoc("nope", "/x", "1"), 1), http.StatusNotFound},
		"invalid document": {"/api/flows/orders", `{"flow": {"id": "orders"}, "revision": 2}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		if code := fx.do(t, "PUT", tc.path, tc.body, nil); code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}
}

func TestLibrary_StartStop(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("orders", "/orders", "{ok: true}"))

	var started flowInfo
	if code := fx.do(t, "POST", "/api/flows/orders/start", "", &started); code != http.StatusOK {
		t.Fatalf("start = %d", code)
	}
	if started.State != host.Running || started.RunningRevision != 1 {
		t.Errorf("started = %+v", started)
	}
	if code := fx.trigger("/orders"); code != http.StatusAccepted {
		t.Errorf("POST /orders = %d, want 202", code)
	}

	// Saving does not redeploy: the running revision stays behind.
	var saved flowInfo
	fx.do(t, "PUT", "/api/flows/orders", saveBody(flowDoc("orders", "/orders", "{ok: 2}"), 1), &saved)
	if saved.Revision != 2 || saved.RunningRevision != 1 || saved.State != host.Running {
		t.Errorf("after save = %+v, want revision 2 with 1 running", saved)
	}

	var stopped flowInfo
	if code := fx.do(t, "POST", "/api/flows/orders/stop", "", &stopped); code != http.StatusOK {
		t.Fatalf("stop = %d", code)
	}
	if stopped.State != host.Stopped || stopped.RunningRevision != 0 {
		t.Errorf("stopped = %+v", stopped)
	}
	if code := fx.trigger("/orders"); code != http.StatusNotFound {
		t.Errorf("POST /orders after stop = %d, want 404", code)
	}

	for _, path := range []string{"/api/flows/nope/start", "/api/flows/nope/stop"} {
		if code := fx.do(t, "POST", path, "", nil); code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, code)
		}
	}
}

func TestLibrary_StartCompileProblems(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("wip", "/wip", ""))

	var e errorResponse
	if code := fx.do(t, "POST", "/api/flows/wip/start", "", &e); code != http.StatusBadRequest {
		t.Fatalf("start = %d, want 400", code)
	}
	if e.Error != "flow does not compile" || len(e.Problems) == 0 {
		t.Errorf("error = %+v, want the compile problems", e)
	}
}

func TestLibrary_StartRouteConflictIsFailedState(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("first", "/orders", "1"))
	fx.create(t, flowDoc("second", "/orders", "1"))
	fx.do(t, "POST", "/api/flows/first/start", "", nil)

	var second flowInfo
	if code := fx.do(t, "POST", "/api/flows/second/start", "", &second); code != http.StatusOK {
		t.Fatalf("start = %d, want 200", code)
	}
	if second.State != host.Failed || !strings.Contains(second.Error, "used by flow first") {
		t.Errorf("second = %+v, want failed naming flow first", second)
	}
}

func TestLibrary_Delete(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("orders", "/orders", "1"))
	fx.do(t, "POST", "/api/flows/orders/start", "", nil)

	if code := fx.do(t, "DELETE", "/api/flows/orders", "", nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
	if code := fx.trigger("/orders"); code != http.StatusNotFound {
		t.Errorf("deleted flow still serves: %d", code)
	}
	if code := fx.do(t, "GET", "/api/flows/orders", "", nil); code != http.StatusNotFound {
		t.Errorf("get after delete = %d", code)
	}
	if code := fx.do(t, "DELETE", "/api/flows/orders", "", nil); code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", code)
	}
}

func TestLibrary_FlowNamedRun(t *testing.T) {
	// "run" is a valid ID; it must not clash with the test-run endpoint.
	fx := newLib(t)
	fx.create(t, flowDoc("run", "/r", "1"))
	if code := fx.do(t, "GET", "/api/flows/run", "", nil); code != http.StatusOK {
		t.Errorf("GET /api/flows/run = %d, want 200", code)
	}
	if code := fx.do(t, "POST", "/api/flows/run/start", "", nil); code != http.StatusOK {
		t.Errorf("start flow 'run' = %d, want 200", code)
	}
}
