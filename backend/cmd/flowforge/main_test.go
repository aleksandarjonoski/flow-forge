package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

const helloFlow = "../../examples/hello.flow.json"

func TestCLI_UsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":         nil,
		"unknown command": {"start", helloFlow},
		"no file":         {"run"},
		"two files":       {"run", "a.json", "b.json"},
		"unknown flag":    {"run", "-port", "1", helloFlow},
		"serve with args": {"serve", helloFlow},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := cli(args, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "usage:") && !strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Errorf("stderr = %q, want usage", stderr.String())
			}
		})
	}
}

func TestCLI_Help(t *testing.T) {
	var stderr bytes.Buffer
	if code := cli([]string{"run", "-h"}, &stderr); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "-addr") {
		t.Errorf("stderr = %q, want flag help", stderr.String())
	}
}

func TestCLI_LoadErrors(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "unknown.flow.json")
	if err := os.WriteFile(unknown, []byte(`{"id":"f","version":"1.0","nodes":[{"id":"n","type":"nope"}],"edges":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args    []string
		wantErr string
	}{
		"missing file":      {[]string{"run", filepath.Join(dir, "missing.json")}, "no such file"},
		"unknown node type": {[]string{"run", unknown}, `unknown type "nope"`},
		"bad address":       {[]string{"run", "-addr", "not-an-address", helloFlow}, "not-an-address"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := cli(tc.args, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantErr)
			}
		})
	}
}

// TestServe_HelloFlow is the Phase 1 milestone: the example flow runs behind a
// real listener, a POST to /webhook goes through transform and log, and the
// engine shuts down cleanly.
func TestServe_HelloFlow(t *testing.T) {
	cf, svc, err := load(helloFlow)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sink := engine.NewMemoryTraceSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cf, svc, ln, sink) }()

	resp, err := http.Post("http://"+ln.Addr().String()+"/webhook", "application/json", strings.NewReader(`{"name":"ada"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	// The execution runs asynchronously; wait for it to finish.
	var logOutput engine.Payload
	deadline := time.Now().Add(3 * time.Second)
	for logOutput == nil {
		if time.Now().After(deadline) {
			t.Fatalf("execution did not complete; trace: %v", sink.Events())
		}
		for _, ev := range sink.Events() {
			if ev.Kind == engine.TraceExecutionFailed {
				t.Fatalf("execution failed: %s", ev.Err)
			}
			if ev.Kind == engine.TraceNodeCompleted && ev.NodeID == "node-3" {
				logOutput = ev.Output
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if want := (engine.Payload{"greeting": "hello ada"}); !reflect.DeepEqual(logOutput, want) {
		t.Errorf("log node output = %v, want %v", logOutput, want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

func TestCLI_ServeBadAddress(t *testing.T) {
	var stderr bytes.Buffer
	if code := cli([]string{"serve", "-addr", "not-an-address"}, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// server is runServer on two loopback listeners, for tests.
type server struct {
	api, flows string // base URLs
	cancel     context.CancelFunc
	done       chan error
}

func startServer(t *testing.T, dir string) *server {
	t.Helper()
	apiLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	flowsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &server{
		api:    "http://" + apiLn.Addr().String(),
		flows:  "http://" + flowsLn.Addr().String(),
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() { s.done <- runServer(ctx, serverConfig{dir: dir, keep: 100}, apiLn, flowsLn) }()
	t.Cleanup(func() { s.stop(t) })
	return s
}

// stop shuts the server down and checks it returned cleanly. Safe to call
// twice.
func (s *server) stop(t *testing.T) {
	t.Helper()
	if s.done == nil {
		return
	}
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("runServer = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not return after cancel")
	}
	s.done = nil
}

// call sends a request and decodes a JSON answer into out (if non-nil).
func call(t *testing.T, method, url, body string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, url, err)
		}
	}
	return resp.StatusCode
}

// TestRunServer is the server round trip: a flow is stored and deployed
// through the API, called on the flows listener, and running again after a
// restart on the same data directory.
func TestRunServer(t *testing.T) {
	dir := t.TempDir()
	s := startServer(t, dir)

	var health map[string]string
	if code := call(t, "GET", s.api+"/api/health", "", &health); code != 200 || health["status"] != "ok" {
		t.Fatalf("health = %d %v", code, health)
	}

	// Test runs still work.
	flowJSON, err := os.ReadFile(helloFlow)
	if err != nil {
		t.Fatal(err)
	}
	var run struct{ Status string }
	call(t, "POST", s.api+"/api/flows/run", `{"flow": `+string(flowJSON)+`, "payload": {"body": {"name": "ada"}}}`, &run)
	if run.Status != "completed" {
		t.Errorf("test run = %q, want completed", run.Status)
	}

	if code := call(t, "POST", s.api+"/api/flows", `{"flow": `+string(flowJSON)+`}`, nil); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	var started struct{ State string }
	call(t, "POST", s.api+"/api/flows/flow-hello/start", "", &started)
	if started.State != "running" {
		t.Fatalf("start state = %q", started.State)
	}
	if code := call(t, "POST", s.flows+"/webhook", `{"name": "ada"}`, nil); code != http.StatusAccepted {
		t.Errorf("POST /webhook = %d, want 202", code)
	}
	// The editor page is served at / beside the API.
	resp, err := http.Get(s.api + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("GET / = %d %s, want an HTML page", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Unknown API paths are API 404s, not the editor.
	if code := call(t, "GET", s.api+"/api/nope", "", nil); code != http.StatusNotFound {
		t.Errorf("GET /api/nope = %d, want 404", code)
	}

	// The editor API is not served on the flows listener, and the reverse.
	if code := call(t, "GET", s.flows+"/api/flows", "", nil); code != http.StatusNotFound {
		t.Errorf("API on the flows listener = %d, want 404", code)
	}
	if code := call(t, "POST", s.api+"/webhook", "{}", nil); code != http.StatusNotFound {
		t.Errorf("flow route on the editor listener = %d, want 404", code)
	}

	// The run is in the history, with its trace.
	var page struct {
		Executions []struct{ ID, Status string }
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(page.Executions) == 0 || page.Executions[0].Status == "running" {
		if time.Now().After(deadline) {
			t.Fatalf("no finished execution in the history: %+v", page)
		}
		time.Sleep(10 * time.Millisecond)
		call(t, "GET", s.api+"/api/executions?flow=flow-hello", "", &page)
	}
	var exec struct {
		Status string
		Trace  []struct{ Kind string }
	}
	call(t, "GET", s.api+"/api/executions/"+page.Executions[0].ID, "", &exec)
	if exec.Status != "completed" || len(exec.Trace) == 0 || exec.Trace[len(exec.Trace)-1].Kind != "execution_completed" {
		t.Errorf("execution = %+v", exec)
	}

	// Restart on the same data directory: the flow is deployed again.
	s.stop(t)
	s = startServer(t, dir)
	var got struct {
		State           string
		RunningRevision int64
	}
	call(t, "GET", s.api+"/api/flows/flow-hello", "", &got)
	if got.State != "running" || got.RunningRevision != 1 {
		t.Errorf("after restart = %+v, want running revision 1", got)
	}
	if code := call(t, "POST", s.flows+"/webhook", `{"name": "ada"}`, nil); code != http.StatusAccepted {
		t.Errorf("POST /webhook after restart = %d, want 202", code)
	}
}

func TestCLI_ServeDataDirInUse(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var stderr bytes.Buffer
	code := cli([]string{"serve", "-addr", "127.0.0.1:0", "-flows-addr", "127.0.0.1:0", "-data", dir}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "in use") {
		t.Errorf("exit %d, stderr %q; want 1 and the data directory in use", code, stderr.String())
	}
}

func TestDataDir(t *testing.T) {
	t.Setenv("FLOWFORGE_DATA", "/from/env")
	if got, _ := dataDir("/from/flag"); got != "/from/flag" {
		t.Errorf("flag: %s", got)
	}
	if got, _ := dataDir(""); got != "/from/env" {
		t.Errorf("env: %s", got)
	}
	t.Setenv("FLOWFORGE_DATA", "")
	home, _ := os.UserHomeDir()
	if got, _ := dataDir(""); got != filepath.Join(home, ".flowforge") {
		t.Errorf("default: %s", got)
	}
}

func TestCLI_LogFormat(t *testing.T) {
	for _, cmd := range []string{"run", "serve"} {
		args := []string{cmd, "-log-format", "xml"}
		if cmd == "run" {
			args = append(args, helloFlow)
		}
		var stderr bytes.Buffer
		if code := cli(args, &stderr); code != 2 || !strings.Contains(stderr.String(), "text or json") {
			t.Errorf("%s: exit %d, stderr %q; want 2 and the allowed formats", cmd, code, stderr.String())
		}
	}

	defer slog.SetDefault(slog.Default())
	var buf bytes.Buffer
	if code := setLogger("json", &buf); code != -1 {
		t.Fatalf("setLogger(json) = %d", code)
	}
	slog.Info("hello", slog.String("k", "v"))
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("json log = %q (%v)", buf.String(), err)
	}
}
