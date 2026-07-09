package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
)

// Validate checks a Flow for structural and semantic correctness using the
// node type registry but without instantiating any node. All discovered
// problems are joined via errors.Join, so one pass surfaces every issue
// instead of forcing the user to fix one error and re-run.
//
// Checks performed:
//   - every Node.Type is registered with reg
//   - every Edge.Source / Edge.Target references an existing node
//   - no node has more than one inbound edge (v1 disallows merge nodes;
//     see docs/engine-v1.md §10)
//   - the graph contains no cycles
//
// Checks NOT performed here (require node instantiation; Compile does them):
//   - at least one trigger exists
//   - triggers have no inbound edges
//   - every node implements ActionNode or TriggerNode (exactly one)
func Validate(f *flow.Flow, reg *Registry) error {
	var errs []error

	nodeIDs := make(map[string]bool, len(f.Nodes))
	for _, n := range f.Nodes {
		nodeIDs[n.ID] = true
		if !reg.Has(n.Type) {
			errs = append(errs, fmt.Errorf("node %q: unknown type %q (not registered)", n.ID, n.Type))
		}
	}

	// Build adjacency from edges that connect known nodes only — edges with
	// unknown endpoints have already been reported and shouldn't cascade
	// into spurious cycle / multi-inbound errors.
	adj := make(map[string][]string, len(f.Nodes))
	for _, n := range f.Nodes {
		adj[n.ID] = nil
	}
	inbound := make(map[string]int, len(f.Nodes))
	for i, e := range f.Edges {
		eName := edgeIdent(i, e)
		if !nodeIDs[e.Source] {
			errs = append(errs, fmt.Errorf("edge %s: source %q does not exist", eName, e.Source))
		}
		if !nodeIDs[e.Target] {
			errs = append(errs, fmt.Errorf("edge %s: target %q does not exist", eName, e.Target))
		}
		if nodeIDs[e.Source] && nodeIDs[e.Target] {
			adj[e.Source] = append(adj[e.Source], e.Target)
			inbound[e.Target]++
		}
	}

	// Iterate f.Nodes (deterministic order) rather than the inbound map.
	for _, n := range f.Nodes {
		if c := inbound[n.ID]; c > 1 {
			errs = append(errs, fmt.Errorf("node %q has %d inbound edges; merge nodes are not supported in v1", n.ID, c))
		}
	}

	if cycle, found := detectCycle(adj); found {
		errs = append(errs, fmt.Errorf("flow contains a cycle: %s", strings.Join(cycle, " -> ")))
	}

	return errors.Join(errs...)
}

// edgeIdent returns a human-friendly identifier for an edge: its ID if set,
// otherwise its index in f.Edges.
func edgeIdent(idx int, e flow.Edge) string {
	if e.ID != "" {
		return fmt.Sprintf("%q", e.ID)
	}
	return fmt.Sprintf("#%d", idx)
}
