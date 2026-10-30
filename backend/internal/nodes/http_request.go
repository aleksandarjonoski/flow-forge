package nodes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// HTTPRequestType is the registered type name of the HTTP request node.
const HTTPRequestType = "http_request"

func init() {
	engine.DefaultRegistry.Register(HTTPRequestType, func() engine.Node { return &HTTPRequest{} })
}

const (
	// maxResponseBytes caps the response body an HTTP request will read.
	maxResponseBytes = 10 << 20
	// requestTimeout is a safety net until per-node timeouts exist.
	requestTimeout = 30 * time.Second
)

// HTTPRequest performs one outbound HTTP call per execution and outputs the
// response as {status, headers, body}. See docs/engine-v1.md §11.
//
// All fields are set once at Init and only read afterwards, so Execute is safe
// for concurrent executions.
type HTTPRequest struct {
	url      string
	method   string
	headers  map[string]string
	bodyFrom []string // path into the input; nil means no request body
	client   *http.Client
}

func (h *HTTPRequest) Type() string { return HTTPRequestType }

func (h *HTTPRequest) Init(config map[string]any, _ *engine.EngineServices) error {
	rawURL, err := stringConfig(config, "url", true, "")
	if err != nil {
		return err
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config %q must be an absolute http(s) URL, got %q", "url", rawURL)
	}
	method, err := stringConfig(config, "method", false, http.MethodGet)
	if err != nil {
		return err
	}
	if method == "" {
		return fmt.Errorf("config %q must not be empty", "method")
	}
	headers, err := headersConfig(config)
	if err != nil {
		return err
	}
	bodyFrom, err := stringConfig(config, "bodyFrom", false, "")
	if err != nil {
		return err
	}

	h.url = rawURL
	h.method = strings.ToUpper(method)
	h.headers = headers
	if bodyFrom != "" {
		h.bodyFrom = strings.Split(bodyFrom, ".")
	}
	h.client = &http.Client{Timeout: requestTimeout}
	return nil
}

func (h *HTTPRequest) Execute(ctx *engine.ExecutionContext, input engine.Payload) (engine.Payload, error) {
	var body io.Reader
	if h.bodyFrom != nil {
		v, err := lookupPath(input, h.bodyFrom)
		if err != nil {
			return nil, fmt.Errorf("bodyFrom: %w", err)
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("bodyFrom: encode JSON: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx.GoCtx, h.method, h.url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	return engine.Payload{
		"status":  resp.StatusCode,
		"headers": flattenHeaders(resp.Header),
		"body":    respBody,
	}, nil
}

// readResponseBody reads the response body: nil if empty, decoded JSON for a
// JSON content type, otherwise the raw string.
func readResponseBody(resp *http.Response) (any, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if !isJSON(resp.Header.Get("Content-Type")) {
		return string(raw), nil
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("invalid JSON response body: %w", err)
	}
	return body, nil
}

// headersConfig reads the optional "headers" config as a map of strings.
func headersConfig(config map[string]any) (map[string]string, error) {
	v, ok := config["headers"]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config %q must be an object, got %T", "headers", v)
	}
	out := make(map[string]string, len(m))
	for k, hv := range m {
		s, ok := hv.(string)
		if !ok {
			return nil, fmt.Errorf("config %q: value of %q must be a string, got %T", "headers", k, hv)
		}
		out[k] = s
	}
	return out, nil
}

// lookupPath walks a dot-separated path through nested maps.
func lookupPath(p engine.Payload, path []string) (any, error) {
	var cur any = p
	for i, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", strings.Join(path[:i], "."))
		}
		if cur, ok = m[key]; !ok {
			return nil, fmt.Errorf("no value at %q", strings.Join(path[:i+1], "."))
		}
	}
	return cur, nil
}
