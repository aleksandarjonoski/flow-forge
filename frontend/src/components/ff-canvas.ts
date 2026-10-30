import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { ClassicPreset, NodeEditor, type GetSchemes } from 'rete'
import { AreaExtensions, AreaPlugin } from 'rete-area-plugin'
import {
  ClassicFlow,
  ConnectionPlugin,
  canMakeConnection,
  getSourceTarget,
  type SocketData,
} from 'rete-connection-plugin'
import {
  LitPlugin,
  Presets as LitPresets,
  type LitArea2D,
} from '@retejs/lit-plugin'
import {
  addEdge,
  createsCycle,
  moveNode,
  nextEdgeId,
  removeEdge,
} from '../flow/edit.ts'
import { FlowLoadError, flowToGraph, type Graph } from '../flow/to-graph.ts'
import type { RunMark } from '../api/trace.ts'
import type { Flow, Position } from '../flow/types.ts'

/** Node and connection types used by the editor. */
export type Schemes = GetSchemes<
  ClassicPreset.Node,
  ClassicPreset.Connection<ClassicPreset.Node, ClassicPreset.Node>
>
type AreaExtra = LitArea2D<Schemes>

// Socket names. Each node has at most one input and one output (see the
// mapping rules in docs/design.md).
const IN = 'in'
const OUT = 'out'

// How long a notice such as "would create a cycle" stays visible.
const NOTICE_MS = 3000

/** Drag-and-drop data type carrying a node type name from the palette. */
export const NODE_TYPE_MIME = 'application/x-flowforge-node-type'

// A dropped node is placed so the pointer lands near its title, not its
// top-left corner. Matches the classic preset's node width (180px).
export const DROP_OFFSET = { x: 90, y: 20 }

/** Detail of a node-drop event. */
export interface NodeDrop {
  type: string
  position: Position
}

/**
 * The graph canvas, built on Rete.js (see docs/adr/0001-graph-library.md).
 * Supports pan, zoom, dragging nodes, selecting nodes and drawing
 * connections.
 *
 * A new `docKey` means a new document: the canvas rebuilds from `flow` and
 * zooms to fit. Any other new `flow` is synced in place (nodes and edges
 * added or removed individually), keeping pan and zoom. A flow that breaks
 * the mapping rules is not drawn; the canvas lists its problems instead.
 *
 * Edits made on the canvas (moving nodes, connecting, disconnecting) never
 * mutate `flow`: each produces a new Flow, stored in `flow` and announced
 * with a `flow-change` event. See "Editing on the canvas" and "Palette, new
 * flow and deleting" in docs/design.md.
 *
 * @fires flow-change - detail is the edited Flow
 * @fires node-select - detail is the picked node's ID, or null when empty
 *   canvas is clicked
 * @fires node-drop - detail is a NodeDrop: a palette item was dropped here
 *
 * The Rete area is created on first render and destroyed on disconnect, so
 * the element is not meant to be moved around the DOM.
 */
@customElement('ff-canvas')
export class FfCanvas extends LitElement {
  static styles = css`
    :host {
      display: block;
      position: relative;
      overflow: hidden;
    }

    #area {
      width: 100%;
      height: 100%;
    }

    .error {
      position: absolute;
      inset: 1rem 1rem auto;
      padding: 0.75rem 1rem;
      border: 1px solid #d33;
      border-radius: 6px;
      background: color-mix(in srgb, #d33 12%, Canvas);
    }

    .error ul {
      margin: 0.25rem 0 0;
      padding-left: 1.25rem;
    }

    /* Test-run marks (docs/design.md, "Live execution view"), set as
       data-run on each node's view element. */
    [data-run] {
      transition:
        filter 0.2s,
        opacity 0.2s;
    }

    [data-run='running'] {
      animation: pulse 1s ease-in-out infinite alternate;
    }

    [data-run='done'] {
      filter: drop-shadow(0 0 3px #2a7) drop-shadow(0 0 6px #2a7);
    }

    [data-run='failed'] {
      filter: drop-shadow(0 0 3px #d33) drop-shadow(0 0 8px #d33);
    }

    [data-run='skipped'] {
      opacity: 0.4;
    }

    [data-run='done']::after,
    [data-run='failed']::after {
      position: absolute;
      top: -0.5rem;
      right: -0.5rem;
      width: 1.375rem;
      height: 1.375rem;
      display: grid;
      place-items: center;
      border-radius: 50%;
      color: white;
      font:
        bold 0.875rem/1 system-ui,
        sans-serif;
    }

    [data-run='done']::after {
      content: '✓';
      background: #2a7;
    }

    [data-run='failed']::after {
      content: '✗';
      background: #d33;
    }

    @keyframes pulse {
      from {
        filter: drop-shadow(0 0 2px #39f);
      }
      to {
        filter: drop-shadow(0 0 6px #39f) drop-shadow(0 0 12px #39f);
      }
    }

    .empty {
      position: absolute;
      inset: 0;
      display: grid;
      place-items: center;
      opacity: 0.6;
      pointer-events: none;
    }

    .notice {
      position: absolute;
      bottom: 1rem;
      left: 50%;
      translate: -50%;
      padding: 0.5rem 1rem;
      border-radius: 6px;
      background: color-mix(in srgb, CanvasText 85%, Canvas);
      color: Canvas;
    }
  `

  /** The flow to display. */
  @property({ attribute: false })
  flow?: Flow

  /** Test-run state per node ID, shown as marks on the nodes. */
  @property({ attribute: false })
  marks?: Map<string, RunMark>

  /** Identifies the document; changing it rebuilds the canvas and zooms to fit. */
  @property({ attribute: false })
  docKey?: unknown

  /**
   * The selected node's ID, highlighted on the canvas. The canvas reports
   * picks with node-select; the owner decides what is selected.
   */
  @property({ attribute: false })
  selected?: string

  /** The Rete editor holding the graph. */
  readonly editor = new NodeEditor<Schemes>()

  private area?: AreaPlugin<Schemes, AreaExtra>
  private connection?: ConnectionPlugin<Schemes, AreaExtra>
  private selection?: ReturnType<typeof AreaExtensions.selectableNodes>

  /** Why the current flow could not be drawn, if it could not. */
  @state()
  private problems?: string[]

  /** A short-lived message, e.g. why a connection was refused. */
  @state()
  private notice?: string

  private noticeTimer?: ReturnType<typeof setTimeout>

  // Loads run one after another, so a quick second assignment to `flow`
  // cannot interleave with the first one's async editor calls.
  private loading: Promise<void> = Promise.resolve()

  // The Flow the editor currently shows. A `flow` that is this same object
  // came from the canvas's own edit and needs no sync.
  private drawn?: Flow

  // True while sync() changes the editor, so its add/remove events are not
  // mistaken for user edits.
  private drawing = false

  // Shared by every socket; all sockets are compatible.
  private readonly socket = new ClassicPreset.Socket('flow')

  render() {
    return html`
      <div id="area"></div>
      ${
        this.problems
          ? html`<div class="error" role="alert">
              <strong>This flow cannot be displayed:</strong>
              <ul>
                ${this.problems.map((p) => html`<li>${p}</li>`)}
              </ul>
            </div>`
          : nothing
      }
      ${
        !this.problems && this.flow?.nodes.length === 0
          ? html`<div class="empty">
              Drag a node from the palette to start.
            </div>`
          : nothing
      }
      ${
        this.notice
          ? html`<div class="notice" role="status">${this.notice}</div>`
          : nothing
      }
    `
  }

  firstUpdated() {
    const container = this.renderRoot.querySelector<HTMLElement>('#area')!
    const area = new AreaPlugin<Schemes, AreaExtra>(container)
    const connection = new ConnectionPlugin<Schemes, AreaExtra>()
    const render = new LitPlugin<Schemes, AreaExtra>()

    render.addPreset(LitPresets.classic.setup())
    connection.addPreset(
      () =>
        new ClassicFlow({
          canMakeConnection: (from, to) => this.canConnect(from, to),
        }),
    )

    this.editor.use(area)
    area.use(connection)
    area.use(render)

    this.selection = AreaExtensions.selectableNodes(
      area,
      AreaExtensions.selector(),
      { accumulating: AreaExtensions.accumulateOnCtrl() },
    )
    AreaExtensions.simpleNodesOrder(area)
    this.area = area
    this.connection = connection
    this.trackEdits(area)
    this.addEventListener('dragover', this.onDragOver)
    this.addEventListener('drop', this.onDrop)
  }

  /** The point in flow coordinates at the middle of the visible canvas. */
  viewCenter(): Position {
    const rect = this.getBoundingClientRect()
    return this.toFlowPoint(
      rect.left + rect.width / 2,
      rect.top + rect.height / 2,
    )
  }

  // toFlowPoint converts a viewport point to flow coordinates, undoing the
  // area's pan and zoom.
  private toFlowPoint(clientX: number, clientY: number): Position {
    const rect = this.getBoundingClientRect()
    const { x, y, k } = this.area?.area.transform ?? { x: 0, y: 0, k: 1 }
    return { x: (clientX - rect.left - x) / k, y: (clientY - rect.top - y) / k }
  }

  private onDragOver = (e: DragEvent) => {
    if (!e.dataTransfer?.types.includes(NODE_TYPE_MIME)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
  }

  private onDrop = (e: DragEvent) => {
    const type = e.dataTransfer?.getData(NODE_TYPE_MIME)
    if (!type) return
    e.preventDefault()
    const at = this.toFlowPoint(e.clientX, e.clientY)
    const detail: NodeDrop = {
      type,
      position: { x: at.x - DROP_OFFSET.x, y: at.y - DROP_OFFSET.y },
    }
    this.dispatchEvent(
      new CustomEvent('node-drop', { detail, bubbles: true, composed: true }),
    )
  }

  // firstUpdated runs before updated in the same cycle, so the area exists
  // by the time the first flow is drawn.
  updated(changed: PropertyValues<this>) {
    const newDoc = changed.has('docKey')
    if (newDoc || (changed.has('flow') && this.flow !== this.drawn)) {
      const flow = this.flow
      this.drawn = flow
      this.loading = this.loading.then(() => this.sync(flow, newDoc))
    }
    if (changed.has('selected')) {
      this.loading = this.loading.then(() => this.highlight(this.selected))
    }
    if (changed.has('marks')) {
      this.loading = this.loading.then(() => this.applyMarks())
    }
  }

  // trackEdits turns editor events caused by the user into Flow edits.
  private trackEdits(area: AreaPlugin<Schemes, AreaExtra>) {
    this.editor.addPipe((ctx) => {
      if (this.drawing || !this.flow) return ctx
      if (ctx.type === 'connectioncreate') {
        // Give the new connection its Flow edge ID before it is stored.
        ctx.data.id = nextEdgeId(this.flow)
      } else if (ctx.type === 'connectioncreated') {
        const { id, source, target } = ctx.data
        this.commit(addEdge(this.flow, { id, source, target }))
      } else if (ctx.type === 'connectionremoved') {
        this.commit(removeEdge(this.flow, ctx.data.id))
      }
      return ctx
    })
    area.addPipe((ctx) => {
      if (ctx.type === 'nodepicked') {
        this.emitSelect(ctx.data.id)
      } else if (
        ctx.type === 'pointerdown' &&
        ctx.data.event.target === area.container
      ) {
        this.emitSelect(null)
      }
      if (ctx.type === 'nodedragged' && !this.drawing && this.flow) {
        const view = area.nodeViews.get(ctx.data.id)
        const node = this.flow.nodes.find((n) => n.id === ctx.data.id)
        if (view && node) {
          const moved = moveNode(this.flow, node.id, view.position)
          const to = moved.nodes.find((n) => n.id === node.id)!.position
          if (to.x !== node.position.x || to.y !== node.position.y) {
            this.commit(moved)
          }
        }
      }
      return ctx
    })
  }

  // canConnect is consulted before a connection is made, and before any
  // edge it would replace is removed. It refuses connections that would
  // create a cycle. After a refusal Rete would keep the half-made connection
  // on the pointer, waiting for another socket; dropping it right away
  // (once Rete has finished handling this pick) avoids a dangling line.
  private canConnect(from: SocketData, to: SocketData): boolean {
    if (!canMakeConnection(from, to)) return false
    const ends = getSourceTarget(from, to)
    if (!ends || !this.flow) return true
    const [source, target] = ends
    if (createsCycle(this.flow, source.nodeId, target.nodeId)) {
      this.showNotice('That connection would create a cycle.')
      queueMicrotask(() => this.connection?.drop())
      return false
    }
    return true
  }

  private commit(next: Flow) {
    this.drawn = next
    this.flow = next
    this.dispatchEvent(
      new CustomEvent('flow-change', {
        detail: next,
        bubbles: true,
        composed: true,
      }),
    )
  }

  private emitSelect(id: string | null) {
    this.dispatchEvent(
      new CustomEvent('node-select', {
        detail: id,
        bubbles: true,
        composed: true,
      }),
    )
  }

  // highlight makes Rete's selection match `selected`.
  private async highlight(id: string | undefined) {
    const selection = this.selection
    if (!selection) return
    for (const node of this.editor.getNodes()) {
      if (node.id === id) {
        if (!node.selected) await selection.select(node.id, false)
      } else if (node.selected) {
        await selection.unselect(node.id)
      }
    }
  }

  private showNotice(message: string) {
    clearTimeout(this.noticeTimer)
    this.notice = message
    this.noticeTimer = setTimeout(() => (this.notice = undefined), NOTICE_MS)
  }

  // sync makes the editor show `flow`. With `fresh` (a new document) it
  // starts from an empty editor and zooms to fit afterwards; otherwise it
  // removes and adds only what differs, keeping pan and zoom.
  private async sync(flow: Flow | undefined, fresh: boolean) {
    const area = this.area
    if (!area) return
    this.drawing = true
    try {
      let graph: Graph | undefined
      try {
        graph = flow && flowToGraph(flow)
      } catch (e) {
        this.problems = e instanceof FlowLoadError ? e.problems : [String(e)]
        await this.editor.clear()
        return
      }
      // Coming back from an undrawable flow, the editor is empty anyway.
      if (fresh || this.problems) await this.editor.clear()
      this.problems = undefined
      if (!graph) return
      await this.apply(area, graph)
      if (fresh) await AreaExtensions.zoomAt(area, this.editor.getNodes())
    } finally {
      this.drawing = false
    }
    await this.highlight(this.selected)
    this.applyMarks()
  }

  // applyMarks sets each node view's data-run attribute from `marks`.
  private applyMarks() {
    for (const [id, view] of this.area?.nodeViews ?? []) {
      const mark = this.marks?.get(id)
      if (mark) view.element.dataset.run = mark
      else delete view.element.dataset.run
    }
  }

  // apply brings the editor's nodes and connections in line with graph.
  private async apply(area: AreaPlugin<Schemes, AreaExtra>, graph: Graph) {
    const wantNodes = new Map(graph.nodes.map((n) => [n.id, n]))
    const wantConns = new Map(graph.connections.map((c) => [c.id, c]))

    for (const c of this.editor.getConnections()) {
      const want = wantConns.get(c.id)
      if (!want || want.source !== c.source || want.target !== c.target) {
        await this.editor.removeConnection(c.id)
      }
    }
    for (const node of this.editor.getNodes()) {
      if (!wantNodes.has(node.id)) await this.editor.removeNode(node.id)
    }

    for (const n of graph.nodes) {
      let node = this.editor.getNode(n.id)
      if (!node) {
        node = new ClassicPreset.Node(n.label)
        node.id = n.id
        if (n.hasInput) node.addInput(IN, new ClassicPreset.Input(this.socket))
        if (n.hasOutput) {
          node.addOutput(OUT, new ClassicPreset.Output(this.socket))
        }
        await this.editor.addNode(node)
      } else if (node.label !== n.label) {
        node.label = n.label
        await area.update('node', n.id)
      }
      const at = area.nodeViews.get(n.id)?.position
      if (at && (at.x !== n.position.x || at.y !== n.position.y)) {
        await area.translate(n.id, n.position)
      }
    }

    for (const c of graph.connections) {
      if (this.editor.getConnection(c.id)) continue
      // flowToGraph guarantees both ends exist, and they were added above.
      const connection = new ClassicPreset.Connection(
        this.editor.getNode(c.source)!,
        OUT,
        this.editor.getNode(c.target)!,
        IN,
      )
      connection.id = c.id
      await this.editor.addConnection(connection)
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    clearTimeout(this.noticeTimer)
    this.removeEventListener('dragover', this.onDragOver)
    this.removeEventListener('drop', this.onDrop)
    this.area?.destroy()
    this.area = undefined
    this.connection = undefined
    this.selection = undefined
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-canvas': FfCanvas
  }
  interface HTMLElementEventMap {
    'flow-change': CustomEvent<Flow>
    'node-select': CustomEvent<string | null>
    'node-drop': CustomEvent<NodeDrop>
  }
}
