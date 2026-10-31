package nodes

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// orderSchema is a small declared schema used across the tests.
func orderSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "integer"},
			"customer": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"email": map[string]any{"type": "string"},
				},
				"required": []any{"email"},
			},
		},
		"required": []any{"id", "customer"},
	}
}

func newParseJSON(t *testing.T, config map[string]any) *ParseJSON {
	t.Helper()
	p := &ParseJSON{}
	if err := p.Init(config, nil); err != nil {
		t.Fatalf("Init(%v): %v", config, err)
	}
	return p
}

func TestParseJSON_Type(t *testing.T) {
	if got := (&ParseJSON{}).Type(); got != "parse_json" {
		t.Errorf("Type() = %q, want parse_json", got)
	}
}

func TestParseJSON_InitErrors(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{"missing schema", map[string]any{}, `"schema" is required`},
		{"schema problems", map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{
			"a": map[string]any{"type": "strin"},
			"b": map[string]any{"type": "string", "minLength": 1},
		}}}, `config "schema": a: unknown type "strin"; b: unsupported keyword "minLength"`},
		{"top level not an object", map[string]any{"schema": map[string]any{"type": "array"}}, "top level must be of type object, got array"},
		{"bad from", map[string]any{"from": "body..x", "schema": orderSchema()}, `"body..x" is not a valid path`},
		{"non-string from", map[string]any{"from": 1, "schema": orderSchema()}, "must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&ParseJSON{}).Init(tc.config, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Init error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseJSON_StructuredBody(t *testing.T) {
	p := newParseJSON(t, map[string]any{"from": "body", "schema": orderSchema()})
	input := engine.Payload{"headers": map[string]any{}, "body": map[string]any{
		"id":       float64(7),
		"customer": map[string]any{"email": "a@b.c", "phone": "123"},
		"debug":    true,
	}}

	out, err := p.Execute(&engine.ExecutionContext{}, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := engine.Payload{"id": float64(7), "customer": map[string]any{"email": "a@b.c"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output = %v, want %v (extra fields dropped)", out, want)
	}
}

func TestParseJSON_StringBody(t *testing.T) {
	p := newParseJSON(t, map[string]any{"from": "body", "schema": orderSchema()})
	input := engine.Payload{"body": `{"id": 1, "customer": {"email": "x@y.z"}}`}

	out, err := p.Execute(&engine.ExecutionContext{}, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["id"] != float64(1) {
		t.Errorf("output = %v, want the parsed object", out)
	}
}

func TestParseJSON_WholeInput(t *testing.T) {
	p := newParseJSON(t, map[string]any{"schema": orderSchema()})
	out, err := p.Execute(&engine.ExecutionContext{}, engine.Payload{
		"id": float64(2), "customer": map[string]any{"email": "e"}, "other": 1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, has := out["other"]; has || out["id"] != float64(2) {
		t.Errorf("output = %v, want the input validated with extras dropped", out)
	}
}

func TestParseJSON_Failures(t *testing.T) {
	p := newParseJSON(t, map[string]any{"from": "body", "schema": orderSchema()})
	cases := []struct {
		name    string
		input   engine.Payload
		wantErr string
	}{
		{"every mismatch listed", engine.Payload{"body": map[string]any{"id": "7", "customer": map[string]any{}}},
			`body does not match the schema: customer.email: required field is missing; id: expected integer, got string`},
		{"invalid JSON text", engine.Payload{"body": `{"id": `}, "body: invalid JSON"},
		{"not an object", engine.Payload{"body": `[1, 2]`}, "body does not match the schema: expected object, got array"},
		{"missing from path", engine.Payload{"headers": map[string]any{}}, `from: no value at "body"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Execute(&engine.ExecutionContext{}, tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
