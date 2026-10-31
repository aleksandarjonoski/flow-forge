import { LitElement, css, html, nothing } from 'lit'
import { customElement, property } from 'lit/decorators.js'
import type { RunOutcome, TraceEvent } from '../api/client.ts'
import { SPEEDS, type Speed } from '../api/player.ts'
import {
  executionDurationNs,
  formatDuration,
  nodeRuns,
  type NodeRun,
} from '../api/trace.ts'
import type { Flow } from '../flow/types.ts'

/**
 * What the panel shows: a run in progress with the trace so far, or how it
 * ended.
 */
export type RunState =
  | { phase: 'running'; trace: TraceEvent[]; replay?: boolean }
  | { phase: 'done'; outcome: RunOutcome }

/**
 * Results of a test run: overall status, then one row per node that ran with
 * its duration and output (or error). Rows appear while the run streams in.
 * See "Test runs" and "Live execution view" in docs/design.md.
 *
 * @fires close - the user closed the panel
 * @fires node-select - detail is the ID of the node whose row was clicked
 * @fires speed-change - detail is the chosen playback Speed
 * @fires replay - the user asked to play the last run again
 */
@customElement('ff-run-panel')
export class FfRunPanel extends LitElement {
  static styles = css`
    :host {
      display: block;
      overflow: auto;
      padding: 0.75rem 1rem;
      font-size: 0.875rem;
    }

    header {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      margin-bottom: 0.5rem;
    }

    h2 {
      margin: 0;
      font-size: 0.9375rem;
    }

    .summary {
      flex: 1;
    }

    .ok {
      color: #2a7;
    }

    .bad {
      color: #d33;
    }

    .muted {
      opacity: 0.7;
    }

    .controls {
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .controls select,
    .controls button {
      font: inherit;
      font-size: 0.8125rem;
    }

    .close {
      border: none;
      background: none;
      font: inherit;
      font-size: 1.25rem;
      line-height: 1;
      color: inherit;
      cursor: pointer;
    }

    ol {
      list-style: none;
      margin: 0;
      padding: 0;
      display: grid;
      gap: 0.25rem;
    }

    li {
      border: 1px solid color-mix(in srgb, currentColor 15%, transparent);
      border-radius: 6px;
      padding: 0.375rem 0.625rem;
    }

    summary {
      display: flex;
      gap: 0.75rem;
      align-items: baseline;
      cursor: pointer;
    }

    .name {
      font-weight: 600;
    }

    .type,
    .duration {
      font-family: ui-monospace, monospace;
      opacity: 0.7;
    }

    .duration {
      margin-left: auto;
    }

    pre {
      margin: 0.375rem 0 0;
      padding: 0.5rem;
      border-radius: 4px;
      background: color-mix(in srgb, currentColor 6%, transparent);
      overflow: auto;
      max-height: 16rem;
      font-size: 0.8125rem;
    }

    ul {
      margin: 0.25rem 0 0;
      padding-left: 1.25rem;
    }
  `

  /** The flow that was run, for node names. */
  @property({ attribute: false })
  flow?: Flow

  /** The run to show. */
  @property({ attribute: false })
  run?: RunState

  /** The playback speed (docs/design.md, "Live execution view"). */
  @property({ attribute: false })
  speed: Speed = 'normal'

  render() {
    const state = this.run
    if (!state) return nothing
    return html`
      <header>
        <h2>Test run</h2>
        <div class="summary" role="status">${this.renderSummary(state)}</div>
        <div class="controls">
          <label for="speed">Playback</label>
          <select
            id="speed"
            title="Slows down only the display; durations are real"
            .value=${this.speed}
            @change=${(e: Event) =>
              this.dispatchEvent(
                new CustomEvent('speed-change', {
                  detail: (e.target as HTMLSelectElement).value as Speed,
                }),
              )}
          >
            ${SPEEDS.map(
              (s) =>
                html`<option
                  value=${s.value}
                  ?selected=${s.value === this.speed}
                >
                  ${s.label}
                </option>`,
            )}
          </select>
          ${
            state.phase === 'done' && state.outcome.ok
              ? html`<button
                  title="Play this run again without running the flow"
                  @click=${() => this.dispatchEvent(new Event('replay'))}
                >
                  Replay
                </button>`
              : nothing
          }
        </div>
        <button
          class="close"
          aria-label="Close results"
          @click=${() => this.dispatchEvent(new Event('close'))}
        >
          ×
        </button>
      </header>
      ${
        state.phase === 'done'
          ? this.renderBody(state.outcome)
          : this.renderRuns(state.trace)
      }
    `
  }

  private renderSummary(state: RunState) {
    if (state.phase === 'running') {
      return state.replay ? html`Replaying…` : html`Running…`
    }
    const { outcome } = state
    if (!outcome.ok) {
      return html`<span class="bad"
        >Could not run: ${outcome.error.error}</span
      >`
    }
    const { result } = outcome
    const took = formatDuration(executionDurationNs(result.trace))
    return result.status === 'completed'
      ? html`<span class="ok">Completed</span>
          <span class="muted">in ${took}</span>`
      : html`<span class="bad">Failed: ${result.error}</span>`
  }

  private renderBody(outcome: RunOutcome) {
    if (!outcome.ok) {
      const problems = outcome.error.problems ?? []
      return problems.length
        ? html`<ul class="bad">
            ${problems.map((p) => html`<li>${p}</li>`)}
          </ul>`
        : nothing
    }
    return html`
      ${this.renderRuns(outcome.result.trace)}
      <div class="muted">Execution ${outcome.result.executionId}</div>
    `
  }

  private renderRuns(trace: TraceEvent[]) {
    return html`<ol>
      ${nodeRuns(trace).map((run) => this.renderRun(run))}
    </ol>`
  }

  private renderRun(run: NodeRun) {
    const node = this.flow?.nodes.find((n) => n.id === run.nodeId)
    const running = this.run?.phase === 'running'
    const label = {
      trigger: html`<span class="muted">emitted</span>`,
      running: html`<span class="muted"
        >${running ? 'running…' : 'did not finish'}</span
      >`,
      completed: html`<span class="ok">✓</span>`,
      failed: html`<span class="bad">✗ failed</span>`,
    }[run.status]
    return html`
      <li data-node=${run.nodeId}>
        <details ?open=${run.status === 'failed'}>
          <summary
            @click=${() =>
              this.dispatchEvent(
                new CustomEvent('node-select', {
                  detail: run.nodeId,
                  bubbles: true,
                  composed: true,
                }),
              )}
          >
            ${label}
            <span class="name">${node?.name || run.nodeId}</span>
            <span class="type">${run.nodeType}</span>
            <span class="duration">${formatDuration(run.durationNs)}</span>
          </summary>
          ${run.error ? html`<pre class="bad">${run.error}</pre>` : nothing}
          ${
            run.output
              ? html`<pre>${JSON.stringify(run.output, null, 2)}</pre>`
              : nothing
          }
        </details>
      </li>
    `
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-run-panel': FfRunPanel
  }
}
