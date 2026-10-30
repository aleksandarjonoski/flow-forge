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
