package nodes

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// greetingSchema declares {greeting, customer: {city, vip}} with greeting
// and customer.city required.
func greetingSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"greeting": map[string]any{"type": "string"},
			"customer": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
					"vip":  map[string]any{"type": "boolean"},
				},
				"required": []any{"city"},
			},
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"greeting", "customer"},
	}
}

func newMappingTransform(t *testing.T, mapping map[string]any) *Transform {
	t.Helper()
	tr := &Transform{}
	if err := tr.Init(map[string]any{"schema": greetingSchema(), "mapping": mapping}, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return tr
}

func TestTransformMapping_BuildsOutput(t *testing.T) {
	tr := newMappingTransform(t, map[string]any{
		"greeting":      `'hello ' + input.body.name`,
		"customer.city": `nodes["node-1"].body.address.city`,
		"customer.vip":  `input.body.orders > 10`,
		"count":         `len(input.body.tags)`,
	})
	ctx := &engine.ExecutionContext{NodeResults: map[string]engine.Payload{
		"node-1": {"body": map[string]any{"address": map[string]any{"city": "Skopje"}}},
	}}
	input := engine.Payload{"body": map[string]any{"name": "ada", "orders": 12, "tags": []any{"a", "b"}}}

	out, err := tr.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := engine.Payload{
		"greeting": "hello ada",
		"customer": map[string]any{"city": "Skopje", "vip": true},
		"count":    2,
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output = %#v, want %#v", out, want)
	}
}

func TestTransformMapping_WholeObjectIsCleaned(t *testing.T) {
	tr := newMappingTransform(t, map[string]any{
		"greeting": `"hi"`,
		"customer": `input.body.customer`,
	})
	out, err := tr.Execute(&engine.ExecutionContext{}, engine.Payload{"body": map[string]any{
		"customer": map[string]any{"city": "Ohrid", "secret": "x"},
	}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !reflect.DeepEqual(out["customer"], map[string]any{"city": "Ohrid"}) {
		t.Errorf("customer = %v, want the extra field dropped", out["customer"])
	}
}

func TestTransformMapping_InitErrors(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{"no schema", map[string]any{"mapping": map[string]any{}}, `"schema" is required in mapping mode`},
		{"bad schema", map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "x"}}}, "mapping": map[string]any{}},
			`config "schema": a: unknown type "x"`},
		{"array top level", map[string]any{"schema": map[string]any{"type": "array"}, "mapping": map[string]any{}}, "top level must be of type object"},
		{"mapping not an object", map[string]any{"schema": greetingSchema(), "mapping": "x"}, "must be an object"},
		{"every mapping problem", map[string]any{"schema": greetingSchema(), "mapping": map[string]any{
			"greeting":     `'hi' +`,
			"customer.zip": `"1000"`,
			"count":        7,
		}}, `config "mapping": customer.zip: no such field in the schema; customer: required field is not mapped; count: expression must be text; greeting: compile: unexpected token EOF`},
		{"unknown identifier", map[string]any{"schema": greetingSchema(), "mapping": map[string]any{
			"greeting": `foo.bar`, "customer.city": `"x"`,
		}}, `greeting: compile: unknown name foo`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Transform{}).Init(tc.config, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Init error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestTransformMapping_RuntimeErrors(t *testing.T) {
	tr := newMappingTransform(t, map[string]any{
		"greeting":      `input.body.name`,
		"customer.city": `input.body.city`,
		"customer.vip":  `input.body.vip`,
	})
	cases := []struct {
		name    string
		input   engine.Payload
		wantErr string
	}{
		{"output fails the schema", engine.Payload{"body": map[string]any{"name": 5, "city": "x", "vip": "yes"}},
			`output does not match the schema: customer.vip: expected boolean, got string; greeting: expected string, got number`},
		{"missing required value", engine.Payload{"body": map[string]any{"city": "x", "vip": true}},
			`greeting: expected string, got null`},
		{"evaluation error names the field", engine.Payload{"body": "text"},
			`customer.city: evaluate:`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tr.Execute(&engine.ExecutionContext{}, tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
