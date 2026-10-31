// Package schema implements the JSON Schema subset FlowForge uses to declare
// data shapes: parsing a schema (rejecting anything outside the subset) and
// validating data against it strictly, dropping fields the schema does not
// describe. See docs/data-v1.md §3.
//
// The editor has a TypeScript twin (frontend/src/flow/schema.ts); both are
// tested against testdata/cases.json, so their behaviour and messages match.
package schema

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// Schema is a parsed schema node.
type Schema struct {
	Type        string
	Properties  map[string]*Schema // object only
	Required    []string           // object only
	Items       *Schema            // array only
	Enum        []any              // string, number, integer only
	Title       string
	Description string
}

var types = []string{"object", "array", "string", "number", "integer", "boolean", "null"}

var keywords = []string{"type", "properties", "required", "items", "enum", "title", "description"}

// Problem is one way a schema or a value is wrong, at a path such as
// "customer.address.city" or "orders[2].id" ("" for the top level).
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Error joins problems into one error, one problem per line.
type Error []Problem

func (e Error) Error() string {
	lines := make([]string, len(e))
	for i, p := range e {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

// Parse reads a schema from decoded JSON (as found in a node's config). It
// reports every problem: unknown keywords, bad types, inconsistent required
// lists, and so on. Problem paths are field paths, so users can find them.
func Parse(raw any) (*Schema, error) {
	var problems []Problem
	s := parse(raw, "", &problems)
	if len(problems) > 0 {
		return nil, Error(problems)
	}
	return s, nil
}

func parse(raw any, path string, problems *[]Problem) *Schema {
	add := func(format string, args ...any) {
		*problems = append(*problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	m, ok := raw.(map[string]any)
	if !ok {
		add("must be a schema object, got %s", typeName(raw))
		return nil
	}
	for _, k := range sortedKeys(m) {
		if !slices.Contains(keywords, k) {
			add("unsupported keyword %q", k)
		}
	}

	s := &Schema{}
	t, ok := m["type"].(string)
	switch {
	case m["type"] == nil:
		add("type is required")
	case !ok:
		add("type must be a string")
	case !slices.Contains(types, t):
		add("unknown type %q", t)
	default:
		s.Type = t
	}
	s.Title = stringKeyword(m, "title", add)
	s.Description = stringKeyword(m, "description", add)

	if props, present := m["properties"]; present {
		pm, ok := props.(map[string]any)
		switch {
		case s.Type != "" && s.Type != "object":
			add("properties only apply to type object")
		case !ok:
			add("properties must be an object")
		default:
			s.Properties = make(map[string]*Schema, len(pm))
			for _, name := range sortedKeys(pm) {
				if name == "" || strings.Contains(name, ".") {
					add("field name %q must be non-empty and must not contain \".\"", name)
					continue
				}
				s.Properties[name] = parse(pm[name], join(path, name), problems)
			}
		}
	}
	if req, present := m["required"]; present {
		list, ok := req.([]any)
		if s.Type != "" && s.Type != "object" {
			add("required only applies to type object")
		} else if !ok {
			add("required must be a list of field names")
		} else {
			for _, r := range list {
				name, ok := r.(string)
				switch {
				case !ok:
					add("required must be a list of field names")
				case s.Properties[name] == nil:
					add("required field %q is not in properties", name)
				default:
					s.Required = append(s.Required, name)
				}
			}
		}
	}
	if items, present := m["items"]; present {
		if s.Type != "" && s.Type != "array" {
			add("items only apply to type array")
		} else {
			s.Items = parse(items, path+"[]", problems)
		}
	}
	if enum, present := m["enum"]; present {
		list, ok := enum.([]any)
		switch {
		case s.Type != "" && s.Type != "string" && s.Type != "number" && s.Type != "integer":
			add("enum only applies to types string, number and integer")
		case !ok || len(list) == 0:
			add("enum must be a non-empty list")
		default:
			for _, v := range list {
				if s.Type != "" && !matches(s.Type, v) {
					add("enum value %s is not of type %s", show(v), s.Type)
				}
			}
			s.Enum = list
		}
	}
	return s
}

func stringKeyword(m map[string]any, key string, add func(string, ...any)) string {
	v, present := m[key]
	if !present {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		add("%s must be a string", key)
	}
	return str
}

// Validate checks value against s. It returns the value with fields not in
// the schema removed, and every problem found. Types are strict: "42" is not
// a number, 1.5 is not an integer.
func (s *Schema) Validate(value any) (any, []Problem) {
	var problems []Problem
	out := s.validate(value, "", &problems)
	return out, problems
}

func (s *Schema) validate(v any, path string, problems *[]Problem) any {
	add := func(format string, args ...any) {
		*problems = append(*problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if !matches(s.Type, v) {
		add("expected %s, got %s", s.Type, typeName(v))
		return v
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e any) bool { return equal(e, v) }) {
		shown := make([]string, len(s.Enum))
		for i, e := range s.Enum {
			shown[i] = show(e)
		}
		add("must be one of %s", strings.Join(shown, ", "))
		return v
	}
	switch s.Type {
	case "object":
		m := v.(map[string]any)
		out := make(map[string]any, len(s.Properties))
		for _, name := range sortedKeys(s.Properties) {
			child, present := m[name]
			if !present {
				if slices.Contains(s.Required, name) {
					*problems = append(*problems, Problem{Path: join(path, name), Message: "required field is missing"})
				}
				continue
			}
			out[name] = s.Properties[name].validate(child, join(path, name), problems)
		}
		return out
	case "array":
		items := toSlice(v)
		if s.Items == nil {
			return items
		}
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = s.Items.validate(item, fmt.Sprintf("%s[%d]", path, i), problems)
		}
		return out
	}
	return v
}

// matches reports whether v is of schema type t.
func matches(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		return v != nil && reflect.TypeOf(v).Kind() == reflect.Slice
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := toFloat(v)
		return ok
	case "integer":
		f, ok := toFloat(v)
		return ok && f == math.Trunc(f)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}

// typeName names v's type in schema terms, for messages.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case map[string]any:
		return "object"
	}
	if _, ok := toFloat(v); ok {
		return "number"
	}
	if reflect.TypeOf(v).Kind() == reflect.Slice {
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

// toFloat converts any Go number (JSON gives float64; expressions may give
// int) to float64.
func toFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	}
	return 0, false
}

func toSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	rv := reflect.ValueOf(v)
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

func equal(a, b any) bool {
	fa, aNum := toFloat(a)
	fb, bNum := toFloat(b)
	if aNum && bNum {
		return fa == fb
	}
	return a == b
}

// show formats a value as JSON for messages.
func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
