import { LitElement, css, html } from 'lit'
import { customElement } from 'lit/decorators.js'
import { NODE_TYPES, type NodeTypeSpec } from '../flow/node-types.ts'
import { NODE_TYPE_MIME } from './ff-canvas.ts'

/**
 * The node palette: the catalog's node types, grouped by kind. Drag one onto
 * the canvas to add it there, or click it (Enter/Space) to ask the owner to
 * add it in the middle of the canvas. See "Palette, new flow and deleting" in
 * docs/design.md.
 *
 * @fires node-add - detail is the node type to add at the canvas center
 */
@customElement('ff-palette')
export class FfPalette extends LitElement {
  static styles = css`
    :host {
      display: block;
      padding: 0.75rem;
      overflow: auto;
      font-size: 0.875rem;
    }

    h2 {
      margin: 0.75rem 0 0.375rem;
      font-size: 0.75rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      opacity: 0.7;
    }

    h2:first-child {
      margin-top: 0;
    }

    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: grid;
      gap: 0.375rem;
    }

    button {
      width: 100%;
      text-align: left;
      font: inherit;
      padding: 0.5rem 0.625rem;
      border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
      border-radius: 6px;
      background: Canvas;
      color: CanvasText;
      cursor: grab;
    }

    button:hover,
    button:focus-visible {
      border-color: color-mix(in srgb, currentColor 60%, transparent);
    }

    .label {
      font-weight: 600;
    }

    .desc {
      opacity: 0.7;
      font-size: 0.8125rem;
    }
  `

  render() {
    return html`
      ${this.renderGroup(
        'Triggers',
        NODE_TYPES.filter((t) => t.kind === 'trigger'),
      )}
      ${this.renderGroup(
        'Actions',
        NODE_TYPES.filter((t) => t.kind === 'action'),
      )}
    `
  }

  private renderGroup(title: string, types: NodeTypeSpec[]) {
    return html`
      <h2>${title}</h2>
      <ul>
        ${types.map(
          (t) => html`
            <li>
              <button
                draggable="true"
                title="Drag onto the canvas, or click to add"
                @dragstart=${(e: DragEvent) => {
                  e.dataTransfer?.setData(NODE_TYPE_MIME, t.type)
                  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
                }}
                @click=${() =>
                  this.dispatchEvent(
                    new CustomEvent('node-add', {
                      detail: t.type,
                      bubbles: true,
                      composed: true,
                    }),
                  )}
              >
                <div class="label">${t.label}</div>
                <div class="desc">${t.description}</div>
              </button>
            </li>
          `,
        )}
      </ul>
    `
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-palette': FfPalette
  }
  interface HTMLElementEventMap {
    'node-add': CustomEvent<string>
  }
}
