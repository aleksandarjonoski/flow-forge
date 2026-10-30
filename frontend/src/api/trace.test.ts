import { describe, expect, it } from 'vitest'
import type { TraceEvent } from './client.ts'
import { executionDurationNs, formatDuration, nodeRuns } from './trace.ts'

const ts = '2026-10-31T00:00:00Z'
const ev = (e: Partial<TraceEvent> & Pick<TraceEvent, 'kind'>): TraceEvent => ({
  executionId: 'x',
  timestamp: ts,
  ...e,
})

describe('nodeRuns', () => {
  it('folds a completed execution into one entry per node', () => {
    const trace = [
      ev({
        kind: 'execution_started',
        nodeId: 't',
        nodeType: 'http_trigger',
        input: { body: 1 },
      }),
      ev({
        kind: 'node_started',
        nodeId: 'a',
        nodeType: 'transform',
        input: { body: 1 },
      }),
      ev({
        kind: 'node_completed',
        nodeId: 'a',
        nodeType: 'transform',
        durationNs: 2000,
        input: { body: 1 },
        output: { g: 'hi' },
      }),
      ev({ kind: 'execution_completed', durationNs: 5000 }),
    ]
    expect(nodeRuns(trace)).toEqual([
      {
        nodeId: 't',
        nodeType: 'http_trigger',
        status: 'trigger',
        output: { body: 1 },
      },
      {
        nodeId: 'a',
        nodeType: 'transform',
        status: 'completed',
        durationNs: 2000,
        input: { body: 1 },
        output: { g: 'hi' },
        error: undefined,
      },
    ])
    expect(executionDurationNs(trace)).toBe(5000)
  })

  it('records failures, nodes still running, and failures without a start', () => {
    const runs = nodeRuns([
      ev({ kind: 'execution_started', nodeId: 't', nodeType: 'cron_trigger' }),
      ev({ kind: 'node_started', nodeId: 'a', nodeType: 'log' }),
      ev({
        kind: 'node_failed',
        nodeId: 'a',
        nodeType: 'log',
        error: 'boom',
        durationNs: 7,
      }),
      ev({ kind: 'node_started', nodeId: 'b', nodeType: 'log' }),
      ev({
        kind: 'node_failed',
        nodeId: 'c',
        nodeType: 'log',
        error: 'context canceled',
      }),
    ])
    expect(runs.map((r) => [r.nodeId, r.status, r.error])).toEqual([
      ['t', 'trigger', undefined],
      ['a', 'failed', 'boom'],
      ['b', 'running', undefined],
      ['c', 'failed', 'context canceled'],
    ])
    expect(executionDurationNs([])).toBeUndefined()
  })
})

describe('formatDuration', () => {
  it('picks a readable unit', () => {
    expect(formatDuration(170_000)).toBe('0.17 ms')
    expect(formatDuration(12_340_000)).toBe('12.3 ms')
    expect(formatDuration(1_204_000_000)).toBe('1.20 s')
    expect(formatDuration(undefined)).toBe('')
  })
})
