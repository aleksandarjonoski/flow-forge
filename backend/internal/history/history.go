// Package history records the executions of deployed flows: a trace sink
// per running flow feeds one writer goroutine, which stores executions and
// their trace events in batches and prunes old ones. See docs/server-v1.md
// §5.
package history

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

const (
	// DefaultQueueSize is how many events may wait for the writer.
	DefaultQueueSize = 10_000
	// DefaultKeep is how many executions are kept per flow.
	DefaultKeep = 1000
	// MaxPayloadBytes is the largest node input or output stored as is;
	// bigger ones are replaced by {"truncated": true, "bytes": n}.
	MaxPayloadBytes = 64 << 10
	// maxBatch caps the events written in one transaction.
	maxBatch = 500
	// pruneEvery is how often old executions are pruned and drops reported.
	pruneEvery = time.Minute
)

// Options configures a Writer.
type Options struct {
	// Keep is how many executions to keep per flow; 0 keeps everything.
	Keep int
	// QueueSize bounds the events waiting to be written; 0 means
	// DefaultQueueSize.
	QueueSize int
}

// Writer stores trace events from every running flow. Events reach it
// through the sinks it hands out (Sink) and are written by one goroutine.
type Writer struct {
	store *store.Store
	keep  int
	queue chan item

	// mu guards closed; senders hold it shared so Close never closes the
	// queue under them.
	mu     sync.RWMutex
	closed bool
	done   chan struct{}

	dropped atomic.Int64 // node events dropped since the last report
}

// item is one trace event on its way to the store.
type item struct {
	flowID    string
	revision  int64
	kind      engine.TraceKind
	execution string
	nodeID    string
	at        time.Time
	err       string
	seq       int
	event     json.RawMessage
	// incomplete is set on an execution's final event when some of its node
	// events were dropped.
	incomplete bool
}

// New starts a Writer for st. Close it after the flows have stopped.
func New(st *store.Store, opts Options) *Writer {
	w := newWriter(st, opts)
	go w.run()
	return w
}

// newWriter returns a Writer whose goroutine is not started yet.
func newWriter(st *store.Store, opts Options) *Writer {
	size := opts.QueueSize
	if size <= 0 {
		size = DefaultQueueSize
	}
	return &Writer{
		store: st,
		keep:  opts.Keep,
		queue: make(chan item, size),
		done:  make(chan struct{}),
	}
}

// Sink returns the trace sink for one running revision of a flow. It has the
// shape of host.SinkFunc.
func (w *Writer) Sink(flowID string, revision int64) engine.TraceSink {
	return &sink{w: w, flowID: flowID, revision: revision, execs: make(map[string]*execState)}
}

// Close writes what is queued, stops the writer and waits for it. Events
// emitted after Close are discarded.
func (w *Writer) Close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	<-w.done
}

// send queues an execution-level event, waiting for room: these are never
// dropped, so every execution gets its final status (server-v1.md §5.2).
func (w *Writer) send(it item) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.closed {
		w.queue <- it
	}
}

// trySend queues a node event if there is room and reports whether it did.
func (w *Writer) trySend(it item) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return true // discarded, but not a drop under load
	}
	select {
	case w.queue <- it:
		return true
	default:
		return false
	}
}

// full reports whether the queue has no room, so a node event need not be
// encoded only to be dropped.
func (w *Writer) full() bool {
	return len(w.queue) == cap(w.queue)
}

// run is the writer goroutine: it batches whatever is queued into one
// transaction, and prunes and reports drops every pruneEvery.
func (w *Writer) run() {
	defer close(w.done)
	tick := time.NewTicker(pruneEvery)
	defer tick.Stop()
	w.prune()

	for {
		select {
		case it, ok := <-w.queue:
			if !ok {
				return
			}
			b := batch{}
			b.add(it)
			open := w.drain(&b)
			w.write(b)
			if !open {
				return
			}
		case <-tick.C:
			w.prune()
			w.reportDrops()
		}
	}
}

// drain adds queued items to b without waiting, up to maxBatch. It reports
// whether the queue is still open.
func (w *Writer) drain(b *batch) bool {
	for b.n < maxBatch {
		select {
		case it, ok := <-w.queue:
			if !ok {
				return false
			}
			b.add(it)
		default:
			return true
		}
	}
	return true
}

func (w *Writer) write(b batch) {
	if err := w.store.WriteBatch(context.Background(), b.Batch); err != nil {
		slog.Error("history: write failed; these events are lost", slog.Int("events", b.n), slog.String("error", err.Error()))
	}
}

func (w *Writer) prune() {
	if w.keep < 1 {
		return
	}
	if _, err := w.store.Prune(context.Background(), w.keep); err != nil {
		slog.Error("history: prune failed", slog.String("error", err.Error()))
	}
}

// reportDrops logs dropped node events at most once per pruneEvery.
func (w *Writer) reportDrops() {
	if n := w.dropped.Swap(0); n > 0 {
		slog.Warn("history: trace events dropped, the writer could not keep up", slog.Int64("events", n))
	}
}

// batch turns queued items into one store.Batch.
type batch struct {
	store.Batch
	n int
}

func (b *batch) add(it item) {
	b.n++
	switch it.kind {
	case engine.TraceExecutionStarted:
		b.Started = append(b.Started, store.Execution{
			ID:            it.execution,
			FlowID:        it.flowID,
			Revision:      it.revision,
			Trigger:       it.nodeID,
			Status:        store.StatusRunning,
			StartedAt:     it.at,
			TraceComplete: true,
		})
	case engine.TraceExecutionCompleted, engine.TraceExecutionFailed:
		status := store.StatusCompleted
		if it.kind == engine.TraceExecutionFailed {
			status = store.StatusFailed
		}
		b.Finished = append(b.Finished, store.Finish{ID: it.execution, Status: status, Error: it.err, FinishedAt: it.at})
		if it.incomplete {
			b.Incomplete = append(b.Incomplete, it.execution)
		}
	}
	b.Events = append(b.Events, store.TraceRecord{ExecutionID: it.execution, Seq: it.seq, Event: it.event})
}

// sink is the trace sink of one running revision. Executions run
// concurrently, so it tracks each one's next sequence number under a lock.
type sink struct {
	w        *Writer
	flowID   string
	revision int64

	mu    sync.Mutex
	execs map[string]*execState
}

type execState struct {
	seq        int
	incomplete bool
}

func (s *sink) Emit(ev engine.TraceEvent) {
	final := ev.Kind == engine.TraceExecutionCompleted || ev.Kind == engine.TraceExecutionFailed
	execLevel := final || ev.Kind == engine.TraceExecutionStarted

	s.mu.Lock()
	st := s.execs[ev.ExecutionID]
	if st == nil {
		st = &execState{}
		s.execs[ev.ExecutionID] = st
	}
	seq := st.seq
	st.seq++
	if !execLevel && s.w.full() {
		st.incomplete = true
		s.mu.Unlock()
		s.w.dropped.Add(1)
		return
	}
	incomplete := st.incomplete
	if final {
		delete(s.execs, ev.ExecutionID)
	}
	s.mu.Unlock()

	it := item{
		flowID:     s.flowID,
		revision:   s.revision,
		kind:       ev.Kind,
		execution:  ev.ExecutionID,
		nodeID:     ev.NodeID,
		at:         ev.Timestamp,
		err:        ev.Err,
		seq:        seq,
		event:      encode(ev),
		incomplete: final && incomplete,
	}
	if execLevel {
		s.w.send(it)
		return
	}
	if !s.w.trySend(it) {
		s.mu.Lock()
		if st := s.execs[ev.ExecutionID]; st != nil {
			st.incomplete = true
		}
		s.mu.Unlock()
		s.w.dropped.Add(1)
	}
}

// encode marshals an event in the API's shape. It runs in the execution's
// goroutine, before later nodes can change the payloads (engine-v1.md §8).
// A node input or output over MaxPayloadBytes is replaced by
// {"truncated": true, "bytes": n}.
func encode(ev engine.TraceEvent) json.RawMessage {
	data, err := json.Marshal(ev)
	if err == nil && len(data) <= MaxPayloadBytes {
		return data
	}
	ev.Input = clip(ev.Input)
	ev.Output = clip(ev.Output)
	if data, err = json.Marshal(ev); err != nil {
		// Unreachable with clipped payloads; keep the event's bones.
		ev.Input, ev.Output = nil, nil
		data, _ = json.Marshal(ev)
	}
	return data
}

func clip(p engine.Payload) engine.Payload {
	if p == nil {
		return nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return engine.Payload{"unencodable": err.Error()}
	}
	if len(data) > MaxPayloadBytes {
		return engine.Payload{"truncated": true, "bytes": len(data)}
	}
	return p
}
