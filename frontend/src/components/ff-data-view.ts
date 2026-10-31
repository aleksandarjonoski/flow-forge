import { LitElement, css, html, nothing, type TemplateResult } from 'lit'
import { customElement, property } from 'lit/decorators.js'
import type { DataView } from '../flow/data-view.ts'
import {
  formatPath,
  hasUnknowns,
  type InsertStyle,
  type PathSegment,
  type Shape,
} from '../flow/shape.ts'

/** Where clicked field paths go: a config field and how it writes paths. */
export interface InsertTarget {
  label: string
  style: InsertStyle
}

/**
 * The input data view: the shape of the data a node receives, as a tree of
 * fields. Clicking a field inserts its path into the current insert target.
 * See "Input data view" in docs/design.md.
 *
 * @fires path-insert - detail is the clicked field's PathSegment[]
 * @fires node-select - detail is the ID of the node the input comes from
 * @fires request-run - the user asked to run the flow to see real data
 */
@customElement('ff-data-view')
export class FfDataView extends LitElement {
  static styles = css`
    :host {
      display: block;
      margin-bottom: 1rem;
      padding: 0.625rem 0.75rem;
      border: 1px solid color-mix(in srgb, currentColor 15%, transparent);
      border-radius: 6px;
      background: color-mix(in srgb, currentColor 3%, transparent);
    }

    h3 {
      margin: 0;
      font-size: 0.875rem;
    }

    .from {
      font-weight: normal;
      opacity: 0.8;
    }

    .source,
    .hint,
    .note {
      opacity: 0.7;
      font-size: 0.8125rem;
    }

    .source {
      margin: 0.125rem 0 0.5rem;
    }

    ul {
      list-style: none;
      margin: 0;
      padding-left: 1rem;
      font-family: ui-monospace, monospace;
      font-size: 0.8125rem;
    }

    .tree > ul {
      padding-left: 0;
    }

    li {
      line-height: 1.6;
    }

    .key {
      font: inherit;
      padding: 0 0.25rem;
      margin-left: -0.25rem;
      border: none;
      border-radius: 3px;
      background: none;
      color: inherit;
      font-weight: 600;
    }

    .key:not(:disabled) {
      cursor: pointer;
    }

    .key:not(:disabled):hover,
    .key:focus-visible {
      background: color-mix(in srgb, #39f 20%, transparent);
    }

    .type {
      opacity: 0.6;
      margin-left: 0.5rem;
    }

    .sample {
      margin-left: 0.5rem;
      color: #2a7;
    }

    .link,
    .run {
      font: inherit;
      padding: 0;
      border: none;
      background: none;
      color: inherit;
      text-decoration: underline;
      cursor: pointer;
    }
  `

  @property({ attribute: false })
  view?: DataView

  /** Where clicked paths go; without one, fields are not clickable. */
  @property({ attribute: false })
  target?: InsertTarget

  /** Whether the flow can be run, to offer "run once" for unknown parts. */
  @property({ type: Boolean })
  canRun = false

  render() {
    const view = this.view
    if (!view) return nothing
    const title =
      view.mode === 'emits'
        ? html`Emits`
        : html`Input
          ${
            view.from
              ? html`<span class="from"
                  >← from
                  <button
                    class="link"
                    @click=${() => this.fire('node-select', view.from!.id)}
                  >
                    ${view.from.name}
                  </button></span
                >`
              : nothing
          }`
    return html`
      <h3>${title}</h3>
      ${this.renderBody(view)}
    `
  }

  private renderBody(view: DataView) {
    if (view.source === 'none' || !view.shape) {
      return html`<div class="hint">
        Nothing is connected to this node's input yet.
      </div>`
    }
    const source =
      view.source === 'run'
        ? 'From the last test run.'
        : view.source === 'declared'
          ? `From the declared schema of ${view.from?.name ?? 'the previous node'}.`
          : view.mode === 'emits'
            ? 'From the trigger type.'
            : "From the previous node's type."
    return html`
      <div class="source">
        ${source}
        ${
          this.target && view.mode === 'input'
            ? html`Click a field to insert it into ${this.target.label}.`
            : nothing
        }
      </div>
      <div class="tree">${this.renderChildren(view.shape, [])}</div>
      ${
        view.source === 'type' && hasUnknowns(view.shape) && this.canRun
          ? html`<div class="hint">
              Parts marked "any" depend on real data.
              <button class="run" @click=${() => this.fire('request-run')}>
                Run the flow once
              </button>
              to see the actual fields.
            </div>`
          : nothing
      }
    `
  }

  // renderChildren lists the fields of an object or the items of an array.
  private renderChildren(shape: Shape, path: PathSegment[]): TemplateResult {
    if (shape.type === 'object') {
      const fields = Object.entries(shape.fields ?? {})
      return html`<ul>
        ${fields.map(([name, child]) =>
          this.renderField(name, child, [...path, name]),
        )}
        ${
          shape.values
            ? html`<li>
                <span class="note">any name</span>
                <span class="type">→ ${typeName(shape.values)}</span>
              </li>`
            : nothing
        }
        ${
          !fields.length && !shape.values
            ? html`<li class="note">(empty)</li>`
            : nothing
        }
      </ul>`
    }
    if (shape.type === 'array' && shape.items) {
      return html`<ul>
        ${this.renderField('[0]', shape.items, [...path, 0])}
      </ul>`
    }
    return this.renderLeaf(shape)
  }

  private renderField(
    label: string,
    shape: Shape,
    path: PathSegment[],
  ): TemplateResult {
    const nested =
      (shape.type === 'object' &&
        (Object.keys(shape.fields ?? {}).length > 0 || shape.values)) ||
      (shape.type === 'array' && shape.items)
    const text = this.target ? formatPath(path, this.target.style) : undefined
    return html`<li>
      <button
        class="key"
        ?disabled=${!text}
        title=${text ? `Insert ${text}` : ''}
        @click=${() => this.fire('path-insert', path)}
      >
        ${label}
      </button>
      ${
        nested
          ? html`<span class="type">${typeName(shape)}</span>
              ${this.renderChildren(shape, path)}`
          : this.renderLeaf(shape)
      }
    </li>`
  }

  private renderLeaf(shape: Shape): TemplateResult {
    return html`<span class="type">${typeName(shape)}</span> ${
        'sample' in shape && shape.sample !== undefined
          ? html`<span class="sample">${preview(shape.sample)}</span>`
          : nothing
      }
      ${
        shape.type === 'any' && shape.note
          ? html`<span class="note">— ${shape.note}</span>`
          : nothing
      }`
  }

  private fire(name: string, detail?: unknown) {
    this.dispatchEvent(
      new CustomEvent(name, { detail, bubbles: true, composed: true }),
    )
  }
}

function typeName(shape: Shape): string {
  return shape.type
}

// preview shows a sample value compactly.
function preview(value: unknown): string {
  const text = JSON.stringify(value)
  return text.length > 40 ? `${text.slice(0, 37)}…` : text
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-data-view': FfDataView
  }
  interface HTMLElementEventMap {
    'path-insert': CustomEvent<PathSegment[]>
    'request-run': CustomEvent<undefined>
  }
}
