import { LitElement, css, html } from 'lit'
import { customElement } from 'lit/decorators.js'
import hello from '../../../backend/examples/hello.flow.json'
import type { Flow } from '../flow/types.ts'
import './ff-canvas.ts'

// Shown until flows can be opened (Phase 2 slice 6).
const initialFlow: Flow = hello

/**
 * Root shell of the FlowForge editor: a header above the graph canvas.
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

    ff-canvas {
      flex: 1;
    }
  `

  render() {
    return html`
      <header><h1>FlowForge</h1></header>
      <ff-canvas .flow=${initialFlow}></ff-canvas>
    `
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-app': FfApp
  }
}
