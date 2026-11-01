import type { Flow } from '../flow/types.ts'
import { apiFetch } from './auth.ts'

// Client for the backend API (docs/api-v1.md). In development the Vite dev
// server proxies /api to `flowforge serve`.

/** One trace event, as the engine emits it (engine-v1.md §8). */
export interface TraceEvent {
  kind:
    | 'execution_started'
    | 'node_started'
    | 'node_completed'
    | 'node_failed'
    | 'execution_completed'
    | 'execution_failed'
  executionId: string
  nodeId?: string
  nodeType?: string
  timestamp: string
  durationNs?: number
  input?: Record<string, unknown>
  output?: Record<string, unknown>
  error?: string
}

/** A test run that happened (completed, or failed at a node). */
export interface RunResult {
  executionId: string
  status: 'completed' | 'failed'
  error?: string
  trace: TraceEvent[]
}

/** Why a test run could not happen at all. */
export interface RunError {
  error: string
  problems?: string[]
}

export type RunOutcome =
  { ok: true; result: RunResult } | { ok: false; error: RunError }

export const UNREACHABLE =
  'The backend is not reachable. Start it with `flowforge serve` (from backend/: go run ./cmd/flowforge serve).'

export const CONNECTION_LOST =
  'The connection to the backend was lost before the run finished.'

/**
 * Asks the backend for a test run: the trigger fires once with payload.
 * The run is streamed (docs/api-v1.md, "Streaming"): onEvent sees each trace
 * event as it happens. Never throws; every failure comes back as
 * { ok: false }.
 */
export async function runFlow(
  flow: Flow,
  trigger: string,
  payload: Record<string, unknown>,
  onEvent: (event: TraceEvent) => void = () => {},
  fetchFn: typeof fetch = apiFetch,
): Promise<RunOutcome> {
  let res: Response
  try {
    res = await fetchFn('/api/flows/run', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'text/event-stream, application/json',
      },
      body: JSON.stringify({ flow, trigger, payload }),
    })
  } catch {
    return { ok: false, error: { error: UNREACHABLE } }
  }

  if (
    res.ok &&
    res.body &&
    res.headers.get('Content-Type')?.startsWith('text/event-stream')
  ) {
    return readStream(res.body, onEvent)
  }

  let body: unknown
  try {
    body = await res.json()
  } catch {
    // The dev proxy answers with a non-JSON error when nothing listens.
    return { ok: false, error: { error: UNREACHABLE } }
  }
  if (res.ok) return { ok: true, result: body as RunResult }
  if (isRunError(body)) return { ok: false, error: body }
  return {
    ok: false,
    error: { error: `Unexpected response from the backend (${res.status}).` },
  }
}

// readStream collects a run's event stream: trace events until the final
// result event.
async function readStream(
  body: ReadableStream<Uint8Array>,
  onEvent: (event: TraceEvent) => void,
): Promise<RunOutcome> {
  const parser = new SSEParser()
  const decoder = new TextDecoder()
  const reader = body.getReader()
  const trace: TraceEvent[] = []
  try {
    for (;;) {
      const { done, value } = await reader.read()
      const text = done
        ? decoder.decode()
        : decoder.decode(value, { stream: true })
      for (const ev of parser.push(text)) {
        if (ev.event === 'trace') {
          const te = JSON.parse(ev.data) as TraceEvent
          trace.push(te)
          onEvent(te)
        } else if (ev.event === 'result') {
          const res = JSON.parse(ev.data) as Omit<RunResult, 'trace'>
          return { ok: true, result: { ...res, trace } }
        }
      }
      if (done) break
    }
  } catch {
    // A network error or a malformed event: treat it like a lost connection.
  }
  return { ok: false, error: { error: CONNECTION_LOST } }
}

/** One Server-Sent Event: its name (default "message") and data. */
export interface SSEMessage {
  event: string
  data: string
}

/**
 * Incremental parser for a text/event-stream body. Feed it decoded text in
 * chunks of any size; it returns the events completed by each chunk. Only
 * the "event" and "data" fields are used; comments and other fields are
 * ignored.
 */
export class SSEParser {
  private buffer = ''
  // A chunk ending in "\r" may be half of a "\r\n" line ending; it is held
  // back until the next chunk shows which.
  private carry = ''

  push(chunk: string): SSEMessage[] {
    let text = this.carry + chunk
    this.carry = ''
    if (text.endsWith('\r')) {
      this.carry = '\r'
      text = text.slice(0, -1)
    }
    this.buffer += text.replace(/\r\n?/g, '\n')
    const out: SSEMessage[] = []
    let end: number
    while ((end = this.buffer.indexOf('\n\n')) >= 0) {
      const block = this.buffer.slice(0, end)
      this.buffer = this.buffer.slice(end + 2)
      let event = 'message'
      const data: string[] = []
      for (const line of block.split('\n')) {
        const colon = line.indexOf(':')
        if (colon === 0) continue // comment
        const field = colon < 0 ? line : line.slice(0, colon)
        let value = colon < 0 ? '' : line.slice(colon + 1)
        if (value.startsWith(' ')) value = value.slice(1)
        if (field === 'event') event = value
        else if (field === 'data') data.push(value)
      }
      if (data.length) out.push({ event, data: data.join('\n') })
    }
    return out
  }
}

function isRunError(v: unknown): v is RunError {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as { error?: unknown }).error === 'string'
  )
}
