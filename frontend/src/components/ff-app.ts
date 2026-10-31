import { LitElement, css, html, nothing } from 'lit'
import { customElement, query, state } from 'lit/decorators.js'
import { live } from 'lit/directives/live.js'
import helloText from '../../../backend/examples/hello.flow.json?raw'
import {
  addNode,
  createFlow,
  removeNode,
  setFlowName,
  setNodeConfig,
  setNodeName,
  withEdgeIds,
} from '../flow/edit.ts'
import { runFlow, type TraceEvent } from '../api/client.ts'
import { TracePlayer, type Speed } from '../api/player.ts'
import { runMarks } from '../api/trace.ts'
import { availableData, dataView, outputView } from '../flow/data-view.ts'
import type { RunOutcome } from '../api/client.ts'
import {
  FlowParseError,
  defaultFileName,
  parseFlow,
  serializeFlow,
  stampSaved,
} from '../flow/file.ts'
import { isTrigger, type Flow, type Position } from '../flow/types.ts'
import { DROP_OFFSET, type FfCanvas, type NodeDrop } from './ff-canvas.ts'
import './ff-canvas.ts'
import './ff-node-panel.ts'
import type { NodeChange } from './ff-node-panel.ts'
import './ff-node-editor.ts'
import './ff-palette.ts'
import type { FfRunDialog, RunRequest } from './ff-run-dialog.ts'
import './ff-run-dialog.ts'
import type { RunState } from './ff-run-panel.ts'
import './ff-run-panel.ts'

// The editor opens with the hello example; New starts an empty flow.
const initialFileName = 'hello.flow.json'
const initialFlow = withEdgeIds(parseFlow(helloText))

// Where the playback speed is remembered. Defined before the class: <ff-app>
// is already in the page, so the element is constructed as soon as the class
// is defined, before the rest of this module runs.
const SPEED_KEY = 'flowforge.playbackSpeed'

function loadSpeed(): Speed {
  try {
    const saved = localStorage.getItem(SPEED_KEY)
    if (saved === 'realtime' || saved === 'normal' || saved === 'slow') {
      return saved
    }
  } catch {
    // Storage unavailable: use the default.
  }
  return 'normal'
}

// Clicking a palette item repeatedly would stack nodes exactly on top of
// each other; each further one is shifted by this much.
const STACK_STEP = 30

/**
 * Root shell of the FlowForge editor: a header (flow name, New / Open /
 * Save / Run), the node palette, the graph canvas with test-run results below
 * it, and the property panel of the selected node. Owns the Flow being
 * edited, the selection, the file it came from, and the last test run. See
 * "Opening and saving files", "Palette, new flow and deleting" and "Test
 * runs" in docs/design.md.
 */
@customElement('ff-app')
export class FfApp extends LitElement {
  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      height: 100%;
    }

    header {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      padding: 0.5rem 1rem;
      border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    h1 {
      margin: 0 0.5rem 0 0;
      font-size: 1.125rem;
    }

    .flow-name {
      flex: 1;
      min-width: 0;
      display: flex;
      align-items: center;
      gap: 0.375rem;
    }

    .flow-name input {
      min-width: 0;
      max-width: 100%;
      font: inherit;
      padding: 0.25rem 0.375rem;
      border: 1px solid transparent;
      border-radius: 4px;
      background: transparent;
      color: inherit;
    }

    .flow-name input:hover,
    .flow-name input:focus {
      border-color: color-mix(in srgb, currentColor 30%, transparent);
    }

    .dirty {
      color: #c80;
    }

    button {
      font: inherit;
      padding: 0.25rem 0.75rem;
      cursor: pointer;
    }

    .message {
      display: flex;
      gap: 1rem;
      align-items: center;
      padding: 0.5rem 1rem;
      background: color-mix(in srgb, #d33 12%, Canvas);
      border-bottom: 1px solid #d33;
    }

    .message span {
      flex: 1;
    }

    .workspace {
      flex: 1;
      display: flex;
      min-height: 0;
    }

    ff-palette {
      width: 14rem;
      flex-shrink: 0;
      border-right: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    .center {
      flex: 1;
      min-width: 0;
      display: flex;
      flex-direction: column;
    }

    ff-canvas {
      flex: 1;
      min-height: 0;
    }

    ff-run-panel {
      max-height: 40%;
      border-top: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    ff-node-panel {
      width: 22rem;
      max-width: 40%;
      flex-shrink: 0;
      border-left: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }
  `

  /** The flow being edited. Every edit replaces it with a new Flow. */
  @state()
  private flow: Flow = initialFlow

  /** The flow as last opened or saved; any other value means unsaved changes. */
  @state()
  private savedFlow: Flow = initialFlow

  /** Name of the file the flow came from, reused when saving. */
  @state()
  private fileName?: string = initialFileName

  /** Changes on Open and New, telling the canvas a new document started. */
  @state()
  private docKey = 0

  /** ID of the node open in the node editor, if any. */
  @state()
  private editing?: string

  /** ID of the selected node, if any. */
  @state()
  private selected?: string

  /** Why the last Open failed, until dismissed. */
  @state()
  private message?: string

  /** The latest test run and the flow it ran, shown below the canvas. */
  @state()
  private run?: { state: RunState; flow: Flow }

  // Increments per test run, so a slow earlier run cannot overwrite the
  // result of a later one.
  private runSeq = 0

  /** How fast runs are played back on screen. Remembered in the browser. */
  @state()
  private speed: Speed = loadSpeed()

  // Paces the display of the current run or replay.
  private player?: TracePlayer

  @query('input[type=file]')
  private fileInput!: HTMLInputElement

  @query('ff-canvas')
  private canvas!: FfCanvas

  @query('ff-run-dialog')
  private runDialog!: FfRunDialog

  private get dirty(): boolean {
    return this.flow !== this.savedFlow
  }

  // The engine (and Open) reject a flow without nodes, so it is not saved.
  private get canSave(): boolean {
    return this.flow.nodes.length > 0
  }

  connectedCallback() {
    super.connectedCallback()
    window.addEventListener('keydown', this.onKeydown)
    window.addEventListener('beforeunload', this.onBeforeUnload)
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    window.removeEventListener('keydown', this.onKeydown)
    window.removeEventListener('beforeunload', this.onBeforeUnload)
  }

  render() {
    const node = this.flow.nodes.find((n) => n.id === this.selected)
    const triggers = this.flow.nodes.filter((n) => isTrigger(n.type))
    return html`
      <header>
        <h1>FlowForge</h1>
        <div class="flow-name">
          <input
            aria-label="Flow name"
            title=${`ID ${this.flow.id}${this.fileName ? ` · ${this.fileName}` : ''}`}
            placeholder=${this.flow.id}
            size=${Math.max(12, (this.flow.name || this.flow.id).length + 1)}
            .value=${live(this.flow.name)}
            @input=${(e: Event) =>
              (this.flow = setFlowName(
                this.flow,
                (e.target as HTMLInputElement).value,
              ))}
          />
          ${
            this.dirty
              ? html`<span
                  class="dirty"
                  title="Unsaved changes"
                  aria-label="Unsaved changes"
                  >●</span
                >`
              : nothing
          }
        </div>
        <button @click=${this.newFlow} title="Start an empty flow">New</button>
        <button @click=${this.open} title="Open a flow file (Ctrl+O)">
          Open…
        </button>
        <button
          @click=${this.save}
          ?disabled=${!this.canSave}
          title=${
            this.canSave
              ? 'Download the flow (Ctrl+S)'
              : 'Add a node first: a flow needs at least one node'
          }
        >
          Save
        </button>
        <button
          @click=${() => this.runDialog.show()}
          ?disabled=${triggers.length === 0}
          title=${
            triggers.length
              ? 'Run the flow once with a test payload'
              : 'Add a trigger first'
          }
        >
          Run…
        </button>
        <ff-run-dialog
          .triggers=${triggers}
          @run-request=${(e: CustomEvent<RunRequest>) =>
            void this.startRun(e.detail)}
        ></ff-run-dialog>
        <input
          type="file"
          accept=".json,application/json"
          hidden
          @change=${this.onFileChosen}
        />
      </header>
      ${
        this.message
          ? html`<div class="message" role="alert">
              <span>${this.message}</span>
              <button @click=${() => (this.message = undefined)}>
                Dismiss
              </button>
            </div>`
          : nothing
      }
      <div class="workspace">
        <ff-palette
          @node-add=${(e: CustomEvent<string>) => this.addAtCenter(e.detail)}
        ></ff-palette>
        <div class="center">
          <ff-canvas
            .flow=${this.flow}
            .docKey=${this.docKey}
            .selected=${node?.id}
            .marks=${this.marks}
            @flow-change=${(e: CustomEvent<Flow>) => (this.flow = e.detail)}
            @node-select=${(e: CustomEvent<string | null>) =>
              (this.selected = e.detail ?? undefined)}
            @node-drop=${(e: CustomEvent<NodeDrop>) =>
              this.addNodeAt(e.detail.type, e.detail.position)}
            @node-open=${(e: CustomEvent<string>) => this.openEditor(e.detail)}
          ></ff-canvas>
          ${
            this.run
              ? html`<ff-run-panel
                  .run=${this.run.state}
                  .flow=${this.run.flow}
                  .speed=${this.speed}
                  @close=${this.closeRun}
                  @speed-change=${(e: CustomEvent<Speed>) =>
                    this.setSpeed(e.detail)}
                  @replay=${() => void this.replay()}
                  @node-select=${(e: CustomEvent<string>) =>
                    (this.selected = e.detail)}
                ></ff-run-panel>`
              : nothing
          }
        </div>
        ${
          node
            ? html`<ff-node-panel
                .node=${node}
                .dataView=${dataView(this.flow, node.id, this.lastRun)}
                ?canRun=${triggers.length > 0}
                @node-select=${(e: CustomEvent<string>) =>
                  (this.selected = e.detail)}
                @request-run=${() => this.runDialog.show()}
                @node-change=${(e: CustomEvent<NodeChange>) =>
                  this.applyNodeChange(e.detail)}
                @node-delete=${(e: CustomEvent<string>) =>
                  this.deleteNode(e.detail)}
                @close=${() => (this.selected = undefined)}
                @node-open=${(e: CustomEvent<string>) =>
                  this.openEditor(e.detail)}
              ></ff-node-panel>`
            : nothing
        }
      </div>
      ${this.renderEditor()}
    `
  }

  private applyNodeChange({ id, name, config }: NodeChange) {
    this.flow = setNodeConfig(setNodeName(this.flow, id, name), id, config)
  }

  private addNodeAt(type: string, at: Position) {
    const { flow, nodeId } = addNode(this.flow, type, at)
    this.flow = flow
    this.selected = nodeId
  }

  private addAtCenter(type: string) {
    const center = this.canvas.viewCenter()
    const at = { x: center.x - DROP_OFFSET.x, y: center.y - DROP_OFFSET.y }
    const taken = (p: Position) =>
      this.flow.nodes.some(
        (n) =>
          n.position.x === Math.round(p.x) && n.position.y === Math.round(p.y),
      )
    while (taken(at)) {
      at.x += STACK_STEP
      at.y += STACK_STEP
    }
    this.addNodeAt(type, at)
  }

  private deleteNode(id: string) {
    this.flow = removeNode(this.flow, id)
    if (this.selected === id) this.selected = undefined
    if (this.editing === id) this.editing = undefined
  }

  // renderEditor shows the node editor over everything while a node is
  // being edited (docs/data-v1.md §7.1).
  private renderEditor() {
    const node = this.flow.nodes.find((n) => n.id === this.editing)
    if (!node) return nothing
    return html`<ff-node-editor
      .node=${node}
      .dataView=${dataView(this.flow, node.id, this.lastRun)}
      .outputData=${outputView(this.flow, node.id, this.lastRun)}
      .sources=${availableData(this.flow, node.id, this.lastRun)}
      ?canRun=${this.flow.nodes.some((n) => isTrigger(n.type))}
      @node-change=${(e: CustomEvent<NodeChange>) =>
        this.applyNodeChange(e.detail)}
      @node-delete=${(e: CustomEvent<string>) => this.deleteNode(e.detail)}
      @node-select=${(e: CustomEvent<string>) => this.openEditor(e.detail)}
      @request-run=${() => this.runDialog.show()}
      @close=${() => (this.editing = undefined)}
    ></ff-node-editor>`
  }

  private openEditor(id: string) {
    this.selected = id
    this.editing = id
  }

  // startRun sends the current flow for a test run. Its events go through
  // the player, so the run is shown at the chosen playback speed; the final
  // result is shown once playback has caught up.
  private async startRun({ trigger, payload }: RunRequest) {
    const { seq, player } = this.beginPlayback(this.flow, false)
    const flow = this.flow
    let streamed = 0
    const outcome = await runFlow(flow, trigger, payload, (event) => {
      streamed++
      player.push(event)
    })
    if (!outcome.ok) {
      player.stop()
    } else if (streamed === 0) {
      // A non-streaming answer: play its trace all the same.
      outcome.result.trace.forEach((e) => player.push(e))
    }
    await this.finishPlayback(seq, player, flow, outcome)
  }

  // replay plays the last run again from its recorded trace.
  private async replay() {
    const run = this.run
    if (run?.state.phase !== 'done' || !run.state.outcome.ok) return
    const { outcome } = run.state
    const { seq, player } = this.beginPlayback(run.flow, true)
    outcome.result.trace.forEach((e) => player.push(e))
    await this.finishPlayback(seq, player, run.flow, outcome)
  }

  // beginPlayback stops any playback in progress and starts a new one that
  // shows events in the results panel and on the canvas.
  private beginPlayback(flow: Flow, replay: boolean) {
    this.player?.stop()
    const seq = ++this.runSeq
    let trace: TraceEvent[] = []
    this.run = { state: { phase: 'running', trace, replay }, flow }
    const player = new TracePlayer((event) => {
      if (seq !== this.runSeq) return
      trace = [...trace, event]
      this.run = { state: { phase: 'running', trace, replay }, flow }
    }, this.speed)
    this.player = player
    return { seq, player }
  }

  private async finishPlayback(
    seq: number,
    player: TracePlayer,
    flow: Flow,
    outcome: RunOutcome,
  ) {
    await player.drained()
    if (seq !== this.runSeq) return
    this.run = { state: { phase: 'done', outcome }, flow }
  }

  private closeRun = () => {
    this.player?.stop()
    this.runSeq++
    this.run = undefined
  }

  private setSpeed(speed: Speed) {
    this.speed = speed
    this.player?.setSpeed(speed)
    try {
      localStorage.setItem(SPEED_KEY, speed)
    } catch {
      // Storage may be unavailable (private mode); the choice just isn't kept.
    }
  }

  // lastRun is the latest finished test run, for the input data view.
  private get lastRun() {
    const run = this.run
    if (run?.state.phase !== 'done' || !run.state.outcome.ok) return undefined
    return { flow: run.flow, trace: run.state.outcome.result.trace }
  }

  // marks derives the canvas marks from the latest run, if any.
  private get marks() {
    const state = this.run?.state
    if (!state) return undefined
    if (state.phase === 'running') {
      return runMarks(state.trace, [], false)
    }
    if (!state.outcome.ok) return undefined
    return runMarks(
      state.outcome.result.trace,
      this.flow.nodes.map((n) => n.id),
      true,
    )
  }

  // load replaces the document: new flow, nothing selected, canvas refit.
  private load(flow: Flow, fileName: string | undefined) {
    this.flow = flow
    this.savedFlow = flow
    this.fileName = fileName
    this.selected = undefined
    this.message = undefined
    this.closeRun()
    this.editing = undefined
    this.docKey++
  }

  private newFlow = () => {
    if (
      this.dirty &&
      !confirm('Discard unsaved changes and start a new flow?')
    ) {
      return
    }
    this.load(createFlow(`flow-${crypto.randomUUID().slice(0, 8)}`), undefined)
  }

  private open = () => {
    if (this.dirty && !confirm('Discard unsaved changes and open a file?')) {
      return
    }
    this.fileInput.click()
  }

  private onFileChosen = async () => {
    const file = this.fileInput.files?.[0]
    // Reset so choosing the same file again still fires change.
    this.fileInput.value = ''
    if (!file) return
    let flow: Flow
    try {
      flow = withEdgeIds(parseFlow(await file.text()))
    } catch (e) {
      const reason = e instanceof FlowParseError ? e.message : String(e)
      this.message = `Could not open ${file.name}: ${reason}`
      return
    }
    this.load(flow, file.name)
  }

  private save = () => {
    if (!this.canSave) return
    // Only a flow with unsaved changes gets a new updatedAt, so saving twice
    // produces the same file.
    const flow = this.dirty ? stampSaved(this.flow, new Date()) : this.flow
    const name = this.fileName ?? defaultFileName(flow)
    download(name, serializeFlow(flow))
    this.flow = flow
    this.savedFlow = flow
    this.fileName = name
  }

  private onKeydown = (e: KeyboardEvent) => {
    if ((e.key === 'Delete' || e.key === 'Backspace') && !hasModifier(e)) {
      // Inside the node editor, Delete never removes the node.
      if (this.selected && !this.editing && !isEditing(e)) {
        e.preventDefault()
        this.deleteNode(this.selected)
      }
      return
    }
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return
    const key = e.key.toLowerCase()
    if (key === 's') {
      e.preventDefault()
      this.save()
    } else if (key === 'o') {
      e.preventDefault()
      this.open()
    }
  }

  private onBeforeUnload = (e: BeforeUnloadEvent) => {
    if (this.dirty) e.preventDefault()
  }
}

function hasModifier(e: KeyboardEvent): boolean {
  return e.ctrlKey || e.metaKey || e.altKey || e.shiftKey
}

// isEditing reports whether a key event is aimed at a text field, where
// Delete and Backspace edit text instead of deleting nodes.
function isEditing(e: KeyboardEvent): boolean {
  const target = e.composedPath()[0]
  if (!(target instanceof HTMLElement)) return false
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement ||
    target.isContentEditable
  )
}

// download hands text to the browser as a file to save.
function download(name: string, text: string) {
  const url = URL.createObjectURL(
    new Blob([text], { type: 'application/json' }),
  )
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

declare global {
  interface HTMLElementTagNameMap {
    'ff-app': FfApp
  }
}
