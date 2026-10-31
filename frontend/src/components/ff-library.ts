import { LitElement, css, html, nothing } from 'lit'
import { customElement, property } from 'lit/decorators.js'
import type { FlowSummary } from '../api/library.ts'
import { notDeployed } from '../flow/library.ts'

/**
 * The flow library: every flow stored on the server, with its state. Click a
 * flow to open it. See docs/server-v1.md §7.1.
 *
 * @fires flow-open - detail is the ID of the flow to open
 * @fires flow-delete - detail is the ID of the flow to delete
 */
@customElement('ff-library')
export class FfLibrary extends LitElement {
  static styles = css`
    :host {
      display: block;
      padding: 0.75rem;
      overflow: auto;
      font-size: 0.875rem;
    }

    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: grid;
      gap: 0.375rem;
    }

    li {
      display: flex;
      align-items: stretch;
      border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
      border-radius: 6px;
    }

    li[aria-current='true'] {
      border-color: color-mix(in srgb, #36c 70%, transparent);
      background: color-mix(in srgb, #36c 8%, Canvas);
    }

    .open {
      flex: 1;
      min-width: 0;
      text-align: left;
      font: inherit;
      padding: 0.5rem 0.625rem;
      border: none;
      background: none;
      color: inherit;
      cursor: pointer;
    }

    .name {
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .meta {
      display: flex;
      flex-wrap: wrap;
      gap: 0.375rem;
      align-items: center;
      margin-top: 0.125rem;
      font-size: 0.75rem;
    }

    .id {
      font-family: ui-monospace, monospace;
      opacity: 0.7;
    }

    .delete {
      border: none;
      background: none;
      font: inherit;
      font-size: 1.125rem;
      color: inherit;
      opacity: 0.5;
      cursor: pointer;
      padding: 0 0.5rem;
    }

    .delete:hover,
    .delete:focus-visible {
      opacity: 1;
      color: #d33;
    }

    .hint {
      opacity: 0.7;
    }

    .error {
      color: #d33;
    }
  `

  /** The stored flows; undefined while loading. */
  @property({ attribute: false })
  flows?: FlowSummary[]

  /** Why the library could not be loaded, e.g. the backend is down. */
  @property()
  error?: string

  /** ID of the flow open in the editor, if it is stored. */
  @property()
  current?: string

  render() {
    if (this.error) {
      return html`<p class="error" role="alert">${this.error}</p>
        <p class="hint">Files can still be opened and downloaded.</p>`
    }
    if (!this.flows) return html`<p class="hint">Loading…</p>`
    if (this.flows.length === 0) {
      return html`<p class="hint">
        No stored flows yet. Save a flow to add it here.
      </p>`
    }
    return html`<ul>
      ${this.flows.map((f) => this.renderFlow(f))}
    </ul>`
  }

  private renderFlow(f: FlowSummary) {
    return html`<li aria-current=${f.id === this.current ? 'true' : 'false'}>
      <button
        class="open"
        title=${`Open ${f.name || f.id}`}
        @click=${() => this.fire('flow-open', f.id)}
      >
        <div class="name">${f.name || f.id}</div>
        <div class="meta">
          <ff-state-badge .flow=${f}></ff-state-badge>
          ${
            notDeployed(f)
              ? html`<span title="Saved changes are not deployed yet"
                  >changes not deployed</span
                >`
              : nothing
          }
          <span class="id">${f.id}</span>
        </div>
      </button>
      <button
        class="delete"
        aria-label=${`Delete ${f.name || f.id}`}
        title="Delete this flow and its run history"
        @click=${() => this.fire('flow-delete', f.id)}
      >
        ×
      </button>
    </li>`
  }

  private fire(name: string, id: string) {
    this.dispatchEvent(
      new CustomEvent(name, { detail: id, bubbles: true, composed: true }),
    )
  }
}

/**
 * A flow's state as a small coloured badge: running, stopped, or failed
 * (with the reason as a tooltip).
 */
@customElement('ff-state-badge')
export class FfStateBadge extends LitElement {
  static styles = css`
    span {
      display: inline-block;
      padding: 0 0.375rem;
      border-radius: 999px;
      font-size: 0.75rem;
      line-height: 1.4;
      border: 1px solid currentColor;
    }

    .running {
      color: #2a7;
    }

    .stopped {
      opacity: 0.7;
    }

    .failed {
      color: #d33;
    }
  `

  @property({ attribute: false })
  flow?: Pick<FlowSummary, 'state' | 'error' | 'runningRevision'>

  render() {
    const f = this.flow
    if (!f) return nothing
    const title = {
      running: `Deployed and running (revision ${f.runningRevision})`,
      stopped: 'Not deployed',
      failed: `Deployed, but it could not start: ${f.error}`,
    }[f.state]
    return html`<span class=${f.state} title=${title}>${f.state}</span>`
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-library': FfLibrary
    'ff-state-badge': FfStateBadge
  }
}
