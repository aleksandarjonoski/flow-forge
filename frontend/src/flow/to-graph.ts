import { isTrigger, type Flow, type Position } from './types.ts'

// flowToGraph turns a Flow into what the canvas draws, following the mapping
// in docs/design.md ("Flow JSON → canvas mapping"). It knows nothing about
// Rete, so the rules can be tested without a DOM.

export interface GraphNode {
  id: string
  label: string
  position: Position
  hasInput: boolean
  hasOutput: boolean
}

export interface GraphConnection {
  id: string
  source: string
  target: string
}

export interface Graph {
  nodes: GraphNode[]
  connections: GraphConnection[]
}

/** A flow that cannot be drawn. problems lists every reason, not just the first. */
export class FlowLoadError extends Error {
  readonly problems: string[]

  constructor(problems: string[]) {
    super(`flow cannot be loaded: ${problems.join('; ')}`)
    this.name = 'FlowLoadError'
    this.problems = problems
  }
}

export function flowToGraph(flow: Flow): Graph {
  const problems: string[] = []
  const types = new Map<string, string>()
  const nodes: GraphNode[] = []

  for (const n of flow.nodes) {
    if (types.has(n.id)) {
      problems.push(`duplicate node id "${n.id}"`)
      continue
    }
    types.set(n.id, n.type)
    const trigger = isTrigger(n.type)
    nodes.push({
      id: n.id,
      label: n.name || n.type,
      position: { x: n.position.x, y: n.position.y },
      hasInput: !trigger,
      hasOutput: true,
    })
  }

  const inbound = new Map<string, string>() // target → edge id
  const connections: GraphConnection[] = []
  for (const e of flow.edges) {
    const before = problems.length
    if (!types.has(e.source)) {
      problems.push(`edge "${e.id}": unknown source node "${e.source}"`)
    }
    if (!types.has(e.target)) {
      problems.push(`edge "${e.id}": unknown target node "${e.target}"`)
    } else if (isTrigger(types.get(e.target)!)) {
      problems.push(
        `edge "${e.id}": trigger "${e.target}" cannot have inbound edges`,
      )
    } else if (inbound.has(e.target)) {
      problems.push(
        `edge "${e.id}": node "${e.target}" already has inbound edge "${inbound.get(e.target)}"; merge nodes are not supported in v1`,
      )
    }
    if (problems.length > before) continue
    inbound.set(e.target, e.id)
    connections.push({ id: e.id, source: e.source, target: e.target })
  }

  if (problems.length > 0) throw new FlowLoadError(problems)
  return { nodes, connections }
}

/**
 * Reports whether two flows have the same nodes (by ID and type) and the same
 * edges, i.e. differ at most in names, positions and config. The canvas uses
 * it to patch instead of redrawing.
 */
export function sameStructure(a: Flow, b: Flow): boolean {
  const nodes = (f: Flow) =>
    f.nodes
      .map((n) => `${n.id}\u0000${n.type}`)
      .sort()
      .join('\n')
  const edges = (f: Flow) =>
    f.edges
      .map((e) => `${e.id}\u0000${e.source}\u0000${e.target}`)
      .sort()
      .join('\n')
  return nodes(a) === nodes(b) && edges(a) === edges(b)
}
