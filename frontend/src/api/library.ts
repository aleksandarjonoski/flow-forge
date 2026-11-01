import { parseFlow, serializeFlow } from '../flow/file.ts'
import { withEdgeIds } from '../flow/edit.ts'
import type { Flow } from '../flow/types.ts'
import { apiFetch } from './auth.ts'
import { UNREACHABLE, type TraceEvent } from './client.ts'

// Client for the flow library and run history (docs/api-v1.md, "Flow
// library" and "Run history"; docs/server-v1.md §6–7).

/** Where a stored flow stands on the server. */
export type FlowState = 'stopped' | 'running' | 'failed'

/** A stored flow without its document, as the library lists it. */
export interface FlowSummary {
  id: string
  name: string
  revision: number
  createdAt: string
  updatedAt: string
  state: FlowState
  /** The revision that runs; 0 unless running. */
  runningRevision: number
  /** Why the flow is failed. */
  error: string
}

/** A stored flow with its document. */
export interface StoredFlow extends FlowSummary {
  flow: Flow
}

export type ExecutionStatus = 'running' | 'completed' | 'failed' | 'interrupted'

/** A recorded execution of a deployed flow. */
export interface Execution {
  id: string
  flowId: string
  revision: number
  trigger: string
  status: ExecutionStatus
  error?: string
  startedAt: string
  finishedAt?: string
  traceComplete: boolean
}

export interface ExecutionWithTrace extends Execution {
  trace: TraceEvent[]
}

export interface ExecutionPage {
  executions: Execution[]
  /** Cursor of the next (older) page; absent on the last page. */
  next?: string
}

/** Why a library call failed. */
export interface LibraryError {
  /** HTTP status, or 0 when the backend could not be reached. */
  status: number
  error: string
  problems?: string[]
  /** On a 409 save: the flow as it is stored now. */
  current?: StoredFlow
}

export type Result<T> =
  { ok: true; value: T } | { ok: false; error: LibraryError }

/** Lists every stored flow. */
export function listFlows(fetchFn: typeof fetch = apiFetch) {
  return call<FlowSummary[]>(
    fetchFn,
    'GET',
    '/api/flows',
    undefined,
    (b) => (b as { flows: FlowSummary[] }).flows,
  )
}

/** Loads one stored flow with its document. */
export function getFlow(id: string, fetchFn: typeof fetch = apiFetch) {
  return call(fetchFn, 'GET', flowPath(id), undefined, storedFlow)
}

/** Stores a new flow; it keeps its ID. */
export function createFlow(flow: Flow, fetchFn: typeof fetch = apiFetch) {
  return call(fetchFn, 'POST', '/api/flows', { flow }, storedFlow)
}

/**
 * Saves a stored flow that was loaded at revision. A 409 means someone else
 * saved in between; error.current then holds their version.
 */
export function saveFlow(
  flow: Flow,
  revision: number,
  fetchFn: typeof fetch = apiFetch,
) {
  return call(fetchFn, 'PUT', flowPath(flow.id), { flow, revision }, storedFlow)
}

/** Deploys the latest saved revision. */
export function startFlow(id: string, fetchFn: typeof fetch = apiFetch) {
  return call(fetchFn, 'POST', `${flowPath(id)}/start`, undefined, storedFlow)
}

/** Stops and undeploys a flow. */
export function stopFlow(id: string, fetchFn: typeof fetch = apiFetch) {
  return call(fetchFn, 'POST', `${flowPath(id)}/stop`, undefined, storedFlow)
}

/** Deletes a flow with its run history. */
export function deleteFlow(id: string, fetchFn: typeof fetch = apiFetch) {
  return call(fetchFn, 'DELETE', flowPath(id), undefined, () => undefined)
}

/** One page of a flow's executions, newest first. */
export function listExecutions(
  flowId: string,
  options: { limit?: number; before?: string } = {},
  fetchFn: typeof fetch = apiFetch,
) {
  const q = new URLSearchParams({ flow: flowId })
  if (options.limit) q.set('limit', String(options.limit))
  if (options.before) q.set('before', options.before)
  return call(
    fetchFn,
    'GET',
    `/api/executions?${q}`,
    undefined,
    (b) => b as ExecutionPage,
  )
}

/** One execution with its trace. */
export function getExecution(id: string, fetchFn: typeof fetch = apiFetch) {
  return call(
    fetchFn,
    'GET',
    `/api/executions/${encodeURIComponent(id)}`,
    undefined,
    (b) => b as ExecutionWithTrace,
  )
}

/** A stored flow without its document. */
export function summaryOf(s: StoredFlow): FlowSummary {
  const {
    id,
    name,
    revision,
    createdAt,
    updatedAt,
    state,
    runningRevision,
    error,
  } = s
  return {
    id,
    name,
    revision,
    createdAt,
    updatedAt,
    state,
    runningRevision,
    error,
  }
}

function flowPath(id: string): string {
  return `/api/flows/${encodeURIComponent(id)}`
}

// storedFlow reads a stored flow from a response, checking its document as
// Open would.
function storedFlow(body: unknown): StoredFlow {
  const b = body as Omit<StoredFlow, 'flow'> & { flow: unknown }
  return { ...b, flow: withEdgeIds(parseFlow(JSON.stringify(b.flow))) }
}

// requestBody writes a request body. The flow goes in canonical form
// (docs/design.md §7.2): the server stores it as sent, so stored flows diff
// as cleanly as saved files.
function requestBody(fields: { flow: Flow; revision?: number }): string {
  const flow = serializeFlow(fields.flow).trimEnd()
  return fields.revision === undefined
    ? `{"flow": ${flow}}`
    : `{"flow": ${flow}, "revision": ${fields.revision}}`
}

// call makes one API request and reads the answer with read. It never
// throws: an unreachable backend, an error status or an unexpected body all
// come back as { ok: false }.
async function call<T>(
  fetchFn: typeof fetch,
  method: string,
  path: string,
  fields: { flow: Flow; revision?: number } | undefined,
  read: (body: unknown) => T,
): Promise<Result<T>> {
  let res: Response
  try {
    res = await fetchFn(path, {
      method,
      headers: fields ? { 'Content-Type': 'application/json' } : {},
      body: fields ? requestBody(fields) : undefined,
    })
  } catch {
    return { ok: false, error: { status: 0, error: UNREACHABLE } }
  }

  if (res.status === 204) {
    return { ok: true, value: read(undefined) }
  }
  let body: unknown
  try {
    body = await res.json()
  } catch {
    // The dev proxy answers with a non-JSON error when nothing listens.
    return { ok: false, error: { status: 0, error: UNREACHABLE } }
  }
  if (!res.ok) {
    const b = (body ?? {}) as {
      error?: unknown
      problems?: string[]
      current?: unknown
    }
    const error: LibraryError = {
      status: res.status,
      error:
        typeof b.error === 'string'
          ? b.error
          : `Unexpected response from the backend (${res.status}).`,
    }
    if (b.problems) error.problems = b.problems
    if (b.current) {
      try {
        error.current = storedFlow(b.current)
      } catch {
        // Leave it out; the caller can still reload.
      }
    }
    return { ok: false, error }
  }
  try {
    return { ok: true, value: read(body) }
  } catch (e) {
    return {
      ok: false,
      error: { status: res.status, error: `Unexpected response: ${String(e)}` },
    }
  }
}
