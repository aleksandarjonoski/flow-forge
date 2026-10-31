package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

var t0 = time.Date(2026, 10, 31, 14, 3, 7, 123_000_000, time.UTC)

func openStore(t *testing.T, flows ...string) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, id := range flows {
		if _, err := st.CreateFlow(context.Background(), id, id, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// events is the trace of one execution: started, one node, then the end.
func events(exec string, failed bool) []engine.TraceEvent {
	evs := []engine.TraceEvent{
		{Kind: engine.TraceExecutionStarted, ExecutionID: exec, NodeID: "trig", Timestamp: t0},
		{Kind: engine.TraceNodeStarted, ExecutionID: exec, NodeID: "n", NodeType: "transform", Timestamp: t0, Input: engine.Payload{"a": 1.0}},
		{Kind: engine.TraceNodeCompleted, ExecutionID: exec, NodeID: "n", NodeType: "transform", Timestamp: t0, Output: engine.Payload{"b": 2.0}},
	}
	end := engine.TraceEvent{Kind: engine.TraceExecutionCompleted, ExecutionID: exec, Timestamp: t0.Add(4 * time.Millisecond)}
	if failed {
		end.Kind = engine.TraceExecutionFailed
		end.Err = "node n (transform): boom"
	}
	return append(evs, end)
}

func get(t *testing.T, st *store.Store, id string) (store.Execution, []map[string]any) {
	t.Helper()
	e, raw, err := st.GetExecution(context.Background(), id)
	if err != nil {
		t.Fatalf("GetExecution %s: %v", id, err)
	}
	var trace []map[string]any
	for _, r := range raw {
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			t.Fatal(err)
		}
		trace = append(trace, m)
	}
	return e, trace
}

func TestWriter_RecordsExecutions(t *testing.T) {
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 3)
	for _, ev := range events("ok", false) {
		sink.Emit(ev)
	}
	for _, ev := range events("bad", true) {
		sink.Emit(ev)
	}
	w.Close() // flushes

	e, trace := get(t, st, "ok")
	want := store.Execution{
		ID: "ok", FlowID: "f", Revision: 3, Trigger: "trig", Status: store.StatusCompleted,
		StartedAt: t0, FinishedAt: t0.Add(4 * time.Millisecond), TraceComplete: true,
	}
	if e != want {
		t.Errorf("execution = %+v\nwant %+v", e, want)
	}
	var kinds []string
	for _, ev := range trace {
		kinds = append(kinds, ev["kind"].(string))
	}
	if got := strings.Join(kinds, " "); got != "execution_started node_started node_completed execution_completed" {
		t.Errorf("trace kinds = %s", got)
	}
	if trace[2]["output"].(map[string]any)["b"] != 2.0 {
		t.Errorf("node output = %v", trace[2]["output"])
	}

	bad, _ := get(t, st, "bad")
	if bad.Status != store.StatusFailed || bad.Error != "node n (transform): boom" {
		t.Errorf("failed execution = %+v", bad)
	}
}

func TestWriter_InterleavedExecutions(t *testing.T) {
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 1)
	a, b := events("a", false), events("b", false)
	for i := range a {
		sink.Emit(a[i])
		sink.Emit(b[i])
	}
	w.Close()
	for _, id := range []string{"a", "b"} {
		e, trace := get(t, st, id)
		if e.Status != store.StatusCompleted || len(trace) != 4 || trace[0]["kind"] != "execution_started" {
			t.Errorf("%s = %+v with %d events", id, e, len(trace))
		}
	}
}

func TestWriter_DropsNodeEventsWhenFull(t *testing.T) {
	st := openStore(t, "f")
	// The goroutine is not running yet, so the queue (room for 2) fills up.
	w := newWriter(st, Options{QueueSize: 2})
	sink := w.Sink("f", 1)
	evs := events("e", false)
	sink.Emit(evs[0]) // started: queued
	sink.Emit(evs[1]) // node: queued, queue now full
	sink.Emit(evs[2]) // node: dropped
	if n := w.dropped.Load(); n != 1 {
		t.Fatalf("dropped = %d, want 1", n)
	}

	// The final event is never dropped: it waits for the writer.
	go w.run()
	sink.Emit(evs[3])
	w.Close()

	e, trace := get(t, st, "e")
	if e.Status != store.StatusCompleted || e.TraceComplete {
		t.Errorf("execution = %+v, want completed with an incomplete trace", e)
	}
	if len(trace) != 3 || trace[2]["kind"] != "execution_completed" {
		t.Errorf("trace = %v, want started, node_started, execution_completed", trace)
	}
}

func TestWriter_TruncatesLargePayloads(t *testing.T) {
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 1)
	big := engine.Payload{"blob": strings.Repeat("x", MaxPayloadBytes)}
	small := engine.Payload{"keep": "me"}
	evs := events("e", false)
	evs[1].Input = big
	evs[2].Input = small
	evs[2].Output = big
	for _, ev := range evs {
		sink.Emit(ev)
	}
	w.Close()

	_, trace := get(t, st, "e")
	input := trace[1]["input"].(map[string]any)
	if input["truncated"] != true || input["bytes"].(float64) <= MaxPayloadBytes {
		t.Errorf("big input = %v, want truncated with its size", input)
	}
	if trace[2]["input"].(map[string]any)["keep"] != "me" {
		t.Errorf("small payload beside a big one was changed: %v", trace[2]["input"])
	}
	if trace[2]["output"].(map[string]any)["truncated"] != true {
		t.Errorf("big output = %v", trace[2]["output"])
	}
}

func TestWriter_Prunes(t *testing.T) {
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 1)
	for i := range 5 {
		for _, ev := range events(fmt.Sprintf("e%d", i), false) {
			ev.Timestamp = ev.Timestamp.Add(time.Duration(i) * time.Second)
			sink.Emit(ev)
		}
	}
	w.Close()

	// A new writer prunes when it starts.
	w = New(st, Options{Keep: 2})
	w.Close()
	page, _, err := st.ListExecutions(context.Background(), "f", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != "e4" || page[1].ID != "e3" {
		t.Errorf("kept %v, want e4 and e3", page)
	}
}

func TestWriter_EmitAfterCloseIsDiscarded(t *testing.T) {
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 1)
	w.Close()
	w.Close() // twice is fine
	for _, ev := range events("late", false) {
		sink.Emit(ev) // must not panic or block
	}
	if _, _, err := st.GetExecution(context.Background(), "late"); err == nil {
		t.Error("an event emitted after Close was written")
	}
}

func TestWriter_WithEngine(t *testing.T) {
	// A real execution through the executor, so the event shapes are the
	// engine's own.
	st := openStore(t, "f")
	w := New(st, Options{})
	sink := w.Sink("f", 1)
	ex := engine.NewExecutor(compiled(t), sink)
	if err := ex.Execute(context.Background(), "t", engine.Payload{"body": map[string]any{"name": "ada"}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	w.Close()

	page, _, _ := st.ListExecutions(context.Background(), "f", 10, "")
	if len(page) != 1 {
		t.Fatalf("executions = %v", page)
	}
	e, trace := get(t, st, page[0].ID)
	if e.Status != store.StatusCompleted || e.Trigger != "t" || len(trace) < 4 {
		t.Errorf("execution = %+v with %d events", e, len(trace))
	}
}

// compiled is an http_trigger (t) feeding a transform.
func compiled(t *testing.T) *engine.CompiledFlow {
	t.Helper()
	fl, err := flow.Parse(strings.NewReader(`{
  "id": "f", "version": "1.0",
  "nodes": [
    {"id": "t", "type": "http_trigger", "config": {"path": "/f"}},
    {"id": "x", "type": "transform", "config": {"expression": "{greeting: 'hello ' + input.body.name}"}}
  ],
  "edges": [{"source": "t", "target": "x"}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	cf, err := engine.Compile(fl, engine.DefaultRegistry, engine.NewEngineServices())
	if err != nil {
		t.Fatal(err)
	}
	return cf
}
