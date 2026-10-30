package nodes

import (
	"fmt"
	"os"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// TransformType is the registered type name of the transform node.
const TransformType = "transform"

func init() {
	engine.DefaultRegistry.Register(TransformType, func() engine.Node { return &Transform{} })
}

// Transform evaluates an expr-lang expression against the node's input and
// returns the result as the output payload. See docs/engine-v1.md §11.
//
// The program and env snapshot are set once at Init and only read afterwards,
// so Execute is safe for concurrent executions.
type Transform struct {
	program *vm.Program
	env     map[string]string
}

func (t *Transform) Type() string { return TransformType }

// Init compiles the expression against the declared identifiers (input,
// nodes, env), so syntax errors and unknown identifiers fail at load time.
func (t *Transform) Init(config map[string]any, _ *engine.EngineServices) error {
	expression, err := stringConfig(config, "expression", true, "")
	if err != nil {
		return err
	}
	program, err := expr.Compile(expression, expr.Env(transformEnv(nil, nil, nil)))
	if err != nil {
		return fmt.Errorf("compile expression: %w", err)
	}
	t.program = program
	t.env = environ()
	return nil
}

func (t *Transform) Execute(ctx *engine.ExecutionContext, input engine.Payload) (engine.Payload, error) {
	result, err := expr.Run(t.program, transformEnv(input, ctx.NodeResults, t.env))
	if err != nil {
		return nil, fmt.Errorf("evaluate expression: %w", err)
	}
	return toPayload(result), nil
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
