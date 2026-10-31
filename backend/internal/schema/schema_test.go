package schema

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

// cases are shared with the TypeScript twin (frontend/src/flow/schema.ts).
type cases struct {
	Parse []struct {
		Name     string   `json:"name"`
		Schema   any      `json:"schema"`
		Problems []string `json:"problems"`
	} `json:"parse"`
	Validate []struct {
		Name     string   `json:"name"`
		Schema   any      `json:"schema"`
		Value    any      `json:"value"`
		Output   any      `json:"output"`
		Problems []string `json:"problems"`
	} `json:"validate"`
}

func loadCases(t *testing.T) cases {
	t.Helper()
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c cases
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func messages(problems []Problem) []string {
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = p.String()
	}
	return out
}

func TestParse_SharedCases(t *testing.T) {
	for _, tc := range loadCases(t).Parse {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := Parse(tc.Schema)
			var got []string
			var se Error
			if errors.As(err, &se) {
				got = messages(se)
			} else if err != nil {
				t.Fatalf("Parse error is %T, want schema.Error", err)
			}
			if len(got) == 0 && len(tc.Problems) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.Problems) {
				t.Errorf("problems =\n  %q\nwant\n  %q", got, tc.Problems)
			}
		})
	}
}

func TestValidate_SharedCases(t *testing.T) {
	for _, tc := range loadCases(t).Validate {
		t.Run(tc.Name, func(t *testing.T) {
			s, err := Parse(tc.Schema)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			out, problems := s.Validate(tc.Value)
			got := messages(problems)
			if len(got) != 0 || len(tc.Problems) != 0 {
				if !reflect.DeepEqual(got, tc.Problems) {
					t.Errorf("problems =\n  %q\nwant\n  %q", got, tc.Problems)
				}
			}
			if tc.Output != nil && !reflect.DeepEqual(out, tc.Output) {
				t.Errorf("output = %v, want %v", out, tc.Output)
			}
		})
	}
}

// Values built by expressions (transform mapping mode) are not only
// JSON-decoded types: ints and typed slices must validate too.
func TestValidate_GoValues(t *testing.T) {
	s, err := Parse(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
			"ratio": map[string]any{"type": "number"},
			"names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, problems := s.Validate(map[string]any{"count": 3, "ratio": int64(2), "names": []string{"a", "b"}})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	want := map[string]any{"count": 3, "ratio": int64(2), "names": []any{"a", "b"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output = %#v, want %#v", out, want)
	}
}

func TestError_OneProblemPerLine(t *testing.T) {
	err := Error{{Path: "a", Message: "x"}, {Message: "y"}}
	if err.Error() != "a: x\ny" {
		t.Errorf("Error() = %q", err.Error())
	}
}
