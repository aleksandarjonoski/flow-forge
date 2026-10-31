package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

// Run history: recorded executions of deployed flows. See
// docs/server-v1.md §6.4.

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// executionInfo is an execution as the API returns it. The single-execution
// answer adds Trace.
type executionInfo struct {
	ID            string            `json:"id"`
	FlowID        string            `json:"flowId"`
	Revision      int64             `json:"revision"`
	Trigger       string            `json:"trigger"`
	Status        store.Status      `json:"status"`
	Error         string            `json:"error,omitempty"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
	TraceComplete bool              `json:"traceComplete"`
	Trace         []json.RawMessage `json:"trace,omitempty"`
}

func newExecutionInfo(e store.Execution) executionInfo {
	info := executionInfo{
		ID:            e.ID,
		FlowID:        e.FlowID,
		Revision:      e.Revision,
		Trigger:       e.Trigger,
		Status:        e.Status,
		Error:         e.Error,
		StartedAt:     e.StartedAt,
		TraceComplete: e.TraceComplete,
	}
	if !e.FinishedAt.IsZero() {
		info.FinishedAt = &e.FinishedAt
	}
	return info
}

type executionPage struct {
	Executions []executionInfo `json:"executions"`
	Next       string          `json:"next,omitempty"`
}

func (l *library) listExecutions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	flowID := q.Get("flow")
	if flowID == "" {
		writeError(w, http.StatusBadRequest, `query parameter "flow" is required`, nil)
		return
	}
	limit := defaultPageSize
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxPageSize {
			writeError(w, http.StatusBadRequest, `"limit" must be a number from 1 to 200`, nil)
			return
		}
		limit = n
	}
	if _, err := l.store.GetFlow(r.Context(), flowID); err != nil {
		writeStoreError(w, err)
		return
	}

	page, next, err := l.store.ListExecutions(r.Context(), flowID, limit, q.Get("before"))
	if errors.Is(err, store.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, `"before" is not a cursor from a previous page`, nil)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := executionPage{Executions: make([]executionInfo, len(page)), Next: next}
	for i, e := range page {
		out.Executions[i] = newExecutionInfo(e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (l *library) getExecution(w http.ResponseWriter, r *http.Request) {
	e, trace, err := l.store.GetExecution(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	info := newExecutionInfo(e)
	info.Trace = trace
	writeJSON(w, http.StatusOK, info)
}
