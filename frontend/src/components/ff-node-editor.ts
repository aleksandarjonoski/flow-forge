import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import type { DataView, SourceTree } from '../flow/data-view.ts'
import { activeMode, nodeType } from '../flow/node-types.ts'
import type { FlowNode } from '../flow/types.ts'
import './ff-data-view.ts'
import './ff-node-panel.ts'

type Tab = 'general' | 'input' | 'output' | 'mapping'

/**
 * The node editor: a wide overlay with General / Input / Output tabs, plus
 * Mapping for a mapping-mode transform (docs/data-v1.md §7.1). It shows the
 * same node as the side panel and reports edits with the same events
 * (node-change, node-delete, node-select, request-run), which bubble out of
 * the panel views inside it.
 *
 * @fires close - the user closed the editor (×, Esc or the backdrop)
 */
@customElement('ff-node-editor')
export class FfNodeEditor extends LitElement {
  static styles = css`
    :host {
      position: fixed;
      inset: 0;
      z-index: 10;
      display: grid;
      place-items: center;
      background: rgb(0 0 0 / 0.35);
    }

    .dialog {
      width: min(76rem, 94vw);
      height: min(52rem, 88vh);
      display: flex;
      flex-direction: column;
      border-radius: 8px;
      background: Canvas;
      color: CanvasText;
      box-shadow: 0 1rem 3rem rgb(0 0 0 / 0.3);
      font-size: 0.875rem;
    }

    header {
      display: flex;
      align-items: baseline;
      gap: 0.75rem;
      padding: 0.875rem 1.25rem 0;
    }

    h2 {
      margin: 0;
      font-size: 1.125rem;
    }

    .type {
      opacity: 0.7;
      flex: 1;
    }

    .close {
      border: none;
      background: none;
      font: inherit;
      font-size: 1.375rem;
      line-height: 1;
      color: inherit;
      cursor: pointer;
    }

    [role='tablist'] {
      display: flex;
      gap: 0.25rem;
      padding: 0.75rem 1.25rem 0;
      border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    [role='tab'] {
      font: inherit;
      padding: 0.375rem 0.875rem;
      border: 1px solid transparent;
      border-bottom: none;
      border-radius: 6px 6px 0 0;
      background: none;
      color: inherit;
      cursor: pointer;
      margin-bottom: -1px;
    }

    [role='tab'][aria-selected='true'] {
      border-color: color-mix(in srgb, currentColor 20%, transparent);
      background: Canvas;
      font-weight: 600;
    }

    [role='tabpanel'] {
      flex: 1;
      overflow: auto;
      padding: 1.25rem;
    }
  `

  @property({ attribute: false })
  node?: FlowNode

  /** What the node receives. */
  @property({ attribute: false })
  dataView?: DataView

  /** What the node outputs. */
  @property({ attribute: false })
  outputData?: DataView

  /** Data the node can read, for the visual mapper. */
  @property({ attribute: false })
  sources: SourceTree[] = []

  @property({ type: Boolean })
  canRun = false

  @state()
  private tab: Tab = 'general'

  willUpdate(changed: PropertyValues<this>) {
    if (changed.has('node') && changed.get('node')?.id !== this.node?.id) {
      this.tab = 'general'
    }
    // The Mapping tab disappears when the transform leaves mapping mode.
    if (this.tab === 'mapping' && !this.tabs().includes('mapping')) {
      this.tab = 'general'
    }
  }

  connectedCallback() {
    super.connectedCallback()
    window.addEventListener('keydown', this.onKeydown)
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    window.removeEventListener('keydown', this.onKeydown)
  }

  render() {
    const node = this.node
    if (!node) return nothing
    const spec = nodeType(node.type)
    const tabs = this.tabs()
    const labels: Record<Tab, string> = {
      general: 'General',
      input: 'Input',
      output: 'Output',
      mapping: 'Mapping',
    }
    return html`
      <div
        class="dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Edit ${node.name || node.id}"
      >
        <header>
          <h2>${node.name || spec?.label || node.type}</h2>
          <span class="type">${spec?.label ?? node.type} · ${node.id}</span>
          <button class="close" aria-label="Close editor" @click=${this.close}>
            ×
          </button>
        </header>
        <div role="tablist">
          ${tabs.map(
            (t) =>
              html`<button
                role="tab"
                aria-selected=${t === this.tab ? 'true' : 'false'}
                @click=${() => (this.tab = t)}
              >
                ${labels[t]}
              </button>`,
          )}
        </div>
        <div role="tabpanel" aria-label=${labels[this.tab]}>
          ${this.renderTab(node)}
        </div>
      </div>
    `
  }

  private renderTab(node: FlowNode) {
    if (this.tab === 'input') {
      return html`<ff-data-view
        .view=${this.dataView}
        ?canRun=${this.canRun}
      ></ff-data-view>`
    }
    return html`<ff-node-panel
      view=${this.tab}
      .node=${node}
      .dataView=${this.dataView}
      .outputData=${this.outputData}
      .sources=${this.sources}
      ?canRun=${this.canRun}
    ></ff-node-panel>`
  }

  // tabs lists the tabs for this node: Mapping only for a mapping-mode
  // transform (a node whose active mode has a mapping field).
  private tabs(): Tab[] {
    const node = this.node
    const spec = node && nodeType(node.type)
    const mode = spec && activeMode(spec, node.config)
    const hasMapping = mode?.keys.some(
      (k) => spec?.config.properties[k]?.type === 'mapping',
    )
    return hasMapping
      ? ['general', 'input', 'output', 'mapping']
      : ['general', 'input', 'output']
  }

  private close = () => {
    this.dispatchEvent(new Event('close'))
  }

  private onKeydown = (e: KeyboardEvent) => {
    // A dialog inside (the browser's confirm) handles its own Esc.
    if (e.key === 'Escape') this.close()
  }

  // Clicking the backdrop (outside the dialog) closes the editor.
  firstUpdated() {
    this.addEventListener('click', (e) => {
      if (e.composedPath()[0] === this) this.close()
    })
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-node-editor': FfNodeEditor
  }
}
