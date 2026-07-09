package engine

import (
	"slices"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

func TestCompile_HappyPath(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a", Config: map[string]any{"foo": "bar"}},
			{ID: "a", Type: "action_a"},
			{ID: "b", Type: "action_b"},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a"},
			{Source: "a", Target: "b"},
		},
	}
	svc := &EngineServices{}
	cf, err := Compile(f, reg, svc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if cf.Flow != f {
		t.Error("CompiledFlow.Flow does not point to input")
	}
	if got := len(cf.Nodes); got != 3 {
		t.Errorf("Nodes len = %d, want 3", got)
	}
	if !slices.Equal(cf.Triggers, []string{"t"}) {
		t.Errorf("Triggers = %v, want [t]", cf.Triggers)
	}
	if !slices.Equal(cf.TopoOrder["t"], []string{"t", "a", "b"}) {
		t.Errorf("TopoOrder[t] = %v, want [t a b]", cf.TopoOrder["t"])
	}

	// Init must have been called on every node with the right config + svc.
	trig := cf.Nodes["t"].(*fakeTrigger)
	if !trig.inited {
		t.Error("trigger Init was not called")
	}
	if trig.config["foo"] != "bar" {
		t.Errorf("trigger config not propagated: %v", trig.config)
	}
	if trig.services != svc {
		t.Error("trigger did not receive EngineServices")
	}
}

func TestCompile_AdjacencyAndTopoOrder(t *testing.T) {
	// Two triggers sharing nothing downstream; verifies per-trigger topo
	// orders are computed independently.
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t1", Type: "trigger_a"},
			{ID: "t2", Type: "trigger_b"},
			{ID: "a", Type: "action_a"},
			{ID: "b", Type: "action_b"},
		},
		Edges: []flow.Edge{
			{Source: "t1", Target: "a"},
			{Source: "t2", Target: "b"},
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !slices.Equal(cf.Triggers, []string{"t1", "t2"}) {
		t.Errorf("Triggers = %v, want [t1 t2]", cf.Triggers)
	}
	if !slices.Equal(cf.TopoOrder["t1"], []string{"t1", "a"}) {
		t.Errorf("TopoOrder[t1] = %v, want [t1 a]", cf.TopoOrder["t1"])
	}
	if !slices.Equal(cf.TopoOrder["t2"], []string{"t2", "b"}) {
		t.Errorf("TopoOrder[t2] = %v, want [t2 b]", cf.TopoOrder["t2"])
	}
	if got := cf.Adjacency["t1"]; !slices.Equal(got, []string{"a"}) {
		t.Errorf("Adjacency[t1] = %v, want [a]", got)
	}
	if got := cf.Adjacency["t2"]; !slices.Equal(got, []string{"b"}) {
		t.Errorf("Adjacency[t2] = %v, want [b]", got)
	}
	if got := len(cf.Adjacency["a"]); got != 0 {
		t.Errorf("Adjacency[a] should be empty, got %v", cf.Adjacency["a"])
	}
}

func TestCompile_InitFailure(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "bad", Type: "init_fails"},
		},
		Edges: []flow.Edge{{Source: "t", Target: "bad"}},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, `node "bad"`, "init_fails", "init", "boom")
}

func TestCompile_NoTriggers(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "a", Type: "action_a"},
			{ID: "b", Type: "action_b"},
		},
		Edges: []flow.Edge{{Source: "a", Target: "b"}},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, "no trigger nodes", "at least one trigger is required")
}

func TestCompile_TriggerHasInboundEdge(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t1", Type: "trigger_a"},
			{ID: "t2", Type: "trigger_b"},
			// t2 has an inbound edge from t1, which is illegal.
		},
		Edges: []flow.Edge{{Source: "t1", Target: "t2"}},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, `trigger "t2"`, "trigger_b", "1 inbound", "must be source nodes")
}

func TestCompile_NodeImplementsNeitherInterface(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "p", Type: "plain"}, // bare Node, not Action or Trigger
		},
		Edges: []flow.Edge{{Source: "t", Target: "p"}},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, `node "p"`, "plain", "neither ActionNode nor TriggerNode")
}

func TestCompile_NodeImplementsBothInterfaces(t *testing.T) {
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "x", Type: "both"},
		},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, `node "x"`, "both", "implements both")
}

func TestCompile_ValidationErrorsBubbleUp(t *testing.T) {
	// If Validate already failed, Compile should return that error and not
	// proceed to instantiation.
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "x", Type: "nope"}, // unknown type — Validate fails
		},
	}
	_, err := Compile(f, reg, &EngineServices{})
	containsAll(t, err, "unknown type", `"nope"`)
}

func TestCompile_NodesGetIndependentInstances(t *testing.T) {
	// Two nodes of the same type must be distinct instances so their
	// internal state doesn't leak across.
	reg := newTestRegistry()
	f := &flow.Flow{
		ID: "f", Version: "1.0",
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger_a"},
			{ID: "a1", Type: "action_a", Config: map[string]any{"k": "one"}},
			{ID: "a2", Type: "action_a", Config: map[string]any{"k": "two"}},
		},
		Edges: []flow.Edge{
			{Source: "t", Target: "a1"},
			// a1 -> a2 would be fine but isn't needed for this test.
		},
	}
	cf, err := Compile(f, reg, &EngineServices{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	a1 := cf.Nodes["a1"].(*fakeAction)
	a2 := cf.Nodes["a2"].(*fakeAction)
	if a1 == a2 {
		t.Fatal("a1 and a2 are the same instance")
	}
	if a1.config["k"] != "one" {
		t.Errorf("a1 config = %v", a1.config)
	}
	if a2.config["k"] != "two" {
		t.Errorf("a2 config = %v", a2.config)
	}
}
