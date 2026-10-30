import { LitElement, css, html, nothing, type PropertyValues } from 'lit'
import { customElement, property, state } from 'lit/decorators.js'
import { ClassicPreset, NodeEditor, type GetSchemes } from 'rete'
import { AreaExtensions, AreaPlugin } from 'rete-area-plugin'
import {
  ConnectionPlugin,
  Presets as ConnectionPresets,
} from 'rete-connection-plugin'
import {
  LitPlugin,
  Presets as LitPresets,
  type LitArea2D,
} from '@retejs/lit-plugin'
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

/**
 * The graph canvas, built on Rete.js (see docs/adr/0001-graph-library.md).
 * Supports pan, zoom, dragging nodes, selecting nodes and drawing
 * connections.
 *
 * Setting `flow` draws that flow and zooms to fit it. A flow that breaks the
 * mapping rules is not drawn; the canvas lists its problems instead. Changes
 * made on the canvas are not written back to `flow` yet (Phase 2 slice 4).
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
  `

  /** The flow to display. */
  @property({ attribute: false })
  flow?: Flow

  /** The Rete editor holding the graph. */
  readonly editor = new NodeEditor<Schemes>()

  private area?: AreaPlugin<Schemes, AreaExtra>

  /** Why the current flow could not be drawn, if it could not. */
  @state()
  private problems?: string[]

  // Loads run one after another, so a quick second assignment to `flow`
  // cannot interleave with the first one's async editor calls.
  private loading: Promise<void> = Promise.resolve()

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
    `
  }

  firstUpdated() {
    const container = this.renderRoot.querySelector<HTMLElement>('#area')!
    const area = new AreaPlugin<Schemes, AreaExtra>(container)
    const connection = new ConnectionPlugin<Schemes, AreaExtra>()
    const render = new LitPlugin<Schemes, AreaExtra>()

    render.addPreset(LitPresets.classic.setup())
    connection.addPreset(ConnectionPresets.classic.setup())

    this.editor.use(area)
    area.use(connection)
    area.use(render)

    AreaExtensions.selectableNodes(area, AreaExtensions.selector(), {
      accumulating: AreaExtensions.accumulateOnCtrl(),
    })
    AreaExtensions.simpleNodesOrder(area)
    this.area = area
  }

  // firstUpdated runs before updated in the same cycle, so the area exists
  // by the time the first flow is drawn.
  updated(changed: PropertyValues<this>) {
    if (changed.has('flow')) {
      const flow = this.flow
      this.loading = this.loading.then(() => this.draw(flow))
    }
  }

  private async draw(flow: Flow | undefined) {
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
    this.area?.destroy()
    this.area = undefined
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-canvas': FfCanvas
  }
}
