// Package api is FlowForge's HTTP API for the visual editor. The contract is
// docs/api-v1.md (test runs) and docs/server-v1.md §6 (the flow library).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/host"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

const (
	// maxBodyBytes caps request bodies.
	maxBodyBytes = 1 << 20
	// runTimeout bounds one test run; a slower run is cancelled and reported
	// as failed.
	runTimeout = 60 * time.Second
)

// Options configures the API handler.
type Options struct {
	// Store and Host serve the flow library. When either is nil, only test
	// runs are served.
	Store *store.Store
	Host  *host.Host
	// Token, when set, is required on every request but GET /api/health
	// (RequireToken).
	Token string
}

// NewHandler returns the API handler. Test runs are compiled against reg.
func NewHandler(reg *engine.Registry, opts Options) http.Handler {
	h := &handler{reg: reg, runTimeout: runTimeout}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/flows/run", h.run)
	if opts.Store != nil && opts.Host != nil {
		(&library{store: opts.Store, host: opts.Host}).register(mux)
	}
	if opts.Token != "" {
		return RequireToken(opts.Token, mux)
	}
	return mux
}

type handler struct {
	reg        *engine.Registry
	runTimeout time.Duration
}

type runRequest struct {
	Flow    json.RawMessage `json:"flow"`
	Trigger string          `json:"trigger"`
	Payload engine.Payload  `json:"payload"`
}

// runResult is how a run ended. It is the final "result" event of a stream.
type runResult struct {
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"` // "completed" or "failed"
	Error       string `json:"error,omitempty"`
}

// runResponse is the JSON (non-streaming) answer: the result plus the trace.
type runResponse struct {
	runResult
	Trace []engine.TraceEvent `json:"trace"`
}

type errorResponse struct {
	Error    string   `json:"error"`
	Problems []string `json:"problems,omitempty"`
}

// run executes one test run: the chosen trigger fires once with the given
// payload. Triggers are compiled but never started. The answer is one JSON
// body, or a Server-Sent Events stream when the client accepts
// text/event-stream. See docs/api-v1.md.
func (h *handler) run(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if status, err := decode(w, r, &req); err != nil {
		writeError(w, status, err.Error(), nil)
		return
	}
	if len(req.Flow) == 0 || string(req.Flow) == "null" {
		writeError(w, http.StatusBadRequest, `"flow" is required`, nil)
		return
	}

	fl, err := flow.Parse(bytes.NewReader(req.Flow))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	cf, err := engine.Compile(fl, h.reg, engine.NewEngineServices())
	if err != nil {
		writeError(w, http.StatusBadRequest, "flow does not compile", problems(err))
		return
	}
	trigger, err := pickTrigger(cf, req.Trigger)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	payload := req.Payload
	if payload == nil {
		payload = engine.Payload{}
	}

	// The request context is cancelled when the client disconnects, which
	// cancels the run too (engine-v1.md §9).
	ctx, cancel := context.WithTimeout(r.Context(), h.runTimeout)
	defer cancel()

	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		sink := &sseSink{w: w, rc: http.NewResponseController(w)}
		sink.flush()
		execErr := engine.NewExecutor(cf, sink).Execute(ctx, trigger, payload)
		sink.event("result", result(sink.executionID, execErr))
		return
	}

	sink := engine.NewMemoryTraceSink()
	execErr := engine.NewExecutor(cf, sink).Execute(ctx, trigger, payload)
	trace := sink.Events()
	var id string
	if len(trace) > 0 {
		id = trace[0].ExecutionID
	}
	writeJSON(w, http.StatusOK, runResponse{runResult: result(id, execErr), Trace: trace})
}

func result(executionID string, execErr error) runResult {
	res := runResult{ExecutionID: executionID, Status: "completed"}
	if execErr != nil {
		res.Status = "failed"
		res.Error = execErr.Error()
	}
	return res
}

// sseSink writes each trace event to the response as a Server-Sent Event
// and flushes it, so the client sees the run as it happens. The executor
// emits from the request's goroutine, so no locking is needed. After a
// failed write (client gone) it stops writing; the run itself is cancelled
// through the request context.
type sseSink struct {
	w           io.Writer
	rc          *http.ResponseController
	executionID string
	broken      bool
}

func (s *sseSink) Emit(e engine.TraceEvent) {
	if s.executionID == "" {
		s.executionID = e.ExecutionID
	}
	s.event("trace", e)
}

func (s *sseSink) event(name string, v any) {
	if s.broken {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		s.broken = true
		return
	}
	s.flush()
}

func (s *sseSink) flush() {
	if err := s.rc.Flush(); err != nil {
		s.broken = true
	}
}

// pickTrigger returns the trigger to fire: the requested one, or the only
// trigger when none was requested.
func pickTrigger(cf *engine.CompiledFlow, requested string) (string, error) {
	if requested == "" {
		if len(cf.Triggers) == 1 {
			return cf.Triggers[0], nil
		}
		return "", fmt.Errorf(`flow has %d triggers (%s); say which one to run with "trigger"`,
			len(cf.Triggers), strings.Join(cf.Triggers, ", "))
	}
	if !slices.Contains(cf.Triggers, requested) {
		return "", fmt.Errorf("%q is not a trigger node of this flow", requested)
	}
	return requested, nil
}

// problems splits an errors.Join result (as returned by Validate and
// Compile) into one message per problem.
func problems(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// decode reads a JSON request body into v, rejecting unknown fields, bodies
// over maxBodyBytes and trailing data. On failure it also returns the status
// code to respond with.
func decode(w http.ResponseWriter, r *http.Request, v any) (int, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", maxBodyBytes)
		}
		return http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return http.StatusBadRequest, errors.New("invalid request body: unexpected data after the JSON object")
	}
	return 0, nil
}

func writeError(w http.ResponseWriter, status int, msg string, details []string) {
	writeJSON(w, status, errorResponse{Error: msg, Problems: details})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
