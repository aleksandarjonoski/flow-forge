import type { Edge, Flow, Position } from './types.ts'

// Pure edit operations on a Flow, following "Editing on the canvas" in
// docs/design.md. Each returns a new Flow and leaves its input untouched.

/** Sets a node's position, rounded to whole pixels. Unknown IDs are a no-op. */
export function moveNode(flow: Flow, nodeId: string, to: Position): Flow {
  const position = { x: Math.round(to.x), y: Math.round(to.y) }
  return {
    ...flow,
    nodes: flow.nodes.map((n) => (n.id === nodeId ? { ...n, position } : n)),
  }
}

/** Appends an edge. Use nextEdgeId for a fresh ID. */
export function addEdge(flow: Flow, edge: Edge): Flow {
  return { ...flow, edges: [...flow.edges, edge] }
}

/** Removes the edge with the given ID. Unknown IDs are a no-op. */
export function removeEdge(flow: Flow, edgeId: string): Flow {
  return { ...flow, edges: flow.edges.filter((e) => e.id !== edgeId) }
}

/**
 * Reports whether adding source → target would create a cycle, i.e. whether
 * source is reachable from target (or they are the same node).
 */
export function createsCycle(
  flow: Flow,
  source: string,
  target: string,
): boolean {
  const out = new Map<string, string[]>()
  for (const e of flow.edges) {
    out.set(e.source, [...(out.get(e.source) ?? []), e.target])
  }
  const seen = new Set<string>()
  const stack = [target]
  while (stack.length > 0) {
    const id = stack.pop()!
    if (id === source) return true
    if (seen.has(id)) continue
    seen.add(id)
    stack.push(...(out.get(id) ?? []))
  }
  return false
}

/**
 * Returns "edge-<n>" with n one above the highest existing "edge-<number>"
 * ID, so IDs stay readable and never collide.
 */
export function nextEdgeId(flow: Flow): string {
  let max = 0
  for (const e of flow.edges) {
    const m = /^edge-(\d+)$/.exec(e.id)
    if (m) max = Math.max(max, Number(m[1]))
  }
  let n = max + 1
  const ids = new Set(flow.edges.map((e) => e.id))
  while (ids.has(`edge-${n}`)) n++
  return `edge-${n}`
}
