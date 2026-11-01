import { LitElement, css, html } from 'lit'
import { customElement, query, state } from 'lit/decorators.js'
import { UNREACHABLE } from '../api/client.ts'
import { checkToken, tokens } from '../api/auth.ts'

/**
 * Dialog asking for the API token when the server refuses a call without
 * it (docs/prod-v1.md §3.4). The token is checked with the server before it
 * is kept.
 *
 * @fires signed-in - the token was accepted and is kept
 */
@customElement('ff-sign-in')
export class FfSignIn extends LitElement {
  static styles = css`
    dialog {
      width: min(28rem, 90vw);
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
      margin: 0 0 0.5rem;
      font-size: 1rem;
    }

    p {
      margin: 0 0 1rem;
    }

    .field {
      display: grid;
      gap: 0.25rem;
      margin-bottom: 1rem;
    }

    label {
      font-weight: 600;
    }

    .remember {
      display: flex;
      align-items: center;
      gap: 0.375rem;
      margin-bottom: 1rem;
      font-weight: normal;
    }

    input,
    button {
      font: inherit;
    }

    input[type='password'] {
      padding: 0.375rem 0.5rem;
      border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
      border-radius: 4px;
      background: Canvas;
      color: CanvasText;
      font-family: ui-monospace, monospace;
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

  @state()
  private error?: string

  @state()
  private checking = false

  @query('dialog')
  private dialog!: HTMLDialogElement

  @query('#token')
  private input!: HTMLInputElement

  @query('#remember')
  private remember!: HTMLInputElement

  /** Whether the dialog is showing. */
  get open(): boolean {
    return this.dialog?.open ?? false
  }

  /** Opens the dialog, unless it is open already. */
  show() {
    if (this.open) return
    this.error = undefined
    this.input.value = ''
    this.dialog.showModal()
  }

  render() {
    return html`
      <dialog>
        <form @submit=${this.onSubmit}>
          <h2>Sign in</h2>
          <p>This FlowForge server needs its API token.</p>
          <div class="field">
            <label for="token">API token</label>
            <input
              id="token"
              type="password"
              autocomplete="current-password"
              spellcheck="false"
              required
              aria-invalid=${this.error ? 'true' : 'false'}
              aria-describedby=${this.error ? 'token-error' : 'token-hint'}
              @input=${() => (this.error = undefined)}
            />
            ${
              this.error
                ? html`<div class="error" id="token-error">${this.error}</div>`
                : html`<div class="hint" id="token-hint">
                    The server's FLOWFORGE_API_TOKEN. Ask whoever runs it.
                  </div>`
            }
          </div>
          <label class="remember">
            <input id="remember" type="checkbox" />
            Remember on this computer
          </label>
          <div class="actions">
            <button type="button" @click=${() => this.dialog.close()}>
              Cancel
            </button>
            <button type="submit" ?disabled=${this.checking}>
              ${this.checking ? 'Checking…' : 'Sign in'}
            </button>
          </div>
        </form>
      </dialog>
    `
  }

  private onSubmit = async (e: SubmitEvent) => {
    e.preventDefault()
    const token = this.input.value.trim()
    if (!token || this.checking) return
    this.checking = true
    const check = await checkToken(token)
    this.checking = false
    if (check === 'rejected') {
      this.error = 'Token not accepted.'
      return
    }
    if (check === 'unreachable') {
      this.error = UNREACHABLE
      return
    }
    tokens.set(token, this.remember.checked)
    this.dialog.close()
    this.dispatchEvent(
      new CustomEvent('signed-in', { bubbles: true, composed: true }),
    )
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-sign-in': FfSignIn
  }
  interface HTMLElementEventMap {
    'signed-in': CustomEvent<void>
  }
}
