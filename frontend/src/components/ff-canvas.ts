import { LitElement, css, html } from 'lit'
import { customElement } from 'lit/decorators.js'
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

/** Node and connection types used by the editor. */
export type Schemes = GetSchemes<
  ClassicPreset.Node,
  ClassicPreset.Connection<ClassicPreset.Node, ClassicPreset.Node>
>
type AreaExtra = LitArea2D<Schemes>

/**
 * The graph canvas, built on Rete.js (see docs/adr/0001-graph-library.md).
 * Supports pan, zoom, dragging nodes, selecting nodes and drawing
 * connections. Loading a flow onto it arrives in Phase 2 slice 3.
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
  `

  /** The Rete editor holding the graph. */
  readonly editor = new NodeEditor<Schemes>()

  private area?: AreaPlugin<Schemes, AreaExtra>

  render() {
    return html`<div id="area"></div>`
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
