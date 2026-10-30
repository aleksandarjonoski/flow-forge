import { describe, expect, it } from 'vitest'
import helloText from '../../../backend/examples/hello.flow.json?raw'
import {
  FlowParseError,
  defaultFileName,
  parseFlow,
  serializeFlow,
  stampSaved,
} from './file.ts'

// minimal returns a valid document with overrides applied, as JSON text.
function minimal(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    id: 'f',
    version: '1.0',
    nodes: [{ id: 'n', type: 'log' }],
    ...overrides,
  })
}

function parseError(text: string): string {
  try {
    parseFlow(text)
  } catch (e) {
    if (e instanceof FlowParseError) return e.message
    throw e
  }
  throw new Error('parseFlow succeeded, want FlowParseError')
}

describe('parseFlow / serializeFlow', () => {
  it('round-trips the hello example byte for byte', () => {
    expect(serializeFlow(parseFlow(helloText))).toBe(helloText)
  })

  it('fills in Go zero values for missing optional fields', () => {
    expect(parseFlow(minimal())).toEqual({
      id: 'f',
      name: '',
      version: '1.0',
      nodes: [
        {
          id: 'n',
          type: 'log',
          name: '',
          position: { x: 0, y: 0 },
          config: {},
        },
      ],
      edges: [],
      metadata: {},
    })
  })

  it('writes schema key order and omits empty optional fields', () => {
    const flow = parseFlow(
      JSON.stringify({
        metadata: { author: 'me' },
        edges: [{ target: 'n', source: 'n', id: 'e', sourceHandle: '' }],
        nodes: [{ config: { b: 1, a: 2 }, type: 'log', id: 'n' }],
        version: '1.0',
        id: 'f',
      }),
    )
    const text = serializeFlow(flow)
    expect(Object.keys(JSON.parse(text) as object)).toEqual([
      'id',
      'name',
      'version',
      'nodes',
      'edges',
      'metadata',
    ])
    expect(text).toContain(
      '"edges": [\n    {\n      "id": "e",\n      "source": "n",\n      "target": "n"\n    }\n  ]',
    )
    // Config keys keep the order they were written in.
    expect(text).toContain('"config": {\n        "b": 1,\n        "a": 2\n')
    expect(text).toContain('"metadata": {\n    "author": "me"\n  }')
    expect(text.endsWith('}\n')).toBe(true)
  })

  it('keeps edge handles when set', () => {
    const flow = parseFlow(
      minimal({
        edges: [{ id: 'e', source: 'n', target: 'n', targetHandle: 'in' }],
      }),
    )
    expect(flow.edges[0]).toEqual({
      id: 'e',
      source: 'n',
      target: 'n',
      targetHandle: 'in',
    })
    expect(serializeFlow(flow)).toContain('"targetHandle": "in"')
  })
})

describe('parseFlow errors (mirroring the engine parser)', () => {
  it.each([
    ['not JSON', '{', /^not valid JSON/],
    ['not an object', '[]', /^flow: must be an object$/],
    ['unknown top-level field', minimal({ extra: 1 }), /unknown field "extra"/],
    [
      'unknown node field',
      minimal({ nodes: [{ id: 'n', type: 'log', color: 'red' }] }),
      /nodes\[0\]: unknown field "color"/,
    ],
    ['missing id', minimal({ id: '' }), /^id is required$/],
    ['missing version', minimal({ version: undefined }), /version is required/],
    ['other version', minimal({ version: '2.0' }), /unsupported version "2.0"/],
    ['no nodes', minimal({ nodes: [] }), /at least one node/],
    [
      'node without type',
      minimal({ nodes: [{ id: 'n' }] }),
      /nodes\[0\] \(n\): type is required/,
    ],
    [
      'duplicate node',
      minimal({
        nodes: [
          { id: 'n', type: 'log' },
          { id: 'n', type: 'log' },
        ],
      }),
      /duplicate node id "n"/,
    ],
    [
      'edge without target',
      minimal({ edges: [{ id: 'e', source: 'n' }] }),
      /edges\[0\]: source and target are required/,
    ],
    [
      'duplicate edge',
      minimal({
        edges: [
          { id: 'e', source: 'n', target: 'n' },
          { id: 'e', source: 'n', target: 'n' },
        ],
      }),
      /duplicate edge id "e"/,
    ],
    [
      'wrong type',
      minimal({ nodes: [{ id: 'n', type: 'log', position: { x: '1' } }] }),
      /nodes\[0\]\.position\.x: must be a number/,
    ],
  ])('%s', (_name, text, want) => {
    expect(parseError(text)).toMatch(want)
  })

  it('allows several edges with empty IDs, like the engine', () => {
    const flow = parseFlow(
      minimal({
        edges: [
          { source: 'n', target: 'n' },
          { source: 'n', target: 'n' },
        ],
      }),
    )
    expect(flow.edges).toHaveLength(2)
  })
})

describe('stampSaved', () => {
  const now = new Date('2026-10-31T09:15:30.123Z')

  it('sets updatedAt, and createdAt when missing', () => {
    const flow = parseFlow(minimal())
    expect(stampSaved(flow, now).metadata).toEqual({
      createdAt: '2026-10-31T09:15:30Z',
      updatedAt: '2026-10-31T09:15:30Z',
    })
    expect(flow.metadata).toEqual({})
  })

  it('keeps an existing createdAt', () => {
    const flow = parseFlow(helloText)
    expect(stampSaved(flow, now).metadata).toEqual({
      createdAt: '2026-05-23T00:00:00Z',
      updatedAt: '2026-10-31T09:15:30Z',
      author: 'flowforge',
    })
  })
})

describe('defaultFileName', () => {
  it('derives a safe name from the flow id', () => {
    const flow = parseFlow(minimal())
    expect(defaultFileName({ ...flow, id: 'flow-hello' })).toBe(
      'flow-hello.flow.json',
    )
    expect(defaultFileName({ ...flow, id: 'a/b c' })).toBe('a-b-c.flow.json')
    expect(defaultFileName({ ...flow, id: '///' })).toBe('flow.flow.json')
  })
})
