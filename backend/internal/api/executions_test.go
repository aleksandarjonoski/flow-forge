package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

var t0 = time.Date(2026, 10, 31, 14, 3, 7, 123_000_000, time.UTC)

// record stores an execution of flow with one trace event; finished == false
// leaves it running.
func (fx *libFixture) record(t *testing.T, flow, id string, started time.Time, finished bool) {
	t.Helper()
	b := store.Batch{
		Started: []store.Execution{{ID: id, FlowID: flow, Revision: 1, Trigger: "t",
			Status: store.StatusRunning, StartedAt: started, TraceComplete: true}},
		Events: []store.TraceRecord{{ExecutionID: id, Seq: 0,
			Event: json.RawMessage(`{"kind":"execution_started","executionId":"` + id + `"}`)}},
	}
	if finished {
		b.Finished = []store.Finish{{ID: id, Status: store.StatusFailed, Error: "boom", FinishedAt: started.Add(time.Millisecond)}}
	}
	if err := fx.store.WriteBatch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func TestExecutions_ListPages(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("f", "/f", "1"))
	for i := range 3 {
		fx.record(t, "f", fmt.Sprintf("e%d", i), t0.Add(time.Duration(i)*time.Second), true)
	}

	var first executionPage
	if code := fx.do(t, "GET", "/api/executions?flow=f&limit=2", "", &first); code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	if len(first.Executions) != 2 || first.Executions[0].ID != "e2" || first.Next == "" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Executions[0].Trace != nil {
		t.Error("the list includes traces")
	}

	var second executionPage
	fx.do(t, "GET", "/api/executions?flow=f&limit=2&before="+url.QueryEscape(first.Next), "", &second)
	if len(second.Executions) != 1 || second.Executions[0].ID != "e0" || second.Next != "" {
		t.Errorf("second page = %+v", second)
	}

	var all map[string]json.RawMessage
	fx.do(t, "GET", "/api/executions?flow=f", "", &all)
	if _, ok := all["next"]; ok {
		t.Error("next present on the last page")
	}

	// A flow with no runs is an empty array, not null.
	fx.create(t, flowDoc("idle", "/idle", "1"))
	var idle map[string]json.RawMessage
	fx.do(t, "GET", "/api/executions?flow=idle", "", &idle)
	if string(idle["executions"]) != "[]" {
		t.Errorf("no runs = %s, want []", idle["executions"])
	}
}

func TestExecutions_ListErrors(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("f", "/f", "1"))
	cases := map[string]int{
		"/api/executions":                     http.StatusBadRequest,
		"/api/executions?flow=nope":           http.StatusNotFound,
		"/api/executions?flow=f&limit=0":      http.StatusBadRequest,
		"/api/executions?flow=f&limit=201":    http.StatusBadRequest,
		"/api/executions?flow=f&limit=x":      http.StatusBadRequest,
		"/api/executions?flow=f&before=bogus": http.StatusBadRequest,
	}
	for path, want := range cases {
		var e errorResponse
		if code := fx.do(t, "GET", path, "", &e); code != want || e.Error == "" {
			t.Errorf("%s = %d %+v, want %d with an error", path, code, e, want)
		}
	}
}

func TestExecutions_Get(t *testing.T) {
	fx := newLib(t)
	fx.create(t, flowDoc("f", "/f", "1"))
	fx.record(t, "f", "done", t0, true)
	fx.record(t, "f", "busy", t0, false)

	var got executionInfo
	if code := fx.do(t, "GET", "/api/executions/done", "", &got); code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	if got.FlowID != "f" || got.Status != store.StatusFailed || got.Error != "boom" ||
		got.FinishedAt == nil || !got.TraceComplete || len(got.Trace) != 1 {
		t.Errorf("execution = %+v", got)
	}

	var busy map[string]any
	fx.do(t, "GET", "/api/executions/busy", "", &busy)
	if _, ok := busy["finishedAt"]; ok || busy["status"] != "running" {
		t.Errorf("running execution = %v, want no finishedAt", busy)
	}

	if code := fx.do(t, "GET", "/api/executions/nope", "", nil); code != http.StatusNotFound {
		t.Errorf("unknown = %d, want 404", code)
	}
}
