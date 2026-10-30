package nodes

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// fakeTrigger stands in for http_trigger until slice 7. It never fires on its
// own; tests drive executions directly through the Executor.
type fakeTrigger struct{}

func (fakeTrigger) Type() string                                      { return "http_trigger" }
func (fakeTrigger) Init(map[string]any, *engine.EngineServices) error { return nil }
func (fakeTrigger) Start(context.Context, engine.Emitter) error       { return nil }
func (fakeTrigger) Stop() error                                       { return nil }

func TestBuiltinsRegistered(t *testing.T) {
	for _, typ := range []string{TransformType, LogType} {
		if !engine.DefaultRegistry.Has(typ) {
			t.Errorf("DefaultRegistry is missing %q", typ)
		}
	}
}

// compileHello compiles examples/hello.flow.json with the real transform and
// log nodes and a fake http_trigger.
func compileHello(t *testing.T) *engine.CompiledFlow {
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
	reg.Register("http_trigger", func() engine.Node { return fakeTrigger{} })
	reg.Register(TransformType, func() engine.Node { return &Transform{} })
	reg.Register(LogType, func() engine.Node { return &Log{} })

	cf, err := engine.Compile(fl, reg, &engine.EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return cf
}

func TestHelloFlow_EndToEnd(t *testing.T) {
	cf := compileHello(t)
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
	cf := compileHello(t)
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
