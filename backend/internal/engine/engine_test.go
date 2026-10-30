package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// ctlTrigger is a TriggerNode the test drives by hand: Start hands its
// Emitter to the test over emits, and Stop is counted.
type ctlTrigger struct {
	fakeNode
	startErr error
	stopErr  error
	emits    chan Emitter
	stops    atomic.Int32
}

func newCtlTrigger(typeName string) *ctlTrigger {
	return &ctlTrigger{fakeNode: fakeNode{typeName: typeName}, emits: make(chan Emitter, 1)}
}

func (c *ctlTrigger) Start(_ context.Context, emit Emitter) error {
	if c.startErr != nil {
		return c.startErr
	}
	c.emits <- emit
	return nil
}

func (c *ctlTrigger) Stop() error {
	c.stops.Add(1)
	return c.stopErr
}

// runFixture compiles t → a with the given trigger and action and starts Run
// in the background. done is closed when Run returns; err then holds its
// result.
type runFixture struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	sink   *MemoryTraceSink
}

// wait blocks until Run returns and yields its error.
func (fx *runFixture) wait(t *testing.T) error {
	t.Helper()
	receive(t, fx.done, "Run to return")
	return fx.err
}

func startRun(t *testing.T, trig *ctlTrigger, act *spyAction) *runFixture {
	t.Helper()
	reg := NewRegistry()
	reg.Register(trig.typeName, func() Node { return trig })
	reg.Register(act.typeName, spyFactory(act))
	f := &flow.Flow{
		ID:    "flow-run",
		Nodes: []flow.Node{{ID: "t", Type: trig.typeName}, {ID: "a", Type: act.typeName}},
		Edges: []flow.Edge{{Source: "t", Target: "a"}},
	}
	svc := NewEngineServices()
	cf, err := Compile(f, reg, svc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fx := &runFixture{cancel: cancel, done: make(chan struct{}), sink: NewMemoryTraceSink()}
	go func() {
		fx.err = Run(ctx, cf, svc, fx.sink)
		close(fx.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-fx.done
	})
	return fx
}

// receive returns the next value from ch, failing the test after a timeout.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// waitFor polls cond until it holds, failing the test after a timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRun_EmitStartsExecution(t *testing.T) {
	trig := newCtlTrigger("ctl")
	act := &spyAction{fakeNode: fakeNode{typeName: "act"}, exec: returns(Payload{"ok": true})}
	fx := startRun(t, trig, act)

	emit := receive(t, trig.emits, "trigger Start")
	if err := emit(Payload{"n": 1}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	waitFor(t, "execution to complete", func() bool {
		evs := fx.sink.Events()
		return len(evs) > 0 && evs[len(evs)-1].Kind == TraceExecutionCompleted
	})
	if got := act.lastInput()["n"]; got != 1 {
		t.Errorf("action input = %v, want trigger payload", act.lastInput())
	}

	fx.cancel()
	if err := fx.wait(t); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	if n := trig.stops.Load(); n != 1 {
		t.Errorf("Stop called %d times, want 1", n)
	}
	if err := emit(Payload{}); !errors.Is(err, ErrEngineStopped) {
		t.Errorf("emit after shutdown = %v, want ErrEngineStopped", err)
	}
}

func TestRun_ShutdownWaitsForInflight(t *testing.T) {
	trig := newCtlTrigger("ctl")
	entered := make(chan struct{})
	release := make(chan struct{})
	act := &spyAction{fakeNode: fakeNode{typeName: "act"}, exec: func(*ExecutionContext, Payload) (Payload, error) {
		close(entered)
		<-release
		return nil, nil
	}}
	fx := startRun(t, trig, act)

	emit := receive(t, trig.emits, "trigger Start")
	if err := emit(nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	receive(t, entered, "action to start")

	fx.cancel()
	select {
	case <-fx.done:
		t.Fatalf("Run returned (%v) while an execution was in flight", fx.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := fx.wait(t); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

func TestRun_StopErrorsReturned(t *testing.T) {
	trig := newCtlTrigger("ctl")
	trig.stopErr = errors.New("stuck")
	fx := startRun(t, trig, &spyAction{fakeNode: fakeNode{typeName: "act"}, exec: returns(nil)})

	receive(t, trig.emits, "trigger Start")
	fx.cancel()
	err := fx.wait(t)
	if err == nil || !strings.Contains(err.Error(), "stop trigger t (ctl): stuck") {
		t.Errorf("Run = %v, want the Stop error", err)
	}
}

func TestRun_StartFailureStopsStartedTriggers(t *testing.T) {
	good := newCtlTrigger("good")
	bad := newCtlTrigger("bad")
	bad.startErr = errors.New("port taken")
	reg := NewRegistry()
	reg.Register("good", func() Node { return good })
	reg.Register("bad", func() Node { return bad })
	reg.Register("act", newActionFactory("act"))
	// Compile sorts triggers by ID, so "t1" (good) starts before "t2" (bad).
	f := &flow.Flow{
		Nodes: []flow.Node{{ID: "t1", Type: "good"}, {ID: "t2", Type: "bad"}, {ID: "a", Type: "act"}},
		Edges: []flow.Edge{{Source: "t1", Target: "a"}},
	}
	svc := NewEngineServices()
	cf, err := Compile(f, reg, svc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Run must return on its own; the context is never cancelled.
	err = Run(context.Background(), cf, svc, nil)
	if err == nil || !strings.Contains(err.Error(), "start trigger t2 (bad): port taken") {
		t.Errorf("Run = %v, want the Start error", err)
	}
	if n := good.stops.Load(); n != 1 {
		t.Errorf("started trigger stopped %d times, want 1", n)
	}
	if n := bad.stops.Load(); n != 0 {
		t.Errorf("failed trigger stopped %d times, want 0", n)
	}
}

// TestRun_StartsCronScheduler waits for a real cron tick (~1s).
func TestRun_StartsCronScheduler(t *testing.T) {
	reg := NewRegistry()
	reg.Register("trigger_a", newTriggerFactory("trigger_a"))
	f := &flow.Flow{Nodes: []flow.Node{{ID: "t", Type: "trigger_a"}}}
	svc := NewEngineServices()
	cf, err := Compile(f, reg, svc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ticked := make(chan struct{}, 1)
	if _, err := svc.Cron.AddFunc("@every 1s", func() {
		select {
		case ticked <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("AddFunc: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cf, svc, nil) }()

	receive(t, ticked, "cron tick")
	cancel()
	if err := receive(t, done, "Run to return"); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}
