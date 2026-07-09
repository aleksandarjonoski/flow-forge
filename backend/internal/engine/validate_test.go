package engine

import (
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// containsAll asserts that every wanted substring appears in the error.
func containsAll(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %v, got nil", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error missing %q\nfull error:\n%s", w, err.Error())
		}
	}
}

func TestValidate_HappyPath(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "n1", Type: "trigger_a"},
			{ID: "n2", Type: "action_a"},
		},
		Edges: []flow.Edge{{Source: "n1", Target: "n2"}},
	}
	if err := Validate(f, reg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_UnknownType(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "n1", Type: "trigger_a"},
			{ID: "n2", Type: "nope"},
		},
	}
	containsAll(t, Validate(f, reg), `node "n2"`, `unknown type`, `"nope"`)
}

func TestValidate_EdgeEndpointMissing(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{{ID: "n1", Type: "trigger_a"}},
		Edges: []flow.Edge{
			{ID: "e1", Source: "n1", Target: "ghost"},
			{ID: "e2", Source: "phantom", Target: "n1"},
		},
	}
	err := Validate(f, reg)
	containsAll(t, err,
		`edge "e1"`, `target`, `"ghost"`,
		`edge "e2"`, `source`, `"phantom"`,
	)
}

func TestValidate_EdgeIdentifierFallsBackToIndex(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{{ID: "n1", Type: "trigger_a"}},
		Edges: []flow.Edge{
			// No edge ID — error should reference the edge by index #0.
			{Source: "n1", Target: "ghost"},
		},
	}
	containsAll(t, Validate(f, reg), "edge #0", `"ghost"`)
}

func TestValidate_MultiInbound(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t1", Type: "trigger_a"},
			{ID: "t2", Type: "trigger_b"},
			{ID: "a1", Type: "action_a"},
		},
		Edges: []flow.Edge{
			{Source: "t1", Target: "a1"},
			{Source: "t2", Target: "a1"},
		},
	}
	containsAll(t, Validate(f, reg), `node "a1"`, "2 inbound", "merge nodes are not supported")
}

func TestValidate_Cycle(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a", Type: "action_a"},
			{ID: "b", Type: "action_b"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "b"},
			{Source: "b", Target: "a"}, // cycle
		},
	}
	// Cycle is a -> b -> a; multi-inbound on a (2 inbound) will also be
	// reported. Both errors should surface together.
	err := Validate(f, reg)
	containsAll(t, err, "cycle", "a -> b -> a", `node "a"`, "2 inbound")
}

func TestValidate_MultipleErrorsReported(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "x", Type: "nope"}, // unknown type
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", Target: "ghost"}, // bad target
		},
	}
	err := Validate(f, reg)
	containsAll(t, err, "unknown type", `"nope"`, `edge "e1"`, `"ghost"`)
}

func TestValidate_UnknownEndpointsDoNotCascadeIntoCycleError(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{{ID: "t", Type: "trigger_a"}},
		Edges: []flow.Edge{
			{Source: "ghost", Target: "t"},
			{Source: "t", Target: "phantom"},
		},
	}
	err := Validate(f, reg)
	if err == nil {
		t.Fatal("expected error from missing endpoints")
	}
	if strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected cycle error from edges with unknown endpoints:\n%s", err)
	}
}
