package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrEngineStopped is returned by an Emitter once the engine has begun
// shutting down. Triggers surface it to their caller (e.g. as an HTTP 500).
var ErrEngineStopped = errors.New("engine stopped")

// Run starts every trigger of cf and blocks until ctx is cancelled, then shuts
// down. See docs/engine-v1.md §4.4.
//
// Each emit starts one execution in its own goroutine and returns at once;
// execution errors go to sink, not to the trigger. svc must be the services
// passed to Compile. Run starts and stops svc.Cron but does not serve
// svc.HTTP — the caller mounts it in an http.Server.
func Run(ctx context.Context, cf *CompiledFlow, svc *EngineServices, sink TraceSink) error {
	inst, err := Start(cf, svc, sink)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return inst.Stop()
}

// Instance is a running flow, returned by Start.
type Instance struct {
	r        *runner
	cancel   context.CancelFunc
	stopOnce sync.Once
	stopErr  error
}

// Start starts every trigger of cf and the scheduler, and returns once they
// are all started. If a trigger fails to start, the ones already started are
// stopped and the error is returned. It is Run without the wait, for callers
// that run many flows and must know whether each one started
// (docs/engine-v1.md §4.4).
func Start(cf *CompiledFlow, svc *EngineServices, sink TraceSink) (*Instance, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{exec: NewExecutor(cf, sink), ctx: ctx}

	for _, id := range cf.Triggers {
		trig, ok := cf.Nodes[id].(TriggerNode)
		if !ok {
			// Compile guarantees every trigger ID is a TriggerNode; this is a
			// defensive backstop.
			cancel()
			return nil, errors.Join(fmt.Errorf("node %s is not a TriggerNode", id), r.shutdown())
		}
		if err := trig.Start(ctx, r.emitter(id)); err != nil {
			cancel()
			return nil, errors.Join(fmt.Errorf("start trigger %s (%s): %w", id, trig.Type(), err), r.shutdown())
		}
		r.started = append(r.started, startedTrigger{id: id, node: trig})
	}

	if svc != nil && svc.Cron != nil {
		svc.Cron.Start()
		r.cron = svc
	}
	return &Instance{r: r, cancel: cancel}, nil
}

// Stop shuts the instance down: in-flight executions are cancelled (they
// abort between nodes), the scheduler and triggers are stopped, and Stop
// waits for the executions to end. It returns the joined trigger Stop
// errors. Calling it again returns the same result.
func (i *Instance) Stop() error {
	i.stopOnce.Do(func() {
		i.cancel()
		i.stopErr = i.r.shutdown()
	})
	return i.stopErr
}

// runner holds the state of one running flow.
type runner struct {
	exec    *Executor
	ctx     context.Context
	started []startedTrigger
	// cron is set once the scheduler has been started, so shutdown only stops
	// what Run started.
	cron *EngineServices

	mu       sync.Mutex
	stopped  bool
	inflight sync.WaitGroup
}

type startedTrigger struct {
	id   string
	node TriggerNode
}

// emitter returns the Emitter for triggerID. Registering with inflight under
// mu, together with the stopped flag, guarantees shutdown's Wait never races
// with a late Add.
func (r *runner) emitter(triggerID string) Emitter {
	return func(p Payload) error {
		r.mu.Lock()
		if r.stopped {
			r.mu.Unlock()
			return ErrEngineStopped
		}
		r.inflight.Add(1)
		r.mu.Unlock()

		go func() {
			defer r.inflight.Done()
			_ = r.exec.Execute(r.ctx, triggerID, p)
		}()
		return nil
	}
}

// shutdown stops the scheduler (waiting for running jobs), rejects further
// emits, stops every started trigger and waits for in-flight executions.
func (r *runner) shutdown() error {
	if r.cron != nil {
		<-r.cron.Cron.Stop().Done()
	}

	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()

	var errs []error
	for _, t := range r.started {
		if err := t.node.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop trigger %s (%s): %w", t.id, t.node.Type(), err))
		}
	}
	r.inflight.Wait()
	return errors.Join(errs...)
}
