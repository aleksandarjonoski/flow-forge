import { describe, expect, it, vi } from 'vitest'
import { TokenStore, authFetch, checkToken, type TokenStorage } from './auth.ts'

function memory(): TokenStorage & { data: Map<string, string> } {
  const data = new Map<string, string>()
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  }
}

function respond(status: number) {
  return vi.fn<typeof fetch>(() =>
    Promise.resolve(new Response('{}', { status })),
  )
}

describe('TokenStore', () => {
  it('keeps the token for the tab, or also on this computer', () => {
    const session = memory()
    const local = memory()
    const store = new TokenStore(session, local)
    expect(store.get()).toBeUndefined()

    store.set('tab', false)
    expect(store.get()).toBe('tab')
    expect(local.data.size).toBe(0)

    store.set('kept', true)
    expect(store.get()).toBe('kept')
    expect(local.data.size).toBe(1)

    // A new tab finds the remembered token.
    expect(new TokenStore(memory(), local).get()).toBe('kept')

    store.clear()
    expect(store.get()).toBeUndefined()
    expect(local.data.size).toBe(0)
  })

  it('survives missing or blocked storage', () => {
    const blocked: TokenStorage = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {
        throw new Error('blocked')
      },
    }
    const store = new TokenStore(blocked, undefined)
    store.set('t', true)
    expect(store.get()).toBeUndefined()
    store.clear()
  })
})

describe('authFetch', () => {
  it('sends the token as a bearer token, keeping other headers', async () => {
    const store = new TokenStore(memory(), memory())
    store.set('secret', false)
    const inner = respond(200)
    await authFetch(
      store,
      () => {},
      inner,
    )('/api/flows', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
    const init = inner.mock.calls[0][1]!
    const headers = new Headers(init.headers)
    expect(init.method).toBe('POST')
    expect(headers.get('Authorization')).toBe('Bearer secret')
    expect(headers.get('Content-Type')).toBe('application/json')
  })

  it('sends no header without a token, and reports a 401', async () => {
    const inner = respond(401)
    const unauthorized = vi.fn()
    const res = await authFetch(
      new TokenStore(memory(), memory()),
      unauthorized,
      inner,
    )('/api/flows')
    expect(res.status).toBe(401)
    expect(
      new Headers(inner.mock.calls[0][1]!.headers).has('Authorization'),
    ).toBe(false)
    expect(unauthorized).toHaveBeenCalledOnce()
  })
})

describe('checkToken', () => {
  it('tells accepted, rejected and unreachable apart', async () => {
    const ok = respond(200)
    expect(await checkToken('t', ok)).toBe('ok')
    expect(new Headers(ok.mock.calls[0][1]!.headers).get('Authorization')).toBe(
      'Bearer t',
    )
    expect(await checkToken('t', respond(401))).toBe('rejected')
    expect(await checkToken('t', respond(502))).toBe('unreachable')
    const down = vi.fn<typeof fetch>(() => Promise.reject(new TypeError('x')))
    expect(await checkToken('t', down)).toBe('unreachable')
  })
})
