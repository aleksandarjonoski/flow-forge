import { describe, expect, it } from 'vitest'
import hello from '../../../backend/examples/hello.flow.json'
import type { TraceEvent } from '../api/client.ts'
import { addEdge, removeEdge, setNodeConfig, setNodeName } from './edit.ts'
import {
  availableData,
  dataView,
  outputView,
  sameUpstream,
} from './data-view.ts'
import { nodeType } from './node-types.ts'
import type { Flow } from './types.ts'

const flow: Flow = hello

// The trace of a hello run with body {name: "ada"}.
const ev = (e: Partial<TraceEvent> & Pick<TraceEvent, 'kind'>): TraceEvent => ({
  executionId: 'x',
  timestamp: 't',
  ...e,
})
const triggerOut = { headers: {}, query: {}, body: { name: 'ada' } }
const trace = [
  ev({
    kind: 'execution_started',
    nodeId: 'node-1',
    nodeType: 'http_trigger',
    input: triggerOut,
  }),
  ev({ kind: 'node_started', nodeId: 'node-2', nodeType: 'transform' }),
  ev({
    kind: 'node_completed',
    nodeId: 'node-2',
    nodeType: 'transform',
    output: { greeting: 'hello ada' },
  }),
  ev({ kind: 'node_started', nodeId: 'node-3', nodeType: 'log' }),
  ev({
    kind: 'node_completed',
    nodeId: 'node-3',
    nodeType: 'log',
    output: { greeting: 'hello ada' },
  }),
  ev({ kind: 'execution_completed' }),
]
const lastRun = { flow, trace }

describe('dataView without a run', () => {
  it('shows the previous node type contract as input', () => {
    expect(dataView(flow, 'node-2')).toEqual({
      mode: 'input',
      from: { id: 'node-1', name: 'HTTP Trigger' },
      shape: nodeType('http_trigger')!.output,
      source: 'type',
    })
  })

  it('shows what a trigger emits', () => {
    expect(dataView(flow, 'node-1')).toMatchObject({
      mode: 'emits',
      source: 'type',
      shape: nodeType('http_trigger')!.output,
    })
  })

  it('follows pass-through nodes upstream', () => {
    // node-4 after the log node receives what reached the log: the
    // transform's output, which no type can know.
    const f = addEdge(
      {
        ...flow,
        nodes: [
          ...flow.nodes,
          {
            id: 'node-4',
            type: 'log',
            name: '',
            position: { x: 0, y: 0 },
            config: {},
          },
        ],
      },
      { id: 'edge-3', source: 'node-3', target: 'node-4' },
    )
    expect(dataView(f, 'node-4')).toEqual({
      mode: 'input',
      from: { id: 'node-3', name: 'Log Output' },
      shape: { type: 'any', note: 'whatever the expression returns' },
      source: 'type',
    })
  })

  it('reports an unconnected node and an unknown one', () => {
    expect(dataView(removeEdge(flow, 'edge-1'), 'node-2')).toEqual({
      mode: 'input',
      source: 'none',
    })
    expect(dataView(flow, 'nope')).toBeUndefined()
  })
})

describe('dataView with a run', () => {
  it('shows the real output of the previous node', () => {
    expect(dataView(flow, 'node-2', lastRun)).toEqual({
      mode: 'input',
      from: { id: 'node-1', name: 'HTTP Trigger' },
      shape: {
        type: 'object',
        fields: {
          headers: { type: 'object', fields: {} },
          query: { type: 'object', fields: {} },
          body: {
            type: 'object',
            fields: { name: { type: 'string', sample: 'ada' } },
          },
        },
      },
      source: 'run',
    })
    expect(dataView(flow, 'node-3', lastRun)?.shape).toEqual({
      type: 'object',
      fields: { greeting: { type: 'string', sample: 'hello ada' } },
    })
  })

  it('ignores the run once something upstream changed', () => {
    const changed = setNodeConfig(flow, 'node-2', { expression: '{ x: 1 }' })
    expect(dataView(changed, 'node-3', lastRun)?.source).toBe('type')
    // The trigger is still unchanged, so node-2's input still comes from the run.
    expect(dataView(changed, 'node-2', lastRun)?.source).toBe('run')
  })

  it('keeps using the run when only names changed', () => {
    const renamed = setNodeName(flow, 'node-2', 'Greeting')
    expect(dataView(renamed, 'node-3', lastRun)).toMatchObject({
      from: { name: 'Greeting' },
      source: 'run',
    })
  })
})

describe('sameUpstream', () => {
  it('compares the chain of nodes and edges up to a node', () => {
    expect(sameUpstream(flow, flow, 'node-3')).toBe(true)
    expect(sameUpstream(removeEdge(flow, 'edge-1'), flow, 'node-3')).toBe(false)
    expect(sameUpstream(flow, flow, 'missing')).toBe(false)
  })
})

describe('dataView with a declared schema', () => {
  // hello with a Parse JSON node between the trigger and the transform.
  const parse = {
    id: 'node-9',
    type: 'parse_json',
    name: 'Parse Order',
    position: { x: 0, y: 0 },
    config: {
      from: 'body',
      schema: {
        type: 'object',
        properties: { name: { type: 'string' }, qty: { type: 'integer' } },
      },
    },
  }
  const withParse: Flow = {
    ...flow,
    nodes: [...flow.nodes, parse],
    edges: [
      { id: 'edge-1', source: 'node-1', target: 'node-9' },
      { id: 'edge-9', source: 'node-9', target: 'node-2' },
      flow.edges[1],
    ],
  }

  it('shows the declared fields without a run', () => {
    expect(dataView(withParse, 'node-2')).toEqual({
      mode: 'input',
      from: { id: 'node-9', name: 'Parse Order' },
      shape: {
        type: 'object',
        fields: { name: { type: 'string' }, qty: { type: 'integer' } },
        required: [],
      },
      source: 'declared',
    })
  })

  it('keeps declared fields after a run that lacked them', () => {
    // The parse ran with an empty output (an optional field was absent).
    const run = {
      flow: withParse,
      trace: [
        ev({
          kind: 'execution_started',
          nodeId: 'node-1',
          nodeType: 'http_trigger',
          input: {},
        }),
        ev({ kind: 'node_started', nodeId: 'node-9', nodeType: 'parse_json' }),
        ev({
          kind: 'node_completed',
          nodeId: 'node-9',
          nodeType: 'parse_json',
          output: { qty: 3 },
        }),
      ],
    }
    expect(dataView(withParse, 'node-2', run)).toEqual({
      mode: 'input',
      from: { id: 'node-9', name: 'Parse Order' },
      shape: {
        type: 'object',
        fields: {
          name: { type: 'string' },
          qty: { type: 'integer', sample: 3 },
        },
        required: [],
      },
      source: 'declared-run',
    })
  })

  it('falls back to "any" while the schema is incomplete', () => {
    const broken = setNodeConfig(withParse, 'node-9', { schema: { type: 'x' } })
    expect(dataView(broken, 'node-2')).toMatchObject({
      source: 'type',
      shape: { type: 'any' },
    })
  })
})

describe('outputView', () => {
  it('shows what a node outputs: run, then type', () => {
    expect(outputView(flow, 'node-2', lastRun)).toEqual({
      mode: 'output',
      shape: {
        type: 'object',
        fields: { greeting: { type: 'string', sample: 'hello ada' } },
      },
      source: 'run',
    })
    expect(outputView(flow, 'node-2')).toEqual({
      mode: 'output',
      shape: { type: 'any', note: 'whatever the expression returns' },
      source: 'type',
    })
    expect(outputView(flow, 'nope')).toBeUndefined()
  })
})

describe('availableData', () => {
  it('lists the input, then earlier nodes nearest first', () => {
    const trees = availableData(flow, 'node-3', lastRun)
    expect(trees.map((t) => [t.label, t.root, t.nodeId, t.source])).toEqual([
      ['input', 'input', 'node-2', 'run'],
      ['$HTTP Trigger', { node: 'node-1' }, 'node-1', 'run'],
    ])
    expect(trees[1].shape).toMatchObject({ type: 'object' })
  })

  it('is empty for a trigger or an unconnected node', () => {
    expect(availableData(flow, 'node-1')).toEqual([])
    expect(availableData(removeEdge(flow, 'edge-1'), 'node-2')).toEqual([])
  })
})
