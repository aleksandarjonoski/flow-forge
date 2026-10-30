import type { Flow } from '../flow/types.ts'

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

/**
 * Asks the backend for a test run: the trigger fires once with payload.
 * Never throws; every failure comes back as { ok: false }.
 */
export async function runFlow(
  flow: Flow,
  trigger: string,
  payload: Record<string, unknown>,
  fetchFn: typeof fetch = fetch,
): Promise<RunOutcome> {
  let res: Response
  try {
    res = await fetchFn('/api/flows/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ flow, trigger, payload }),
    })
  } catch {
    return { ok: false, error: { error: UNREACHABLE } }
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

function isRunError(v: unknown): v is RunError {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as { error?: unknown }).error === 'string'
  )
}
