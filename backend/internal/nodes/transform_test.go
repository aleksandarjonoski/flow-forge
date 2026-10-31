package nodes

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

func newTransform(t *testing.T, expression string) *Transform {
	t.Helper()
	tr := &Transform{}
	if err := tr.Init(map[string]any{"expression": expression}, nil); err != nil {
		t.Fatalf("Init(%q): %v", expression, err)
	}
	return tr
}

func TestTransform_Type(t *testing.T) {
	if got := (&Transform{}).Type(); got != "transform" {
		t.Errorf("Type() = %q, want transform", got)
	}
}

func TestTransform_InitErrors(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{"no mode", map[string]any{}, `needs either "expression", or "schema" and "mapping"`},
		{"both modes", map[string]any{"expression": "1", "mapping": map[string]any{}}, "use one mode"},
		{"empty expression", map[string]any{"expression": ""}, "must not be empty"},
		{"non-string expression", map[string]any{"expression": 42}, "must be a string"},
		{"syntax error", map[string]any{"expression": "input.("}, "compile expression"},
		{"unknown identifier", map[string]any{"expression": "foo.bar"}, "compile expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Transform{}).Init(tc.config, nil)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestTransform_MapResult(t *testing.T) {
	tr := newTransform(t, "{ greeting: 'hello ' + input.body.name }")
	input := engine.Payload{"body": map[string]any{"name": "ada"}}

	out, err := tr.Execute(&engine.ExecutionContext{}, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := engine.Payload{"greeting": "hello ada"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output = %v, want %v", out, want)
	}
}

func TestTransform_ScalarResultIsWrapped(t *testing.T) {
	tr := newTransform(t, "input.n * 2")

	out, err := tr.Execute(&engine.ExecutionContext{}, engine.Payload{"n": 21})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := engine.Payload{"value": 42}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output = %v, want %v", out, want)
	}
}

func TestTransform_NilResult(t *testing.T) {
	tr := newTransform(t, "nil")

	out, err := tr.Execute(&engine.ExecutionContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != nil {
		t.Errorf("output = %v, want nil", out)
	}
}

func TestTransform_ReadsNodeResults(t *testing.T) {
	tr := newTransform(t, `{ id: nodes["node-1"].body.id }`)
	ctx := &engine.ExecutionContext{
		NodeResults: map[string]engine.Payload{
			"node-1": {"body": map[string]any{"id": "abc"}},
		},
	}

	out, err := tr.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["id"] != "abc" {
		t.Errorf("output = %v, want id=abc", out)
	}
}

func TestTransform_ReadsEnv(t *testing.T) {
	t.Setenv("FLOWFORGE_TEST_VAR", "from-env")
	// env is snapshotted at Init, so Init must run after Setenv.
	tr := newTransform(t, "env.FLOWFORGE_TEST_VAR")

	out, err := tr.Execute(&engine.ExecutionContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["value"] != "from-env" {
		t.Errorf("output = %v, want value=from-env", out)
	}
}

func TestTransform_RuntimeError(t *testing.T) {
	tr := newTransform(t, "input.a / input.b")

	_, err := tr.Execute(&engine.ExecutionContext{}, engine.Payload{"a": 1, "b": "x"})
	if err == nil {
		t.Fatal("Execute succeeded, want error")
	}
	if !strings.Contains(err.Error(), "evaluate expression") {
		t.Errorf("error = %q, want it to mention evaluate expression", err)
	}
}
