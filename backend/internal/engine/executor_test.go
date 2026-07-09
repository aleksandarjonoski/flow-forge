package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// spyAction is an ActionNode test double whose Execute behavior is supplied by
// a closure. It records every input it receives so tests can assert on data
// flow. Register it via spyFactory, which hands the registry the same instance
// each time so the test can inspect it after execution.
type spyAction struct {
	fakeNode
	mu     sync.Mutex
	inputs []Payload
	exec   func(ec *ExecutionContext, input Payload) (Payload, error)
}

func (a *spyAction) Execute(ec *ExecutionContext, input Payload) (Payload, error) {
	a.mu.Lock()
	// Snapshot the input so later mutation elsewhere can't corrupt the record.
	a.inputs = append(a.inputs, maps.Clone(input))
	a.mu.Unlock()
	return a.exec(ec, input)
}

func (a *spyAction) lastInput() Payload {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.inputs) == 0 {
		return nil
	}
	return a.inputs[len(a.inputs)-1]
}

func (a *spyAction) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.inputs)
}

func spyFactory(a *spyAction) NodeFactory {
	return func() Node { return a }
}

// passThrough returns an exec func that emits the given output, ignoring input.
func returns(out Payload) func(*ExecutionContext, Payload) (Payload, error) {
	return func(*ExecutionContext, Payload) (Payload, error) { return out, nil }
}

// kinds extracts the ordered list of trace kinds from a memory sink.
func kinds(sink *MemoryTraceSink) []TraceKind {
	evs := sink.Events()
	out := make([]TraceKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func TestExecute_HappyPath_LinearChain(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))

	a := &spyAction{fakeNode: fakeNode{typeName: "act_a"}, exec: returns(Payload{"step": "a"})}
	b := &spyAction{fakeNode: fakeNode{typeName: "act_b"}, exec: returns(Payload{"step": "b"})}
	reg.Register("act_a", spyFactory(a))
	reg.Register("act_b", spyFactory(b))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "act_a"},
			{ID: "b", Type: "act_b"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "b"},
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sink := NewMemoryTraceSink()
	ex := NewExecutor(cf, sink)
	trigPayload := Payload{"from": "trigger"}
	if err := ex.Execute(context.Background(), "t", trigPayload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// First action receives the trigger's emitted payload.
	if got := a.lastInput()["from"]; got != "trigger" {
		t.Errorf("action a input = %v, want trigger payload", a.lastInput())
	}
	// Second action receives the first action's output.
	if got := b.lastInput()["step"]; got != "a" {
		t.Errorf("action b input = %v, want {step:a}", b.lastInput())
	}

	want := []TraceKind{
		TraceExecutionStarted,
		TraceNodeStarted, TraceNodeCompleted, // a
		TraceNodeStarted, TraceNodeCompleted, // b
		TraceExecutionCompleted,
	}
	if got := kinds(sink); !slices.Equal(got, want) {
		t.Errorf("trace kinds = %v, want %v", got, want)
	}

	// Every event in one execution shares the same non-empty ExecutionID.
	execID := sink.Events()[0].ExecutionID
	if execID == "" {
		t.Fatal("ExecutionID is empty")
	}
	for _, e := range sink.Events() {
		if e.ExecutionID != execID {
			t.Errorf("event %s has ExecutionID %q, want %q", e.Kind, e.ExecutionID, execID)
		}
	}
}

func TestExecute_HaltOnError(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))

	boom := errors.New("kaboom")
	a := &spyAction{fakeNode: fakeNode{typeName: "act_a"}, exec: returns(Payload{"step": "a"})}
	bad := &spyAction{fakeNode: fakeNode{typeName: "act_bad"}, exec: func(*ExecutionContext, Payload) (Payload, error) {
		return nil, boom
	}}
	downstream := &spyAction{fakeNode: fakeNode{typeName: "act_c"}, exec: returns(nil)}
	reg.Register("act_a", spyFactory(a))
	reg.Register("act_bad", spyFactory(bad))
	reg.Register("act_c", spyFactory(downstream))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "act_a"},
			{ID: "bad", Type: "act_bad"},
			{ID: "c", Type: "act_c"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "bad"},
			{Source: "bad", Target: "c"},
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sink := NewMemoryTraceSink()
	ex := NewExecutor(cf, sink)
	err = ex.Execute(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// §7: error is wrapped as "node <id> (<type>): <cause>" and unwraps to cause.
	if !errors.Is(err, boom) {
		t.Errorf("errors.Is(err, boom) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "node bad (act_bad)") {
		t.Errorf("error missing node context: %v", err)
	}
	// Downstream node must not run (halt-on-first-error).
	if downstream.callCount() != 0 {
		t.Errorf("downstream ran %d times, want 0", downstream.callCount())
	}

	want := []TraceKind{
		TraceExecutionStarted,
		TraceNodeStarted, TraceNodeCompleted, // a
		TraceNodeStarted, TraceNodeFailed, // bad
		TraceExecutionFailed,
	}
	if got := kinds(sink); !slices.Equal(got, want) {
		t.Errorf("trace kinds = %v, want %v", got, want)
	}
}

func TestExecute_PanicRecovered(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	p := &spyAction{fakeNode: fakeNode{typeName: "act_panic"}, exec: func(*ExecutionContext, Payload) (Payload, error) {
		panic("held wrong")
	}}
	reg.Register("act_panic", spyFactory(p))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "p", Type: "act_panic"},
		},
		Edges: []flow.Edge{{Source: "t", Target: "p"}},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sink := NewMemoryTraceSink()
	ex := NewExecutor(cf, sink)
	err = ex.Execute(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if !strings.Contains(err.Error(), "panic: held wrong") {
		t.Errorf("error = %v, want to contain panic value", err)
	}
	if got := kinds(sink); !slices.Equal(got, []TraceKind{
		TraceExecutionStarted, TraceNodeStarted, TraceNodeFailed, TraceExecutionFailed,
	}) {
		t.Errorf("trace kinds = %v", got)
	}
}

func TestExecute_Cancellation(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	a := &spyAction{fakeNode: fakeNode{typeName: "act_a"}, exec: returns(nil)}
	reg.Register("act_a", spyFactory(a))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "act_a"},
		},
		Edges: []flow.Edge{{Source: "t", Target: "a"}},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before we start

	sink := NewMemoryTraceSink()
	ex := NewExecutor(cf, sink)
	if err := ex.Execute(ctx, "t", nil); err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if a.callCount() != 0 {
		t.Errorf("action ran %d times despite cancellation, want 0", a.callCount())
	}
}

func TestExecute_UnknownTrigger(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	f := &flow.Flow{
		ID:    "flow-1",
		Nodes: []flow.Node{{ID: "t", Type: "trigger_a"}},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ex := NewExecutor(cf, nil) // nil sink → no-op, must not panic
	if err := ex.Execute(context.Background(), "does-not-exist", nil); err == nil {
		t.Fatal("expected error for unknown trigger, got nil")
	}
}

func TestExecute_Branching(t *testing.T) {
	// t → a → {b, c}: both branches receive a's output.
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	a := &spyAction{fakeNode: fakeNode{typeName: "act_a"}, exec: returns(Payload{"from": "a"})}
	b := &spyAction{fakeNode: fakeNode{typeName: "act_b"}, exec: returns(nil)}
	c := &spyAction{fakeNode: fakeNode{typeName: "act_c"}, exec: returns(nil)}
	reg.Register("act_a", spyFactory(a))
	reg.Register("act_b", spyFactory(b))
	reg.Register("act_c", spyFactory(c))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "act_a"},
			{ID: "b", Type: "act_b"},
			{ID: "c", Type: "act_c"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "b"},
			{Source: "a", Target: "c"},
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	ex := NewExecutor(cf, NewMemoryTraceSink())
	if err := ex.Execute(context.Background(), "t", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if b.lastInput()["from"] != "a" {
		t.Errorf("branch b input = %v, want a's output", b.lastInput())
	}
	if c.lastInput()["from"] != "a" {
		t.Errorf("branch c input = %v, want a's output", c.lastInput())
	}
}

func TestExecute_NodeResultsAccumulate(t *testing.T) {
	// c reads a non-predecessor upstream result via ctx.NodeResults.
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	a := &spyAction{fakeNode: fakeNode{typeName: "act_a"}, exec: returns(Payload{"id": 42})}
	b := &spyAction{fakeNode: fakeNode{typeName: "act_b"}, exec: returns(Payload{"noise": true})}
	var seen Payload
	c := &spyAction{fakeNode: fakeNode{typeName: "act_c"}, exec: func(ec *ExecutionContext, _ Payload) (Payload, error) {
		seen = ec.NodeResults["a"] // reach past immediate predecessor (b)
		return nil, nil
	}}
	reg.Register("act_a", spyFactory(a))
	reg.Register("act_b", spyFactory(b))
	reg.Register("act_c", spyFactory(c))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "act_a"},
			{ID: "b", Type: "act_b"},
			{ID: "c", Type: "act_c"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "b"},
			{Source: "b", Target: "c"},
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ex := NewExecutor(cf, nil)
	if err := ex.Execute(context.Background(), "t", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if seen == nil || seen["id"] != 42 {
		t.Errorf("NodeResults[a] seen by c = %v, want {id:42}", seen)
	}
}

func TestExecute_ConcurrentExecutionsIndependent(t *testing.T) {
	// Fire the same subgraph many times concurrently; each execution must get
	// a distinct ExecutionID and not race. Run under -race to catch sharing.
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	// Stateless echo action shared across all executions.
	echo := &spyAction{fakeNode: fakeNode{typeName: "act_echo"}, exec: func(_ *ExecutionContext, in Payload) (Payload, error) {
		return in, nil
	}}
	reg.Register("act_echo", spyFactory(echo))

	f := &flow.Flow{
		ID: "flow-1",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "e", Type: "act_echo"},
		},
		Edges: []flow.Edge{{Source: "t", Target: "e"}},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sink := NewMemoryTraceSink()
	ex := NewExecutor(cf, sink)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := ex.Execute(context.Background(), "t", Payload{"i": i}); err != nil {
				t.Errorf("Execute #%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	ids := map[string]struct{}{}
	for _, e := range sink.Events() {
		if e.Kind == TraceExecutionStarted {
			ids[e.ExecutionID] = struct{}{}
		}
	}
	if len(ids) != n {
		t.Errorf("distinct ExecutionIDs = %d, want %d", len(ids), n)
	}
}
