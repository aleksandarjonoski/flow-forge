import { LitElement, css, html, nothing, svg, type PropertyValues } from 'lit'
import { customElement, property, query, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import type { SourceTree } from '../flow/data-view.ts'
import { findRefs, refExpr, rootKey, type Ref } from '../flow/refs.ts'
import { parseSchema, schemaFields } from '../flow/schema.ts'
import type { PathSegment, Shape } from '../flow/shape.ts'

/** Detail of a mapping-set event: a field's new expression, or none. */
export interface MappingSet {
  path: string
  expression?: string
}

/** Detail of a mapping-add event: a dragged source to add as a new field. */
export interface MappingAdd {
  /** Suggested field name. */
  name: string
  shape: Shape
  expression: string
}

interface SourceRow {
  key: string
  ref: Ref
  label: string
  shape: Shape
  depth: number
}

interface Line {
  d: string
  direct: boolean
  active: boolean
}

// The dragged source travels as this data type (only within the mapper).
const DRAG_TYPE = 'application/x-flowforge-ref'

/**
 * The visual mapper (docs/data-v1.md §7.2): available data on the left, the
 * output schema's fields on the right, and lines between them — solid for
 * direct mappings, dashed for the fields a computed expression uses.
 * Drag a source field onto an output field to map it, or onto the drop zone
 * to add it as a new field. Click a source field to insert its path into the
 * output field being edited.
 *
 * @fires mapping-set - detail is a MappingSet
 * @fires mapping-add - detail is a MappingAdd
 */
@customElement('ff-mapper')
export class FfMapper extends LitElement {
  static styles = css`
    :host {
      display: block;
      font-size: 0.8125rem;
    }

    .mapper {
      position: relative;
      display: grid;
      grid-template-columns: minmax(14rem, 2fr) 7rem minmax(18rem, 3fr);
      align-items: start;
    }

    h3 {
      margin: 0 0 0.5rem;
      font-size: 0.875rem;
    }

    .col {
      min-width: 0;
    }

    .source {
      margin-bottom: 0.75rem;
      padding: 0.375rem 0;
      border: 1px solid color-mix(in srgb, currentColor 15%, transparent);
      border-radius: 6px;
      background: color-mix(in srgb, currentColor 3%, transparent);
    }

    .row {
      display: flex;
      align-items: center;
      gap: 0.5rem;
      min-height: 1.75rem;
      padding: 0 0.625rem 0 calc(0.625rem + var(--depth) * 1rem);
      font-family: ui-monospace, monospace;
    }

    .src-key {
      font: inherit;
      font-weight: 600;
      padding: 0 0.25rem;
      margin-left: -0.25rem;
      border: none;
      border-radius: 3px;
      background: none;
      color: inherit;
      cursor: grab;
    }

    .src-key:hover,
    .src-key:focus-visible {
      background: color-mix(in srgb, #39f 20%, transparent);
    }

    .src-head {
      font-family: system-ui, sans-serif;
    }

    .muted,
    .type {
      opacity: 0.6;
      font-weight: normal;
    }

    .sample {
      color: #2a7;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .target {
      display: grid;
      grid-template-columns: minmax(7rem, 2fr) 3fr;
      gap: 0.5rem;
      align-items: center;
      min-height: 2rem;
      padding: 0.125rem 0.25rem 0.125rem calc(0.25rem + var(--depth) * 1rem);
      border-radius: 4px;
    }

    .target.covered .t-name {
      opacity: 0.55;
    }

    .target.over {
      background: color-mix(in srgb, #39f 18%, transparent);
    }

    .t-name {
      font-family: ui-monospace, monospace;
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .fx {
      font-style: italic;
      color: #c80;
      margin-left: 0.25rem;
    }

    .missing {
      color: #d33;
    }

    .target input {
      font: inherit;
      font-family: ui-monospace, monospace;
      padding: 0.25rem 0.375rem;
      border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
      border-radius: 4px;
      background: Canvas;
      color: CanvasText;
      min-width: 0;
    }

    .add-zone {
      margin-top: 0.75rem;
      padding: 0.75rem;
      border: 2px dashed color-mix(in srgb, currentColor 25%, transparent);
      border-radius: 6px;
      text-align: center;
      opacity: 0.75;
    }

    .add-zone.over {
      border-color: #39f;
      opacity: 1;
    }

    .notice {
      margin-top: 0.5rem;
      color: #d33;
    }

    svg {
      position: absolute;
      inset: 0;
      width: 100%;
      height: 100%;
      pointer-events: none;
      overflow: visible;
    }

    path {
      fill: none;
      stroke: #39f;
      stroke-width: 1.5;
    }

    path.computed {
      stroke: #c80;
      stroke-dasharray: 5 4;
    }

    path.active {
      stroke-width: 3;
    }
  `

  /** Data the node can read: input first, then earlier nodes. */
  @property({ attribute: false })
  sources: SourceTree[] = []

  /** The output schema (raw, as in config). */
  @property({ attribute: false })
  schema?: unknown

  /** The mapping: output field path → expression. */
  @property({ attribute: false })
  mapping: Record<string, unknown> = {}

  // The output field being edited; clicked source paths go there.
  @state()
  private focused?: string

  @state()
  private over?: string

  @state()
  private notice?: string

  @state()
  private lines: Line[] = []

  // The source being dragged, readable during dragover (dataTransfer data
  // is not).
  private dragging?: SourceRow

  @query('.mapper')
  private mapperEl!: HTMLElement

  private resizeObserver = new ResizeObserver(() => this.measure())

  render() {
    const { schema } = parseSchema(this.schema)
    const fields = schema?.type === 'object' ? schemaFields(schema) : []
    return html`
      <div class="mapper">
        <div class="col left">
          <h3>Available data</h3>
          ${
            this.sources.length
              ? this.sources.map((s) => this.renderSource(s))
              : html`<div class="muted">
                  Connect a node to this one's input to see its data.
                </div>`
          }
        </div>
        <div></div>
        <div class="col right">
          <h3>Output</h3>
          ${
            schema?.type === 'object'
              ? fields.map((f) =>
                  this.renderTarget(
                    f.path,
                    f.name,
                    f.type,
                    f.required,
                    f.depth,
                  ),
                )
              : html`<div class="muted">
                  The output schema is not valid yet.
                </div>`
          }
          <div
            class="add-zone ${this.over === '+' ? 'over' : ''}"
            @dragover=${(e: DragEvent) => this.allowDrop(e, '+')}
            @dragleave=${() => (this.over = undefined)}
            @drop=${this.dropToAdd}
          >
            Drop a field here to add it to the output
          </div>
          ${this.notice ? html`<div class="notice" role="status">${this.notice}</div>` : nothing}
        </div>
        <svg aria-hidden="true">
          ${this.lines.map(
            (l) =>
              svg`<path d=${l.d} class="${l.direct ? '' : 'computed'} ${l.active ? 'active' : ''}"></path>`,
          )}
        </svg>
      </div>
    `
  }

  private renderSource(source: SourceTree) {
    const rows = sourceRows(source)
    const via = {
      run: 'last run',
      declared: 'declared schema',
      'declared-run': 'declared schema · last run values',
      type: 'node type',
    }[source.source]
    return html`<div class="source">
      ${rows.map((row, i) =>
        i === 0
          ? html`<div
              class="row src-row src-head"
              data-ref=${row.key}
              style="--depth: 0"
            >
              ${this.renderKey(row, source.label)}
              <span class="muted">← ${source.nodeName} · ${via}</span>
            </div>`
          : html`<div
              class="row src-row"
              data-ref=${row.key}
              style="--depth: ${row.depth}"
            >
              ${this.renderKey(row, row.label)}
              <span class="type">${row.shape.type}</span>
              ${
                'sample' in row.shape && row.shape.sample !== undefined
                  ? html`<span class="sample"
                      >${JSON.stringify(row.shape.sample)}</span
                    >`
                  : nothing
              }
            </div>`,
      )}
    </div>`
  }

  private renderKey(row: SourceRow, label: string) {
    return html`<button
      class="src-key"
      draggable="true"
      title="Drag onto an output field, or click to insert ${refExpr(row.ref)}"
      @dragstart=${(e: DragEvent) => {
        this.dragging = row
        e.dataTransfer?.setData(DRAG_TYPE, row.key)
        if (e.dataTransfer) e.dataTransfer.effectAllowed = 'link'
      }}
      @dragend=${() => {
        this.dragging = undefined
        this.over = undefined
      }}
      @click=${() => this.insert(row)}
    >
      ${label}
    </button>`
  }

  private renderTarget(
    path: string,
    name: string,
    type: string,
    required: boolean,
    depth: number,
  ) {
    const value = this.mapping[path]
    const expr = typeof value === 'string' ? value : ''
    const computed = expr !== '' && !findRefs(expr).direct
    // A field inside an object that is mapped as a whole gets its value from
    // that mapping and cannot be mapped itself (docs/data-v1.md §5).
    const coveredBy = this.coveringKey(path)
    return html`<div
      class="target ${this.over === path ? 'over' : ''} ${coveredBy ? 'covered' : ''}"
      data-path=${path}
      style="--depth: ${depth}"
      @dragover=${(e: DragEvent) => !coveredBy && this.allowDrop(e, path)}
      @dragleave=${() => (this.over = undefined)}
      @drop=${(e: DragEvent) => this.dropOnField(e, path)}
    >
      <span
        class="t-name ${required && !expr && !coveredBy ? 'missing' : ''}"
        title=${path}
        >${name}${required ? ' *' : ''} <span class="type">${type}</span>${
          computed ? html`<span class="fx" title="Computed">ƒ</span>` : nothing
        }</span
      >
      <input
        aria-label="Mapping of ${path}"
        data-map-path=${path}
        spellcheck="false"
        ?disabled=${!!coveredBy}
        placeholder=${
          coveredBy
            ? `comes with ${coveredBy}`
            : type === 'object'
              ? 'drop or type — whole object'
              : required
                ? 'required'
                : ''
        }
        .value=${live(expr)}
        @focus=${() => (this.focused = path)}
        @input=${(e: Event) =>
          this.set(path, (e.target as HTMLInputElement).value)}
      />
    </div>`
  }

  // coveringKey is the mapped object field that contains path, if any.
  private coveringKey(path: string): string | undefined {
    return Object.entries(this.mapping).find(
      ([k, v]) => typeof v === 'string' && v !== '' && path.startsWith(`${k}.`),
    )?.[0]
  }

  connectedCallback() {
    super.connectedCallback()
    void this.updateComplete.then(() => {
      if (this.mapperEl) this.resizeObserver.observe(this.mapperEl)
    })
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    this.resizeObserver.disconnect()
  }

  updated(changed: PropertyValues) {
    // Lines depend on layout, so measure after rendering — except after a
    // change of the lines themselves, which would loop.
    if (changed.size === 1 && changed.has('lines')) return
    requestAnimationFrame(() => this.measure())
  }

  // measure computes the lines from the rendered rows' positions.
  private measure() {
    const mapper = this.mapperEl
    if (!mapper) return
    const box = mapper.getBoundingClientRect()
    const left = mapper.querySelector('.left')!.getBoundingClientRect()
    const right = mapper.querySelector('.right')!.getBoundingClientRect()
    const lines: Line[] = []
    for (const [path, value] of Object.entries(this.mapping)) {
      if (typeof value !== 'string') continue
      const target = mapper.querySelector<HTMLElement>(
        `.target[data-path="${CSS.escape(path)}"]`,
      )
      if (!target) continue
      const { direct, refs } = findRefs(value)
      for (const ref of direct ? [direct] : refs) {
        const source = this.sourceRowFor(ref)
        if (!source) continue
        const s = source.getBoundingClientRect()
        const t = target.getBoundingClientRect()
        const x1 = left.right - box.left
        const y1 = s.top + s.height / 2 - box.top
        const x2 = right.left - box.left
        const y2 = t.top + t.height / 2 - box.top
        const bend = (x2 - x1) / 2
        lines.push({
          d: `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`,
          direct: !!direct,
          active: path === this.focused,
        })
      }
    }
    if (JSON.stringify(lines) !== JSON.stringify(this.lines)) this.lines = lines
  }

  // sourceRowFor finds the row a reference points at: the exact field, or
  // its nearest shown ancestor (references can go deeper than the known
  // shape, and array indexes are shown as [0]).
  private sourceRowFor(ref: Ref): HTMLElement | undefined {
    const segments = ref.segments.map((s) => (typeof s === 'number' ? 0 : s))
    for (let n = segments.length; n >= 0; n--) {
      const key = rowKey(ref.root, segments.slice(0, n))
      const el = this.mapperEl.querySelector<HTMLElement>(
        `.src-row[data-ref="${CSS.escape(key)}"]`,
      )
      if (el) return el
    }
    return undefined
  }

  private allowDrop(e: DragEvent, over: string) {
    if (!this.dragging) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'link'
    this.over = over
  }

  private dropOnField(e: DragEvent, path: string) {
    const row = this.dragging
    if (!row) return
    e.preventDefault()
    this.over = undefined
    this.dragging = undefined
    this.notice = undefined
    this.set(path, refExpr(row.ref))
  }

  private dropToAdd = (e: DragEvent) => {
    const row = this.dragging
    if (!row) return
    e.preventDefault()
    this.over = undefined
    this.dragging = undefined
    if (row.shape.type === 'any') {
      this.notice = `The type of ${row.label} is not known yet; run the flow once, or add the field by hand.`
      return
    }
    this.notice = undefined
    const last = [...row.ref.segments]
      .reverse()
      .find((s) => typeof s === 'string')
    const detail: MappingAdd = {
      name: (last ?? 'data').replaceAll('.', '_') || 'data',
      shape: row.shape,
      expression: refExpr(row.ref),
    }
    this.dispatchEvent(new CustomEvent('mapping-add', { detail }))
  }

  // insert writes a clicked source path into the focused output field, at
  // the cursor.
  private insert(row: SourceRow) {
    const path = this.focused
    if (!path) {
      this.notice =
        'Click into an output field first, then on the data to insert.'
      return
    }
    this.notice = undefined
    const input = this.renderRoot.querySelector<HTMLInputElement>(
      `input[data-map-path="${CSS.escape(path)}"]`,
    )
    const text = refExpr(row.ref)
    const value = this.mapping[path]
    const current = typeof value === 'string' ? value : ''
    const start = input?.selectionStart ?? current.length
    const end = input?.selectionEnd ?? current.length
    const next = current.slice(0, start) + text + current.slice(end)
    if (input) {
      input.value = next
      input.focus()
      input.setSelectionRange(start + text.length, start + text.length)
    }
    this.set(path, next)
  }

  private set(path: string, expression: string) {
    const detail: MappingSet = {
      path,
      expression: expression === '' ? undefined : expression,
    }
    this.dispatchEvent(new CustomEvent('mapping-set', { detail }))
  }
}

// sourceRows flattens a source's shape into rows: the root, then its fields
// depth-first; array items appear once, as [0].
function sourceRows(source: SourceTree): SourceRow[] {
  const rows: SourceRow[] = []
  const walk = (
    shape: Shape,
    segments: PathSegment[],
    label: string,
    depth: number,
  ) => {
    rows.push({
      key: rowKey(source.root, segments),
      ref: { root: source.root, segments },
      label,
      shape,
      depth,
    })
    if (shape.type === 'object') {
      for (const [name, child] of Object.entries(shape.fields ?? {})) {
        walk(child, [...segments, name], name, depth + 1)
      }
    } else if (shape.type === 'array' && shape.items) {
      walk(shape.items, [...segments, 0], '[0]', depth + 1)
    }
  }
  walk(source.shape, [], source.label, 0)
  return rows
}

function rowKey(root: Ref['root'], segments: PathSegment[]): string {
  return [rootKey(root), ...segments.map(String)].join('\u0001')
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-mapper': FfMapper
  }
  interface HTMLElementEventMap {
    'mapping-set': CustomEvent<MappingSet>
    'mapping-add': CustomEvent<MappingAdd>
  }
}
