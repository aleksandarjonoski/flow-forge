import { LitElement, css, html, nothing } from 'lit'
import { customElement, query, state } from 'lit/decorators.js'
import helloText from '../../../backend/examples/hello.flow.json?raw'
import { setNodeConfig, setNodeName } from '../flow/edit.ts'
import {
  FlowParseError,
  defaultFileName,
  parseFlow,
  serializeFlow,
  stampSaved,
} from '../flow/file.ts'
import type { Flow } from '../flow/types.ts'
import './ff-canvas.ts'
import './ff-node-panel.ts'
import type { NodeChange } from './ff-node-panel.ts'

// The editor starts with the hello example until "New flow" exists (slice 7).
const initialFileName = 'hello.flow.json'
const initialFlow = parseFlow(helloText)

/**
 * Root shell of the FlowForge editor: a header with the flow's name and
 * Open / Save, above the graph canvas, with the property panel of the
 * selected node beside it. Owns the Flow being edited, the selection, and the
 * file it came from. See "Opening and saving files" in docs/design.md.
 */
@customElement('ff-app')
export class FfApp extends LitElement {
  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      height: 100%;
    }

    header {
      display: flex;
      align-items: center;
      gap: 1rem;
      padding: 0.5rem 1rem;
      border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    h1 {
      margin: 0;
      font-size: 1.125rem;
    }

    .flow-name {
      flex: 1;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      opacity: 0.8;
    }

    .dirty {
      color: #c80;
    }

    button {
      font: inherit;
      padding: 0.25rem 0.75rem;
      cursor: pointer;
    }

    .message {
      display: flex;
      gap: 1rem;
      align-items: center;
      padding: 0.5rem 1rem;
      background: color-mix(in srgb, #d33 12%, Canvas);
      border-bottom: 1px solid #d33;
    }

    .message span {
      flex: 1;
    }

    .workspace {
      flex: 1;
      display: flex;
      min-height: 0;
    }

    ff-canvas {
      flex: 1;
    }

    ff-node-panel {
      width: 22rem;
      max-width: 50%;
      border-left: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }
  `

  /** The flow being edited. Canvas and panel report every edit as a new Flow. */
  @state()
  private flow: Flow = initialFlow

  /** The flow as last opened or saved; any other value means unsaved changes. */
  @state()
  private savedFlow: Flow = initialFlow

  /** Name of the file the flow came from, reused when saving. */
  @state()
  private fileName?: string = initialFileName

  /** ID of the selected node, if any. */
  @state()
  private selected?: string

  /** Why the last Open failed, until dismissed. */
  @state()
  private message?: string

  @query('input[type=file]')
  private fileInput!: HTMLInputElement

  private get dirty(): boolean {
    return this.flow !== this.savedFlow
  }

  connectedCallback() {
    super.connectedCallback()
    window.addEventListener('keydown', this.onKeydown)
    window.addEventListener('beforeunload', this.onBeforeUnload)
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    window.removeEventListener('keydown', this.onKeydown)
    window.removeEventListener('beforeunload', this.onBeforeUnload)
  }

  render() {
    const node = this.flow.nodes.find((n) => n.id === this.selected)
    return html`
      <header>
        <h1>FlowForge</h1>
        <div class="flow-name" title=${this.fileName ?? ''}>
          ${this.flow.name || this.flow.id}
          ${
            this.dirty
              ? html`<span
                  class="dirty"
                  title="Unsaved changes"
                  aria-label="Unsaved changes"
                  >●</span
                >`
              : nothing
          }
        </div>
        <button @click=${this.open} title="Open a flow file (Ctrl+O)">
          Open…
        </button>
        <button @click=${this.save} title="Download the flow (Ctrl+S)">
          Save
        </button>
        <input
          type="file"
          accept=".json,application/json"
          hidden
          @change=${this.onFileChosen}
        />
      </header>
      ${
        this.message
          ? html`<div class="message" role="alert">
              <span>${this.message}</span>
              <button @click=${() => (this.message = undefined)}>
                Dismiss
              </button>
            </div>`
          : nothing
      }
      <div class="workspace">
        <ff-canvas
          .flow=${this.flow}
          .selected=${node?.id}
          @flow-change=${(e: CustomEvent<Flow>) => (this.flow = e.detail)}
          @node-select=${(e: CustomEvent<string | null>) =>
            (this.selected = e.detail ?? undefined)}
        ></ff-canvas>
        ${
          node
            ? html`<ff-node-panel
                .node=${node}
                @node-change=${(e: CustomEvent<NodeChange>) =>
                  this.applyNodeChange(e.detail)}
                @close=${() => (this.selected = undefined)}
              ></ff-node-panel>`
            : nothing
        }
      </div>
    `
  }

  private applyNodeChange({ id, name, config }: NodeChange) {
    this.flow = setNodeConfig(setNodeName(this.flow, id, name), id, config)
  }

  private open = () => {
    if (this.dirty && !confirm('Discard unsaved changes and open a file?')) {
      return
    }
    this.fileInput.click()
  }

  private onFileChosen = async () => {
    const file = this.fileInput.files?.[0]
    // Reset so choosing the same file again still fires change.
    this.fileInput.value = ''
    if (!file) return
    let flow: Flow
    try {
      flow = parseFlow(await file.text())
    } catch (e) {
      const reason = e instanceof FlowParseError ? e.message : String(e)
      this.message = `Could not open ${file.name}: ${reason}`
      return
    }
    this.flow = flow
    this.savedFlow = flow
    this.fileName = file.name
    this.selected = undefined
    this.message = undefined
  }

  private save = () => {
    // Only a flow with unsaved changes gets a new updatedAt, so saving twice
    // produces the same file.
    const flow = this.dirty ? stampSaved(this.flow, new Date()) : this.flow
    const name = this.fileName ?? defaultFileName(flow)
    download(name, serializeFlow(flow))
    this.flow = flow
    this.savedFlow = flow
    this.fileName = name
  }

  private onKeydown = (e: KeyboardEvent) => {
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return
    const key = e.key.toLowerCase()
    if (key === 's') {
      e.preventDefault()
      this.save()
    } else if (key === 'o') {
      e.preventDefault()
      this.open()
    }
  }

  private onBeforeUnload = (e: BeforeUnloadEvent) => {
    if (this.dirty) e.preventDefault()
  }
}

// download hands text to the browser as a file to save.
function download(name: string, text: string) {
  const url = URL.createObjectURL(
    new Blob([text], { type: 'application/json' }),
  )
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-app': FfApp
  }
}
