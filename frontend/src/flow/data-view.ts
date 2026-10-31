import type { TraceEvent } from '../api/client.ts'
import { nodeRuns } from '../api/trace.ts'
import { nodeType } from './node-types.ts'
import { parseSchema } from './schema.ts'
import { inferShape, shapeFromSchema, type Shape } from './shape.ts'
import { isTrigger, type Flow, type FlowNode } from './types.ts'

// What the input data view shows for a node (docs/design.md, "Input data
// view"; docs/data-v1.md §7.4): the shape of the data it receives, from the
// last test run when that still applies, else from a declared schema, else
// from the previous node's output contract.

/** A finished test run: the flow as it was run and its trace. */
export interface LastRun {
  flow: Flow
  trace: TraceEvent[]
}

export interface DataView {
  /**
   * "input" for what an action receives; "emits" for triggers, which
   * receive nothing; "output" for what any node outputs (Output tab).
   */
  mode: 'input' | 'emits' | 'output'
  /** The node the input comes from (input mode). */
  from?: { id: string; name: string }
  /** The data's shape; undefined when nothing is connected. */
  shape?: Shape
  /** Where the shape comes from. */
  source: 'run' | 'declared' | 'type' | 'none'
}

export function dataView(
  flow: Flow,
  nodeId: string,
  lastRun?: LastRun,
): DataView | undefined {
  const node = findNode(flow, nodeId)
  if (!node) return undefined
  if (isTrigger(node.type)) {
    return { mode: 'emits', ...outputOf(flow, node, lastRun, new Set()) }
  }
  const from = predecessor(flow, nodeId)
  if (!from) return { mode: 'input', source: 'none' }
  return {
    mode: 'input',
    from: { id: from.id, name: from.name || from.type },
    ...outputOf(flow, from, lastRun, new Set()),
  }
}

/** What a node outputs: from the last run, its declared schema, or its type. */
export function outputView(
  flow: Flow,
  nodeId: string,
  lastRun?: LastRun,
): DataView | undefined {
  const node = findNode(flow, nodeId)
  if (!node) return undefined
  return { mode: 'output', ...outputOf(flow, node, lastRun, new Set()) }
}

// outputOf returns what a node outputs: its real output in the last run, if
// that run still matches the flow up to this node; otherwise its declared
// schema or its type's contract, following pass-through nodes upstream.
function outputOf(
  flow: Flow,
  node: FlowNode,
  lastRun: LastRun | undefined,
  seen: Set<string>,
): { shape: Shape; source: 'run' | 'declared' | 'type' } {
  if (lastRun && sameUpstream(flow, lastRun.flow, node.id)) {
    const run = nodeRuns(lastRun.trace).find((r) => r.nodeId === node.id)
    if (run && (run.status === 'completed' || run.status === 'trigger')) {
      return { shape: inferShape(run.output ?? {}), source: 'run' }
    }
  }
  const output = nodeType(node.type)?.output ?? {
    type: 'any',
    note: 'unknown node type',
  }
  const contract = typeof output === 'function' ? output(node.config) : output
  if (contract === 'declared') {
    const { schema } = parseSchema(node.config.schema)
    return schema?.type === 'object'
      ? { shape: shapeFromSchema(schema), source: 'declared' }
      : {
          shape: { type: 'any', note: 'its schema is not complete yet' },
          source: 'type',
        }
  }
  if (contract !== 'input') return { shape: contract, source: 'type' }

  const from = predecessor(flow, node.id)
  if (!from || seen.has(node.id)) {
    return {
      shape: { type: 'any', note: 'passes its input through' },
      source: 'type',
    }
  }
  seen.add(node.id)
  return outputOf(flow, from, lastRun, seen)
}

/**
 * Reports whether node `id` and everything upstream of it are the same in
 * both flows: same IDs, types, config and wiring. Only then does a run of
 * `ran` say anything about this node's output in `current`.
 */
export function sameUpstream(current: Flow, ran: Flow, id: string): boolean {
  const a = upstreamSignature(current, id)
  return a !== undefined && a === upstreamSignature(ran, id)
}

function upstreamSignature(flow: Flow, id: string): string | undefined {
  const parts: string[] = []
  const seen = new Set<string>()
  let node = findNode(flow, id)
  if (!node) return undefined
  while (node && !seen.has(node.id)) {
    seen.add(node.id)
    parts.push(
      `${node.id}\u0000${node.type}\u0000${JSON.stringify(node.config)}`,
    )
    node = predecessor(flow, node.id)
  }
  return parts.join('\n')
}

function findNode(flow: Flow, id: string): FlowNode | undefined {
  return flow.nodes.find((n) => n.id === id)
}

// predecessor returns the node with an edge into id. Nodes have at most one
// inbound edge (engine-v1.md §10).
function predecessor(flow: Flow, id: string): FlowNode | undefined {
  const edge = flow.edges.find((e) => e.target === id)
  return edge && findNode(flow, edge.source)
}
