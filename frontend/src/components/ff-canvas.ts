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
import { FlowLoadError, flowToGraph } from '../flow/to-graph.ts'
import type { Flow } from '../flow/types.ts'

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

/**
 * The graph canvas, built on Rete.js (see docs/adr/0001-graph-library.md).
 * Supports pan, zoom, dragging nodes, selecting nodes and drawing
 * connections.
 *
 * Setting `flow` draws that flow and zooms to fit it. A flow that breaks the
 * mapping rules is not drawn; the canvas lists its problems instead.
 *
 * Edits made on the canvas (moving nodes, connecting, disconnecting) never
 * mutate `flow`: each produces a new Flow, stored in `flow` and announced
 * with a `flow-change` event. The canvas redraws only when given a Flow it
 * did not produce itself. See "Editing on the canvas" in docs/design.md.
 *
 * @fires flow-change - detail is the edited Flow
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

  /** The Rete editor holding the graph. */
  readonly editor = new NodeEditor<Schemes>()

  private area?: AreaPlugin<Schemes, AreaExtra>
  private connection?: ConnectionPlugin<Schemes, AreaExtra>

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
  // came from the canvas's own edit and needs no redraw.
  private drawn?: Flow

  // True while draw() rebuilds the editor, so its add/remove events are not
  // mistaken for user edits.
  private drawing = false

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

    AreaExtensions.selectableNodes(area, AreaExtensions.selector(), {
      accumulating: AreaExtensions.accumulateOnCtrl(),
    })
    AreaExtensions.simpleNodesOrder(area)
    this.area = area
    this.connection = connection
    this.trackEdits(area)
  }

  // firstUpdated runs before updated in the same cycle, so the area exists
  // by the time the first flow is drawn.
  updated(changed: PropertyValues<this>) {
    if (changed.has('flow') && this.flow !== this.drawn) {
      const flow = this.flow
      this.drawn = flow
      this.loading = this.loading.then(() => this.draw(flow))
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

  private showNotice(message: string) {
    clearTimeout(this.noticeTimer)
    this.notice = message
    this.noticeTimer = setTimeout(() => (this.notice = undefined), NOTICE_MS)
  }

  private async draw(flow: Flow | undefined) {
    this.drawing = true
    try {
      await this.rebuild(flow)
    } finally {
      this.drawing = false
    }
  }

  private async rebuild(flow: Flow | undefined) {
    const area = this.area
    if (!area) return
    await this.editor.clear()
    this.problems = undefined
    if (!flow) return

    let graph
    try {
      graph = flowToGraph(flow)
    } catch (e) {
      this.problems = e instanceof FlowLoadError ? e.problems : [String(e)]
      return
    }

    const socket = new ClassicPreset.Socket('flow')
    const nodes = new Map<string, ClassicPreset.Node>()
    for (const n of graph.nodes) {
      const node = new ClassicPreset.Node(n.label)
      node.id = n.id
      if (n.hasInput) node.addInput(IN, new ClassicPreset.Input(socket))
      if (n.hasOutput) node.addOutput(OUT, new ClassicPreset.Output(socket))
      await this.editor.addNode(node)
      await area.translate(node.id, n.position)
      nodes.set(n.id, node)
    }
    for (const c of graph.connections) {
      const connection = new ClassicPreset.Connection(
        nodes.get(c.source)!,
        OUT,
        nodes.get(c.target)!,
        IN,
      )
      connection.id = c.id
      await this.editor.addConnection(connection)
    }
    await AreaExtensions.zoomAt(area, this.editor.getNodes())
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    clearTimeout(this.noticeTimer)
    this.area?.destroy()
    this.area = undefined
    this.connection = undefined
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-canvas': FfCanvas
  }
  interface HTMLElementEventMap {
    'flow-change': CustomEvent<Flow>
  }
}
