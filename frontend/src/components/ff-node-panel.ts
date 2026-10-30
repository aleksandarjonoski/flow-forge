import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import { withConfigValue } from '../flow/edit.ts'
import {
  nodeType,
  validateConfig,
  type ConfigProperty,
  type StringMapProperty,
  type StringProperty,
} from '../flow/node-types.ts'
import type { FlowNode } from '../flow/types.ts'

/** Detail of a node-change event: the node's new name and config. */
export interface NodeChange {
  id: string
  name: string
  config: Record<string, unknown>
}

/**
 * Property panel for one node: edits its name and config, driven by the node
 * type's schema from the catalog. Never mutates `node`; every edit fires a
 * node-change event and the owner passes the updated node back. See "Node
 * type catalog and property panel" in docs/design.md.
 *
 * @fires node-change - detail is a NodeChange
 * @fires close - the user closed the panel
 */
@customElement('ff-node-panel')
export class FfNodePanel extends LitElement {
  static styles = css`
    :host {
      display: block;
      padding: 1rem;
      overflow: auto;
      font-size: 0.875rem;
    }

    header {
      display: flex;
      align-items: start;
      justify-content: space-between;
      gap: 0.5rem;
    }

    h2 {
      margin: 0;
      font-size: 1rem;
    }

    .muted {
      opacity: 0.7;
    }

    dl {
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 0.125rem 0.75rem;
      margin: 0.75rem 0 1rem;
    }

    dt {
      opacity: 0.7;
    }

    dd {
      margin: 0;
      font-family: ui-monospace, monospace;
    }

    .field {
      display: grid;
      gap: 0.25rem;
      margin-bottom: 1rem;
    }

    label {
      font-weight: 600;
    }

    input,
    select,
    textarea {
      font: inherit;
      padding: 0.375rem 0.5rem;
      border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
      border-radius: 4px;
      background: Canvas;
      color: CanvasText;
      min-width: 0;
    }

    textarea {
      font-family: ui-monospace, monospace;
      min-height: 5rem;
      resize: vertical;
    }

    [aria-invalid='true'] {
      border-color: #d33;
    }

    .hint {
      opacity: 0.7;
    }

    .error {
      color: #d33;
    }

    .row {
      display: grid;
      grid-template-columns: 1fr 1fr auto;
      gap: 0.25rem;
    }

    button {
      font: inherit;
      cursor: pointer;
    }

    .close {
      border: none;
      background: none;
      font-size: 1.25rem;
      line-height: 1;
      color: inherit;
    }
  `

  /** The node being edited. */
  @property({ attribute: false })
  node?: FlowNode

  // Raw JSON editor state for node types missing from the catalog.
  @state()
  private jsonDraft = ''

  @state()
  private jsonError?: string

  willUpdate(changed: PropertyValues<this>) {
    const before = changed.get('node')
    if (changed.has('node') && before?.id !== this.node?.id) {
      this.jsonDraft = JSON.stringify(this.node?.config ?? {}, null, 2)
      this.jsonError = undefined
    }
  }

  render() {
    const node = this.node
    if (!node) return nothing
    const spec = nodeType(node.type)
    const errors = spec ? validateConfig(spec.config, node.config) : {}
    return html`
      <header>
        <div>
          <h2>${node.name || spec?.label || node.type}</h2>
          ${spec ? html`<div class="muted">${spec.description}</div>` : nothing}
        </div>
        <button
          class="close"
          aria-label="Close"
          @click=${() => this.dispatchEvent(new Event('close'))}
        >
          ×
        </button>
      </header>
      <dl>
        <dt>ID</dt>
        <dd>${node.id}</dd>
        <dt>Type</dt>
        <dd>${node.type}</dd>
      </dl>
      <div class="field">
        <label for="name">Name</label>
        <input
          id="name"
          .value=${live(node.name)}
          placeholder=${spec?.label ?? node.type}
          @input=${(e: InputEvent) => this.emit({ name: inputValue(e) })}
        />
      </div>
      ${
        spec
          ? Object.entries(spec.config.properties).map(([key, prop]) =>
              this.renderField(key, prop, node.config[key], errors[key]),
            )
          : this.renderJSON()
      }
    `
  }

  private renderField(
    key: string,
    prop: ConfigProperty,
    value: unknown,
    error: string | undefined,
  ) {
    const id = `cfg-${key}`
    const required = this.isRequired(key)
    return html`
      <div class="field">
        <label for=${id}>${prop.title}${required ? ' *' : ''}</label>
        ${
          prop.type === 'string'
            ? this.renderString(id, key, prop, value, error)
            : this.renderMap(key, prop, value)
        }
        ${
          prop.description
            ? html`<div class="hint">${prop.description}</div>`
            : nothing
        }
        ${
          error
            ? html`<div class="error" id="${id}-error">${error}</div>`
            : nothing
        }
      </div>
    `
  }

  private renderString(
    id: string,
    key: string,
    prop: StringProperty,
    value: unknown,
    error: string | undefined,
  ) {
    const text = typeof value === 'string' ? value : ''
    // An empty field means "unset": the key is removed from config.
    const onInput = (e: Event) =>
      this.setConfig(key, inputValue(e) === '' ? undefined : inputValue(e))
    const invalid = error ? 'true' : 'false'
    const describedBy = error ? `${id}-error` : nothing
    if (prop.enum) {
      const unset = prop.default ? `Default (${prop.default})` : 'Choose…'
      return html`
        <select
          id=${id}
          aria-invalid=${invalid}
          aria-describedby=${describedBy}
          .value=${live(text)}
          @change=${onInput}
        >
          <option value="">${unset}</option>
          ${prop.enum.map((v) => html`<option value=${v}>${v}</option>`)}
          ${
            text && !prop.enum.includes(text)
              ? html`<option value=${text}>${text}</option>`
              : nothing
          }
        </select>
      `
    }
    if (prop['x-multiline']) {
      return html`
        <textarea
          id=${id}
          aria-invalid=${invalid}
          aria-describedby=${describedBy}
          placeholder=${prop.default ?? ''}
          .value=${live(text)}
          @input=${onInput}
        ></textarea>
      `
    }
    return html`
      <input
        id=${id}
        aria-invalid=${invalid}
        aria-describedby=${describedBy}
        placeholder=${prop.default ?? ''}
        .value=${live(text)}
        @input=${onInput}
      />
    `
  }

  // renderMap edits a string→string object as rows. Names are applied on
  // change (not per keystroke) so a half-typed name never collides with
  // another row; a name that is already taken is refused.
  private renderMap(key: string, prop: StringMapProperty, value: unknown) {
    const entries =
      typeof value === 'object' && value !== null && !Array.isArray(value)
        ? Object.entries(value as Record<string, unknown>)
        : []
    const write = (next: [string, unknown][]) =>
      this.setConfig(key, next.length ? Object.fromEntries(next) : undefined)
    const hasBlank = entries.some(([k]) => k === '')
    return html`
      ${entries.map(
        ([name, v], i) => html`
          <div class="row">
            <input
              aria-label="${prop.title} name"
              placeholder="Name"
              .value=${live(name)}
              @change=${(e: Event) => {
                const next = inputValue(e).trim()
                if (next === name) return
                if (entries.some(([k], j) => j !== i && k === next)) {
                  ;(e.target as HTMLInputElement).value = name
                  return
                }
                write(entries.map((en, j) => (j === i ? [next, en[1]] : en)))
              }}
            />
            <input
              aria-label="${prop.title} value"
              placeholder="Value"
              .value=${live(typeof v === 'string' ? v : String(v))}
              @input=${(e: Event) =>
                write(
                  entries.map((en, j) =>
                    j === i ? [en[0], inputValue(e)] : en,
                  ),
                )}
            />
            <button
              aria-label="Remove ${name || 'row'}"
              @click=${() => write(entries.filter((_, j) => j !== i))}
            >
              −
            </button>
          </div>
        `,
      )}
      <div>
        <button
          ?disabled=${hasBlank}
          @click=${() => write([...entries, ['', '']])}
        >
          Add
        </button>
      </div>
    `
  }

  private renderJSON() {
    return html`
      <div class="field">
        <label for="cfg-json">Config (JSON)</label>
        <textarea
          id="cfg-json"
          aria-invalid=${this.jsonError ? 'true' : 'false'}
          .value=${live(this.jsonDraft)}
          @input=${(e: Event) => this.onJSONInput(inputValue(e))}
        ></textarea>
        <div class="hint">
          This node type is not in the catalog, so its config is edited as raw
          JSON.
        </div>
        ${
          this.jsonError
            ? html`<div class="error">${this.jsonError}</div>`
            : nothing
        }
      </div>
    `
  }

  private onJSONInput(text: string) {
    this.jsonDraft = text
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      this.jsonError = 'Not valid JSON.'
      return
    }
    if (
      typeof parsed !== 'object' ||
      parsed === null ||
      Array.isArray(parsed)
    ) {
      this.jsonError = 'Must be a JSON object.'
      return
    }
    this.jsonError = undefined
    this.emit({ config: parsed as Record<string, unknown> })
  }

  private isRequired(key: string): boolean {
    return (
      nodeType(this.node?.type ?? '')?.config.required?.includes(key) ?? false
    )
  }

  private setConfig(key: string, value: unknown) {
    if (!this.node) return
    this.emit({ config: withConfigValue(this.node.config, key, value) })
  }

  private emit(change: Partial<Omit<NodeChange, 'id'>>) {
    const node = this.node
    if (!node) return
    const detail: NodeChange = {
      id: node.id,
      name: change.name ?? node.name,
      config: change.config ?? node.config,
    }
    this.dispatchEvent(
      new CustomEvent('node-change', { detail, bubbles: true, composed: true }),
    )
  }
}

function inputValue(e: Event): string {
  return (e.target as HTMLInputElement | HTMLSelectElement).value
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-node-panel': FfNodePanel
  }
  interface HTMLElementEventMap {
    'node-change': CustomEvent<NodeChange>
  }
}
