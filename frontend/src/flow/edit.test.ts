import { describe, expect, it } from 'vitest'
import {
  addEdge,
  createsCycle,
  moveNode,
  nextEdgeId,
  removeEdge,
  setNodeConfig,
  setNodeName,
  withConfigValue,
} from './edit.ts'
import type { Edge, Flow } from './types.ts'

function flow(edges: Edge[] = []): Flow {
  return {
    id: 'f',
    name: 'f',
    version: '1.0',
    nodes: ['a', 'b', 'c'].map((id) => ({
      id,
      type: 'log',
      name: id,
      position: { x: 0, y: 0 },
      config: {},
    })),
    edges,
    metadata: {},
  }
}

describe('moveNode', () => {
  it('moves only the given node, rounding the position', () => {
    const before = flow()
    const after = moveNode(before, 'b', { x: 10.4, y: -3.6 })
    expect(after.nodes.map((n) => n.position)).toEqual([
      { x: 0, y: 0 },
      { x: 10, y: -4 },
      { x: 0, y: 0 },
    ])
    expect(before.nodes[1].position).toEqual({ x: 0, y: 0 })
  })

  it('ignores an unknown node', () => {
    const before = flow()
    expect(moveNode(before, 'zzz', { x: 1, y: 1 }).nodes).toEqual(before.nodes)
  })
})

describe('nextEdgeId', () => {
  it('numbers after the highest edge-<n> ID', () => {
    const f = flow([
      { id: 'edge-2', source: 'a', target: 'b' },
      { id: 'custom', source: 'b', target: 'c' },
    ])
    expect(nextEdgeId(f)).toBe('edge-3')
  })

  it('starts at edge-1', () => {
    expect(nextEdgeId(flow())).toBe('edge-1')
  })
})

describe('addEdge', () => {
  it('appends the edge without mutating the input', () => {
    const before = flow()
    const after = addEdge(before, { id: 'edge-1', source: 'a', target: 'b' })
    expect(after.edges).toEqual([{ id: 'edge-1', source: 'a', target: 'b' }])
    expect(before.edges).toHaveLength(0)
  })
})

describe('removeEdge', () => {
  it('removes only the given edge', () => {
    const after = removeEdge(
      flow([
        { id: 'edge-1', source: 'a', target: 'b' },
        { id: 'edge-2', source: 'b', target: 'c' },
      ]),
      'edge-1',
    )
    expect(after.edges.map((e) => e.id)).toEqual(['edge-2'])
  })
})

describe('createsCycle', () => {
  const chain = flow([
    { id: 'edge-1', source: 'a', target: 'b' },
    { id: 'edge-2', source: 'b', target: 'c' },
  ])

  it('detects a back edge', () => {
    expect(createsCycle(chain, 'c', 'a')).toBe(true)
    expect(createsCycle(chain, 'b', 'a')).toBe(true)
  })

  it('detects a self loop', () => {
    expect(createsCycle(chain, 'b', 'b')).toBe(true)
  })

  it('allows forward and unrelated edges', () => {
    expect(createsCycle(chain, 'a', 'c')).toBe(false)
    expect(createsCycle(flow(), 'c', 'a')).toBe(false)
  })
})

describe('setNodeName / setNodeConfig', () => {
  it('changes only the given node', () => {
    const before = flow()
    const renamed = setNodeName(before, 'b', 'Bee')
    expect(renamed.nodes.map((n) => n.name)).toEqual(['a', 'Bee', 'c'])
    const configured = setNodeConfig(before, 'c', { level: 'warn' })
    expect(configured.nodes.map((n) => n.config)).toEqual([
      {},
      {},
      { level: 'warn' },
    ])
    expect(before.nodes[1].name).toBe('b')
    expect(before.nodes[2].config).toEqual({})
  })
})

describe('withConfigValue', () => {
  it('sets a key, or removes it for undefined', () => {
    const config = { a: '1', b: '2' }
    expect(withConfigValue(config, 'c', '3')).toEqual({
      a: '1',
      b: '2',
      c: '3',
    })
    expect(withConfigValue(config, 'a', undefined)).toEqual({ b: '2' })
    expect(config).toEqual({ a: '1', b: '2' })
  })
})
