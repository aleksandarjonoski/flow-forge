import { describe, expect, it } from 'vitest'
import {
  copyId,
  executionNotes,
  executionOutcome,
  flowIdFromHash,
  hashForFlow,
  notDeployed,
} from './library.ts'
import type { ExecutionWithTrace } from '../api/library.ts'

describe('notDeployed', () => {
  it('is true only for a running flow behind its saved revision', () => {
    expect(
      notDeployed({ state: 'running', revision: 3, runningRevision: 2 }),
    ).toBe(true)
    expect(
      notDeployed({ state: 'running', revision: 3, runningRevision: 3 }),
    ).toBe(false)
    expect(
      notDeployed({ state: 'stopped', revision: 3, runningRevision: 0 }),
    ).toBe(false)
    expect(
      notDeployed({ state: 'failed', revision: 3, runningRevision: 0 }),
    ).toBe(false)
  })
})

describe('copyId', () => {
  it('finds the first free copy ID', () => {
    expect(copyId('orders', ['orders'])).toBe('orders-copy')
    expect(copyId('orders', ['orders', 'orders-copy'])).toBe('orders-copy-2')
    expect(copyId('orders', ['orders', 'orders-copy', 'orders-copy-2'])).toBe(
      'orders-copy-3',
    )
  })
})

describe('location hash', () => {
  it('round-trips flow IDs', () => {
    for (const id of ['orders', 'a.b_c-d', 'x y', 'ü']) {
      expect(flowIdFromHash(hashForFlow(id))).toBe(id)
    }
  })

  it('ignores other hashes', () => {
    expect(flowIdFromHash('')).toBeUndefined()
    expect(flowIdFromHash('#/other/x')).toBeUndefined()
    expect(flowIdFromHash('#/flows/')).toBeUndefined()
    expect(flowIdFromHash('#/flows/%E0%A4%A')).toBeUndefined()
  })
})

const exec: ExecutionWithTrace = {
  id: 'e1',
  flowId: 'f',
  revision: 2,
  trigger: 't',
  status: 'completed',
  startedAt: '2026-10-31T10:00:00Z',
  finishedAt: '2026-10-31T10:00:01Z',
  traceComplete: true,
  trace: [],
}

describe('executionOutcome', () => {
  it('maps statuses to a run result', () => {
    expect(executionOutcome(exec)).toEqual({
      ok: true,
      result: {
        executionId: 'e1',
        status: 'completed',
        error: undefined,
        trace: [],
      },
    })
    const failed = executionOutcome({
      ...exec,
      status: 'failed',
      error: 'boom',
    })
    expect(failed.ok && failed.result).toMatchObject({
      status: 'failed',
      error: 'boom',
    })
    const cut = executionOutcome({ ...exec, status: 'interrupted' })
    expect(cut.ok && cut.result.status).toBe('failed')
    expect(cut.ok && cut.result.error).toMatch(/interrupted/)
  })
})

describe('executionNotes', () => {
  it('warns when the canvas differs from what ran', () => {
    expect(executionNotes(exec, { revision: 2, dirty: false })).toEqual([])
    expect(executionNotes(exec, { revision: 3, dirty: false })[0]).toMatch(
      /revision 2.*revision 3/,
    )
    expect(executionNotes(exec, { revision: 2, dirty: true })[0]).toMatch(
      /unsaved changes/,
    )
  })

  it('warns about an incomplete trace', () => {
    const notes = executionNotes(
      { ...exec, traceComplete: false },
      { revision: 2, dirty: false },
    )
    expect(notes).toHaveLength(1)
    expect(notes[0]).toMatch(/incomplete/)
  })
})
