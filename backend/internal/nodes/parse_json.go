package nodes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/schema"
)

// ParseJSONType is the registered type name of the Parse JSON node.
const ParseJSONType = "parse_json"

func init() {
	engine.DefaultRegistry.Register(ParseJSONType, func() engine.Node { return &ParseJSON{} })
}

// ParseJSON turns raw or loosely shaped data into data that matches a
// declared schema: a JSON string is parsed, structured data is taken as is,
// and the result is validated strictly with extra fields dropped. See
// docs/data-v1.md §4.
//
// All fields are set once at Init and only read afterwards, so Execute is safe
// for concurrent executions.
type ParseJSON struct {
	from   []string // path into the input; nil means the whole input
	schema *schema.Schema
}

func (p *ParseJSON) Type() string { return ParseJSONType }

// Init reads `from` and parses `schema`, so a bad path or schema fails at
// load time.
func (p *ParseJSON) Init(config map[string]any, _ *engine.EngineServices) error {
	from, err := stringConfig(config, "from", false, "")
	if err != nil {
		return err
	}
	if from != "" {
		p.from = strings.Split(from, ".")
		for _, seg := range p.from {
			if seg == "" {
				return fmt.Errorf("config %q: %q is not a valid path", "from", from)
			}
		}
	}

	raw, ok := config["schema"]
	if !ok || raw == nil {
		return fmt.Errorf("config %q is required", "schema")
	}
	s, err := schema.Parse(raw)
	if err != nil {
		return fmt.Errorf("config %q: %s", "schema", oneLine(err))
	}
	if s.Type != "object" {
		return fmt.Errorf("config %q: the top level must be of type object, got %s", "schema", s.Type)
	}
	p.schema = s
	return nil
}

func (p *ParseJSON) Execute(_ *engine.ExecutionContext, input engine.Payload) (engine.Payload, error) {
	var value any = input
	if p.from != nil {
		v, err := lookupPath(input, p.from)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		value = v
	}
	if text, ok := value.(string); ok {
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return nil, fmt.Errorf("%s: invalid JSON: %w", p.source(), err)
		}
	}

	out, problems := p.schema.Validate(value)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s does not match the schema: %s", p.source(), oneLine(schema.Error(problems)))
	}
	// The top level is an object (checked at Init) and validation passed.
	return out.(map[string]any), nil
}

// source names the parsed value in error messages.
func (p *ParseJSON) source() string {
	if p.from == nil {
		return "input"
	}
	return strings.Join(p.from, ".")
}

// oneLine joins a multi-problem error onto one line, so it reads well in
// logs and traces.
func oneLine(err error) string {
	var se schema.Error
	if !errors.As(err, &se) {
		return err.Error()
	}
	parts := make([]string, len(se))
	for i, problem := range se {
		parts[i] = problem.String()
	}
	return strings.Join(parts, "; ")
}
