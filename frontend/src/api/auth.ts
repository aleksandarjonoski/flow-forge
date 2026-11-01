// The API token (docs/prod-v1.md §3.4): kept per tab in sessionStorage, or
// in localStorage when the user chose to be remembered, and sent with every
// API call. A 401 tells the app to ask for it.

/** The part of the Web Storage API the token needs. */
export type TokenStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

const KEY = 'flowforge.apiToken'

/** Where the token is kept: per tab, or remembered on this computer. */
export class TokenStore {
  private session: TokenStorage | undefined
  private local: TokenStorage | undefined

  constructor(
    session: TokenStorage | undefined,
    local: TokenStorage | undefined,
  ) {
    this.session = session
    this.local = local
  }

  /** The token in use, if any. A tab's own token wins. */
  get(): string | undefined {
    return read(this.session) ?? read(this.local)
  }

  /** Keeps token for this tab, and on this computer if remember is set. */
  set(token: string, remember: boolean) {
    this.clear()
    write(this.session, token)
    if (remember) write(this.local, token)
  }

  /** Forgets the token everywhere. */
  clear() {
    for (const s of [this.session, this.local]) {
      try {
        s?.removeItem(KEY)
      } catch {
        // Storage can be blocked; there is nothing to forget then.
      }
    }
  }
}

function read(s: TokenStorage | undefined): string | undefined {
  try {
    return s?.getItem(KEY) || undefined
  } catch {
    return undefined
  }
}

function write(s: TokenStorage | undefined, token: string) {
  try {
    s?.setItem(KEY, token)
  } catch {
    // Blocked storage: the token lasts as long as the page then.
  }
}

// storage returns window[name], or undefined where it is missing or blocked.
function storage(name: 'sessionStorage' | 'localStorage') {
  try {
    return typeof window === 'undefined' ? undefined : window[name]
  } catch {
    return undefined
  }
}

/** The editor's token store. */
export const tokens = new TokenStore(
  storage('sessionStorage'),
  storage('localStorage'),
)

/**
 * Returns a fetch that sends the store's token as a bearer token and calls
 * onUnauthorized when the server answers 401.
 */
export function authFetch(
  store: TokenStore,
  onUnauthorized: () => void,
  fetchFn: typeof fetch = (input, init) => fetch(input, init),
): typeof fetch {
  return async (input, init) => {
    const token = store.get()
    const headers = new Headers(init?.headers)
    if (token) headers.set('Authorization', `Bearer ${token}`)
    const res = await fetchFn(input, { ...init, headers })
    if (res.status === 401) onUnauthorized()
    return res
  }
}

const unauthorized = new EventTarget()

/** Calls listener whenever an API call is refused for want of a token. */
export function onUnauthorized(listener: () => void): () => void {
  unauthorized.addEventListener('unauthorized', listener)
  return () => unauthorized.removeEventListener('unauthorized', listener)
}

/** The fetch every API call uses by default. */
export const apiFetch = authFetch(tokens, () =>
  unauthorized.dispatchEvent(new Event('unauthorized')),
)

export type TokenCheck = 'ok' | 'rejected' | 'unreachable'

/** Asks the server whether token is accepted, without keeping it. */
export async function checkToken(
  token: string,
  fetchFn: typeof fetch = (input, init) => fetch(input, init),
): Promise<TokenCheck> {
  try {
    const res = await fetchFn('/api/flows', {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.status === 401) return 'rejected'
    // The dev proxy answers 5xx when nothing listens.
    return res.status >= 500 ? 'unreachable' : 'ok'
  } catch {
    return 'unreachable'
  }
}
