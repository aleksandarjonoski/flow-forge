import { LitElement, css, html, nothing } from 'lit'
import { customElement, state } from 'lit/decorators.js'
import hello from '../../../backend/examples/hello.flow.json'
import { setNodeConfig, setNodeName } from '../flow/edit.ts'
import type { Flow } from '../flow/types.ts'
import './ff-canvas.ts'
import './ff-node-panel.ts'
import type { NodeChange } from './ff-node-panel.ts'

// Shown until flows can be opened (Phase 2 slice 6).
const initialFlow: Flow = hello

/**
 * Root shell of the FlowForge editor: a header above the graph canvas, with
 * the property panel of the selected node beside it. Owns the Flow being
 * edited and the selection.
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
      padding: 0.75rem 1rem;
      border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    h1 {
      margin: 0;
      font-size: 1.125rem;
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

  /** ID of the selected node, if any. */
  @state()
  private selected?: string

  render() {
    const node = this.flow.nodes.find((n) => n.id === this.selected)
    return html`
      <header><h1>FlowForge</h1></header>
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
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-app': FfApp
  }
}
