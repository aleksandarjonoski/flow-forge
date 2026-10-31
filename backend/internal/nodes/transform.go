package nodes

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/schema"
)

// TransformType is the registered type name of the transform node.
const TransformType = "transform"

func init() {
	engine.DefaultRegistry.Register(TransformType, func() engine.Node { return &Transform{} })
}

// Transform computes a new payload from its input, in one of two modes (see
// docs/engine-v1.md §11 and docs/data-v1.md §5):
//   - expression: one expr-lang expression whose result is the output;
//   - mapping: a declared output schema plus one expression per field; the
//     output is built field by field and must match the schema.
//
// The programs, schema and env snapshot are set once at Init and only read
// afterwards, so Execute is safe for concurrent executions.
type Transform struct {
	// Expression mode.
	program *vm.Program

	// Mapping mode.
	schema   *schema.Schema
	mappings []fieldMapping // sorted by path

	env map[string]string
}

// fieldMapping is one compiled mapping: the field it fills and its program.
type fieldMapping struct {
	path    string
	program *vm.Program
}

func (t *Transform) Type() string { return TransformType }

// Init picks the mode from the config and compiles everything, so syntax
// errors, unknown identifiers and mapping mistakes fail at load time.
func (t *Transform) Init(config map[string]any, _ *engine.EngineServices) error {
	_, hasExpr := config["expression"]
	_, hasSchema := config["schema"]
	_, hasMapping := config["mapping"]
	switch {
	case hasExpr && (hasSchema || hasMapping):
		return errors.New(`config has both "expression" and "schema"/"mapping"; use one mode`)
	case hasExpr:
		if err := t.initExpression(config); err != nil {
			return err
		}
	case hasSchema || hasMapping:
		if err := t.initMapping(config); err != nil {
			return err
		}
	default:
		return errors.New(`config needs either "expression", or "schema" and "mapping"`)
	}
	t.env = environ()
	return nil
}

func (t *Transform) initExpression(config map[string]any) error {
	expression, err := stringConfig(config, "expression", true, "")
	if err != nil {
		return err
	}
	program, err := compile(expression)
	if err != nil {
		return fmt.Errorf("compile expression: %w", err)
	}
	t.program = program
	return nil
}

// initMapping parses the schema and compiles every field's expression,
// reporting all problems at once.
func (t *Transform) initMapping(config map[string]any) error {
	raw, ok := config["schema"]
	if !ok || raw == nil {
		return fmt.Errorf("config %q is required in mapping mode", "schema")
	}
	s, err := schema.Parse(raw)
	if err != nil {
		return fmt.Errorf("config %q: %s", "schema", oneLine(err))
	}
	if s.Type != "object" {
		return fmt.Errorf("config %q: the top level must be of type object, got %s", "schema", s.Type)
	}
	mapping, ok := config["mapping"].(map[string]any)
	if config["mapping"] != nil && !ok {
		return fmt.Errorf("config %q must be an object of field path → expression", "mapping")
	}

	var problems []string
	for _, p := range s.CheckMapping(mapKeys(mapping)) {
		problems = append(problems, p.String())
	}
	var mappings []fieldMapping
	for _, path := range mapKeys(mapping) {
		text, ok := mapping[path].(string)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expression must be text", path))
			continue
		}
		program, err := compile(text)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: compile: %s", path, oneLineText(err.Error())))
			continue
		}
		mappings = append(mappings, fieldMapping{path: path, program: program})
	}
	if len(problems) > 0 {
		return fmt.Errorf("config %q: %s", "mapping", strings.Join(problems, "; "))
	}
	t.schema = s
	t.mappings = mappings
	return nil
}

func (t *Transform) Execute(ctx *engine.ExecutionContext, input engine.Payload) (engine.Payload, error) {
	env := transformEnv(input, ctx.NodeResults, t.env)
	if t.program != nil {
		result, err := expr.Run(t.program, env)
		if err != nil {
			return nil, fmt.Errorf("evaluate expression: %w", err)
		}
		return toPayload(result), nil
	}

	out := map[string]any{}
	var problems []string
	for _, m := range t.mappings {
		v, err := expr.Run(m.program, env)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: evaluate: %s", m.path, oneLineText(err.Error())))
			continue
		}
		setPath(out, strings.Split(m.path, "."), v)
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	cleaned, mismatches := t.schema.Validate(out)
	if len(mismatches) > 0 {
		return nil, fmt.Errorf("output does not match the schema: %s", oneLine(schema.Error(mismatches)))
	}
	// The top level is an object (checked at Init) and validation passed.
	return cleaned.(map[string]any), nil
}

// compile compiles an expression against the declared identifiers (input,
// nodes, env).
func compile(expression string) (*vm.Program, error) {
	return expr.Compile(expression, expr.Env(transformEnv(nil, nil, nil)))
}

// transformEnv builds the identifier set visible to expressions. Init calls it
// with nil values purely to declare the identifiers and their types.
func transformEnv(input engine.Payload, nodes map[string]engine.Payload, env map[string]string) map[string]any {
	return map[string]any{
		"input": input,
		"nodes": nodes,
		"env":   env,
	}
}

// setPath sets v at a dot path inside out, creating objects on the way.
// CheckMapping guarantees no mapped path lies inside another one.
func setPath(out map[string]any, path []string, v any) {
	cur := out
	for _, name := range path[:len(path)-1] {
		next, ok := cur[name].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[name] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = v
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// oneLineText collapses expr-lang's multi-line error messages (which point
// at the offending column) onto one line.
func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// toPayload coerces an expression result into a Payload: maps pass through,
// nil stays nil, anything else is wrapped as {"value": v}.
func toPayload(v any) engine.Payload {
	switch r := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return r
	default:
		return engine.Payload{"value": r}
	}
}

// environ returns the process environment as a map.
func environ() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}
