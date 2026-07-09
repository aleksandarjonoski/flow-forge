package engine

import (
	"errors"
	"fmt"
	"slices"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// CompiledFlow is a Flow that has been validated, instantiated, and analyzed
// for execution. See docs/engine-v1.md §3.3.
//
// TopoOrder is keyed by trigger ID so each execution can walk only its own
// subgraph. Each topo order includes the trigger itself as the first
// element; the executor (slice 5) skips it because triggers are not
// ActionNodes.
type CompiledFlow struct {
	Flow      *flow.Flow
	Nodes     map[string]Node
	Adjacency map[string][]string
	Triggers  []string
	TopoOrder map[string][]string
}

// Compile turns a parsed Flow into a CompiledFlow ready for execution.
//
// Pipeline:
//  1. Validate (structural / type / cycle checks).
//  2. Instantiate each node via the registry and call Init.
//  3. Classify each node as Action or Trigger by type assertion.
//  4. Verify trigger-specific rules (at least one trigger exists, triggers
//     have no inbound edges).
//  5. Pre-compute the per-trigger topological execution order.
//
// All errors are joined into a single returned error so a failed compile
// surfaces every problem at once.
func Compile(f *flow.Flow, reg *Registry, svc *EngineServices) (*CompiledFlow, error) {
	if err := Validate(f, reg); err != nil {
		return nil, err
	}

	var errs []error

	nodes := make(map[string]Node, len(f.Nodes))
	for _, n := range f.Nodes {
		instance, err := reg.Create(n.Type)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %q: %w", n.ID, err))
			continue
		}
		if err := instance.Init(n.Config, svc); err != nil {
			errs = append(errs, fmt.Errorf("node %q (%s): init: %w", n.ID, n.Type, err))
			continue
		}
		nodes[n.ID] = instance
	}

	// Classify instantiated nodes. Iterate f.Nodes for deterministic order.
	var triggers []string
	for _, n := range f.Nodes {
		instance, ok := nodes[n.ID]
		if !ok {
			continue // init failure already reported above
		}
		_, isAction := instance.(ActionNode)
		_, isTrigger := instance.(TriggerNode)
		switch {
		case isAction && isTrigger:
			errs = append(errs, fmt.Errorf("node %q (%s): implements both ActionNode and TriggerNode; must be exactly one", n.ID, n.Type))
		case !isAction && !isTrigger:
			errs = append(errs, fmt.Errorf("node %q (%s): implements neither ActionNode nor TriggerNode", n.ID, n.Type))
		case isTrigger:
			triggers = append(triggers, n.ID)
		}
	}
	slices.Sort(triggers)

	// Only complain about "no triggers" if there were successfully classified
	// nodes; otherwise we're cascading from earlier errors.
	if len(nodes) > 0 && len(triggers) == 0 {
		errs = append(errs, errors.New("flow contains no trigger nodes; at least one trigger is required"))
	}

	// Build adjacency over all declared nodes (not just successfully
	// instantiated ones — the graph shape is independent of init success).
	adj := make(map[string][]string, len(f.Nodes))
	for _, n := range f.Nodes {
		adj[n.ID] = nil
	}
	for _, e := range f.Edges {
		// Validate has already rejected unknown endpoints; skip defensively.
		if _, ok := adj[e.Source]; !ok {
			continue
		}
		if _, ok := adj[e.Target]; !ok {
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
	}

	inbound := make(map[string]int, len(f.Nodes))
	for _, e := range f.Edges {
		inbound[e.Target]++
	}
	for _, tid := range triggers {
		if c := inbound[tid]; c > 0 {
			errs = append(errs, fmt.Errorf("trigger %q (%s) has %d inbound edge(s); triggers must be source nodes", tid, nodes[tid].Type(), c))
		}
	}

	topoOrder := make(map[string][]string, len(triggers))
	for _, tid := range triggers {
		reach := reachable(adj, tid)
		order, err := topoSort(adj, reach)
		if err != nil {
			errs = append(errs, fmt.Errorf("trigger %q: %w", tid, err))
			continue
		}
		topoOrder[tid] = order
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &CompiledFlow{
		Flow:      f,
		Nodes:     nodes,
		Adjacency: adj,
		Triggers:  triggers,
		TopoOrder: topoOrder,
	}, nil
}
