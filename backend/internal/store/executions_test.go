package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 14, 3, 7, 123_000_000, time.UTC)

// writeRun records a finished execution with two trace events, started at
// t0.
func writeRun(t *testing.T, s *Store, flowID, id string, status Status) {
	t.Helper()
	writeRunAt(t, s, flowID, id, status, t0)
}

func writeRunAt(t *testing.T, s *Store, flowID, id string, status Status, started time.Time) {
	t.Helper()
	err := s.WriteBatch(context.Background(), Batch{
		Started: []Execution{{
			ID: id, FlowID: flowID, Revision: 1, Trigger: "node-1",
			Status: StatusRunning, StartedAt: started, TraceComplete: true,
		}},
		Events: []TraceRecord{
			{ExecutionID: id, Seq: 0, Event: json.RawMessage(`{"kind":"execution_started"}`)},
			{ExecutionID: id, Seq: 1, Event: json.RawMessage(`{"kind":"execution_completed"}`)},
		},
		Finished: []Finish{{ID: id, Status: status, FinishedAt: started.Add(4 * time.Millisecond)}},
	})
	if err != nil {
		t.Fatalf("WriteBatch %s: %v", id, err)
	}
}

func TestExecutions_WriteAndGet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))

	// Started in one batch, events and the end in later ones, as the writer
	// will do.
	if err := s.WriteBatch(ctx, Batch{Started: []Execution{{
		ID: "e1", FlowID: "f", Revision: 3, Trigger: "node-1",
		Status: StatusRunning, StartedAt: t0, TraceComplete: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	running, _, err := s.GetExecution(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusRunning || !running.FinishedAt.IsZero() {
		t.Errorf("running = %+v", running)
	}

	if err := s.WriteBatch(ctx, Batch{
		Events: []TraceRecord{
			{ExecutionID: "e1", Seq: 1, Event: json.RawMessage(`{"kind":"node_started"}`)},
			{ExecutionID: "e1", Seq: 0, Event: json.RawMessage(`{"kind":"execution_started"}`)},
		},
		Finished: []Finish{{ID: "e1", Status: StatusFailed, Error: "node node-2: boom", FinishedAt: t0.Add(time.Second)}},
	}); err != nil {
		t.Fatal(err)
	}

	got, trace, err := s.GetExecution(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	want := Execution{
		ID: "e1", FlowID: "f", Revision: 3, Trigger: "node-1",
		Status: StatusFailed, Error: "node node-2: boom",
		StartedAt: t0, FinishedAt: t0.Add(time.Second), TraceComplete: true,
	}
	if got != want {
		t.Errorf("execution = %+v\nwant %+v", got, want)
	}
	if len(trace) != 2 || string(trace[0]) != `{"kind":"execution_started"}` || string(trace[1]) != `{"kind":"node_started"}` {
		t.Errorf("trace = %s, want events in seq order", trace)
	}
}

func TestExecutions_GetUnknown(t *testing.T) {
	s := openTest(t)
	if _, _, err := s.GetExecution(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExecutions_Incomplete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))
	writeRun(t, s, "f", "e1", StatusCompleted)

	if err := s.WriteBatch(ctx, Batch{Incomplete: []string{"e1"}}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetExecution(ctx, "e1")
	if got.TraceComplete {
		t.Error("trace still marked complete")
	}
}

func TestExecutions_WritesForDeletedFlowAreSkipped(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "keep", "K", []byte(`{}`))

	// The flow "gone" was deleted while its writes were queued. The batch
	// still succeeds, and the other flow's writes in it are kept.
	err := s.WriteBatch(ctx, Batch{
		Started: []Execution{
			{ID: "e-gone", FlowID: "gone", Trigger: "t", Status: StatusRunning, StartedAt: t0},
			{ID: "e-keep", FlowID: "keep", Trigger: "t", Status: StatusRunning, StartedAt: t0},
		},
		Events: []TraceRecord{
			{ExecutionID: "e-gone", Seq: 0, Event: json.RawMessage(`{}`)},
			{ExecutionID: "e-keep", Seq: 0, Event: json.RawMessage(`{}`)},
		},
		Finished: []Finish{{ID: "e-gone", Status: StatusCompleted, FinishedAt: t0}},
	})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if _, _, err := s.GetExecution(ctx, "e-gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("execution of a deleted flow was written: %v", err)
	}
	if _, trace, err := s.GetExecution(ctx, "e-keep"); err != nil || len(trace) != 1 {
		t.Errorf("e-keep = %v, %d events; want written with 1 event", err, len(trace))
	}
}

func TestExecutions_FailedBatchWritesNothing(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))
	writeRun(t, s, "f", "e1", StatusCompleted)

	// The duplicate seq fails the batch; the new execution in it must not be
	// written either.
	err := s.WriteBatch(ctx, Batch{
		Started: []Execution{{ID: "e2", FlowID: "f", Trigger: "t", Status: StatusRunning, StartedAt: t0}},
		Events:  []TraceRecord{{ExecutionID: "e1", Seq: 0, Event: json.RawMessage(`{}`)}},
	})
	if err == nil {
		t.Fatal("WriteBatch succeeded with a duplicate event")
	}
	if _, _, err := s.GetExecution(ctx, "e2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("part of a failed batch was written: %v", err)
	}
}

func TestExecutions_ListPages(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))
	s.CreateFlow(ctx, "other", "O", []byte(`{}`))
	// e0..e4, one second apart; e5 and e6 share a start time, so the ID
	// breaks the tie.
	for i := range 5 {
		writeRunAt(t, s, "f", fmt.Sprintf("e%d", i), StatusCompleted, t0.Add(time.Duration(i)*time.Second))
	}
	writeRunAt(t, s, "f", "e5", StatusCompleted, t0.Add(10*time.Second))
	writeRunAt(t, s, "f", "e6", StatusCompleted, t0.Add(10*time.Second))
	writeRun(t, s, "other", "x", StatusCompleted)

	var ids []string
	cursor := ""
	pages := 0
	for {
		page, next, err := s.ListExecutions(ctx, "f", 3, cursor)
		if err != nil {
			t.Fatalf("ListExecutions: %v", err)
		}
		pages++
		for _, e := range page {
			ids = append(ids, e.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if want := "e6 e5 e4 e3 e2 e1 e0"; joinIDs(ids) != want {
		t.Errorf("ids = %v, want %s", ids, want)
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}

	// A last page that is exactly full has no next cursor.
	if _, next, _ := s.ListExecutions(ctx, "f", 7, ""); next != "" {
		t.Errorf("next = %q after a page holding everything", next)
	}
	if page, next, err := s.ListExecutions(ctx, "none", 3, ""); err != nil || page == nil || len(page) != 0 || next != "" {
		t.Errorf("unknown flow = %v, %q, %v; want [], \"\", nil", page, next, err)
	}
}

func TestExecutions_ListBadCursor(t *testing.T) {
	s := openTest(t)
	for _, c := range []string{"!!!", encodeCursor("not a time", "e"), "bm8tc2VwYXJhdG9y"} {
		if _, _, err := s.ListExecutions(context.Background(), "f", 10, c); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v, want ErrBadCursor", c, err)
		}
	}
}

func TestExecutions_MarkInterrupted(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "f", "F", []byte(`{}`))
	writeRun(t, s, "f", "done", StatusCompleted)
	s.WriteBatch(ctx, Batch{Started: []Execution{
		{ID: "cut", FlowID: "f", Trigger: "t", Status: StatusRunning, StartedAt: t0},
	}})

	n, err := s.MarkInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("MarkInterrupted = %d, %v; want 1, nil", n, err)
	}
	if got, _, _ := s.GetExecution(ctx, "cut"); got.Status != StatusInterrupted {
		t.Errorf("cut status = %s", got.Status)
	}
	if got, _, _ := s.GetExecution(ctx, "done"); got.Status != StatusCompleted {
		t.Errorf("done status = %s", got.Status)
	}
}

func TestExecutions_Prune(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.CreateFlow(ctx, "a", "A", []byte(`{}`))
	s.CreateFlow(ctx, "b", "B", []byte(`{}`))
	for i := range 5 {
		writeRunAt(t, s, "a", fmt.Sprintf("a%d", i), StatusCompleted, t0.Add(time.Duration(i)*time.Second))
	}
	writeRun(t, s, "b", "b0", StatusCompleted)

	if n, err := s.Prune(ctx, 0); err != nil || n != 0 {
		t.Fatalf("Prune(0) = %d, %v; want nothing deleted", n, err)
	}
	n, err := s.Prune(ctx, 2)
	if err != nil || n != 3 {
		t.Fatalf("Prune(2) = %d, %v; want 3", n, err)
	}
	page, _, _ := s.ListExecutions(ctx, "a", 10, "")
	var ids []string
	for _, e := range page {
		ids = append(ids, e.ID)
	}
	if joinIDs(ids) != "a4 a3" {
		t.Errorf("kept = %v, want the newest two", ids)
	}
	if _, _, err := s.GetExecution(ctx, "b0"); err != nil {
		t.Errorf("other flow's execution pruned: %v", err)
	}
	var events int
	s.db.QueryRow("SELECT count(*) FROM trace_events").Scan(&events)
	if events != 3*2 {
		t.Errorf("trace events = %d, want 6 (pruned executions' events deleted)", events)
	}
}

func TestTimeFormat(t *testing.T) {
	got := formatTime(time.Date(2026, 10, 31, 15, 3, 7, 123_456_789, time.FixedZone("CET", 3600)))
	if want := "2026-10-31T14:03:07.123Z"; got != want {
		t.Errorf("formatTime = %s, want %s", got, want)
	}
	back, err := parseTime(got)
	if err != nil || !back.Equal(time.Date(2026, 10, 31, 14, 3, 7, 123_000_000, time.UTC)) {
		t.Errorf("parseTime = %v, %v", back, err)
	}
}
