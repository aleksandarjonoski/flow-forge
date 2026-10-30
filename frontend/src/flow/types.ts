import { nodeType } from './node-types.ts'

// The Flow JSON document, mirroring backend/internal/flow/flow.go. The JSON
// field names are the contract shared with the engine.

export interface Flow {
  id: string
  name: string
  version: string
  nodes: FlowNode[]
  edges: Edge[]
  metadata: Metadata
}

export interface FlowNode {
  id: string
  type: string
  name: string
  position: Position
  config: Record<string, unknown>
}

export interface Edge {
  id: string
  source: string
  sourceHandle?: string // unused in v1, reserved
  target: string
  targetHandle?: string // unused in v1, reserved
}

export interface Position {
  x: number
  y: number
}

export interface Metadata {
  createdAt?: string
  updatedAt?: string
  author?: string
}

/** Reports whether a node type starts executions (and so has no input). */
export function isTrigger(type: string): boolean {
  return nodeType(type)?.kind === 'trigger'
}
