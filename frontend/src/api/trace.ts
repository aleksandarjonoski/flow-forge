import type { TraceEvent } from './client.ts'

/** What happened to one node during an execution. */
export interface NodeRun {
  nodeId: string
  nodeType: string
  status: 'trigger' | 'running' | 'completed' | 'failed'
  durationNs?: number
  input?: Record<string, unknown>
  output?: Record<string, unknown>
  error?: string
}

/**
 * Folds a trace into one entry per node, in the order the nodes ran. The
 * trigger comes first, with its emitted payload as output. A node with a
 * start but no end event is still "running".
 */
export function nodeRuns(trace: TraceEvent[]): NodeRun[] {
  const runs: NodeRun[] = []
  const byId = new Map<string, NodeRun>()
  for (const ev of trace) {
    if (ev.kind === 'execution_started' && ev.nodeId) {
      runs.push({
        nodeId: ev.nodeId,
        nodeType: ev.nodeType ?? '',
        status: 'trigger',
        output: ev.input,
      })
    } else if (ev.kind === 'node_started' && ev.nodeId) {
      const run: NodeRun = {
        nodeId: ev.nodeId,
        nodeType: ev.nodeType ?? '',
        status: 'running',
        input: ev.input,
      }
      runs.push(run)
      byId.set(ev.nodeId, run)
    } else if (
      (ev.kind === 'node_completed' || ev.kind === 'node_failed') &&
      ev.nodeId
    ) {
      let run = byId.get(ev.nodeId)
      if (!run) {
        // Cancellation fails a node that never started (engine-v1.md §9).
        run = {
          nodeId: ev.nodeId,
          nodeType: ev.nodeType ?? '',
          status: 'running',
        }
        runs.push(run)
        byId.set(ev.nodeId, run)
      }
      run.status = ev.kind === 'node_completed' ? 'completed' : 'failed'
      run.durationNs = ev.durationNs
      run.output = ev.output
      run.error = ev.error
    }
  }
  return runs
}

/** Total execution time, from the execution's final event. */
export function executionDurationNs(trace: TraceEvent[]): number | undefined {
  const last = trace.at(-1)
  return last?.kind === 'execution_completed' ||
    last?.kind === 'execution_failed'
    ? last.durationNs
    : undefined
}

/** Formats nanoseconds for people: "0.17 ms", "12.3 ms", "1.20 s". */
export function formatDuration(ns: number | undefined): string {
  if (ns === undefined) return ''
  const ms = ns / 1e6
  if (ms < 1) return `${ms.toFixed(2)} ms`
  if (ms < 1000) return `${ms.toFixed(1)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

/** How a node is marked on the canvas after (or during) a run. */
export type RunMark = 'running' | 'done' | 'failed' | 'skipped'

/**
 * Marks every node of a flow for the canvas: running, done (including the
 * trigger once it has emitted) or failed per the trace so far. Once the run
 * has finished, nodes it did not reach are "skipped"; while it is still
 * going they are left unmarked.
 */
export function runMarks(
  trace: TraceEvent[],
  nodeIds: string[],
  finished: boolean,
): Map<string, RunMark> {
  const marks = new Map<string, RunMark>()
  for (const run of nodeRuns(trace)) {
    marks.set(
      run.nodeId,
      run.status === 'trigger' || run.status === 'completed'
        ? 'done'
        : run.status,
    )
  }
  if (finished) {
    for (const id of nodeIds) if (!marks.has(id)) marks.set(id, 'skipped')
  }
  return marks
}
