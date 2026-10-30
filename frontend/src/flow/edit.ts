import { nodeType } from './node-types.ts'
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

/** Returns an empty flow with the given ID, named "Untitled flow". */
export function createFlow(id: string): Flow {
  return {
    id,
    name: 'Untitled flow',
    version: '1.0',
    nodes: [],
    edges: [],
    metadata: {},
  }
}

/** Sets the flow's display name. */
export function setFlowName(flow: Flow, name: string): Flow {
  return { ...flow, name }
}

/**
 * Adds a node of the given type at a position (rounded), named after the
 * type's label, with an empty config. Returns the new Flow and the node's ID.
 */
export function addNode(
  flow: Flow,
  type: string,
  at: Position,
): { flow: Flow; nodeId: string } {
  const nodeId = nextId(
    'node',
    flow.nodes.map((n) => n.id),
  )
  const node = {
    id: nodeId,
    type,
    name: nodeType(type)?.label ?? type,
    position: { x: Math.round(at.x), y: Math.round(at.y) },
    config: {},
  }
  return { flow: { ...flow, nodes: [...flow.nodes, node] }, nodeId }
}

/** Removes a node and every edge into or out of it. Unknown IDs are a no-op. */
export function removeNode(flow: Flow, nodeId: string): Flow {
  return {
    ...flow,
    nodes: flow.nodes.filter((n) => n.id !== nodeId),
    edges: flow.edges.filter((e) => e.source !== nodeId && e.target !== nodeId),
  }
}

/** Sets a node's display name. Unknown IDs are a no-op. */
export function setNodeName(flow: Flow, nodeId: string, name: string): Flow {
  return {
    ...flow,
    nodes: flow.nodes.map((n) => (n.id === nodeId ? { ...n, name } : n)),
  }
}

/** Replaces a node's config. Unknown IDs are a no-op. */
export function setNodeConfig(
  flow: Flow,
  nodeId: string,
  config: Record<string, unknown>,
): Flow {
  return {
    ...flow,
    nodes: flow.nodes.map((n) => (n.id === nodeId ? { ...n, config } : n)),
  }
}

/**
 * Returns config with key set to value, or with key removed when value is
 * undefined (so the engine applies its default).
 */
export function withConfigValue(
  config: Record<string, unknown>,
  key: string,
  value: unknown,
): Record<string, unknown> {
  const next = { ...config }
  if (value === undefined) delete next[key]
  else next[key] = value
  return next
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
 * Gives every edge without an ID a fresh edge-<n> ID. The engine allows edges
 * without IDs, but the canvas needs one per edge. Returns flow itself when
 * nothing changed.
 */
export function withEdgeIds(flow: Flow): Flow {
  if (flow.edges.every((e) => e.id !== '')) return flow
  let next = flow
  const edges = flow.edges.map((e) => {
    if (e.id !== '') return e
    const edge = { ...e, id: nextEdgeId(next) }
    next = { ...next, edges: [...next.edges, edge] }
    return edge
  })
  return { ...flow, edges }
}

/** Returns the next free edge ID; see nextId. */
export function nextEdgeId(flow: Flow): string {
  return nextId(
    'edge',
    flow.edges.map((e) => e.id),
  )
}

/**
 * Returns "<prefix>-<n>" with n one above the highest existing
 * "<prefix>-<number>" ID, so IDs stay readable and never collide.
 */
function nextId(prefix: string, existing: string[]): string {
  const pattern = new RegExp(`^${prefix}-(\\d+)$`)
  let max = 0
  for (const id of existing) {
    const m = pattern.exec(id)
    if (m) max = Math.max(max, Number(m[1]))
  }
  const taken = new Set(existing)
  let n = max + 1
  while (taken.has(`${prefix}-${n}`)) n++
  return `${prefix}-${n}`
}
