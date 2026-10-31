// Package host runs the stored flows of a FlowForge server: one engine
// instance per deployed flow, all sharing one trigger mux. See
// docs/server-v1.md §4.
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

// State is where a stored flow stands (server-v1.md §4.1).
type State string

const (
	// Stopped means not deployed.
	Stopped State = "stopped"
	// Running means deployed and running.
	Running State = "running"
	// Failed means deployed, but it could not start.
	Failed State = "failed"
)

// Status is a flow's state as the editor shows it.
type Status struct {
	State State
	// RunningRevision is the revision that runs; 0 unless Running.
	RunningRevision int64
	// Error says why the flow is Failed.
	Error string
}

// CompileError is returned by Start when the latest revision does not parse
// or compile. Nothing was changed: a running older revision keeps running.
type CompileError struct {
	Err error
}

func (e *CompileError) Error() string { return "flow does not compile: " + e.Err.Error() }
func (e *CompileError) Unwrap() error { return e.Err }

// SinkFunc returns the trace sink for one running revision of a flow. A nil
// sink discards trace events.
type SinkFunc func(flowID string, revision int64) engine.TraceSink

// Options configures a Host.
type Options struct {
	// Sink gives each started flow its trace sink. Nil discards traces.
	Sink SinkFunc
}

// Host runs flows. Lifecycle changes (start, stop, delete) are serialized;
// Status and serving the mux are safe at any time.
type Host struct {
	store *store.Store
	reg   *engine.Registry
	mux   *engine.HTTPMux
	sink  SinkFunc

	// ops serializes lifecycle changes, which may wait for executions to end.
	ops sync.Mutex
	// mu guards flows; it is never held while waiting.
	mu    sync.RWMutex
	flows map[string]*entry
}

// entry is a flow the host has tried to run: running, or failed to start.
type entry struct {
	state    State
	inst     *engine.Instance
	revision int64
	err      string
}

// New returns a Host for the flows in st, compiled against reg. Nothing runs
// until Boot or Start.
func New(st *store.Store, reg *engine.Registry, opts Options) *Host {
	return &Host{
		store: st,
		reg:   reg,
		mux:   engine.NewHTTPMux(),
		sink:  opts.Sink,
		flows: make(map[string]*entry),
	}
}

// Handler serves the http_trigger routes of every running flow. Mount it on
// the flows listener.
func (h *Host) Handler() http.Handler { return h.mux }

// BootResult summarizes Boot.
type BootResult struct {
	Started, Failed int
	// Interrupted is how many executions were left running by the previous
	// server process.
	Interrupted int64
}

// Boot prepares a freshly started server (server-v1.md §4.5): executions
// still marked running were cut off by a crash or kill and become
// interrupted, then every deployed flow is started. A flow that fails to
// start is Failed; the others still start.
func (h *Host) Boot(ctx context.Context) (BootResult, error) {
	h.ops.Lock()
	defer h.ops.Unlock()

	var res BootResult
	n, err := h.store.MarkInterrupted(ctx)
	if err != nil {
		return res, err
	}
	res.Interrupted = n

	flows, err := h.store.ListFlows(ctx)
	if err != nil {
		return res, err
	}
	for _, f := range flows {
		if !f.Enabled {
			continue
		}
		f, err := h.store.GetFlow(ctx, f.ID)
		if err != nil {
			return res, err
		}
		cf, svc, err := h.compile(f)
		if err != nil {
			h.set(f.ID, &entry{state: Failed, err: err.Error()})
		} else {
			h.run(f.ID, f.Revision, cf, svc)
		}
		if h.Status(f.ID).State == Running {
			res.Started++
		} else {
			res.Failed++
			slog.Warn("flow failed to start", slog.String("flow", f.ID), slog.String("error", h.Status(f.ID).Error))
		}
	}
	return res, nil
}

// Start deploys the latest saved revision of a flow (server-v1.md §4.4). It
// compiles first: if that fails, it returns a *CompileError and changes
// nothing. Otherwise the flow is marked deployed, a running older revision is
// stopped, and the new one starts. A start failure, such as a route held by
// another flow, is not an error: the returned status is Failed and says why.
// Returns store.ErrNotFound for an unknown flow.
func (h *Host) Start(ctx context.Context, id string) (Status, error) {
	h.ops.Lock()
	defer h.ops.Unlock()

	f, err := h.store.GetFlow(ctx, id)
	if err != nil {
		return Status{}, err
	}
	cf, svc, err := h.compile(f)
	if err != nil {
		return h.Status(id), &CompileError{Err: err}
	}
	if err := h.store.SetEnabled(ctx, id, true); err != nil {
		return Status{}, err
	}
	h.stop(id)
	h.run(id, f.Revision, cf, svc)
	return h.Status(id), nil
}

// Stop stops a flow and marks it not deployed. In-flight executions are
// cancelled and waited for. Returns store.ErrNotFound for an unknown flow.
func (h *Host) Stop(ctx context.Context, id string) error {
	h.ops.Lock()
	defer h.ops.Unlock()

	if err := h.store.SetEnabled(ctx, id, false); err != nil {
		return err
	}
	h.stop(id)
	return nil
}

// Delete stops a flow if it runs, then deletes it with its run history.
// Returns store.ErrNotFound for an unknown flow.
func (h *Host) Delete(ctx context.Context, id string) error {
	h.ops.Lock()
	defer h.ops.Unlock()

	if _, err := h.store.GetFlow(ctx, id); err != nil {
		return err
	}
	h.stop(id)
	return h.store.DeleteFlow(ctx, id)
}

// Status returns a flow's state. A flow the host never ran is Stopped.
func (h *Host) Status(id string) Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e, ok := h.flows[id]
	if !ok {
		return Status{State: Stopped}
	}
	st := Status{State: e.state, Error: e.err}
	if e.state == Running {
		st.RunningRevision = e.revision
	}
	return st
}

// Shutdown stops every running flow and waits for their executions. Flows
// stay deployed, so the next Boot starts them again.
func (h *Host) Shutdown() error {
	h.ops.Lock()
	defer h.ops.Unlock()

	h.mu.RLock()
	ids := make([]string, 0, len(h.flows))
	for id := range h.flows {
		ids = append(ids, id)
	}
	h.mu.RUnlock()

	var errs []error
	for _, id := range ids {
		if err := h.stop(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// compile parses and compiles a stored flow with its own services: the
// shared mux, tagged with the flow's ID, and its own cron scheduler
// (server-v1.md §4.2).
func (h *Host) compile(f store.Flow) (*engine.CompiledFlow, *engine.EngineServices, error) {
	fl, err := flow.Parse(bytes.NewReader(f.Document))
	if err != nil {
		return nil, nil, err
	}
	svc := engine.NewEngineServices()
	svc.HTTP = h.mux.Owned(f.ID)
	cf, err := engine.Compile(fl, h.reg, svc)
	if err != nil {
		return nil, nil, err
	}
	return cf, svc, nil
}

// run starts a compiled flow and records the outcome. Callers hold ops.
func (h *Host) run(id string, revision int64, cf *engine.CompiledFlow, svc *engine.EngineServices) {
	var sink engine.TraceSink
	if h.sink != nil {
		sink = h.sink(id, revision)
	}
	inst, err := engine.Start(cf, svc, sink)
	if err != nil {
		h.set(id, &entry{state: Failed, err: err.Error()})
		return
	}
	h.set(id, &entry{state: Running, inst: inst, revision: revision})
}

// stop stops a flow's instance, if it runs, and forgets the flow. Callers
// hold ops.
func (h *Host) stop(id string) error {
	h.mu.Lock()
	e := h.flows[id]
	delete(h.flows, id)
	h.mu.Unlock()
	if e == nil || e.inst == nil {
		return nil
	}
	if err := e.inst.Stop(); err != nil {
		return fmt.Errorf("stop flow %s: %w", id, err)
	}
	return nil
}

func (h *Host) set(id string, e *entry) {
	h.mu.Lock()
	h.flows[id] = e
	h.mu.Unlock()
}
