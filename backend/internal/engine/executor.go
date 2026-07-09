package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Executor runs individual executions of a CompiledFlow. It is created once
// per compiled flow and reused across every trigger event. See
// docs/engine-v1.md §4.5 (per-event execution) and §7 (error handling).
//
// An Executor holds no per-execution state: cf, sink and pred are read-only
// after construction, so Execute is safe to call concurrently for different
// events. (The node instances in cf.Nodes are shared across executions, so a
// node's Execute must itself be safe for concurrent invocation — the built-in
// action nodes keep all mutable state in their config, set once at Init.)
type Executor struct {
	cf   *CompiledFlow
	sink TraceSink
	// pred maps a node ID to its single upstream node. Merge nodes (>1 inbound
	// edge) are rejected at validation (§10), so each node has at most one
	// predecessor. A node absent from the map has no inbound edge.
	pred map[string]string
}

// NewExecutor returns an Executor for cf that reports every step to sink. If
// sink is nil, tracing is disabled via a no-op sink so callers never have to
// nil-check.
func NewExecutor(cf *CompiledFlow, sink TraceSink) *Executor {
	if sink == nil {
		sink = noopSink{}
	}
	pred := make(map[string]string, len(cf.Flow.Edges))
	for _, e := range cf.Flow.Edges {
		pred[e.Target] = e.Source
	}
	return &Executor{cf: cf, sink: sink, pred: pred}
}

// Execute runs one execution of the subgraph rooted at triggerID, seeded with
// the trigger's emitted payload. Every call gets a fresh ExecutionContext with
// its own NodeResults, so concurrent executions never share state.
//
// Nodes are visited in the trigger's pre-computed topological order. Each
// action node receives the output of its predecessor (or the trigger payload,
// for a direct successor of the trigger). Results accumulate in
// ctx.NodeResults so a node may also read any completed upstream result.
//
// The policy is halt-on-first-error (§7): the first node that returns an error
// (or panics) aborts the execution and Execute returns a wrapped error. Other
// concurrent executions are unaffected.
func (e *Executor) Execute(goCtx context.Context, triggerID string, trigger Payload) error {
	order, ok := e.cf.TopoOrder[triggerID]
	if !ok {
		return fmt.Errorf("unknown trigger %q", triggerID)
	}

	ec := &ExecutionContext{
		FlowID:      e.cf.Flow.ID,
		ExecutionID: newExecutionID(),
		NodeResults: make(map[string]Payload, len(order)),
		Trace:       e.sink,
		GoCtx:       goCtx,
	}

	start := time.Now()
	e.sink.Emit(TraceEvent{
		Kind:        TraceExecutionStarted,
		ExecutionID: ec.ExecutionID,
		NodeID:      triggerID,
		NodeType:    e.cf.Nodes[triggerID].Type(),
		Timestamp:   time.Now(),
		Input:       trigger,
	})

	for _, nodeID := range order {
		// The trigger heads its own topo order but is not an ActionNode. Seed
		// its emitted payload as its "result" so a direct successor reads it
		// uniformly through the predecessor lookup below.
		if nodeID == triggerID {
			ec.NodeResults[triggerID] = trigger
			continue
		}

		// Honor cancellation between nodes (§9): a cancelled context aborts the
		// execution before starting the next node.
		if err := goCtx.Err(); err != nil {
			return e.fail(ec, nodeID, e.cf.Nodes[nodeID].Type(), start, time.Now(), err)
		}

		node := e.cf.Nodes[nodeID]
		action, ok := node.(ActionNode)
		if !ok {
			// Compile guarantees every non-trigger node is an ActionNode; this
			// is a defensive backstop.
			return e.fail(ec, nodeID, node.Type(), start, time.Now(),
				fmt.Errorf("node is not an ActionNode"))
		}

		var input Payload
		if p, ok := e.pred[nodeID]; ok {
			input = ec.NodeResults[p]
		}

		nodeStart := time.Now()
		e.sink.Emit(TraceEvent{
			Kind:        TraceNodeStarted,
			ExecutionID: ec.ExecutionID,
			NodeID:      nodeID,
			NodeType:    node.Type(),
			Timestamp:   nodeStart,
			Input:       input,
		})

		output, err := safeExecute(action, ec, input)
		if err != nil {
			return e.fail(ec, nodeID, node.Type(), start, nodeStart, err)
		}

		ec.NodeResults[nodeID] = output
		e.sink.Emit(TraceEvent{
			Kind:        TraceNodeCompleted,
			ExecutionID: ec.ExecutionID,
			NodeID:      nodeID,
			NodeType:    node.Type(),
			Timestamp:   time.Now(),
			DurationNs:  time.Since(nodeStart),
			Input:       input,
			Output:      output,
		})
	}

	e.sink.Emit(TraceEvent{
		Kind:        TraceExecutionCompleted,
		ExecutionID: ec.ExecutionID,
		Timestamp:   time.Now(),
		DurationNs:  time.Since(start),
	})
	return nil
}

// fail emits the NodeFailed and ExecutionFailed trace events and returns the
// wrapped error described in §7. nodeStart is when the failing node began (for
// the node-level duration); execStart is when the whole execution began.
func (e *Executor) fail(ec *ExecutionContext, nodeID, nodeType string, execStart, nodeStart time.Time, err error) error {
	now := time.Now()
	e.sink.Emit(TraceEvent{
		Kind:        TraceNodeFailed,
		ExecutionID: ec.ExecutionID,
		NodeID:      nodeID,
		NodeType:    nodeType,
		Timestamp:   now,
		DurationNs:  now.Sub(nodeStart),
		Err:         err.Error(),
	})
	wrapped := fmt.Errorf("node %s (%s): %w", nodeID, nodeType, err)
	e.sink.Emit(TraceEvent{
		Kind:        TraceExecutionFailed,
		ExecutionID: ec.ExecutionID,
		Timestamp:   now,
		DurationNs:  now.Sub(execStart),
		Err:         wrapped.Error(),
	})
	return wrapped
}

// safeExecute invokes action.Execute, recovering any panic and converting it
// into an error so a misbehaving node can never crash the engine (§7).
func safeExecute(action ActionNode, ec *ExecutionContext, input Payload) (out Payload, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return action.Execute(ec, input)
}

// newExecutionID returns a random 128-bit hex identifier, unique per run.
// crypto/rand.Read does not fail on supported platforms (guaranteed since Go
// 1.24), so the error is intentionally ignored.
func newExecutionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// noopSink discards every event. Used when NewExecutor is given a nil sink.
type noopSink struct{}

func (noopSink) Emit(TraceEvent) {}
