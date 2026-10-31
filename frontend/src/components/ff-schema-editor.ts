import { LitElement, css, html, nothing, type TemplateResult } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import type { SchemaType } from '../flow/schema.ts'
import {
  ITEMS,
  addField,
  enumText,
  removeField,
  renameField,
  setDescription,
  setEnum,
  setRequired,
  setType,
  type FieldPath,
  type RawSchema,
} from '../flow/schema-edit.ts'

/** Detail of a schema-edit event. */
export interface SchemaEdit {
  schema: RawSchema
  /** Set when a field was renamed, so mappings can follow. */
  renamed?: { from: FieldPath; to: FieldPath }
  /** Set when a field was removed, so mappings can follow. */
  removed?: FieldPath
}

const TYPES: SchemaType[] = [
  'string',
  'number',
  'integer',
  'boolean',
  'object',
  'array',
  'null',
]

/**
 * Tree editor for a declared schema (docs/data-v1.md §7.3): one row per
 * field with name, type, required, description and allowed values; nested
 * objects and array items indented below. Never mutates `schema`; every
 * edit fires schema-edit with the new schema.
 *
 * @fires schema-edit - detail is a SchemaEdit
 */
@customElement('ff-schema-editor')
export class FfSchemaEditor extends LitElement {
  static styles = css`
    :host {
      display: block;
      font-size: 0.8125rem;
    }

    .head,
    .row {
      display: grid;
      grid-template-columns:
        minmax(8rem, 1.2fr) 6.5rem 4.75rem minmax(6rem, 1.5fr)
        minmax(5rem, 1fr) 1.75rem;
      gap: 0.375rem;
      align-items: center;
    }

    .head {
      font-weight: 600;
      opacity: 0.7;
      padding-bottom: 0.25rem;
      border-bottom: 1px solid color-mix(in srgb, currentColor 15%, transparent);
      margin-bottom: 0.25rem;
    }

    .row {
      padding: 0.125rem 0;
    }

    .name {
      display: flex;
      align-items: center;
      gap: 0.25rem;
      padding-left: calc(var(--depth) * 1rem);
    }

    .items-label {
      padding-left: calc(var(--depth) * 1rem);
      font-style: italic;
      opacity: 0.8;
    }

    input,
    select,
    button {
      font: inherit;
      min-width: 0;
    }

    input[type='text'],
    select {
      padding: 0.25rem 0.375rem;
      border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
      border-radius: 4px;
      background: Canvas;
      color: CanvasText;
      width: 100%;
      box-sizing: border-box;
    }

    .name input {
      font-family: ui-monospace, monospace;
      font-weight: 600;
    }

    .req {
      justify-self: center;
    }

    .remove {
      border: none;
      background: none;
      color: inherit;
      cursor: pointer;
      opacity: 0.6;
    }

    .remove:hover {
      opacity: 1;
      color: #d33;
    }

    .add {
      margin: 0.25rem 0 0.375rem calc(var(--depth) * 1rem);
      cursor: pointer;
    }

    .error {
      color: #d33;
      margin: 0.25rem 0;
    }

    .muted {
      opacity: 0.7;
    }
  `

  /** The schema being edited, as stored in config. */
  @property({ attribute: false })
  schema?: unknown

  @state()
  private error?: string

  render() {
    const schema = this.schema
    if (!isObject(schema) || schema.type !== 'object') {
      return html`<div class="muted">
          The top level of a schema is an object.
        </div>
        <button class="add" @click=${() => this.edit({ type: 'object' })}>
          Start with an empty object
        </button>`
    }
    return html`
      <div class="head">
        <span>Field</span><span>Type</span><span>Required</span
        ><span>Description</span><span>Allowed values</span><span></span>
      </div>
      ${this.renderObject(schema, [], 0)}
      ${this.error ? html`<div class="error" role="alert">${this.error}</div>` : nothing}
    `
  }

  private renderObject(
    node: RawSchema,
    path: FieldPath,
    depth: number,
  ): TemplateResult {
    const props = isObject(node.properties) ? node.properties : {}
    const required = Array.isArray(node.required) ? node.required : []
    return html`
      ${Object.entries(props).map(([name, child]) =>
        this.renderField(
          name,
          isObject(child) ? child : {},
          [...path, name],
          required.includes(name),
          depth,
        ),
      )}
      <button
        class="add"
        style="--depth: ${depth}"
        @click=${() => this.edit(addField(this.root(), path).schema)}
      >
        + Add field${path.length ? html` to ${displayPath(path)}` : nothing}
      </button>
    `
  }

  private renderField(
    name: string,
    node: RawSchema,
    path: FieldPath,
    required: boolean,
    depth: number,
  ): TemplateResult {
    const type = typeof node.type === 'string' ? node.type : ''
    const enumAllowed =
      type === 'string' || type === 'number' || type === 'integer'
    const items = isObject(node.items) ? node.items : undefined
    return html`
      <div class="row" data-field=${displayPath(path)}>
        <span class="name" style="--depth: ${depth}">
          <input
            type="text"
            aria-label="Name of ${displayPath(path)}"
            .value=${live(name)}
            @change=${(e: Event) => this.rename(path, e.target as HTMLInputElement)}
          />
        </span>
        ${this.renderType(path, type, `Type of ${displayPath(path)}`)}
        <input
          class="req"
          type="checkbox"
          aria-label="${displayPath(path)} is required"
          .checked=${required}
          @change=${(e: Event) =>
            this.edit(
              setRequired(
                this.root(),
                path,
                (e.target as HTMLInputElement).checked,
              ),
            )}
        />
        <input
          type="text"
          aria-label="Description of ${displayPath(path)}"
          .value=${live(typeof node.description === 'string' ? node.description : '')}
          @change=${(e: Event) =>
            this.edit(
              setDescription(
                this.root(),
                path,
                (e.target as HTMLInputElement).value,
              ),
            )}
        />
        ${
          enumAllowed
            ? html`<input
                type="text"
                aria-label="Allowed values of ${displayPath(path)}"
                placeholder="any"
                .value=${live(enumText(node))}
                @change=${(e: Event) =>
                  this.edit(
                    setEnum(
                      this.root(),
                      path,
                      (e.target as HTMLInputElement).value,
                    ),
                  )}
              />`
            : html`<span></span>`
        }
        <button
          class="remove"
          aria-label="Remove ${displayPath(path)}"
          title="Remove field"
          @click=${() => this.edit(removeField(this.root(), path), { removed: path })}
        >
          ✕
        </button>
      </div>
      ${type === 'object' ? this.renderObject(node, path, depth + 1) : nothing}
      ${
        type === 'array'
          ? html`<div class="row">
                <span class="items-label" style="--depth: ${depth + 1}"
                  >items</span
                >
                ${this.renderType(
                  [...path, ITEMS],
                  typeof items?.type === 'string' ? items.type : '',
                  `Item type of ${displayPath(path)}`,
                )}
              </div>
              ${
                items?.type === 'object'
                  ? this.renderObject(items, [...path, ITEMS], depth + 2)
                  : nothing
              }`
          : nothing
      }
    `
  }

  private renderType(path: FieldPath, type: string, label: string) {
    return html`<select
      aria-label=${label}
      .value=${live(type)}
      @change=${(e: Event) =>
        this.edit(
          setType(
            this.root(),
            path,
            (e.target as HTMLSelectElement).value as SchemaType,
          ),
        )}
    >
      ${
        TYPES.includes(type as SchemaType)
          ? nothing
          : html`<option value=${type}>${type || '(none)'}</option>`
      }
      ${TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
    </select>`
  }

  private rename(path: FieldPath, input: HTMLInputElement) {
    const result = renameField(this.root(), path, input.value.trim())
    if ('error' in result) {
      this.error = result.error
      input.value = path.at(-1)!
      return
    }
    const to = [...path.slice(0, -1), input.value.trim()]
    this.edit(result.schema, { renamed: { from: path, to } })
  }

  private root(): RawSchema {
    return isObject(this.schema) ? this.schema : { type: 'object' }
  }

  private edit(schema: RawSchema, extra: Omit<SchemaEdit, 'schema'> = {}) {
    this.error = undefined
    const detail: SchemaEdit = { schema, ...extra }
    this.dispatchEvent(new CustomEvent('schema-edit', { detail }))
  }
}

// displayPath writes a field path for people: customer.city, orders[].id.
function displayPath(path: FieldPath): string {
  return path.join('.').replaceAll(`.${ITEMS}`, ITEMS)
}

function isObject(v: unknown): v is RawSchema {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-schema-editor': FfSchemaEditor
  }
  interface HTMLElementEventMap {
    'schema-edit': CustomEvent<SchemaEdit>
  }
}
