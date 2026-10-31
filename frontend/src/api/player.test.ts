import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TraceEvent } from './client.ts'
import { TracePlayer, holdAfter } from './player.ts'

const ev = (kind: TraceEvent['kind'], nodeId?: string): TraceEvent => ({
  kind,
  nodeId,
  executionId: 'x',
  timestamp: 't',
})

// A complete one-node run: trigger, one action, end.
const run = [
  ev('execution_started', 't'),
  ev('node_started', 'a'),
  ev('node_completed', 'a'),
  ev('execution_completed'),
]

describe('holdAfter', () => {
  it('keeps running nodes longest and never delays at real time', () => {
    expect(holdAfter('node_started', 'normal')).toBe(600)
    expect(holdAfter('execution_started', 'normal')).toBe(300)
    expect(holdAfter('node_completed', 'slow')).toBe(375)
    expect(holdAfter('execution_completed', 'slow')).toBe(0)
    for (const e of run) expect(holdAfter(e.kind, 'realtime')).toBe(0)
  })
})

describe('TracePlayer', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  function player(speed: 'realtime' | 'normal' | 'slow') {
    const shown: string[] = []
    const p = new TracePlayer((e) => shown.push(e.kind), speed)
    return { p, shown }
  }

  it('releases events at the pace of the speed', async () => {
    const { p, shown } = player('normal')
    run.forEach((e) => p.push(e))
    let drained = false
    void p.drained().then(() => (drained = true))

    expect(shown).toEqual(['execution_started'])
    await vi.advanceTimersByTimeAsync(300)
    expect(shown).toEqual(['execution_started', 'node_started'])
    await vi.advanceTimersByTimeAsync(599)
    expect(shown).toHaveLength(2) // the node is still "running"
    await vi.advanceTimersByTimeAsync(1)
    expect(shown).toHaveLength(3)
    expect(drained).toBe(false)
    await vi.advanceTimersByTimeAsync(150)
    expect(shown).toHaveLength(4)
    expect(drained).toBe(true)
  })

  it('shows late events as soon as they arrive', async () => {
    const { p, shown } = player('normal')
    p.push(run[0])
    await vi.advanceTimersByTimeAsync(5000) // the run is slower than the pace
    p.push(run[1])
    expect(shown).toEqual(['execution_started', 'node_started'])
  })

  it('shows everything at once at real time', async () => {
    const { p, shown } = player('realtime')
    run.forEach((e) => p.push(e))
    expect(shown).toHaveLength(4)
    await expect(p.drained()).resolves.toBeUndefined()
  })

  it('jumps to the end when switched to real time', () => {
    const { p, shown } = player('slow')
    run.forEach((e) => p.push(e))
    expect(shown).toHaveLength(1)
    p.setSpeed('realtime')
    expect(shown).toHaveLength(4)
  })

  it('stop drops the queue and resolves drained', async () => {
    const { p, shown } = player('slow')
    run.forEach((e) => p.push(e))
    const drained = p.drained()
    p.stop()
    p.push(ev('node_started', 'b'))
    await expect(drained).resolves.toBeUndefined()
    expect(shown).toEqual(['execution_started'])
  })
})
