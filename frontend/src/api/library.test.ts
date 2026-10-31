import { describe, expect, it, vi } from 'vitest'
import { UNREACHABLE } from './client.ts'
import {
  createFlow,
  deleteFlow,
  getFlow,
  listExecutions,
  listFlows,
  saveFlow,
  startFlow,
} from './library.ts'
import { addNode, createFlow as newFlow } from '../flow/edit.ts'
import { serializeFlow } from '../flow/file.ts'

const flow = addNode(newFlow('orders'), 'log', { x: 0, y: 0 }).flow

const meta = {
  id: 'orders',
  name: '',
  revision: 2,
  createdAt: '2026-10-31T10:00:00Z',
  updatedAt: '2026-10-31T11:00:00Z',
  state: 'running',
  runningRevision: 1,
  error: '',
}
const stored = { ...meta, flow }

function respond(status: number, body?: unknown) {
  return vi.fn<typeof fetch>(() =>
    Promise.resolve(
      new Response(body === undefined ? null : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
}

describe('library client', () => {
  it('sends the flow in canonical form', async () => {
    const fetchFn = respond(200, stored)
    await saveFlow(flow, 2, fetchFn)
    const [path, init] = fetchFn.mock.calls[0]
    expect(path).toBe('/api/flows/orders')
    expect(init?.method).toBe('PUT')
    expect(init?.body).toBe(
      `{"flow": ${serializeFlow(flow).trimEnd()}, "revision": 2}`,
    )
    expect(JSON.parse(init?.body as string)).toEqual({ flow, revision: 2 })

    const create = respond(201, stored)
    await createFlow(flow, create)
    expect(create.mock.calls[0][1]?.body).toBe(
      `{"flow": ${serializeFlow(flow).trimEnd()}}`,
    )
  })

  it('reads a stored flow and checks its document', async () => {
    const r = await getFlow('orders', respond(200, stored))
    expect(r).toEqual({ ok: true, value: { ...stored, flow } })

    const bad = await getFlow('orders', respond(200, { ...meta, flow: {} }))
    expect(bad.ok).toBe(false)
  })

  it('escapes IDs in paths', async () => {
    const fetchFn = respond(200, stored)
    await startFlow('a/b', fetchFn)
    expect(fetchFn.mock.calls[0][0]).toBe('/api/flows/a%2Fb/start')
  })

  it('returns the current flow on a conflict', async () => {
    const r = await saveFlow(
      flow,
      1,
      respond(409, { error: 'saved by someone else', current: stored }),
    )
    expect(r.ok).toBe(false)
    if (r.ok) return
    expect(r.error.status).toBe(409)
    expect(r.error.current?.revision).toBe(2)
    expect(r.error.current?.flow).toEqual(flow)
  })

  it('passes compile problems through', async () => {
    const r = await startFlow(
      'orders',
      respond(400, { error: 'flow does not compile', problems: ['a', 'b'] }),
    )
    expect(r).toEqual({
      ok: false,
      error: {
        status: 400,
        error: 'flow does not compile',
        problems: ['a', 'b'],
      },
    })
  })

  it('reports an unreachable backend', async () => {
    const down = vi.fn<typeof fetch>(() => Promise.reject(new TypeError('x')))
    expect(await listFlows(down)).toEqual({
      ok: false,
      error: { status: 0, error: UNREACHABLE },
    })
    // The dev proxy's error page is not JSON.
    const proxy = vi.fn<typeof fetch>(() =>
      Promise.resolve(new Response('Bad Gateway', { status: 502 })),
    )
    expect((await listFlows(proxy)).ok).toBe(false)
  })

  it('handles 204 and query strings', async () => {
    expect(await deleteFlow('orders', respond(204))).toEqual({
      ok: true,
      value: undefined,
    })

    const fetchFn = respond(200, { executions: [] })
    const r = await listExecutions(
      'orders',
      { limit: 20, before: 'c=1' },
      fetchFn,
    )
    expect(fetchFn.mock.calls[0][0]).toBe(
      '/api/executions?flow=orders&limit=20&before=c%3D1',
    )
    expect(r).toEqual({ ok: true, value: { executions: [] } })
  })

  it('lists flows', async () => {
    expect(await listFlows(respond(200, { flows: [meta] }))).toEqual({
      ok: true,
      value: [meta],
    })
  })
})
