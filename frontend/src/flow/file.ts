import type { Edge, Flow, FlowNode, Metadata, Position } from './types.ts'

// Reading and writing Flow JSON files, following "Opening and saving files"
// in docs/design.md. parseFlow mirrors backend/internal/flow/parser.go so the
// editor accepts exactly what the engine accepts.

/** The only schema version this build understands. */
export const SCHEMA_VERSION = '1.0'

/** A file that is not a valid Flow document. */
export class FlowParseError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'FlowParseError'
  }
}

const FLOW_KEYS = ['id', 'name', 'version', 'nodes', 'edges', 'metadata']
const NODE_KEYS = ['id', 'type', 'name', 'position', 'config']
const EDGE_KEYS = ['id', 'source', 'sourceHandle', 'target', 'targetHandle']
const POSITION_KEYS = ['x', 'y']
const METADATA_KEYS = ['createdAt', 'updatedAt', 'author']

/** Parses and checks a Flow document. Throws FlowParseError on any problem. */
export function parseFlow(text: string): Flow {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch (e) {
    throw new FlowParseError(`not valid JSON: ${(e as Error).message}`)
  }
  const doc = object(raw, 'flow', FLOW_KEYS)
  const flow: Flow = {
    id: str(doc.id, 'id'),
    name: str(doc.name, 'name'),
    version: str(doc.version, 'version'),
    nodes: array(doc.nodes, 'nodes').map((n, i) => node(n, `nodes[${i}]`)),
    edges: array(doc.edges, 'edges').map((e, i) => edge(e, `edges[${i}]`)),
    metadata: metadata(doc.metadata),
  }

  if (flow.id === '') fail('id is required')
  if (flow.version === '') fail('version is required')
  if (flow.version !== SCHEMA_VERSION) {
    fail(
      `unsupported version "${flow.version}" (this editor understands "${SCHEMA_VERSION}")`,
    )
  }
  if (flow.nodes.length === 0) fail('must contain at least one node')
  const nodeIds = new Set<string>()
  flow.nodes.forEach((n, i) => {
    if (n.id === '') fail(`nodes[${i}]: id is required`)
    if (n.type === '') fail(`nodes[${i}] (${n.id}): type is required`)
    if (nodeIds.has(n.id)) fail(`duplicate node id "${n.id}"`)
    nodeIds.add(n.id)
  })
  const edgeIds = new Set<string>()
  flow.edges.forEach((e, i) => {
    if (e.source === '' || e.target === '') {
      fail(`edges[${i}]: source and target are required`)
    }
    if (e.id !== '') {
      if (edgeIds.has(e.id)) fail(`duplicate edge id "${e.id}"`)
      edgeIds.add(e.id)
    }
  })
  return flow
}

/** Serializes a flow in canonical form: schema key order, 2-space indent. */
export function serializeFlow(flow: Flow): string {
  const doc = {
    id: flow.id,
    name: flow.name,
    version: flow.version,
    nodes: flow.nodes.map((n) => ({
      id: n.id,
      type: n.type,
      name: n.name,
      position: { x: n.position.x, y: n.position.y },
      config: n.config,
    })),
    edges: flow.edges.map((e) => ({
      id: e.id,
      source: e.source,
      ...(e.sourceHandle ? { sourceHandle: e.sourceHandle } : {}),
      target: e.target,
      ...(e.targetHandle ? { targetHandle: e.targetHandle } : {}),
    })),
    metadata: {
      ...(flow.metadata.createdAt
        ? { createdAt: flow.metadata.createdAt }
        : {}),
      ...(flow.metadata.updatedAt
        ? { updatedAt: flow.metadata.updatedAt }
        : {}),
      ...(flow.metadata.author ? { author: flow.metadata.author } : {}),
    },
  }
  return JSON.stringify(doc, null, 2) + '\n'
}

/**
 * Returns flow with metadata.updatedAt set to now (UTC, whole seconds), and
 * createdAt too if it is missing. Used when saving unsaved changes.
 */
export function stampSaved(flow: Flow, now: Date): Flow {
  const at = now.toISOString().replace(/\.\d{3}Z$/, 'Z')
  return {
    ...flow,
    metadata: {
      ...flow.metadata,
      createdAt: flow.metadata.createdAt || at,
      updatedAt: at,
    },
  }
}

/** The default download name for a flow that did not come from a file. */
export function defaultFileName(flow: Flow): string {
  const base = flow.id.replace(/[^\w.-]+/g, '-').replace(/^-+|-+$/g, '')
  return `${base || 'flow'}.flow.json`
}

function node(raw: unknown, at: string): FlowNode {
  const n = object(raw, at, NODE_KEYS)
  return {
    id: str(n.id, `${at}.id`),
    type: str(n.type, `${at}.type`),
    name: str(n.name, `${at}.name`),
    position: position(n.position, `${at}.position`),
    config:
      n.config === undefined || n.config === null
        ? {}
        : object(n.config, `${at}.config`),
  }
}

function edge(raw: unknown, at: string): Edge {
  const e = object(raw, at, EDGE_KEYS)
  const out: Edge = {
    id: str(e.id, `${at}.id`),
    source: str(e.source, `${at}.source`),
    target: str(e.target, `${at}.target`),
  }
  const sourceHandle = str(e.sourceHandle, `${at}.sourceHandle`)
  const targetHandle = str(e.targetHandle, `${at}.targetHandle`)
  if (sourceHandle) out.sourceHandle = sourceHandle
  if (targetHandle) out.targetHandle = targetHandle
  return out
}

function position(raw: unknown, at: string): Position {
  if (raw === undefined || raw === null) return { x: 0, y: 0 }
  const p = object(raw, at, POSITION_KEYS)
  return { x: num(p.x, `${at}.x`), y: num(p.y, `${at}.y`) }
}

function metadata(raw: unknown): Metadata {
  if (raw === undefined || raw === null) return {}
  const m = object(raw, 'metadata', METADATA_KEYS)
  const out: Metadata = {}
  for (const key of METADATA_KEYS as (keyof Metadata)[]) {
    const v = str(m[key], `metadata.${key}`)
    if (v) out[key] = v
  }
  return out
}

// The helpers below treat a missing value (undefined or null) as Go's zero
// value, and any other wrong type as an error.

function object(
  raw: unknown,
  at: string,
  allowed?: string[],
): Record<string, unknown> {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    fail(`${at}: must be an object`)
  }
  const obj = raw as Record<string, unknown>
  if (allowed) {
    for (const key of Object.keys(obj)) {
      if (!allowed.includes(key)) fail(`${at}: unknown field "${key}"`)
    }
  }
  return obj
}

function array(raw: unknown, at: string): unknown[] {
  if (raw === undefined || raw === null) return []
  if (!Array.isArray(raw)) fail(`${at}: must be an array`)
  return raw as unknown[]
}

function str(raw: unknown, at: string): string {
  if (raw === undefined || raw === null) return ''
  if (typeof raw !== 'string') fail(`${at}: must be a string`)
  return raw
}

function num(raw: unknown, at: string): number {
  if (raw === undefined || raw === null) return 0
  if (typeof raw !== 'number') fail(`${at}: must be a number`)
  return raw
}

function fail(message: string): never {
  throw new FlowParseError(message)
}
