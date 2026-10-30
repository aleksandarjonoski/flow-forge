package nodes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

func TestBuiltinsRegistered(t *testing.T) {
	for _, typ := range []string{HTTPTriggerType, HTTPRequestType, TransformType, LogType} {
		if !engine.DefaultRegistry.Has(typ) {
			t.Errorf("DefaultRegistry is missing %q", typ)
		}
	}
}

// compileHello compiles examples/hello.flow.json against an isolated registry
// holding the real built-in nodes.
func compileHello(t *testing.T) (*engine.CompiledFlow, *engine.EngineServices) {
	t.Helper()
	f, err := os.Open("../../examples/hello.flow.json")
	if err != nil {
		t.Fatalf("open example: %v", err)
	}
	defer f.Close()
	fl, err := flow.Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	reg := engine.NewRegistry()
	reg.Register(HTTPTriggerType, func() engine.Node { return &HTTPTrigger{} })
	reg.Register(TransformType, func() engine.Node { return &Transform{} })
	reg.Register(LogType, func() engine.Node { return &Log{} })

	svc := engine.NewEngineServices()
	cf, err := engine.Compile(fl, reg, svc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return cf, svc
}

func TestHelloFlow_EndToEnd(t *testing.T) {
	cf, _ := compileHello(t)
	var buf bytes.Buffer
	cf.Nodes["node-3"].(*Log).logger = newLog(t, nil, &buf).logger

	sink := engine.NewMemoryTraceSink()
	trigger := engine.Payload{"body": map[string]any{"name": "ada"}}
	if err := engine.NewExecutor(cf, sink).Execute(context.Background(), "node-1", trigger); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var kinds []engine.TraceKind
	outputs := map[string]engine.Payload{}
	for _, ev := range sink.Events() {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == engine.TraceNodeCompleted {
			outputs[ev.NodeID] = ev.Output
		}
	}
	wantKinds := []engine.TraceKind{
		engine.TraceExecutionStarted,
		engine.TraceNodeStarted, engine.TraceNodeCompleted, // transform
		engine.TraceNodeStarted, engine.TraceNodeCompleted, // log
		engine.TraceExecutionCompleted,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Errorf("trace kinds = %v, want %v", kinds, wantKinds)
	}

	want := engine.Payload{"greeting": "hello ada"}
	if !reflect.DeepEqual(outputs["node-2"], want) {
		t.Errorf("transform output = %v, want %v", outputs["node-2"], want)
	}
	if !reflect.DeepEqual(outputs["node-3"], want) {
		t.Errorf("log output = %v, want passthrough %v", outputs["node-3"], want)
	}
	if !strings.Contains(buf.String(), `"greeting":"hello ada"`) {
		t.Errorf("log record = %q, want it to contain the greeting", buf.String())
	}
}

func TestHelloFlow_TransformFailureHalts(t *testing.T) {
	cf, _ := compileHello(t)
	var buf bytes.Buffer
	cf.Nodes["node-3"].(*Log).logger = newLog(t, nil, &buf).logger

	// No body: "'hello ' + input.body.name" fails, so log must never run.
	err := engine.NewExecutor(cf, nil).Execute(context.Background(), "node-1", engine.Payload{})
	if err == nil {
		t.Fatal("Execute succeeded, want error")
	}
	if !strings.Contains(err.Error(), "node node-2 (transform)") {
		t.Errorf("error = %q, want it to name node-2", err)
	}
	if buf.Len() != 0 {
		t.Errorf("log node ran after failure: %q", buf.String())
	}
}

// TestHelloFlow_OverHTTP drives the whole flow through a real HTTP request:
// http_trigger → transform → log.
func TestHelloFlow_OverHTTP(t *testing.T) {
	cf, svc := compileHello(t)
	var buf bytes.Buffer
	cf.Nodes["node-3"].(*Log).logger = newLog(t, nil, &buf).logger

	exec := engine.NewExecutor(cf, nil)
	trigger := cf.Nodes["node-1"].(*HTTPTrigger)
	emit := func(p engine.Payload) error {
		return exec.Execute(context.Background(), "node-1", p)
	}
	if err := trigger.Start(context.Background(), emit); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer trigger.Stop()

	srv := httptest.NewServer(svc.HTTP)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/webhook", "application/json", strings.NewReader(`{"name":"ada"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if !strings.Contains(buf.String(), `"greeting":"hello ada"`) {
		t.Errorf("log record = %q, want it to contain the greeting", buf.String())
	}
}
