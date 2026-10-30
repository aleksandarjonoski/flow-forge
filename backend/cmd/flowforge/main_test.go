package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

const helloFlow = "../../examples/hello.flow.json"

func TestCLI_UsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":         nil,
		"unknown command": {"start", helloFlow},
		"no file":         {"run"},
		"two files":       {"run", "a.json", "b.json"},
		"unknown flag":    {"run", "-port", "1", helloFlow},
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
