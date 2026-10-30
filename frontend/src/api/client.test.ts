import { describe, expect, it, vi } from 'vitest'
import {
  CONNECTION_LOST,
  SSEParser,
  UNREACHABLE,
  runFlow,
  type TraceEvent,
} from './client.ts'
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
    await runFlow(flow, 'node-1', { body: { a: 1 } }, undefined, fetchFn)
    expect(fetchFn).toHaveBeenCalledWith('/api/flows/run', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'text/event-stream, application/json',
      },
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
      undefined,
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
      undefined,
      respond(400, '{"error":"flow does not compile","problems":["a","b"]}'),
    )
    expect(outcome).toEqual({
      ok: false,
      error: { error: 'flow does not compile', problems: ['a', 'b'] },
    })
  })

  it('reports an unreachable backend', async () => {
    const down = vi.fn(() => Promise.reject(new TypeError('fetch failed')))
    expect(await runFlow(flow, 't', {}, undefined, down)).toEqual({
      ok: false,
      error: { error: UNREACHABLE },
    })
    // What the dev proxy returns when nothing listens: an error without JSON.
    expect(await runFlow(flow, 't', {}, undefined, respond(500, ''))).toEqual({
      ok: false,
      error: { error: UNREACHABLE },
    })
  })

  it('reports an unexpected JSON error shape', async () => {
    expect(
      await runFlow(flow, 't', {}, undefined, respond(502, '{"x":1}')),
    ).toEqual({
      ok: false,
      error: { error: 'Unexpected response from the backend (502).' },
    })
  })
})

// stream returns a fetch that answers with an event stream delivered in the
// given chunks.
function stream(chunks: string[]): typeof fetch {
  return () => {
    const enc = new TextEncoder()
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        for (const ch of chunks) c.enqueue(enc.encode(ch))
        c.close()
      },
    })
    return Promise.resolve(
      new Response(body, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      }),
    )
  }
}

describe('runFlow streaming', () => {
  const started =
    '{"kind":"execution_started","executionId":"x","timestamp":"t"}'
  const finished =
    '{"kind":"execution_completed","executionId":"x","timestamp":"t"}'

  it('reports each trace event and returns the collected run', async () => {
    const seen: TraceEvent[] = []
    const outcome = await runFlow(
      flow,
      't',
      {},
      (e) => seen.push(e),
      stream([
        `event: trace\ndata: ${started}\n\nevent: tr`,
        `ace\ndata: ${finished}\n`,
        '\nevent: result\ndata: {"executionId":"x","status":"completed"}\n\n',
      ]),
    )
    expect(seen.map((e) => e.kind)).toEqual([
      'execution_started',
      'execution_completed',
    ])
    expect(outcome).toEqual({
      ok: true,
      result: { executionId: 'x', status: 'completed', trace: seen },
    })
  })

  it('reports a stream that ends without a result', async () => {
    const outcome = await runFlow(
      flow,
      't',
      {},
      undefined,
      stream([`event: trace\ndata: ${started}\n\n`]),
    )
    expect(outcome).toEqual({ ok: false, error: { error: CONNECTION_LOST } })
  })
})

describe('SSEParser', () => {
  it('handles split chunks, CRLF, comments, defaults and multi-line data', () => {
    const p = new SSEParser()
    expect(p.push('event: a\r\nda')).toEqual([])
    expect(p.push('ta: 1\r\n\r\n: comment\n\ndata: x\ndata: y\n\n')).toEqual([
      { event: 'a', data: '1' },
      { event: 'message', data: 'x\ny' },
    ])
    // A CRLF split across chunks is one line break, not two.
    expect(p.push('data: z\r')).toEqual([])
    expect(p.push('\ndata: w\r\n\r\n')).toEqual([
      { event: 'message', data: 'z\nw' },
    ])
    expect(p.push('event: b\n')).toEqual([])
    expect(p.push('data:no-space\n\n')).toEqual([
      { event: 'b', data: 'no-space' },
    ])
  })
})
