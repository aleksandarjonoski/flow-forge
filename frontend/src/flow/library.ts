import type { RunOutcome } from '../api/client.ts'
import type { ExecutionWithTrace, FlowSummary } from '../api/library.ts'

// Rules of the editor's flow library and run history (docs/server-v1.md
// §7), kept out of the components so they can be tested.

/** Whether a running flow runs an older revision than the one saved. */
export function notDeployed(
  f: Pick<FlowSummary, 'state' | 'revision' | 'runningRevision'>,
): boolean {
  return f.state === 'running' && f.runningRevision < f.revision
}

/**
 * The ID for a copy of a flow: `<id>-copy`, then `<id>-copy-2`, … until one
 * is not taken.
 */
export function copyId(id: string, taken: Iterable<string>): string {
  const used = new Set(taken)
  const base = `${id}-copy`
  if (!used.has(base)) return base
  for (let n = 2; ; n++) {
    if (!used.has(`${base}-${n}`)) return `${base}-${n}`
  }
}

/** The flow ID in a location hash `#/flows/<id>`, if any. */
export function flowIdFromHash(hash: string): string | undefined {
  const m = /^#\/flows\/(.+)$/.exec(hash)
  if (!m) return undefined
  try {
    return decodeURIComponent(m[1])
  } catch {
    return undefined
  }
}

/** The location hash for a stored flow. */
export function hashForFlow(id: string): string {
  return `#/flows/${encodeURIComponent(id)}`
}

/**
 * A recorded execution as a run outcome, so the results panel and the canvas
 * show and replay it like a test run (§7.3). An interrupted execution counts
 * as failed, with a reason saying so.
 */
export function executionOutcome(e: ExecutionWithTrace): RunOutcome {
  const failed = e.status === 'failed' || e.status === 'interrupted'
  return {
    ok: true,
    result: {
      executionId: e.id,
      status: failed ? 'failed' : 'completed',
      error:
        e.status === 'interrupted'
          ? 'interrupted: the server stopped before this execution finished'
          : e.error,
      trace: e.trace,
    },
  }
}

/**
 * Notes to show above a recorded execution: when it ran another revision
 * than the one on the canvas, or lost trace events.
 */
export function executionNotes(
  e: Pick<ExecutionWithTrace, 'revision' | 'traceComplete'>,
  canvas: { revision?: number; dirty: boolean },
): string[] {
  const notes: string[] = []
  if (canvas.revision !== undefined && e.revision !== canvas.revision) {
    notes.push(
      `This run used revision ${e.revision}; the canvas shows revision ${canvas.revision}, so it may differ from what ran.`,
    )
  } else if (canvas.dirty) {
    notes.push(
      'The canvas has unsaved changes, so it may differ from what ran.',
    )
  }
  if (!e.traceComplete) {
    notes.push(
      'Trace incomplete: some node events were dropped because the server could not record them fast enough.',
    )
  }
  return notes
}
