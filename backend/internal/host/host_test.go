package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

// doc returns a flow: an http_trigger on POST path, then a transform with the
// given expression.
func doc(id, path, expression string) []byte {
	return fmt.Appendf(nil, `{
  "id": %q,
  "version": "1.0",
  "nodes": [
    {"id": "t", "type": "http_trigger", "position": {"x": 0, "y": 0}, "config": {"path": %q}},
    {"id": "x", "type": "transform", "position": {"x": 0, "y": 0}, "config": {"expression": %q}}
  ],
  "edges": [{"id": "e", "source": "t", "target": "x"}]
}`, id, path, expression)
}

const okExpr = "{ok: true}"

// sinks records the sink handed to each started revision.
type sinks struct {
	mu  sync.Mutex
	got map[string]*engine.MemoryTraceSink // "flow@revision" → sink
}

func (s *sinks) fn(flowID string, revision int64) engine.TraceSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	sink := engine.NewMemoryTraceSink()
	s.got[fmt.Sprintf("%s@%d", flowID, revision)] = sink
	return sink
}

func (s *sinks) get(key string) *engine.MemoryTraceSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got[key]
}

type fixture struct {
	dir   string
	store *store.Store
	host  *Host
	sinks *sinks
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{dir: t.TempDir(), sinks: &sinks{got: map[string]*engine.MemoryTraceSink{}}}
	fx.open(t)
	return fx
}

// open opens the store and a new host on fx.dir, closing both at cleanup.
func (fx *fixture) open(t *testing.T) {
	t.Helper()
	st, err := store.Open(fx.dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	fx.store = st
	fx.host = New(st, engine.DefaultRegistry, Options{Sink: fx.sinks.fn})
	t.Cleanup(func() {
		fx.host.Shutdown()
		st.Close()
	})
}

func (fx *fixture) create(t *testing.T, id string, document []byte) {
	t.Helper()
	if _, err := fx.store.CreateFlow(context.Background(), id, id, document); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
}

func (fx *fixture) save(t *testing.T, id string, document []byte, revision int64) {
	t.Helper()
	if _, err := fx.store.SaveFlow(context.Background(), id, id, document, revision); err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}
}

func (fx *fixture) start(t *testing.T, id string) Status {
	t.Helper()
	st, err := fx.host.Start(context.Background(), id)
	if err != nil {
		t.Fatalf("Start %s: %v", id, err)
	}
	return st
}

// post sends POST path to the flows mux and returns the status code.
func (fx *fixture) post(path string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	fx.host.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func (fx *fixture) enabled(t *testing.T, id string) bool {
	t.Helper()
	f, err := fx.store.GetFlow(context.Background(), id)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	return f.Enabled
}

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

func TestStart_RunsFlow(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/orders", okExpr))

	st := fx.start(t, "orders")
	if st != (Status{State: Running, RunningRevision: 1}) {
		t.Errorf("status = %+v, want running revision 1", st)
	}
	if !fx.enabled(t, "orders") {
		t.Error("flow not marked deployed")
	}
	if code := fx.post("/orders"); code != http.StatusAccepted {
		t.Fatalf("POST /orders = %d, want 202", code)
	}
	sink := fx.sinks.get("orders@1")
	if sink == nil {
		t.Fatal("no sink for orders@1")
	}
	waitFor(t, "the execution", func() bool {
		evs := sink.Events()
		return len(evs) > 0 && evs[len(evs)-1].Kind == engine.TraceExecutionCompleted
	})
}

func TestStart_Unknown(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.host.Start(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
}

func TestStart_CompileErrorChangesNothing(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/orders", okExpr))
	fx.start(t, "orders")

	// Revision 2 has a broken expression.
	fx.save(t, "orders", doc("orders", "/orders", "{ok: "), 1)
	st, err := fx.host.Start(context.Background(), "orders")
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *CompileError", err)
	}
	if st != (Status{State: Running, RunningRevision: 1}) {
		t.Errorf("status = %+v, want revision 1 still running", st)
	}
	if code := fx.post("/orders"); code != http.StatusAccepted {
		t.Errorf("POST /orders = %d, want 202 from revision 1", code)
	}

	// A never-started broken flow stays stopped and undeployed.
	fx.create(t, "broken", doc("broken", "/broken", "{ok: "))
	if _, err := fx.host.Start(context.Background(), "broken"); !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *CompileError", err)
	}
	if st := fx.host.Status("broken"); st.State != Stopped {
		t.Errorf("broken status = %+v, want stopped", st)
	}
	if fx.enabled(t, "broken") {
		t.Error("broken flow marked deployed")
	}
}

func TestStart_RedeploysNewRevision(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/v1", okExpr))
	fx.start(t, "orders")

	fx.save(t, "orders", doc("orders", "/v2", okExpr), 1)
	if st := fx.host.Status("orders"); st.RunningRevision != 1 {
		t.Errorf("saving changed the running revision: %+v", st)
	}
	st := fx.start(t, "orders")
	if st != (Status{State: Running, RunningRevision: 2}) {
		t.Errorf("status = %+v, want running revision 2", st)
	}
	if code := fx.post("/v1"); code != http.StatusNotFound {
		t.Errorf("old route = %d, want 404", code)
	}
	if code := fx.post("/v2"); code != http.StatusAccepted {
		t.Errorf("new route = %d, want 202", code)
	}
}

func TestStart_RouteConflict(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "first", doc("first", "/orders", okExpr))
	fx.create(t, "second", doc("second", "/orders", okExpr))
	fx.start(t, "first")

	st := fx.start(t, "second")
	if st.State != Failed || !strings.Contains(st.Error, "route POST /orders is used by flow first") {
		t.Errorf("second = %+v, want failed naming flow first", st)
	}
	if !fx.enabled(t, "second") {
		t.Error("failed flow should stay deployed, so a fix and redeploy is enough")
	}
	if fx.host.Status("first").State != Running {
		t.Error("first flow was disturbed")
	}
	if code := fx.post("/orders"); code != http.StatusAccepted {
		t.Errorf("POST /orders = %d, want 202 from the first flow", code)
	}

	// Once the first flow stops, the second can start.
	if err := fx.host.Stop(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if st := fx.start(t, "second"); st.State != Running {
		t.Errorf("second after first stopped = %+v", st)
	}
}

func TestStop(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/orders", okExpr))
	fx.start(t, "orders")

	if err := fx.host.Stop(context.Background(), "orders"); err != nil {
		t.Fatal(err)
	}
	if st := fx.host.Status("orders"); st != (Status{State: Stopped}) {
		t.Errorf("status = %+v, want stopped", st)
	}
	if fx.enabled(t, "orders") {
		t.Error("still marked deployed")
	}
	if code := fx.post("/orders"); code != http.StatusNotFound {
		t.Errorf("POST /orders = %d, want 404", code)
	}
	// Stopping a stopped flow is fine; an unknown one is not.
	if err := fx.host.Stop(context.Background(), "orders"); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	if err := fx.host.Stop(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown flow err = %v, want store.ErrNotFound", err)
	}
}

func TestDelete_StopsRunningFlow(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/orders", okExpr))
	fx.start(t, "orders")

	if err := fx.host.Delete(context.Background(), "orders"); err != nil {
		t.Fatal(err)
	}
	if code := fx.post("/orders"); code != http.StatusNotFound {
		t.Errorf("POST /orders = %d, want 404", code)
	}
	if _, err := fx.store.GetFlow(context.Background(), "orders"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("flow still stored: %v", err)
	}
	if err := fx.host.Delete(context.Background(), "orders"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second Delete err = %v, want store.ErrNotFound", err)
	}
}

func TestBoot_StartsDeployedFlows(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	fx.create(t, "good", doc("good", "/good", okExpr))
	fx.create(t, "idle", doc("idle", "/idle", okExpr))
	fx.create(t, "broken", doc("broken", "/broken", "{ok: "))
	fx.start(t, "good")
	fx.store.SetEnabled(ctx, "broken", true) // deployed, then broken by hand
	// An execution the previous process never finished.
	fx.store.WriteBatch(ctx, store.Batch{Started: []store.Execution{{
		ID: "cut", FlowID: "good", Trigger: "t", Status: store.StatusRunning, StartedAt: time.Now(),
	}}})

	// Restart: shut down and open a new host on the same data directory.
	if err := fx.host.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if code := fx.post("/good"); code != http.StatusNotFound {
		t.Errorf("after Shutdown POST /good = %d, want 404", code)
	}
	if !fx.enabled(t, "good") {
		t.Error("Shutdown undeployed the flow")
	}
	fx.store.Close()
	fx.open(t)

	res, err := fx.host.Boot(ctx)
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if res != (BootResult{Started: 1, Failed: 1, Interrupted: 1}) {
		t.Errorf("Boot = %+v, want 1 started, 1 failed, 1 interrupted", res)
	}
	if code := fx.post("/good"); code != http.StatusAccepted {
		t.Errorf("POST /good = %d, want 202", code)
	}
	if st := fx.host.Status("idle"); st.State != Stopped {
		t.Errorf("idle = %+v, want stopped", st)
	}
	if st := fx.host.Status("broken"); st.State != Failed || st.Error == "" {
		t.Errorf("broken = %+v, want failed with the compile error", st)
	}
	if e, _, _ := fx.store.GetExecution(ctx, "cut"); e.Status != store.StatusInterrupted {
		t.Errorf("cut execution = %s, want interrupted", e.Status)
	}
}

func TestStop_WaitsForInflightExecution(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "orders", doc("orders", "/orders", okExpr))
	fx.start(t, "orders")
	if code := fx.post("/orders"); code != http.StatusAccepted {
		t.Fatalf("POST = %d", code)
	}
	// Stop returns only after the execution has finished, so its last event
	// is already in the sink.
	if err := fx.host.Stop(context.Background(), "orders"); err != nil {
		t.Fatal(err)
	}
	evs := fx.sinks.get("orders@1").Events()
	if len(evs) == 0 {
		t.Fatal("no trace events")
	}
	if k := evs[len(evs)-1].Kind; k != engine.TraceExecutionCompleted && k != engine.TraceExecutionFailed {
		t.Errorf("last event = %s, want the execution's end", k)
	}
}
