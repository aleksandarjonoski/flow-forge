package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// HTTPTriggerType is the registered type name of the HTTP trigger node.
const HTTPTriggerType = "http_trigger"

func init() {
	engine.DefaultRegistry.Register(HTTPTriggerType, func() engine.Node { return &HTTPTrigger{} })
}

// maxBodyBytes caps the request body an HTTP trigger will read.
const maxBodyBytes = 1 << 20

// HTTPTrigger fires one execution per HTTP request on its method + path. It
// registers on the engine's shared HTTPMux at Start and removes itself at
// Stop. See docs/engine-v1.md §11.
type HTTPTrigger struct {
	path   string
	method string
	mux    *engine.HTTPMux
}

func (t *HTTPTrigger) Type() string { return HTTPTriggerType }

func (t *HTTPTrigger) Init(config map[string]any, services *engine.EngineServices) error {
	path, err := stringConfig(config, "path", true, "")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("config %q must start with /, got %q", "path", path)
	}
	method, err := stringConfig(config, "method", false, http.MethodPost)
	if err != nil {
		return err
	}
	if method == "" {
		return fmt.Errorf("config %q must not be empty", "method")
	}
	if services == nil || services.HTTP == nil {
		return errors.New("engine services provide no HTTP mux")
	}
	t.path = path
	t.method = strings.ToUpper(method)
	t.mux = services.HTTP
	return nil
}

// Start registers the route and returns immediately; requests are served by
// whoever mounts the mux.
func (t *HTTPTrigger) Start(_ context.Context, emit engine.Emitter) error {
	return t.mux.Handle(t.method, t.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.serve(w, r, emit)
	}))
}

func (t *HTTPTrigger) Stop() error {
	t.mux.Remove(t.method, t.path)
	return nil
}

// serve turns one request into an emitted payload and maps the outcome to a
// status code: 202 on success, 400/413 for a bad body, 500 if emit fails.
func (t *HTTPTrigger) serve(w http.ResponseWriter, r *http.Request, emit engine.Emitter) {
	body, status, err := readBody(w, r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	query := make(map[string]any)
	for k, v := range r.URL.Query() {
		query[k] = v[0]
	}

	if err := emit(engine.Payload{"headers": flattenHeaders(r.Header), "query": query, "body": body}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// readBody reads the request body: nil if empty, decoded JSON for a JSON
// content type, otherwise the raw string. On failure it returns the status
// code to respond with.
func readBody(w http.ResponseWriter, r *http.Request) (any, int, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", maxBodyBytes)
		}
		return nil, http.StatusBadRequest, fmt.Errorf("read body: %w", err)
	}
	if len(raw) == 0 {
		return nil, 0, nil
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		return string(raw), 0, nil
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err)
	}
	return body, 0, nil
}

// flattenHeaders maps each canonical header name to its values joined with
// ", ", which is how HTTP itself combines repeated fields.
func flattenHeaders(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// isJSON reports whether a Content-Type is application/json or a +json type.
func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}
