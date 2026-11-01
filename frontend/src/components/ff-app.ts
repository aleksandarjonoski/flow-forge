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
import { onUnauthorized, tokens } from '../api/auth.ts'
import { runFlow, type TraceEvent } from '../api/client.ts'
import {
  createFlow as createStored,
  deleteFlow,
  getExecution,
  getFlow,
  listFlows,
  saveFlow,
  startFlow,
  stopFlow,
  summaryOf,
  type FlowSummary,
  type LibraryError,
  type StoredFlow,
} from '../api/library.ts'
import { TracePlayer, type Speed } from '../api/player.ts'
import { runMarks } from '../api/trace.ts'
import { availableData, dataView, outputView } from '../flow/data-view.ts'
import type { RunOutcome } from '../api/client.ts'
import {
  copyId,
  executionNotes,
  executionOutcome,
  flowIdFromHash,
  hashForFlow,
  notDeployed,
} from '../flow/library.ts'
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
import './ff-history.ts'
import './ff-library.ts'
import './ff-node-panel.ts'
import type { NodeChange } from './ff-node-panel.ts'
import './ff-node-editor.ts'
import './ff-palette.ts'
import type { FfRunDialog, RunRequest } from './ff-run-dialog.ts'
import './ff-run-dialog.ts'
import type { RunState } from './ff-run-panel.ts'
import './ff-run-panel.ts'
import type { FfSignIn } from './ff-sign-in.ts'
import './ff-sign-in.ts'

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

// How often the flow library is refreshed while it is shown.
const LIBRARY_REFRESH_MS = 5000

/** A message bar: text, and buttons that act on it. */
interface Message {
  text: string
  actions?: { label: string; run: () => void }[]
}

/** The run shown below the canvas: a test run, a deploy, or a recording. */
interface ShownRun {
  state: RunState
  /** The flow the run is shown against. */
  flow: Flow
  heading: string
  notes: string[]
  /** For a recorded execution: its ID, to highlight it in History. */
  executionId?: string
}

/**
 * Root shell of the FlowForge editor: a header (flow name and state, New /
 * Open file / Download / Save / Deploy / Stop / History / Run), the node
 * palette or the flow library, the graph canvas with results and run history
 * below it, and the property panel of the selected node. Owns the Flow being
 * edited, the selection, where it is stored (the server, or a file), and the
 * run on show. See "Opening and saving files", "Palette, new flow and
 * deleting", "Test runs" and "Flow library" in docs/design.md, and
 * docs/server-v1.md §7.
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

    .left {
      width: 14rem;
      flex-shrink: 0;
      display: flex;
      flex-direction: column;
      border-right: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    .left > :last-child {
      flex: 1;
      min-height: 0;
    }

    [role='tablist'] {
      display: flex;
      gap: 0.25rem;
      padding: 0.375rem 0.5rem 0;
      border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    [role='tab'] {
      font: inherit;
      font-size: 0.8125rem;
      padding: 0.25rem 0.625rem;
      border: 1px solid transparent;
      border-bottom: none;
      border-radius: 6px 6px 0 0;
      background: none;
      color: inherit;
      cursor: pointer;
      margin-bottom: -1px;
    }

    [role='tab'][aria-selected='true'] {
      border-color: color-mix(in srgb, currentColor 20%, transparent);
      background: Canvas;
      font-weight: 600;
    }

    [role='tablist'] .close {
      margin-left: auto;
      border: none;
      background: none;
      font: inherit;
      font-size: 1.25rem;
      line-height: 1;
      color: inherit;
      cursor: pointer;
      padding: 0 0.25rem;
    }

    .bottom {
      max-height: 40%;
      display: flex;
      flex-direction: column;
      border-top: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    }

    .bottom > :last-child {
      overflow: auto;
      min-height: 0;
    }

    ff-history {
      padding: 0.5rem 1rem;
    }

    .empty {
      padding: 0.75rem 1rem;
      opacity: 0.7;
    }

    .undeployed {
      font-size: 0.75rem;
      color: #c80;
      white-space: nowrap;
    }

    .message.info {
      background: color-mix(in srgb, #36c 10%, Canvas);
      border-bottom-color: #36c;
    }

    .sep {
      width: 1px;
      align-self: stretch;
      background: color-mix(in srgb, currentColor 20%, transparent);
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

  /** Name of the file the flow came from, reused when downloading. */
  @state()
  private fileName?: string = initialFileName

  /**
   * Where the open flow is stored on the server: the revision it was loaded
   * or saved at, and its state. Undefined for a flow that is not stored
   * (new, or opened from a file).
   */
  @state()
  private stored?: FlowSummary

  /** Every stored flow, for the library; undefined until loaded. */
  @state()
  private library?: FlowSummary[]

  /** Why the library could not be loaded. */
  @state()
  private libraryError?: string

  /** What the left column shows. */
  @state()
  private leftTab: 'nodes' | 'flows' = 'nodes'

  /** What the bottom panel shows, if anything. */
  @state()
  private bottom?: 'results' | 'history'

  /** A save, deploy or stop is in progress. */
  @state()
  private busy = false

  /** Changes on Open and New, telling the canvas a new document started. */
  @state()
  private docKey = 0

  /** ID of the node open in the node editor, if any. */
  @state()
  private editing?: string

  /** ID of the selected node, if any. */
  @state()
  private selected?: string

  /** A message for the user (an error, or a choice to make), until dismissed. */
  @state()
  private message?: Message

  /** Whether message is an error (red) or a question (blue). */
  @state()
  private messageKind: 'error' | 'info' = 'error'

  /** The run shown below the canvas, and the flow it is shown against. */
  @state()
  private run?: ShownRun

  private libraryTimer?: ReturnType<typeof setInterval>

  /** Whether an API token is kept, so there is something to sign out of. */
  @state()
  private signedIn = tokens.get() !== undefined

  private offUnauthorized?: () => void

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

  @query('ff-sign-in')
  private signInDialog!: FfSignIn

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
    window.addEventListener('hashchange', this.onHashChange)
    // The server wants a token: ask for it (prod-v1.md §3.4).
    this.offUnauthorized = onUnauthorized(() => this.signInDialog?.show())
    this.libraryTimer = setInterval(() => {
      if (this.leftTab === 'flows' && !document.hidden) {
        void this.refreshLibrary()
      }
    }, LIBRARY_REFRESH_MS)
    void this.refreshLibrary()
    // A link to a stored flow opens it instead of the example.
    const id = flowIdFromHash(location.hash)
    if (id) void this.openStored(id, false)
  }

  disconnectedCallback() {
    super.disconnectedCallback()
    window.removeEventListener('keydown', this.onKeydown)
    window.removeEventListener('beforeunload', this.onBeforeUnload)
    window.removeEventListener('hashchange', this.onHashChange)
    this.offUnauthorized?.()
    clearInterval(this.libraryTimer)
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
          ${this.renderStoredState()}
        </div>
        <button @click=${this.newFlow} title="Start an empty flow">New</button>
        <button @click=${this.open} title="Open a flow file (Ctrl+O)">
          Open file…
        </button>
        <button
          @click=${this.download}
          ?disabled=${!this.canSave}
          title="Download the flow as a file"
        >
          Download
        </button>
        <span class="sep"></span>
        <button
          @click=${() => void this.save()}
          ?disabled=${!this.canSave || this.busy}
          title=${
            this.canSave
              ? 'Save the flow on the server (Ctrl+S)'
              : 'Add a node first: a flow needs at least one node'
          }
        >
          Save
        </button>
        <button
          @click=${() => void this.deploy()}
          ?disabled=${!this.canSave || this.busy}
          title="Save, then run the flow on the server until it is stopped"
        >
          Deploy
        </button>
        ${
          this.stored && this.stored.state !== 'stopped'
            ? html`<button
                @click=${() => void this.stop()}
                ?disabled=${this.busy}
                title="Stop the flow on the server"
              >
                Stop
              </button>`
            : nothing
        }
        <button
          @click=${() => this.toggleBottom('history')}
          ?disabled=${!this.stored}
          aria-pressed=${this.bottom === 'history' ? 'true' : 'false'}
          title=${
            this.stored
              ? 'Runs of the deployed flow'
              : 'Save the flow first: only stored flows have a history'
          }
        >
          History
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
        ${
          this.signedIn
            ? html`<span class="sep"></span>
                <button
                  @click=${this.signOut}
                  title="Forget the API token in this browser"
                >
                  Sign out
                </button>`
            : nothing
        }
        <ff-sign-in @signed-in=${this.onSignedIn}></ff-sign-in>
      </header>
      ${this.renderMessage()}
      <div class="workspace">
        ${this.renderLeft()}
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
          ${this.renderBottom()}
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

  private renderStoredState() {
    const s = this.stored
    if (!s) {
      return html`<span class="undeployed" title="Save puts it on the server"
        >not stored</span
      >`
    }
    return html`<ff-state-badge .flow=${s}></ff-state-badge> ${
        notDeployed(s)
          ? html`<span
              class="undeployed"
              title=${`Revision ${s.runningRevision} runs; revision ${s.revision} is saved`}
              >changes not deployed</span
            >`
          : nothing
      }`
  }

  private renderMessage() {
    const m = this.message
    if (!m) return nothing
    return html`<div
      class="message ${this.messageKind}"
      role=${this.messageKind === 'error' ? 'alert' : 'status'}
    >
      <span>${m.text}</span>
      ${(m.actions ?? []).map(
        (a) =>
          html`<button
            @click=${() => {
              this.message = undefined
              a.run()
            }}
          >
            ${a.label}
          </button>`,
      )}
      <button @click=${() => (this.message = undefined)}>Dismiss</button>
    </div>`
  }

  private renderLeft() {
    const tab = (id: 'nodes' | 'flows', label: string) =>
      html`<button
        role="tab"
        aria-selected=${this.leftTab === id ? 'true' : 'false'}
        @click=${() => {
          this.leftTab = id
          if (id === 'flows') void this.refreshLibrary()
        }}
      >
        ${label}
      </button>`
    return html`<div class="left">
      <div role="tablist" aria-label="Left panel">
        ${tab('nodes', 'Nodes')} ${tab('flows', 'Flows')}
      </div>
      ${
        this.leftTab === 'nodes'
          ? html`<ff-palette
              @node-add=${(e: CustomEvent<string>) => this.addAtCenter(e.detail)}
            ></ff-palette>`
          : html`<ff-library
              .flows=${this.library}
              .error=${this.libraryError}
              .current=${this.stored?.id}
              @flow-open=${(e: CustomEvent<string>) =>
                void this.openStored(e.detail, true)}
              @flow-delete=${(e: CustomEvent<string>) =>
                void this.deleteStored(e.detail)}
            ></ff-library>`
      }
    </div>`
  }

  private renderBottom() {
    if (!this.bottom) return nothing
    const tab = (id: 'results' | 'history', label: string, disabled = false) =>
      html`<button
        role="tab"
        aria-selected=${this.bottom === id ? 'true' : 'false'}
        ?disabled=${disabled}
        @click=${() => (this.bottom = id)}
      >
        ${label}
      </button>`
    return html`<div class="bottom">
      <div role="tablist" aria-label="Bottom panel">
        ${tab('results', 'Results')} ${tab('history', 'History', !this.stored)}
        <button
          class="close"
          aria-label="Close panel"
          @click=${() => (this.bottom = undefined)}
        >
          ×
        </button>
      </div>
      ${
        this.bottom === 'history' && this.stored
          ? html`<ff-history
              .flowId=${this.stored.id}
              .selected=${this.run?.executionId}
              @execution-open=${(e: CustomEvent<string>) =>
                void this.openExecution(e.detail)}
            ></ff-history>`
          : this.renderResults()
      }
    </div>`
  }

  private renderResults() {
    const run = this.run
    if (!run) {
      return html`<p class="empty">
        No run to show. Use Run… for a test run, or open a run from History.
      </p>`
    }
    return html`<ff-run-panel
      .run=${run.state}
      .flow=${run.flow}
      .speed=${this.speed}
      .heading=${run.heading}
      .notes=${run.notes}
      @close=${this.closeRun}
      @speed-change=${(e: CustomEvent<Speed>) => this.setSpeed(e.detail)}
      @replay=${() => void this.replay()}
      @node-select=${(e: CustomEvent<string>) => (this.selected = e.detail)}
    ></ff-run-panel>`
  }

  private toggleBottom(which: 'results' | 'history') {
    this.bottom = this.bottom === which ? undefined : which
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
    const { seq, player } = this.beginPlayback(this.flow, false, 'Test run')
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
    const { seq, player } = this.beginPlayback(
      run.flow,
      true,
      run.heading,
      run.notes,
      run.executionId,
    )
    outcome.result.trace.forEach((e) => player.push(e))
    await this.finishPlayback(seq, player, run.flow, outcome)
  }

  // openExecution plays a recorded execution of the stored flow on the
  // canvas, like a test run (docs/server-v1.md §7.3).
  private async openExecution(id: string) {
    const r = await getExecution(id)
    if (!r.ok) {
      this.showError(`Could not open the run: ${r.error.error}`)
      return
    }
    const e = r.value
    const notes = executionNotes(e, {
      revision: this.stored?.revision,
      dirty: this.dirty,
    })
    const started = new Date(e.startedAt).toLocaleString()
    this.bottom = 'results'
    const { seq, player } = this.beginPlayback(
      this.flow,
      true,
      `Run of ${started}`,
      notes,
      e.id,
    )
    e.trace.forEach((ev) => player.push(ev))
    await this.finishPlayback(seq, player, this.flow, executionOutcome(e))
  }

  // beginPlayback stops any playback in progress and starts a new one that
  // shows events in the results panel and on the canvas.
  private beginPlayback(
    flow: Flow,
    replay: boolean,
    heading: string,
    notes: string[] = [],
    executionId?: string,
  ) {
    this.player?.stop()
    const seq = ++this.runSeq
    let trace: TraceEvent[] = []
    const shown = { flow, heading, notes, executionId }
    this.run = { state: { phase: 'running', trace, replay }, ...shown }
    if (this.bottom !== 'results') this.bottom = 'results'
    const player = new TracePlayer((event) => {
      if (seq !== this.runSeq) return
      trace = [...trace, event]
      this.run = { state: { phase: 'running', trace, replay }, ...shown }
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
    if (seq !== this.runSeq || !this.run) return
    this.run = { ...this.run, state: { phase: 'done', outcome }, flow }
  }

  private closeRun = () => {
    this.player?.stop()
    this.runSeq++
    this.run = undefined
    if (this.bottom === 'results') this.bottom = undefined
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
  // stored says where it is stored on the server, if it is; the location
  // hash follows it.
  private load(flow: Flow, fileName: string | undefined, stored?: FlowSummary) {
    this.flow = flow
    this.savedFlow = flow
    this.fileName = fileName
    this.stored = stored
    this.selected = undefined
    this.message = undefined
    this.closeRun()
    this.bottom = undefined
    this.editing = undefined
    this.docKey++
    this.setHash(stored?.id)
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

  // setHash points the location at a stored flow (or at nothing) without
  // adding history entries or triggering hashchange.
  private setHash(id: string | undefined) {
    const hash = id ? hashForFlow(id) : ''
    if (location.hash === hash) return
    history.replaceState(null, '', hash || location.pathname + location.search)
  }

  private onHashChange = () => {
    const id = flowIdFromHash(location.hash)
    if (id && id !== this.stored?.id) void this.openStored(id, true)
  }

  private showError(text: string, actions?: Message['actions']) {
    this.messageKind = 'error'
    this.message = { text, actions }
  }

  private ask(text: string, actions: NonNullable<Message['actions']>) {
    this.messageKind = 'info'
    this.message = { text, actions }
  }

  // refreshLibrary reloads the library list. The open flow's state is
  // updated from it, but never its revision: that stays the one the editor
  // loaded, so the lost-update guard still works.
  private async refreshLibrary() {
    const r = await listFlows()
    if (!r.ok) {
      this.library = undefined
      this.libraryError =
        r.error.status === 0
          ? r.error.error
          : `Could not load flows: ${r.error.error}`
      return
    }
    this.library = r.value
    this.libraryError = undefined
    const mine = this.stored && r.value.find((f) => f.id === this.stored?.id)
    if (mine && this.stored) {
      const { state, runningRevision, error } = mine
      this.stored = { ...this.stored, state, runningRevision, error }
    }
  }

  // openStored opens a flow from the server, asking first when there are
  // unsaved changes.
  private async openStored(id: string, confirmDiscard: boolean) {
    if (id === this.stored?.id && !this.dirty) return
    if (
      confirmDiscard &&
      this.dirty &&
      !confirm('Discard unsaved changes and open another flow?')
    ) {
      this.setHash(this.stored?.id)
      return
    }
    const r = await getFlow(id)
    if (!r.ok && r.error.status === 401) {
      // The sign-in dialog is showing; the flow opens after signing in.
      return
    }
    if (!r.ok) {
      this.showError(`Could not open flow ${id}: ${r.error.error}`)
      this.setHash(this.stored?.id)
      return
    }
    this.loadStored(r.value)
  }

  private onSignedIn = () => {
    this.signedIn = true
    this.message = undefined
    void this.refreshLibrary()
    const id = flowIdFromHash(location.hash)
    if (id && id !== this.stored?.id) void this.openStored(id, true)
  }

  private signOut = () => {
    tokens.clear()
    this.signedIn = false
    // A server that needs the token asks for it again.
    void this.refreshLibrary()
  }

  private loadStored(s: StoredFlow) {
    this.load(s.flow, undefined, summaryOf(s))
  }

  // save stores the flow on the server: created the first time, saved at the
  // loaded revision after that. Reports whether it worked.
  private async save(overwrite?: number): Promise<boolean> {
    if (!this.canSave || this.busy) return false
    const before = this.flow
    // Only a flow with unsaved changes gets a new updatedAt, so saving twice
    // stores the same document.
    const flow = this.dirty ? stampSaved(before, new Date()) : before
    const revision = overwrite ?? this.stored?.revision
    this.busy = true
    const r =
      revision === undefined
        ? await createStored(flow)
        : await saveFlow(flow, revision)
    this.busy = false
    if (!r.ok) {
      this.saveFailed(flow, r.error)
      return false
    }
    // Edits made while the request was out stay unsaved.
    if (this.flow === before) this.flow = flow
    this.savedFlow = flow
    const summary = summaryOf(r.value)
    this.stored = summary
    this.message = undefined
    this.setHash(summary.id)
    void this.refreshLibrary()
    return true
  }

  private saveFailed(flow: Flow, e: LibraryError) {
    if (e.status === 0) {
      this.showError(`${e.error} Use Download to keep the flow as a file.`)
    } else if (e.status === 409 && e.current) {
      const current = e.current
      this.ask(
        `Someone else saved ${flow.id} meanwhile (it is now at revision ${current.revision}).`,
        [
          { label: 'Reload theirs', run: () => this.loadStored(current) },
          {
            label: 'Overwrite with mine',
            run: () => void this.save(current.revision),
          },
        ],
      )
    } else if (e.status === 409) {
      // Creating, but the ID is taken by another stored flow.
      this.ask(`A flow with ID ${flow.id} is already stored.`, [
        {
          label: 'Open the stored one',
          run: () => void this.openStored(flow.id, true),
        },
        { label: 'Save mine as a copy', run: () => void this.saveAsCopy() },
      ])
    } else {
      this.showError(`Could not save: ${e.error}`)
    }
  }

  // saveAsCopy gives the open flow a free ID and stores it as a new flow.
  private async saveAsCopy() {
    const taken = (this.library ?? []).map((f) => f.id)
    const id = copyId(this.flow.id, [...taken, this.flow.id])
    this.flow = { ...this.flow, id }
    this.stored = undefined
    await this.save()
  }

  // deploy saves the flow if needed, then runs it on the server. Compile
  // problems are shown like a test run's.
  private async deploy() {
    if (!this.stored || this.dirty) {
      if (!(await this.save())) return
    }
    const id = this.stored?.id
    if (!id) return
    this.busy = true
    const r = await startFlow(id)
    this.busy = false
    if (!r.ok) {
      if (r.error.problems) {
        this.player?.stop()
        this.runSeq++
        this.run = {
          state: { phase: 'done', outcome: { ok: false, error: r.error } },
          flow: this.flow,
          heading: 'Deploy',
          notes: [],
        }
        this.bottom = 'results'
      } else {
        this.showError(`Could not deploy: ${r.error.error}`)
      }
      return
    }
    this.updateStored(r.value)
    if (r.value.state === 'failed') {
      this.showError(`Deployed, but the flow could not start: ${r.value.error}`)
    } else {
      this.message = undefined
    }
  }

  private async stop() {
    const id = this.stored?.id
    if (!id) return
    this.busy = true
    const r = await stopFlow(id)
    this.busy = false
    if (!r.ok) {
      this.showError(`Could not stop: ${r.error.error}`)
      return
    }
    this.updateStored(r.value)
  }

  // updateStored takes the state from a start or stop answer. The revision
  // is kept: the open document is still the one loaded or saved.
  private updateStored(s: StoredFlow) {
    if (!this.stored || s.id !== this.stored.id) return
    const { state, runningRevision, error } = s
    this.stored = { ...this.stored, state, runningRevision, error }
    void this.refreshLibrary()
  }

  private async deleteStored(id: string) {
    const name = this.library?.find((f) => f.id === id)?.name || id
    if (
      !confirm(`Delete ${name} and its run history? This cannot be undone.`)
    ) {
      return
    }
    const r = await deleteFlow(id)
    if (!r.ok) {
      this.showError(`Could not delete ${name}: ${r.error.error}`)
      return
    }
    if (this.stored?.id === id) {
      // The flow stays open, as an unsaved flow that is no longer stored.
      this.stored = undefined
      this.savedFlow = { ...this.flow }
      this.bottom = this.bottom === 'history' ? undefined : this.bottom
      this.setHash(undefined)
    }
    void this.refreshLibrary()
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
      this.showError(`Could not open ${file.name}: ${reason}`)
      return
    }
    // A file whose flow ID is already stored: open it as a copy, or as the
    // new content of the stored flow (§7.2).
    await this.refreshLibrary()
    const existing = this.library?.find((f) => f.id === flow.id)
    if (!existing) {
      this.load(flow, file.name)
      return
    }
    this.load(flow, file.name)
    this.ask(
      `${file.name} has the ID of the stored flow ${existing.name || existing.id}.`,
      [
        {
          label: 'Open as a copy',
          run: () => {
            const taken = (this.library ?? []).map((f) => f.id)
            this.flow = { ...flow, id: copyId(flow.id, taken) }
          },
        },
        {
          label: 'Replace the stored one',
          run: () => {
            // Unsaved until Save, which then overwrites the stored flow.
            this.stored = existing
            this.savedFlow = { ...flow }
            this.setHash(existing.id)
          },
        },
      ],
    )
  }

  // download exports the flow as a file in canonical form.
  private download = () => {
    if (!this.canSave) return
    const name = this.fileName ?? defaultFileName(this.flow)
    downloadFile(name, serializeFlow(this.flow))
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
      void this.save()
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

// downloadFile hands text to the browser as a file to save.
function downloadFile(name: string, text: string) {
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
