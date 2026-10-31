package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// TraceKind identifies a kind of trace event. String-typed (not int) so
// JSON output is self-describing without needing a mapping table.
type TraceKind string

const (
	TraceExecutionStarted   TraceKind = "execution_started"
	TraceNodeStarted        TraceKind = "node_started"
	TraceNodeCompleted      TraceKind = "node_completed"
	TraceNodeFailed         TraceKind = "node_failed"
	TraceExecutionCompleted TraceKind = "execution_completed"
	TraceExecutionFailed    TraceKind = "execution_failed"
)

// TraceEvent is the single record emitted to a TraceSink for every observable
// step in an execution. See docs/engine-v1.md §8.
//
// Fields that don't apply to a given Kind are omitted from JSON via
// omitempty, so per-execution events stay compact.
type TraceEvent struct {
	Kind        TraceKind     `json:"kind"`
	ExecutionID string        `json:"executionId"`
	NodeID      string        `json:"nodeId,omitempty"`
	NodeType    string        `json:"nodeType,omitempty"`
	Timestamp   time.Time     `json:"timestamp"`
	DurationNs  time.Duration `json:"durationNs,omitempty"`
	Input       Payload       `json:"input,omitempty"`
	Output      Payload       `json:"output,omitempty"`
	Err         string        `json:"error,omitempty"`
}

// TraceSink receives trace events. The engine never logs directly; every
// observable step goes through a sink, so the UI step-debugger and the
// production log shipper consume the same stream.
type TraceSink interface {
	Emit(TraceEvent)
}

// WriterTraceSink writes one JSON object per line to an io.Writer. Safe for
// concurrent use. NewStdoutTraceSink is the common case for the CLI.
type WriterTraceSink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriterTraceSink returns a sink that writes JSON-lines to w.
func NewWriterTraceSink(w io.Writer) *WriterTraceSink {
	return &WriterTraceSink{w: w}
}

// NewStdoutTraceSink returns a sink that writes JSON-lines to os.Stdout.
func NewStdoutTraceSink() *WriterTraceSink {
	return NewWriterTraceSink(os.Stdout)
}

// Emit serializes the event to JSON and writes it followed by a newline.
// Marshal errors are silently dropped — tracing is best-effort and must not
// crash the executor. Write errors are likewise ignored for the same reason.
func (s *WriterTraceSink) Emit(e TraceEvent) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.w.Write(data)
	_, _ = s.w.Write([]byte{'\n'})
}

// MemoryTraceSink captures every event in memory. Used in tests and as the
// backing store for the future step-debugger. Safe for concurrent use.
type MemoryTraceSink struct {
	mu     sync.Mutex
	events []TraceEvent
}

// NewMemoryTraceSink returns an empty in-memory sink.
func NewMemoryTraceSink() *MemoryTraceSink {
	return &MemoryTraceSink{}
}

// Emit appends the event to the in-memory buffer.
func (s *MemoryTraceSink) Emit(e TraceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Events returns a snapshot copy of the captured events. The returned slice
// is owned by the caller; subsequent Emits do not mutate it.
func (s *MemoryTraceSink) Events() []TraceEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TraceEvent, len(s.events))
	copy(out, s.events)
	return out
}

// Reset discards all captured events.
func (s *MemoryTraceSink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:0]
}

// MultiSink fans events out to every wrapped sink in order. If a sink
// panics, the panic propagates — sinks must be well-behaved.
type MultiSink struct {
	sinks []TraceSink
}

// NewMultiSink returns a sink that delegates to each of sinks in turn.
func NewMultiSink(sinks ...TraceSink) *MultiSink {
	return &MultiSink{sinks: sinks}
}

// Emit forwards e to every wrapped sink in registration order.
func (m *MultiSink) Emit(e TraceEvent) {
	for _, s := range m.sinks {
		s.Emit(e)
	}
}

// RunLogSink writes one log record per finished execution: INFO when it
// completed, ERROR when it failed, with the execution ID, trigger, status,
// duration and error, plus the attributes given at construction (e.g. the
// flow ID). It is the technical log of every run, for log shippers; `log`
// nodes remain for business messages. See docs/server-v1.md §5.3. Safe for
// concurrent use.
type RunLogSink struct {
	logger *slog.Logger
	attrs  []slog.Attr

	mu sync.Mutex
	// triggers maps a running execution to its trigger, which only the
	// execution_started event names.
	triggers map[string]string
}

// NewRunLogSink returns a sink that logs to logger, adding attrs to every
// record.
func NewRunLogSink(logger *slog.Logger, attrs ...slog.Attr) *RunLogSink {
	return &RunLogSink{logger: logger, attrs: attrs, triggers: make(map[string]string)}
}

// Emit records the trigger of a starting execution and logs a finished one.
func (s *RunLogSink) Emit(e TraceEvent) {
	var level slog.Level
	var status string
	switch e.Kind {
	case TraceExecutionStarted:
		s.mu.Lock()
		s.triggers[e.ExecutionID] = e.NodeID
		s.mu.Unlock()
		return
	case TraceExecutionCompleted:
		level, status = slog.LevelInfo, "completed"
	case TraceExecutionFailed:
		level, status = slog.LevelError, "failed"
	default:
		return
	}
	s.mu.Lock()
	trigger := s.triggers[e.ExecutionID]
	delete(s.triggers, e.ExecutionID)
	s.mu.Unlock()

	attrs := make([]slog.Attr, 0, len(s.attrs)+5)
	attrs = append(attrs, s.attrs...)
	attrs = append(attrs,
		slog.String("execution", e.ExecutionID),
		slog.String("trigger", trigger),
		slog.String("status", status),
		slog.Duration("duration", e.DurationNs),
	)
	if e.Err != "" {
		attrs = append(attrs, slog.String("error", e.Err))
	}
	s.logger.LogAttrs(context.Background(), level, "execution", attrs...)
}
