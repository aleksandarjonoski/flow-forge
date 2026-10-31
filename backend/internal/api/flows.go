package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/host"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

// The flow library: stored flows, deploy and stop, and their run history
// (executions.go). See docs/server-v1.md §6.

// flowIDPattern is what a stored flow's ID must match: it appears in URLs and
// log lines (server-v1.md §6.2).
var flowIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// library serves the flow library endpoints.
type library struct {
	store *store.Store
	host  *host.Host
}

func (l *library) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", l.health)
	mux.HandleFunc("GET /api/flows", l.list)
	mux.HandleFunc("POST /api/flows", l.create)
	mux.HandleFunc("GET /api/flows/{id}", l.get)
	mux.HandleFunc("PUT /api/flows/{id}", l.save)
	mux.HandleFunc("DELETE /api/flows/{id}", l.remove)
	mux.HandleFunc("POST /api/flows/{id}/start", l.start)
	mux.HandleFunc("POST /api/flows/{id}/stop", l.stop)
	mux.HandleFunc("GET /api/executions", l.listExecutions)
	mux.HandleFunc("GET /api/executions/{id}", l.getExecution)
}

// flowInfo is a stored flow as the API returns it (server-v1.md §6.3). The
// list leaves out Flow.
type flowInfo struct {
	Flow            json.RawMessage `json:"flow,omitempty"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Revision        int64           `json:"revision"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	State           host.State      `json:"state"`
	RunningRevision int64           `json:"runningRevision"`
	Error           string          `json:"error"`
}

func (l *library) info(f store.Flow) flowInfo {
	st := l.host.Status(f.ID)
	return flowInfo{
		Flow:            f.Document,
		ID:              f.ID,
		Name:            f.Name,
		Revision:        f.Revision,
		CreatedAt:       f.CreatedAt,
		UpdatedAt:       f.UpdatedAt,
		State:           st.State,
		RunningRevision: st.RunningRevision,
		Error:           st.Error,
	}
}

func (l *library) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (l *library) list(w http.ResponseWriter, r *http.Request) {
	flows, err := l.store.ListFlows(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]flowInfo, len(flows))
	for i, f := range flows {
		out[i] = l.info(f)
	}
	writeJSON(w, http.StatusOK, map[string][]flowInfo{"flows": out})
}

type createRequest struct {
	Flow json.RawMessage `json:"flow"`
}

func (l *library) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if status, err := decode(w, r, &req); err != nil {
		writeError(w, status, err.Error(), nil)
		return
	}
	fl, doc, ok := parseDocument(w, req.Flow)
	if !ok {
		return
	}
	if !flowIDPattern.MatchString(fl.ID) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"flow id %q must be 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", fl.ID), nil)
		return
	}
	f, err := l.store.CreateFlow(r.Context(), fl.ID, fl.Name, doc)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l.info(f))
}

func (l *library) get(w http.ResponseWriter, r *http.Request) {
	f, err := l.store.GetFlow(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l.info(f))
}

type saveRequest struct {
	Flow json.RawMessage `json:"flow"`
	// Revision is the revision the editor loaded (the lost-update guard).
	Revision int64 `json:"revision"`
}

// conflictResponse answers a save that lost the race: the error and the flow
// as it is stored now, so the editor can offer to reload or overwrite.
type conflictResponse struct {
	Error   string   `json:"error"`
	Current flowInfo `json:"current"`
}

func (l *library) save(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req saveRequest
	if status, err := decode(w, r, &req); err != nil {
		writeError(w, status, err.Error(), nil)
		return
	}
	if req.Revision < 1 {
		writeError(w, http.StatusBadRequest, `"revision" is required: the revision the flow was loaded at`, nil)
		return
	}
	fl, doc, ok := parseDocument(w, req.Flow)
	if !ok {
		return
	}
	if fl.ID != id {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("flow id %q does not match %q in the URL; a flow's id cannot change", fl.ID, id), nil)
		return
	}
	f, err := l.store.SaveFlow(r.Context(), id, fl.Name, doc, req.Revision)
	if errors.Is(err, store.ErrConflict) {
		current, err := l.store.GetFlow(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, conflictResponse{
			Error: fmt.Sprintf("flow %s was saved by someone else: it is at revision %d, not %d",
				id, current.Revision, req.Revision),
			Current: l.info(current),
		})
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l.info(f))
}

func (l *library) remove(w http.ResponseWriter, r *http.Request) {
	if err := l.host.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *library) start(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := l.host.Start(r.Context(), id)
	var ce *host.CompileError
	if errors.As(err, &ce) {
		writeError(w, http.StatusBadRequest, "flow does not compile", problems(ce.Err))
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	l.writeFlow(w, r, id)
}

func (l *library) stop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := l.host.Stop(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	l.writeFlow(w, r, id)
}

// writeFlow answers with the stored flow, as after a start or stop.
func (l *library) writeFlow(w http.ResponseWriter, r *http.Request, id string) {
	f, err := l.store.GetFlow(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l.info(f))
}

// parseDocument checks a flow document from a request: it must be present
// and pass flow.Parse — what the editor's Open requires. It need not compile
// (server-v1.md §6.2). It returns the document to store: the bytes as sent,
// ending in one newline like a canonical file (a JSON value inside a request
// cannot carry it). On failure it writes the 400 and returns false.
func parseDocument(w http.ResponseWriter, raw json.RawMessage) (*flow.Flow, []byte, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		writeError(w, http.StatusBadRequest, `"flow" is required`, nil)
		return nil, nil, false
	}
	fl, err := flow.Parse(bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return nil, nil, false
	}
	doc := append(bytes.TrimRight(raw, " \t\r\n"), '\n')
	return fl, doc, true
}

// writeStoreError maps store errors to status codes: unknown → 404, taken ID
// → 409, anything else → 500.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error(), nil)
	case errors.Is(err, store.ErrExists):
		writeError(w, http.StatusConflict, err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, err.Error(), nil)
	}
}
