import { LitElement, css, html, nothing } from 'lit'
import { customElement, property, query, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import { nodeType } from '../flow/node-types.ts'
import type { FlowNode } from '../flow/types.ts'

/** Detail of a run-request event. */
export interface RunRequest {
  trigger: string
  payload: Record<string, unknown>
}

/**
 * Dialog for starting a test run: pick the trigger and edit, as JSON, the
 * payload it emits. Payloads start from the trigger type's sample and are
 * remembered per trigger while the page is open. See "Test runs" in
 * docs/design.md.
 *
 * @fires run-request - detail is a RunRequest
 */
@customElement('ff-run-dialog')
export class FfRunDialog extends LitElement {
  static styles = css`
    dialog {
      width: min(36rem, 90vw);
      padding: 1.25rem;
      border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
      border-radius: 8px;
      background: Canvas;
      color: CanvasText;
      font-size: 0.875rem;
    }

    dialog::backdrop {
      background: rgb(0 0 0 / 0.3);
    }

    h2 {
      margin: 0 0 1rem;
      font-size: 1rem;
    }

    .field {
      display: grid;
      gap: 0.25rem;
      margin-bottom: 1rem;
    }

    label {
      font-weight: 600;
    }

    select,
    textarea,
    button {
      font: inherit;
    }

    select,
    textarea {
      padding: 0.375rem 0.5rem;
      border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
      border-radius: 4px;
      background: Canvas;
      color: CanvasText;
    }

    textarea {
      font-family: ui-monospace, monospace;
      min-height: 12rem;
      resize: vertical;
    }

    .hint {
      opacity: 0.7;
    }

    .error {
      color: #d33;
    }

    .actions {
      display: flex;
      justify-content: flex-end;
      gap: 0.5rem;
    }

    button {
      padding: 0.375rem 1rem;
      cursor: pointer;
    }
  `

  /** The flow's trigger nodes; one of them is run. */
  @property({ attribute: false })
  triggers: FlowNode[] = []

  @state()
  private trigger?: string

  @state()
  private error?: string

  // Payload text per trigger ID, kept across openings of the dialog.
  @state()
  private drafts = new Map<string, string>()

  @query('dialog')
  private dialog!: HTMLDialogElement

  /** Opens the dialog. */
  show() {
    if (!this.triggers.some((t) => t.id === this.trigger)) {
      this.trigger = this.triggers[0]?.id
    }
    this.error = undefined
    this.dialog.showModal()
  }

  render() {
    const trigger = this.triggers.find((t) => t.id === this.trigger)
    return html`
      <dialog @close=${() => (this.error = undefined)}>
        <form method="dialog" @submit=${this.onSubmit}>
          <h2>Test run</h2>
          <div class="field">
            <label for="trigger">Trigger</label>
            <select
              id="trigger"
              .value=${live(this.trigger ?? '')}
              @change=${(e: Event) => {
                this.trigger = (e.target as HTMLSelectElement).value
                this.error = undefined
              }}
            >
              ${this.triggers.map(
                (t) =>
                  html`<option value=${t.id}>
                    ${t.name || t.type} (${t.id})
                  </option>`,
              )}
            </select>
          </div>
          <div class="field">
            <label for="payload">Payload</label>
            <textarea
              id="payload"
              spellcheck="false"
              aria-invalid=${this.error ? 'true' : 'false'}
              aria-describedby=${this.error ? 'payload-error' : nothing}
              .value=${live(trigger ? this.draft(trigger) : '')}
              @input=${(e: Event) => {
                if (!trigger) return
                this.drafts = new Map(this.drafts).set(
                  trigger.id,
                  (e.target as HTMLTextAreaElement).value,
                )
                this.error = undefined
              }}
            ></textarea>
            <div class="hint">
              What the trigger emits, as JSON. The nodes after it receive this
              as their input. The flow runs once; nothing is left running.
            </div>
            ${
              this.error
                ? html`<div class="error" id="payload-error">
                    ${this.error}
                  </div>`
                : nothing
            }
          </div>
          <div class="actions">
            <button type="button" @click=${() => this.dialog.close()}>
              Cancel
            </button>
            <button type="submit">Run</button>
          </div>
        </form>
      </dialog>
    `
  }

  private draft(trigger: FlowNode): string {
    const saved = this.drafts.get(trigger.id)
    if (saved !== undefined) return saved
    const sample = nodeType(trigger.type)?.samplePayload?.() ?? {}
    return JSON.stringify(sample, null, 2)
  }

  private onSubmit = (e: SubmitEvent) => {
    const trigger = this.triggers.find((t) => t.id === this.trigger)
    if (!trigger) return
    let payload: unknown
    try {
      payload = JSON.parse(this.draft(trigger))
    } catch {
      payload = undefined
    }
    if (
      typeof payload !== 'object' ||
      payload === null ||
      Array.isArray(payload)
    ) {
      // Keep the dialog open to fix the payload.
      e.preventDefault()
      this.error = 'The payload must be a JSON object.'
      return
    }
    const detail: RunRequest = {
      trigger: trigger.id,
      payload: payload as Record<string, unknown>,
    }
    this.dispatchEvent(
      new CustomEvent('run-request', { detail, bubbles: true, composed: true }),
    )
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-run-dialog': FfRunDialog
  }
  interface HTMLElementEventMap {
    'run-request': CustomEvent<RunRequest>
  }
}
