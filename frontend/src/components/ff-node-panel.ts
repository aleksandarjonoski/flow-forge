import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import { withConfigValue } from '../flow/edit.ts'
import {
  nodeType,
  activeMode,
  mappingProblems,
  validateNode,
  type ConfigMode,
  type MappingProperty,
  type NodeTypeSpec,
  type ConfigProperty,
  type SchemaProperty,
  type StringMapProperty,
  type StringProperty,
} from '../flow/node-types.ts'
import type { DataView } from '../flow/data-view.ts'
import {
  parseSchema,
  problemText,
  schemaFields,
  type Schema,
} from '../flow/schema.ts'
import {
  formatPath,
  inferShape,
  schemaFromShape,
  shapeAt,
  type PathSegment,
} from '../flow/shape.ts'
import {
  mappingKey,
  removeMappingKeys,
  renameMappingKeys,
} from '../flow/schema-edit.ts'
import type { FlowNode } from '../flow/types.ts'
import './ff-schema-editor.ts'
import type { SchemaEdit } from './ff-schema-editor.ts'
import './ff-data-view.ts'
import type { InsertTarget } from './ff-data-view.ts'

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
 * @fires node-delete - detail is the node's ID; the user asked to delete it
 * @fires node-select - (from the data view) the node the input comes from
 * @fires request-run - (from the data view) run the flow to see real data
 * @fires node-open - detail is the node's ID; open it in the node editor
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

    :host([view='general']),
    :host([view='output']),
    :host([view='mapping']) {
      padding: 0;
      overflow: visible;
    }

    .open-editor {
      margin-bottom: 1rem;
    }

    .columns {
      display: grid;
      grid-template-columns: minmax(16rem, 2fr) 3fr;
      gap: 1.5rem;
      align-items: start;
    }

    .schema-section {
      margin-bottom: 1.5rem;
    }

    h3 {
      margin: 0 0 0.375rem;
      font-size: 0.9375rem;
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

    .delete {
      margin-top: 0.5rem;
      color: #d33;
    }

    textarea.schema {
      min-height: 12rem;
    }

    .problems {
      margin: 0;
      padding-left: 1.25rem;
    }

    .row-actions {
      display: flex;
      gap: 0.375rem;
      flex-wrap: wrap;
    }

    .sample {
      display: grid;
      gap: 0.25rem;
      padding: 0.5rem;
      border: 1px dashed color-mix(in srgb, currentColor 30%, transparent);
      border-radius: 4px;
    }

    .sample button {
      justify-self: start;
    }

    .label {
      font-weight: 600;
    }

    .modes {
      display: flex;
      gap: 1rem;
    }

    .modes label {
      font-weight: normal;
    }

    .mapping {
      display: grid;
      gap: 0.25rem;
    }

    .map-row {
      display: grid;
      grid-template-columns: minmax(6rem, 40%) 1fr;
      align-items: center;
      gap: 0.5rem;
      padding-left: calc(var(--depth) * 0.875rem);
    }

    .map-field {
      font-family: ui-monospace, monospace;
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .map-field .type {
      font-weight: normal;
      opacity: 0.6;
    }

    .map-row input {
      font-family: ui-monospace, monospace;
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

  /**
   * Which part to show: the whole side panel, or one tab of the node editor
   * (docs/data-v1.md §7.1).
   */
  @property({ reflect: true })
  view: 'panel' | 'general' | 'output' | 'mapping' = 'panel'

  /** What the node receives, shown above the config. */
  @property({ attribute: false })
  dataView?: DataView

  /** What the node outputs (Output tab). */
  @property({ attribute: false })
  outputData?: DataView

  /** Whether the flow can be run (passed on to the data view). */
  @property({ type: Boolean })
  canRun = false

  // The config field that clicked input paths go into: the insertable
  // field focused last, or the first insertable one.
  @state()
  private insertKey?: string

  // Raw JSON editor state for node types missing from the catalog.
  @state()
  private jsonDraft = ''

  @state()
  private jsonError?: string

  // Schema fields are edited as JSON text; the text is kept here while it
  // does not parse, so typing is never interrupted.
  @state()
  private schemaDrafts = new Map<string, string>()

  @state()
  private schemaJSONError?: string

  // "Generate from sample": whether the sample box is open, its text, and
  // why the last attempt failed.
  @state()
  private sampleOpen = false

  @state()
  private sampleText = ''

  @state()
  private sampleError?: string

  willUpdate(changed: PropertyValues<this>) {
    const before = changed.get('node')
    if (changed.has('node') && before?.id !== this.node?.id) {
      this.jsonDraft = JSON.stringify(this.node?.config ?? {}, null, 2)
      this.jsonError = undefined
      this.insertKey = undefined
      this.schemaDrafts = new Map()
      this.schemaJSONError = undefined
      this.sampleOpen = false
      this.sampleText = ''
      this.sampleError = undefined
    }
  }

  render() {
    const node = this.node
    if (!node) return nothing
    const spec = nodeType(node.type)
    const errors = spec ? validateNode(spec, node.config) : {}
    const mode = spec ? activeMode(spec, node.config) : undefined
    if (this.view === 'output')
      return this.renderOutput(node, spec, mode, errors)
    if (this.view === 'mapping')
      return this.renderMappingView(node, spec, mode, errors)

    // Panel and General: the form. In General, schema and mapping fields
    // live in the editor's Output and Mapping tabs instead.
    const inTabs = (prop: ConfigProperty) =>
      this.view === 'general' &&
      (prop.type === 'schema' || prop.type === 'mapping')
    const fields = spec
      ? Object.entries(spec.config.properties).filter(
          ([key]) => !mode || mode.keys.includes(key),
        )
      : []
    const moved = fields.filter(([, prop]) => inTabs(prop))
    return html`
      ${
        this.view === 'panel'
          ? html`<header>
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
            </header>`
          : nothing
      }
      <dl>
        <dt>ID</dt>
        <dd>${node.id}</dd>
        <dt>Type</dt>
        <dd>${node.type}</dd>
      </dl>
      ${
        this.view === 'panel'
          ? html`<button
              class="open-editor"
              title="Open the wide editor with Input, Output and Mapping tabs (or double-click the node)"
              @click=${() => this.fire('node-open', node.id)}
            >
              Open editor
            </button>`
          : nothing
      }
      ${this.renderInputView()}
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
          ? html`${mode ? this.renderModes(spec, mode) : nothing}
            ${fields
              .filter(([, prop]) => !inTabs(prop))
              .map(([key, prop]) =>
                this.renderField(key, prop, node.config[key], errors[key]),
              )}
            ${
              moved.length
                ? html`<div class="hint">
                    ${moved.map(([, p]) => p.title).join(' and ')}: see the
                    ${
                      moved.some(([, p]) => p.type === 'mapping')
                        ? 'Output and Mapping tabs'
                        : 'Output tab'
                    }.
                  </div>`
                : nothing
            }`
          : this.renderJSON()
      }
      <button class="delete" @click=${() => this.fire('node-delete', node.id)}>
        Delete node
      </button>
    `
  }

  // renderInputView is the Input box, with click-to-insert into the form.
  private renderInputView() {
    return html`<ff-data-view
      .view=${this.dataView}
      .target=${this.dataView?.mode === 'input' ? this.insertTarget() : undefined}
      ?canRun=${this.canRun}
      @path-insert=${(e: CustomEvent<PathSegment[]>) => this.insertPath(e.detail)}
    ></ff-data-view>`
  }

  // renderOutput is the editor's Output tab: what the node outputs, and the
  // schema editor for nodes that declare their output.
  private renderOutput(
    node: FlowNode,
    spec: NodeTypeSpec | undefined,
    mode: ConfigMode | undefined,
    errors: Record<string, string>,
  ) {
    const schemaProps = spec
      ? Object.entries(spec.config.properties).filter(
          ([key, prop]) =>
            prop.type === 'schema' && (!mode || mode.keys.includes(key)),
        )
      : []
    if (!schemaProps.length) {
      return html`<ff-data-view .view=${this.outputData}></ff-data-view> ${
          spec?.modes
            ? html`<div class="hint">
                Switch to Mapping mode in the General tab to declare this
                transform's output.
              </div>`
            : nothing
        }`
    }
    return html`
      ${
        this.outputData?.source === 'run'
          ? html`<ff-data-view .view=${this.outputData}></ff-data-view>`
          : nothing
      }
      ${schemaProps.map(
        ([key, prop]) => html`
          <section class="schema-section">
            <h3>${prop.title}</h3>
            ${prop.description ? html`<div class="hint">${prop.description}</div>` : nothing}
            <ff-schema-editor
              .schema=${node.config[key]}
              @schema-edit=${(e: CustomEvent<SchemaEdit>) =>
                this.applySchemaEdit(spec!, key, e.detail)}
            ></ff-schema-editor>
          </section>
          <section class="schema-section">
            <h3>JSON and generators</h3>
            ${this.renderField(key, { ...prop, description: undefined }, node.config[key], errors[key])}
          </section>
        `,
      )}
    `
  }

  // renderMappingView is the editor's Mapping tab: the Input tree beside the
  // mapping rows (the visual mapper's lines come in slice 5).
  private renderMappingView(
    node: FlowNode,
    spec: NodeTypeSpec | undefined,
    mode: ConfigMode | undefined,
    errors: Record<string, string>,
  ) {
    const mappingProp = spec
      ? Object.entries(spec.config.properties).find(
          ([key, prop]) =>
            prop.type === 'mapping' && (!mode || mode.keys.includes(key)),
        )
      : undefined
    if (!mappingProp) {
      return html`<div class="hint">
        This node has no mapping. Switch a transform to Mapping mode in the
        General tab.
      </div>`
    }
    const [key, prop] = mappingProp
    return html`<div class="columns">
      <div>${this.renderInputView()}</div>
      <div>${this.renderField(key, prop, node.config[key], errors[key])}</div>
    </div>`
  }

  // applySchemaEdit stores a tree-editor edit, carrying renames and removals
  // over to mappings that use this schema (docs/data-v1.md §7.3).
  private applySchemaEdit(spec: NodeTypeSpec, key: string, edit: SchemaEdit) {
    const node = this.node
    if (!node) return
    const config: Record<string, unknown> = {
      ...node.config,
      [key]: edit.schema,
    }
    for (const [mKey, mProp] of Object.entries(spec.config.properties)) {
      if (mProp.type !== 'mapping' || mProp.schemaKey !== key) continue
      let mapping = mappingOf(config[mKey])
      const from = edit.renamed && mappingKey(edit.renamed.from)
      const to = edit.renamed && mappingKey(edit.renamed.to)
      if (from && to) mapping = renameMappingKeys(mapping, from, to)
      const removed = edit.removed && mappingKey(edit.removed)
      if (removed) mapping = removeMappingKeys(mapping, removed)
      if (mKey in config) config[mKey] = mapping
    }
    // The JSON view shows the new schema, not an older draft.
    const drafts = new Map(this.schemaDrafts)
    drafts.delete(key)
    this.schemaDrafts = drafts
    this.schemaJSONError = undefined
    this.emit({ config })
  }

  private fire(name: string, detail: unknown) {
    this.dispatchEvent(
      new CustomEvent(name, { detail, bubbles: true, composed: true }),
    )
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
            : prop.type === 'schema'
              ? this.renderSchema(id, key, prop, value)
              : prop.type === 'mapping'
                ? this.renderMapping(key, prop, value)
                : this.renderMap(key, prop, value)
        }
        ${
          prop.description
            ? html`<div class="hint">${prop.description}</div>`
            : nothing
        }
        ${
          // Schema and mapping fields list all their problems themselves.
          error && prop.type !== 'schema' && prop.type !== 'mapping'
            ? html`<div class="error" id="${id}-error">${error}</div>`
            : nothing
        }
      </div>
    `
  }

  // renderSchema edits a declared schema (docs/data-v1.md §7.3): as JSON
  // text, applied whenever it parses; or generated from a pasted sample or
  // from the data of the last test run.
  private renderSchema(
    id: string,
    key: string,
    prop: SchemaProperty,
    value: unknown,
  ) {
    const text =
      this.schemaDrafts.get(key) ??
      (value === undefined ? '' : JSON.stringify(value, null, 2))
    const problems =
      value === undefined || this.schemaJSONError ? [] : schemaProblems(value)
    const fromRun = this.runSampleSchema()
    return html`
      <textarea
        id=${id}
        class="schema"
        spellcheck="false"
        placeholder='{ "type": "object", "properties": { … } }'
        aria-invalid=${this.schemaJSONError || problems.length ? 'true' : 'false'}
        .value=${live(text)}
        @input=${(e: Event) => this.onSchemaInput(key, inputValue(e))}
      ></textarea>
      ${
        this.schemaJSONError
          ? html`<div class="error">${this.schemaJSONError}</div>`
          : nothing
      }
      ${
        problems.length
          ? html`<ul class="error problems">
              ${problems.map((p) => html`<li>${p}</li>`)}
            </ul>`
          : nothing
      }
      <div class="row-actions">
        <button
          @click=${() => {
            this.sampleOpen = !this.sampleOpen
            this.sampleError = undefined
          }}
        >
          Generate from sample…
        </button>
        <button
          ?disabled=${!fromRun}
          title=${
            fromRun
              ? 'Use the data this node received in the last test run'
              : 'Run the flow first; the value at "From" must be an object'
          }
          @click=${() => fromRun && this.applySchema(key, fromRun, prop)}
        >
          Generate from last run
        </button>
      </div>
      ${
        this.sampleOpen
          ? html`<div class="sample">
              <label for="sample">Example JSON</label>
              <textarea
                id="sample"
                spellcheck="false"
                placeholder='{ "name": "ada", "address": { "city": "Skopje" } }'
                .value=${live(this.sampleText)}
                @input=${(e: Event) => {
                  this.sampleText = inputValue(e)
                  this.sampleError = undefined
                }}
              ></textarea>
              ${
                this.sampleError
                  ? html`<div class="error">${this.sampleError}</div>`
                  : nothing
              }
              <button @click=${() => this.generateFromSample(key, prop)}>
                Generate
              </button>
            </div>`
          : nothing
      }
    `
  }

  private onSchemaInput(key: string, text: string) {
    this.schemaDrafts = new Map(this.schemaDrafts).set(key, text)
    if (text.trim() === '') {
      this.schemaJSONError = undefined
      this.setConfig(key, undefined)
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      this.schemaJSONError =
        'Not valid JSON yet; the last valid schema is kept.'
      return
    }
    this.schemaJSONError = undefined
    this.setConfig(key, parsed)
  }

  private generateFromSample(key: string, prop: SchemaProperty) {
    let sample: unknown
    try {
      sample = JSON.parse(this.sampleText)
    } catch {
      this.sampleError = 'The example is not valid JSON.'
      return
    }
    if (
      typeof sample !== 'object' ||
      sample === null ||
      Array.isArray(sample)
    ) {
      this.sampleError = 'The example must be a JSON object.'
      return
    }
    if (this.applySchema(key, schemaFromShape(inferShape(sample)), prop)) {
      this.sampleOpen = false
      this.sampleText = ''
    }
  }

  // applySchema replaces the schema, asking first if one is already set.
  // Returns whether it was applied.
  private applySchema(key: string, schema: Schema, prop: SchemaProperty) {
    const current = this.node?.config[key]
    if (
      current !== undefined &&
      !confirm(`Replace the current ${prop.title.toLowerCase()}?`)
    ) {
      return false
    }
    this.schemaDrafts = new Map(this.schemaDrafts).set(
      key,
      JSON.stringify(schema, null, 2),
    )
    this.schemaJSONError = undefined
    this.setConfig(key, schema)
    return true
  }

  // runSampleSchema is a schema generated from the data this node received
  // in the last test run, at its "from" path; undefined when there is none.
  private runSampleSchema(): Schema | undefined {
    const view = this.dataView
    if (view?.mode !== 'input' || view.source !== 'run' || !view.shape) {
      return undefined
    }
    const from = this.node?.config.from
    const path = typeof from === 'string' && from ? from.split('.') : []
    const shape = shapeAt(view.shape, path)
    return shape?.type === 'object' ? schemaFromShape(shape) : undefined
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
    // Focusing an insertable field makes it the target for clicked paths.
    const onFocus = prop['x-insert'] ? () => (this.insertKey = key) : undefined
    if (prop['x-multiline']) {
      return html`
        <textarea
          id=${id}
          aria-invalid=${invalid}
          aria-describedby=${describedBy}
          placeholder=${prop.default ?? ''}
          .value=${live(text)}
          @input=${onInput}
          @focus=${onFocus}
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
        @focus=${onFocus}
      />
    `
  }

  // insertTargets lists the places clicked input paths can go: string config
  // fields with an insert style, and every mapping row, of the active mode.
  private insertTargets(): Target[] {
    const node = this.node
    const spec = nodeType(node?.type ?? '')
    if (!node || !spec) return []
    const mode = activeMode(spec, node.config)
    const targets: Target[] = []
    for (const [key, prop] of Object.entries(spec.config.properties)) {
      if (mode && !mode.keys.includes(key)) continue
      if (prop.type === 'string' && prop['x-insert']) {
        targets.push({
          id: key,
          label: prop.title,
          style: prop['x-insert'],
          selector: `#cfg-${key}`,
          current: () => stringOr(this.node?.config[key]),
          write: (next) => this.setConfig(key, next === '' ? undefined : next),
        })
      } else if (prop.type === 'mapping') {
        const { schema } = parseSchema(node.config[prop.schemaKey])
        if (schema?.type !== 'object') continue
        for (const field of schemaFields(schema)) {
          targets.push({
            id: `mapping:${field.path}`,
            label: `the mapping of ${field.path}`,
            style: 'expr',
            selector: `[data-map-path="${CSS.escape(field.path)}"]`,
            current: () =>
              stringOr(mappingOf(this.node?.config[key])[field.path]),
            write: (next) => this.setMapping(key, field.path, next),
          })
        }
      }
    }
    return targets
  }

  // insertTarget is where clicked input paths go: the insertable field
  // focused last, or the first one.
  private insertTarget(): Target | undefined {
    const targets = this.insertTargets()
    return targets.find((t) => t.id === this.insertKey) ?? targets[0]
  }

  // insertPath writes a clicked input path into the target field: at the
  // cursor for expressions and templates; replacing the value for a plain
  // path field, which holds exactly one path.
  private insertPath(segments: PathSegment[]) {
    const target = this.insertTarget()
    if (!target) return
    const text = formatPath(segments, target.style)
    if (!text) return
    const el = this.renderRoot.querySelector<
      HTMLInputElement | HTMLTextAreaElement
    >(target.selector)
    const current = target.current()
    let next = text
    let cursor = text.length
    if (target.style !== 'path') {
      const start = el?.selectionStart ?? current.length
      const end = el?.selectionEnd ?? current.length
      next = current.slice(0, start) + text + current.slice(end)
      cursor = start + text.length
    }
    // Update the field right away so the cursor can be placed after the
    // inserted text; the new config then comes back with the same value.
    if (el) {
      el.value = next
      el.focus()
      el.setSelectionRange(cursor, cursor)
    }
    target.write(next)
  }

  // renderModes shows the mode switch for node types with several modes.
  private renderModes(spec: NodeTypeSpec, mode: ConfigMode) {
    return html`
      <div class="field">
        <span class="label">Mode</span>
        <div class="modes" role="radiogroup" aria-label="Mode">
          ${spec.modes!.map(
            (m) =>
              html`<label
                ><input
                  type="radio"
                  name="mode"
                  .checked=${m === mode}
                  @change=${() => this.switchMode(spec, mode, m)}
                />
                ${m.label}</label
              >`,
          )}
        </div>
      </div>
    `
  }

  // switchMode removes the current mode's settings (asking first if there
  // is anything to lose) and seeds the new mode's.
  private switchMode(spec: NodeTypeSpec, from: ConfigMode, to: ConfigMode) {
    const node = this.node
    if (!node || from === to) return
    const config = { ...node.config }
    const meaningful = from.keys.some(
      (k) =>
        k in config &&
        JSON.stringify(config[k]) !== JSON.stringify(seed(spec, k)),
    )
    if (
      meaningful &&
      !confirm(
        `Switch to ${to.label} mode? The ${from.label.toLowerCase()} settings will be removed.`,
      )
    ) {
      this.requestUpdate() // put the radio back
      return
    }
    for (const k of from.keys) delete config[k]
    for (const k of to.keys) {
      const value = seed(spec, k)
      if (value !== undefined) config[k] = value
    }
    this.schemaDrafts = new Map()
    this.schemaJSONError = undefined
    this.insertKey = undefined
    this.emit({ config })
  }

  // renderMapping lists the output schema's fields, each with the
  // expression that fills it (docs/data-v1.md §5). The visual mapper with
  // lines comes later (§7.2); this is its text form.
  private renderMapping(key: string, prop: MappingProperty, value: unknown) {
    const schemaValue = this.node?.config[prop.schemaKey]
    const { schema } = parseSchema(schemaValue)
    if (schema?.type !== 'object') {
      return html`<div class="hint">
        Define a valid output schema above to map its fields.
      </div>`
    }
    const fields = schemaFields(schema)
    if (!fields.length) {
      return html`<div class="hint">
        Add fields to the output schema above, then map each one here.
      </div>`
    }
    const mapping = mappingOf(value)
    const problems = mappingProblems(value ?? {}, schemaValue)
    return html`
      <div class="mapping">
        ${fields.map(
          (f) => html`
            <div class="map-row" style="--depth: ${f.depth}">
              <span class="map-field"
                >${f.name}${f.required ? ' *' : ''}
                <span class="type">${f.type}</span></span
              >
              <input
                aria-label="Mapping of ${f.path}"
                data-map-path=${f.path}
                spellcheck="false"
                placeholder=${
                  f.type === 'object'
                    ? 'whole object (optional)'
                    : f.required
                      ? 'required'
                      : ''
                }
                .value=${live(stringOr(mapping[f.path]))}
                @focus=${() => (this.insertKey = `mapping:${f.path}`)}
                @input=${(e: Event) =>
                  this.setMapping(key, f.path, inputValue(e))}
              />
            </div>
          `,
        )}
      </div>
      ${
        problems.length
          ? html`<ul class="error problems">
              ${problems.map((p) => html`<li>${p}</li>`)}
            </ul>`
          : nothing
      }
    `
  }

  // setMapping sets one field's expression; an empty one unmaps the field.
  private setMapping(key: string, path: string, text: string) {
    const mapping = { ...mappingOf(this.node?.config[key]) }
    if (text === '') delete mapping[path]
    else mapping[path] = text
    this.setConfig(key, mapping)
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
    const spec = nodeType(this.node?.type ?? '')
    if (!spec || !this.node) return false
    const mode = activeMode(spec, this.node.config)
    return (mode?.required ?? spec.config.required ?? []).includes(key)
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

/** A place clicked input paths can be inserted into. */
interface Target extends InsertTarget {
  id: string
  selector: string
  current: () => string
  write: (next: string) => void
}

// seed is the starting value of a config key when its mode is switched on:
// an empty object schema and an empty mapping for the mapping mode.
function seed(spec: NodeTypeSpec, key: string): unknown {
  const prop = spec.config.properties[key]
  if (prop?.type === 'schema') return { type: 'object' }
  if (prop?.type === 'mapping') return {}
  return undefined
}

function mappingOf(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function stringOr(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// schemaProblems lists everything wrong with a declared schema, including a
// top level that is not an object.
function schemaProblems(value: unknown): string[] {
  const { schema, problems } = parseSchema(value)
  if (problems.length) return problems.map(problemText)
  return schema?.type === 'object'
    ? []
    : ['The top level must be of type object.']
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
    'node-delete': CustomEvent<string>
  }
}
