package engine

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTraceEvent_JSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	e := TraceEvent{
		Kind:        TraceNodeCompleted,
		ExecutionID: "exec-1",
		NodeID:      "node-2",
		NodeType:    "transform",
		Timestamp:   ts,
		DurationNs:  150 * time.Millisecond,
		// JSON unmarshals numbers as float64, so use float64 here so the
		// round-trip comparison succeeds without type-checking gymnastics.
		Input:  Payload{"a": float64(1)},
		Output: Payload{"b": float64(2)},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back TraceEvent
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Kind != e.Kind {
		t.Errorf("Kind = %v, want %v", back.Kind, e.Kind)
	}
	if back.NodeID != e.NodeID {
		t.Errorf("NodeID = %q, want %q", back.NodeID, e.NodeID)
	}
	if back.DurationNs != e.DurationNs {
		t.Errorf("DurationNs = %v, want %v", back.DurationNs, e.DurationNs)
	}
	if back.Input["a"] != e.Input["a"] {
		t.Errorf("Input[a] = %v, want %v", back.Input["a"], e.Input["a"])
	}
}

func TestTraceEvent_OmitemptyOnExecutionLevel(t *testing.T) {
	e := TraceEvent{
		Kind:        TraceExecutionStarted,
		ExecutionID: "exec-1",
		Timestamp:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Execution-level events should not carry node-level fields.
	for _, field := range []string{"nodeId", "nodeType", "durationNs", "input", "output", "error"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Errorf("expected %q to be omitted; got: %s", field, data)
		}
	}
}

func TestWriterTraceSink_EmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	sink := NewWriterTraceSink(&buf)
	sink.Emit(TraceEvent{Kind: TraceExecutionStarted, ExecutionID: "a"})
	sink.Emit(TraceEvent{Kind: TraceExecutionCompleted, ExecutionID: "a"})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got := len(lines); got != 2 {
		t.Fatalf("got %d lines, want 2; output:\n%s", got, buf.String())
	}
	for i, line := range lines {
		var e TraceEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d not valid JSON: %v (%q)", i, err, line)
		}
	}
}

func TestWriterTraceSink_ConcurrentEmits(t *testing.T) {
	var buf bytes.Buffer
	sink := NewWriterTraceSink(&buf)
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.Emit(TraceEvent{Kind: TraceNodeStarted, ExecutionID: "e", NodeID: "node"})
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got := len(lines); got != n {
		t.Fatalf("got %d lines, want %d", got, n)
	}
	// Every line must be valid JSON — proves writes aren't interleaved.
	for _, line := range lines {
		var e TraceEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("invalid JSON under concurrency: %v (%q)", err, line)
		}
	}
}

func TestMemoryTraceSink_CapturesEvents(t *testing.T) {
	sink := NewMemoryTraceSink()
	sink.Emit(TraceEvent{Kind: TraceExecutionStarted, ExecutionID: "a"})
	sink.Emit(TraceEvent{Kind: TraceNodeStarted, ExecutionID: "a", NodeID: "n1"})
	sink.Emit(TraceEvent{Kind: TraceNodeCompleted, ExecutionID: "a", NodeID: "n1"})

	events := sink.Events()
	if got := len(events); got != 3 {
		t.Fatalf("Events() len = %d, want 3", got)
	}
	if events[0].Kind != TraceExecutionStarted {
		t.Errorf("Events[0].Kind = %v", events[0].Kind)
	}
	if events[2].NodeID != "n1" {
		t.Errorf("Events[2].NodeID = %q", events[2].NodeID)
	}
}

func TestMemoryTraceSink_EventsIsCopy(t *testing.T) {
	sink := NewMemoryTraceSink()
	sink.Emit(TraceEvent{Kind: TraceNodeStarted, ExecutionID: "a"})

	snap := sink.Events()
	sink.Emit(TraceEvent{Kind: TraceNodeCompleted, ExecutionID: "a"})

	if len(snap) != 1 {
		t.Errorf("snapshot mutated after a later Emit: got len=%d, want 1", len(snap))
	}
}

func TestMemoryTraceSink_Reset(t *testing.T) {
	sink := NewMemoryTraceSink()
	sink.Emit(TraceEvent{Kind: TraceNodeStarted})
	sink.Reset()
	if got := len(sink.Events()); got != 0 {
		t.Errorf("after Reset, Events() len = %d, want 0", got)
	}
}

func TestMultiSink_FansOut(t *testing.T) {
	a := NewMemoryTraceSink()
	b := NewMemoryTraceSink()
	multi := NewMultiSink(a, b)

	multi.Emit(TraceEvent{Kind: TraceExecutionStarted, ExecutionID: "e"})
	multi.Emit(TraceEvent{Kind: TraceExecutionCompleted, ExecutionID: "e"})

	if got := len(a.Events()); got != 2 {
		t.Errorf("sink a got %d events, want 2", got)
	}
	if got := len(b.Events()); got != 2 {
		t.Errorf("sink b got %d events, want 2", got)
	}
}

func TestMultiSink_PreservesOrder(t *testing.T) {
	mem := NewMemoryTraceSink()
	multi := NewMultiSink(mem)
	want := []TraceKind{TraceExecutionStarted, TraceNodeStarted, TraceNodeCompleted, TraceExecutionCompleted}
	for _, k := range want {
		multi.Emit(TraceEvent{Kind: k, ExecutionID: "e"})
	}
	got := mem.Events()
	for i, e := range got {
		if e.Kind != want[i] {
			t.Errorf("Events[%d].Kind = %v, want %v", i, e.Kind, want[i])
		}
	}
}

func TestRunLogSink(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	s := NewRunLogSink(logger, slog.String("flow", "orders"), slog.Int64("revision", 3))

	s.Emit(TraceEvent{Kind: TraceExecutionStarted, ExecutionID: "ok", NodeID: "trig"})
	s.Emit(TraceEvent{Kind: TraceExecutionStarted, ExecutionID: "bad", NodeID: "cron"})
	s.Emit(TraceEvent{Kind: TraceNodeCompleted, ExecutionID: "ok", NodeID: "n"}) // not logged
	s.Emit(TraceEvent{Kind: TraceExecutionCompleted, ExecutionID: "ok", DurationNs: 12 * time.Millisecond})
	s.Emit(TraceEvent{Kind: TraceExecutionFailed, ExecutionID: "bad", DurationNs: 4 * time.Millisecond, Err: "node n (transform): boom"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records, want 2:\n%s", len(lines), buf.String())
	}
	var ok, bad map[string]any
	json.Unmarshal([]byte(lines[0]), &ok)
	json.Unmarshal([]byte(lines[1]), &bad)

	want := map[string]any{
		"level": "INFO", "msg": "execution", "flow": "orders", "revision": 3.0,
		"execution": "ok", "trigger": "trig", "status": "completed", "duration": float64(12 * time.Millisecond),
	}
	for k, v := range want {
		if ok[k] != v {
			t.Errorf("completed record %s = %v, want %v", k, ok[k], v)
		}
	}
	if _, has := ok["error"]; has {
		t.Error("completed record has an error")
	}
	if bad["level"] != "ERROR" || bad["status"] != "failed" || bad["trigger"] != "cron" || bad["error"] != "node n (transform): boom" {
		t.Errorf("failed record = %v", bad)
	}
	if len(s.triggers) != 0 {
		t.Errorf("finished executions not forgotten: %v", s.triggers)
	}
}
