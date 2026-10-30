import { describe, expect, it } from 'vitest'
import hello from '../../../backend/examples/hello.flow.json'
import { FlowLoadError, flowToGraph } from './to-graph.ts'
import type { Edge, Flow, FlowNode } from './types.ts'

function node(id: string, type: string, name = ''): FlowNode {
  return { id, type, name, position: { x: 0, y: 0 }, config: {} }
}

function edge(id: string, source: string, target: string): Edge {
  return { id, source, target }
}

function flow(nodes: FlowNode[], edges: Edge[]): Flow {
  return { id: 'f', name: 'f', version: '1.0', nodes, edges, metadata: {} }
}

function problemsOf(f: Flow): string[] {
  try {
    flowToGraph(f)
  } catch (e) {
    if (e instanceof FlowLoadError) return e.problems
    throw e
  }
  throw new Error('flowToGraph succeeded, want FlowLoadError')
}

describe('flowToGraph', () => {
  it('maps the hello example', () => {
    // Assigning to Flow also type-checks the example against the model.
    const example: Flow = hello
    expect(flowToGraph(example)).toEqual({
      nodes: [
        {
          id: 'node-1',
          label: 'HTTP Trigger',
          position: { x: 100, y: 200 },
          hasInput: false,
          hasOutput: true,
        },
        {
          id: 'node-2',
          label: 'Transform Data',
          position: { x: 400, y: 200 },
          hasInput: true,
          hasOutput: true,
        },
        {
          id: 'node-3',
          label: 'Log Output',
          position: { x: 700, y: 200 },
          hasInput: true,
          hasOutput: true,
        },
      ],
      connections: [
        { id: 'edge-1', source: 'node-1', target: 'node-2' },
        { id: 'edge-2', source: 'node-2', target: 'node-3' },
      ],
    })
  })

  it('gives triggers no input and other types both sockets', () => {
    const g = flowToGraph(
      flow(
        [
          node('c', 'cron_trigger'),
          node('h', 'http_request'),
          node('x', 'some_plugin_type'),
        ],
        [],
      ),
    )
    expect(g.nodes.map((n) => [n.id, n.hasInput, n.hasOutput])).toEqual([
      ['c', false, true],
      ['h', true, true],
      ['x', true, true],
    ])
  })

  it('labels a node with its type when it has no name', () => {
    const g = flowToGraph(
      flow([node('a', 'log'), node('b', 'log', 'Mine')], []),
    )
    expect(g.nodes.map((n) => n.label)).toEqual(['log', 'Mine'])
  })

  it('reports every problem at once', () => {
    const problems = problemsOf(
      flow(
        [
          node('t', 'http_trigger'),
          node('a', 'log'),
          node('b', 'log'),
          node('a', 'log'),
        ],
        [
          edge('e1', 'missing', 'a'),
          edge('e2', 'a', 'gone'),
          edge('e3', 'a', 't'),
          edge('e4', 't', 'b'),
          edge('e5', 'a', 'b'),
        ],
      ),
    )
    expect(problems).toEqual([
      'duplicate node id "a"',
      'edge "e1": unknown source node "missing"',
      'edge "e2": unknown target node "gone"',
      'edge "e3": trigger "t" cannot have inbound edges',
      'edge "e5": node "b" already has inbound edge "e4"; merge nodes are not supported in v1',
    ])
  })

  it('does not count a rejected edge as a node inbound edge', () => {
    // e1 is rejected (unknown source), so e2 is the first valid inbound edge.
    const problems = problemsOf(
      flow(
        [node('a', 'log'), node('b', 'log')],
        [edge('e1', 'missing', 'b'), edge('e2', 'a', 'b')],
      ),
    )
    expect(problems).toEqual(['edge "e1": unknown source node "missing"'])
  })
})
