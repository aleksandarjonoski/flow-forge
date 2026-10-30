import { LitElement, css, html } from 'lit'
import { customElement } from 'lit/decorators.js'

/**
 * Root shell of the FlowForge editor. For now it only lays out the header and
 * an empty canvas area; the graph canvas arrives in Phase 2 slice 2.
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

    main {
      flex: 1;
      display: grid;
      place-items: center;
      opacity: 0.6;
    }
  `

  render() {
    return html`
      <header><h1>FlowForge</h1></header>
      <main>Canvas coming soon.</main>
    `
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-app': FfApp
  }
}
