import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { listExecutions, type Execution } from '../api/library.ts'
import { formatDuration } from '../api/trace.ts'

/** How often the list is refreshed while it is shown. */
const REFRESH_MS = 5000
const PAGE_SIZE = 50

/**
 * Run history of a stored flow: its recorded executions, newest first,
 * refreshed every few seconds while shown. Click one to play it back. See
 * docs/server-v1.md §7.3.
 *
 * @fires execution-open - detail is the ID of the execution to open
 */
@customElement('ff-history')
export class FfHistory extends LitElement {
  static styles = css`
    :host {
      display: block;
      font-size: 0.875rem;
    }

    table {
      width: 100%;
      border-collapse: collapse;
    }

    th {
      text-align: left;
      font-weight: 600;
      font-size: 0.75rem;
      opacity: 0.7;
      padding: 0.25rem 0.5rem;
    }

    td {
      padding: 0.25rem 0.5rem;
      border-top: 1px solid color-mix(in srgb, currentColor 12%, transparent);
      white-space: nowrap;
    }

    tbody tr {
      cursor: pointer;
    }

    tbody tr:hover,
    tbody tr:focus-visible {
      background: color-mix(in srgb, currentColor 6%, transparent);
    }

    tr[aria-selected='true'] {
      background: color-mix(in srgb, #36c 12%, transparent);
    }

    .mono {
      font-family: ui-monospace, monospace;
    }

    .muted {
      opacity: 0.7;
    }

    .completed {
      color: #2a7;
    }

    .failed,
    .interrupted,
    .error {
      color: #d33;
    }

    .running {
      color: #36c;
    }

    .err {
      max-width: 30rem;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    button {
      font: inherit;
      margin-top: 0.5rem;
    }
  `

  /** The stored flow whose executions to list. */
  @property()
  flowId?: string

  /** The execution shown in the results panel, if any. */
  @property()
  selected?: string

  @state()
  private executions?: Execution[]

  @state()
  private next?: string

  @state()
  private error?: string

  private timer?: ReturnType<typeof setInterval>
  // Only the newest load may update the list.
  private loadSeq = 0

  connectedCallback() {
    super.connectedCallback()
    this.timer = setInterval(() => {
      if (!document.hidden) void this.refresh()
    }, REFRESH_MS)
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    clearInterval(this.timer)
  }

  willUpdate(changed: PropertyValues<this>) {
    if (changed.has('flowId')) {
      this.executions = undefined
      this.next = undefined
      this.error = undefined
      void this.refresh()
    }
  }

  /**
   * Reloads the newest page. Older pages already loaded with "Load more" are
   * kept below it.
   */
  async refresh() {
    const flowId = this.flowId
    if (!flowId) return
    const seq = ++this.loadSeq
    const r = await listExecutions(flowId, { limit: PAGE_SIZE })
    if (seq !== this.loadSeq || flowId !== this.flowId) return
    if (!r.ok) {
      this.error = r.error.error
      return
    }
    this.error = undefined
    const fresh = r.value.executions
    const older = (this.executions ?? []).filter(
      (e) =>
        !fresh.some((f) => f.id === e.id) &&
        e.startedAt <= (fresh.at(-1)?.startedAt ?? ''),
    )
    this.executions = [...fresh, ...older]
    if (older.length === 0) this.next = r.value.next
  }

  private async loadMore() {
    const flowId = this.flowId
    if (!flowId || !this.next) return
    const seq = ++this.loadSeq
    const r = await listExecutions(flowId, {
      limit: PAGE_SIZE,
      before: this.next,
    })
    if (seq !== this.loadSeq || flowId !== this.flowId) return
    if (!r.ok) {
      this.error = r.error.error
      return
    }
    this.executions = [...(this.executions ?? []), ...r.value.executions]
    this.next = r.value.next
  }

  render() {
    if (this.error && !this.executions) {
      return html`<p class="error" role="alert">${this.error}</p>`
    }
    if (!this.executions) return html`<p class="muted">Loading…</p>`
    if (this.executions.length === 0) {
      return html`<p class="muted">
        No runs yet. Runs of the deployed flow appear here; test runs do not.
      </p>`
    }
    return html`
      ${this.error ? html`<p class="error" role="alert">${this.error}</p>` : nothing}
      <table>
        <thead>
          <tr>
            <th>Started</th>
            <th>Status</th>
            <th>Duration</th>
            <th>Trigger</th>
            <th>Revision</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          ${this.executions.map((e) => this.renderRow(e))}
        </tbody>
      </table>
      ${
        this.next
          ? html`<button @click=${() => void this.loadMore()}>
              Load more
            </button>`
          : nothing
      }
    `
  }

  private renderRow(e: Execution) {
    const open = () =>
      this.dispatchEvent(
        new CustomEvent('execution-open', {
          detail: e.id,
          bubbles: true,
          composed: true,
        }),
      )
    return html`<tr
      tabindex="0"
      aria-selected=${e.id === this.selected ? 'true' : 'false'}
      @click=${open}
      @keydown=${(ev: KeyboardEvent) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault()
          open()
        }
      }}
    >
      <td title=${e.startedAt}>${formatStarted(e.startedAt)}</td>
      <td class=${e.status}>
        ${e.status}${
          e.traceComplete
            ? nothing
            : html` <span class="muted" title="Some trace events were dropped"
                >(trace incomplete)</span
              >`
        }
      </td>
      <td class="mono">
        ${e.finishedAt ? formatElapsed(e.startedAt, e.finishedAt) : '—'}
      </td>
      <td class="mono">${e.trigger}</td>
      <td class="mono">${e.revision}</td>
      <td class="err" title=${e.error ?? ''}>${e.error ?? ''}</td>
    </tr>`
  }
}

// formatStarted shows a start time in local time: the time of day for
// today, the date and time otherwise.
function formatStarted(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const time = d.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  return d.toDateString() === now.toDateString()
    ? time
    : `${d.toLocaleDateString()} ${time}`
}

// formatElapsed shows how long an execution took. Stored times have
// millisecond precision, so anything shorter shows as "< 1 ms".
function formatElapsed(start: string, end: string): string {
  const ms = Date.parse(end) - Date.parse(start)
  return ms < 1 ? '< 1 ms' : formatDuration(ms * 1e6)
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-history': FfHistory
  }
}
