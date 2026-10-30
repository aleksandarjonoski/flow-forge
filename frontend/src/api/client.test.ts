import { describe, expect, it, vi } from 'vitest'
import { UNREACHABLE, runFlow } from './client.ts'
import { createFlow } from '../flow/edit.ts'

const flow = createFlow('f')

function respond(status: number, body: string): typeof fetch {
  return vi.fn(() =>
    Promise.resolve(
      new Response(body, {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
}

describe('runFlow', () => {
  it('posts flow, trigger and payload as JSON', async () => {
    const fetchFn = respond(
      200,
      '{"executionId":"x","status":"completed","trace":[]}',
    )
    await runFlow(flow, 'node-1', { body: { a: 1 } }, fetchFn)
    expect(fetchFn).toHaveBeenCalledWith('/api/flows/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        flow,
        trigger: 'node-1',
        payload: { body: { a: 1 } },
      }),
    })
  })

  it('returns the run result on 200, including failed runs', async () => {
    const outcome = await runFlow(
      flow,
      't',
      {},
      respond(
        200,
        '{"executionId":"x","status":"failed","error":"boom","trace":[]}',
      ),
    )
    expect(outcome).toEqual({
      ok: true,
      result: { executionId: 'x', status: 'failed', error: 'boom', trace: [] },
    })
  })

  it('returns the API error with its problems', async () => {
    const outcome = await runFlow(
      flow,
      't',
      {},
      respond(400, '{"error":"flow does not compile","problems":["a","b"]}'),
    )
    expect(outcome).toEqual({
      ok: false,
      error: { error: 'flow does not compile', problems: ['a', 'b'] },
    })
  })

  it('reports an unreachable backend', async () => {
    const down = vi.fn(() => Promise.reject(new TypeError('fetch failed')))
    expect(await runFlow(flow, 't', {}, down)).toEqual({
      ok: false,
      error: { error: UNREACHABLE },
    })
    // What the dev proxy returns when nothing listens: an error without JSON.
    expect(await runFlow(flow, 't', {}, respond(500, ''))).toEqual({
      ok: false,
      error: { error: UNREACHABLE },
    })
  })

  it('reports an unexpected JSON error shape', async () => {
    expect(await runFlow(flow, 't', {}, respond(502, '{"x":1}'))).toEqual({
      ok: false,
      error: { error: 'Unexpected response from the backend (502).' },
    })
  })
})
