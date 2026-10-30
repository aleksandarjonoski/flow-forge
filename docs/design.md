# FlowForge — Design Overview

Status: **Living document.** Updated as the project evolves.
Scope: the *whole* product — vision, capabilities, full-stack architecture, multi-phase roadmap.
Companion doc: [engine-v1.md](engine-v1.md) is the implementation contract for the v1 execution engine. This document references it but does not duplicate it.

---

## 1. Vision

FlowForge is a **Go-native visual integration platform**.

A developer designs an integration as a graph of nodes (HTTP, queues, databases, transformations, AI calls, …) in a browser-based visual editor. The graph is stored as a JSON document — the *Flow*. A Go runtime loads the Flow and executes it.

Eventually, the same Flow can also be **compiled to a standalone Go binary**: a small, self-contained microservice that ships in a Docker image, runs on goroutines, and has no FlowForge dependency at runtime.

### The angle

The integration / iPaaS market has well-known shapes:

| Tool | Strength | Weakness |
|---|---|---|
| TIBCO BusinessWorks, MuleSoft | enterprise depth | heavy, Java-centric, expensive, slow to deploy |
| Apache NiFi | streaming-first | JVM, ops-heavy, dated UX |
| Node-RED | great UX | single-process Node.js, limited for production loads |
| n8n | great UX, large catalog | Node.js, hosted-first, JS-only extensibility |
| Workato, Zapier | no-code accessibility | not for developers, not self-hostable, opaque execution |

**No one is doing Go-native, developer-first, self-hostable, with a path to compiled binaries.** That is FlowForge's wedge.

### What FlowForge is *not*

- Not a no-code tool. The audience is developers.
- Not a SaaS. Self-hosting is the default; SaaS may come later.
- Not a workflow-orchestration framework (Temporal, Airflow). Those model long-running business processes with durable state and retries. FlowForge models *integrations*: data moves between systems, mostly stateless, mostly in real time.
- Not a generic data pipeline framework (NiFi, Beam). Those are streaming-batch heavy. FlowForge is event-and-request shaped.

---

## 2. Goals

### Product goals

1. **A developer can build a working integration in 10 minutes** — drag 3 nodes, connect them, hit run, see it work.
2. **Flows are Git-friendly.** JSON, diff-able, mergeable, code-reviewable. No binary blobs, no hidden state.
3. **Runtime is fast and tiny.** A FlowForge process should comfortably handle thousands of events per second on a single core, with sub-100MB RAM at idle.
4. **Self-hostable in one binary.** No Postgres, no Redis, no Kubernetes required to get started. SQLite is the default.
5. **Plugin-extensible from day one.** New node types ship as Go packages, not core changes.
6. **A flow can become a standalone Go service.** This is the long-term differentiator (Phase 6).

### Engineering goals

1. **JSON is the source of truth.** UI, runtime, codegen, and CLI all consume the same Flow JSON.
2. **Every execution is traceable.** Every node start/finish emits a structured event. Debugging is a first-class feature, not retrofitted.
3. **No surprises.** Validation rejects ambiguous flows (e.g., multi-inbound edges without defined merge semantics) instead of inventing implicit behavior.
4. **Each implementation slice is independently testable.** No big-bang integrations.
5. **The contract between UI and backend is the Flow JSON schema.** Either side can be replaced.

---

## 3. Non-Goals

Stated explicitly so we don't drift:

- We will not build a flow language richer than JSON+expressions. No Turing-complete config.
- We will not target every cloud's proprietary services in v1. (S3, Pub/Sub, etc. come later as plugins.)
- We will not build a hosted/SaaS offering until the self-hosted product is solid.
- We will not implement a generic durable workflow engine (Temporal territory). FlowForge executions are in-memory and best-effort in v1.
- We will not optimize the visual editor for non-technical users. The audience is developers; we assume some technical literacy.

---

## 4. Capabilities by Phase

What FlowForge *can do* at each milestone. Each phase is a meaningful product step, not just an engineering subtask.

### Phase 1 — Engine MVP ✅
- Load a Flow JSON file from disk.
- Validate it (cycles, unknown node types, dangling edges).
- Run it indefinitely. Triggers fire, executions run, results are logged.
- 5 node types: `http_trigger`, `cron_trigger`, `http_request`, `transform`, `log`.
- CLI: `flowforge run flow.json`.
- Trace events emitted to stdout as JSON lines.

### Phase 2 — Visual Editor MVP (current)
- Browser-based canvas: drag nodes, connect edges, edit properties.
- Save flow JSON to disk via backend API.
- Load existing flow JSON for editing.
- Run a flow from the UI. Watch the trace events live.

### Phase 3 — Server + Storage
- Persistent flow library in SQLite.
- REST API: CRUD flows, start/stop a flow, list executions.
- Multiple flows hosted by one server process.
- Execution log persisted, searchable from the UI.

### Phase 4 — Plugin Maturation
- Connector node types: Kafka, RabbitMQ, Postgres, Redis, S3, SFTP, SMTP/IMAP.
- Plugin SDK + docs for third-party authors.
- Per-node timeouts and retry policy in config.
- Configurable concurrency per node.

### Phase 5 — Advanced Execution
- Merge nodes with explicit semantics (zip / wait-all / first-wins).
- Error edges → run a handler subgraph on failure.
- Parallel fan-out within a single execution.
- Sub-flows (a node references another flow).
- Streaming triggers (Kafka consumer emits continuously).
- Hot reload of edited flows without process restart.

### Phase 6 — Code Generation (the unique angle)
- "Generate Go Project" → a standalone module that compiles to a binary.
- Generated code is readable, idiomatic Go that uses goroutines and channels.
- Re-import edited generated code back into the visual editor.
- Docker image build target.

### Phase 7 — Observability
- Web dashboard: list of flows, last N executions, error rate, latency.
- Prometheus metrics endpoint.
- Trace events streamed to the UI via websocket (real-time step debugger).
- Replay an execution from its trace.

### Phase 8 — Enterprise / Scale
- Auth + RBAC (who can edit / run / view which flows).
- Versioning + Git integration (flows live in a Git repo).
- Cluster runtime: multiple FlowForge processes share triggers and load.
- AI nodes as a first-class category (LLM call, embedding, vector search).

The phases are mostly sequential but not strict — Phase 4 (more connectors) can ship piecewise alongside Phase 5 (advanced execution).

---

## 5. Architecture (Full Stack)

Five layers, top to bottom:

```
┌─────────────────────────────────────────────────────────┐
│ 1. Visual Builder (Frontend)                            │
│    LitElement · TypeScript · Vite · Graph library       │
└──────────────────────────┬──────────────────────────────┘
                           │  Flow JSON (HTTP API)
                           ▼
┌─────────────────────────────────────────────────────────┐
│ 2. Server (Backend HTTP API)                            │
│    Go net/http · CRUD flows · execution control         │
└──────────────────────────┬──────────────────────────────┘
                           │  Compiled flow + lifecycle
                           ▼
┌─────────────────────────────────────────────────────────┐
│ 3. Execution Engine                                     │
│    DAG validator · executor · trace sink · context      │
└──────────────────────────┬──────────────────────────────┘
                           │  Node interface contract
                           ▼
┌─────────────────────────────────────────────────────────┐
│ 4. Node System                                          │
│    Built-in nodes · plugin registry · engine services   │
└──────────────────────────┬──────────────────────────────┘
                           │  go-sql · net/http · etc.
                           ▼
┌─────────────────────────────────────────────────────────┐
│ 5. Storage & Integrations                               │
│    SQLite (flows) · external systems (HTTP, Kafka, …)   │
└─────────────────────────────────────────────────────────┘
```

### Layer 1 — Visual Builder (Phase 2+)

- **Tech**: LitElement components, TypeScript, Vite for dev/build, [Rete.js](https://retejs.org) v2 as the graph library, rendered through its official Lit plugin ([ADR 0001](adr/0001-graph-library.md)).
- **Owns**: canvas, drag-and-drop, edge routing, property panel, JSON serialization.
- **Knows nothing about Go.** Communicates with the backend only via the Flow JSON schema + a small REST API.
- **Output**: a Flow JSON document.

### Layer 2 — Server (Phase 3+)

- **Tech**: Go `net/http` + a thin router. No heavy framework.
- **Owns**: REST endpoints for flow CRUD, start/stop, execution log, trace streaming (websocket).
- **Persists**: flows + execution history in SQLite.
- **Hosts**: the engine. In single-process deployment, server and engine live in one binary.

In Phase 1, this layer is absent — the CLI runs the engine directly against a JSON file.

### Layer 3 — Execution Engine

- **Tech**: pure Go, stdlib + minimal deps (`expr-lang/expr`, `robfig/cron/v3`).
- **Owns**: parse → validate → compile → run.
- **Defined fully in** [engine-v1.md](engine-v1.md). Read it for: node interface, DAG algorithms, error policy, tracing, concurrency, engine-owned services.

### Layer 4 — Node System

- **Tech**: each node is a Go type implementing `ActionNode` or `TriggerNode`.
- **Registration**: built-in nodes self-register via `init()`. Plugins register against the same registry.
- **Engine services injection**: triggers that need a shared HTTP server or cron scheduler receive them at `Init`.
- **Plugins in v1**: compile-time linked. Not using Go's `plugin` package — too fragile across platforms.

### Layer 5 — Storage & External Integrations

- **Flow library**: SQLite, single file. Default location: `~/.flowforge/flows.db`.
- **Execution history**: same SQLite database, separate table.
- **External systems**: every connection (HTTP, DB, Kafka, …) lives behind a node implementation. The engine itself never touches the network.

---

## 6. The Flow JSON Schema (the contract)

Every layer speaks Flow JSON. The schema is the most important artifact in the codebase.

Current version: **1.0** (locked for Phase 1). Defined informally in the engine doc and concretely in the parser (`backend/internal/flow/flow.go`).

Shape:
```json
{
  "id": "string",
  "name": "string",
  "version": "1.0",
  "nodes": [ { "id", "type", "name", "position", "config" } ],
  "edges": [ { "id", "source", "target", "sourceHandle?", "targetHandle?" } ],
  "metadata": { "createdAt", "updatedAt", "author" }
}
```

Key design choices:
- **Type is a string**, not an enum. Plugins extend the type space without schema changes.
- **Config is `map[string]any`**, intentionally untyped at the schema level. Each node type defines its own config contract.
- **Position is part of the schema**, so editor state round-trips perfectly.
- **Edges are first-class objects with IDs**, not just `[from, to]` tuples — supports edge metadata in future versions (labels, weights, error-edge types).

The schema will evolve. Version bumps follow semver: breaking changes go to 2.0, additive changes stay at 1.x. Older runtimes refuse newer documents.

---

## 7. Key Design Principles

The commitments that distinguish FlowForge from "yet another workflow tool."

### 7.1 JSON is the source of truth
Every artifact derives from the Flow JSON. The visual editor *renders* it; the runtime *executes* it; the future codegen *compiles* it. Nothing is hidden in a database row that isn't in the JSON.

### 7.2 Git-friendly by construction
JSON formatting is stable (key order preserved, 2-space indent). Two semantically-equal flows produce identical text. Diffs are reviewable.

### 7.3 Plugin-first node system
The 5 built-in nodes are not special. They use the same registry, same interface, same lifecycle as third-party plugins. If we can't build something as a plugin, the design is wrong.

### 7.4 Validation over inference
When the engine encounters an ambiguous configuration (e.g., multi-inbound edges in v1), it **refuses** instead of inventing a default. Implicit behavior is the source of every long-term integration bug.

### 7.5 Trace everything
Every observable step emits a structured trace event. The UI step-debugger and the production log shipper consume the same stream.

### 7.6 Boring tech in the runtime
Go stdlib first. Minimal dependencies. The runtime is the hot path; it must be predictable. (The frontend is allowed more dependency latitude — it's the cold path.)

### 7.7 The unique angle: compile to native
Long-term, a Flow is a *specification* that can be either *interpreted* (FlowForge runtime) or *compiled* (standalone Go binary). This dual-mode is the moat. Phase 6.

---

## 8. Implementation Roadmap

The complete plan. Each entry is a meaningful milestone, not a single PR.

### Phase 0 — Foundation ✅
- [x] Repo skeleton + license
- [x] [engine-v1.md](engine-v1.md) design contract
- [x] [design.md](design.md) (this document)
- [x] Slice 1: `flow` package — schema structs + JSON parser + 12 unit tests

### Phase 1 — Engine MVP ✅
The 10 engine slices defined in [engine-v1.md §15](engine-v1.md#L294). Recapped here:

| # | Slice | Description | Status |
|---|---|---|---|
| 1 | `flow` package (structs + parser + tests) | Go structs mirroring the v1 Flow JSON schema; `flow.Parse` decodes a document and rejects duplicate node IDs. | ✅ done |
| 2 | `engine/topo.go` (cycle detection, topo sort, reachability) | Graph algorithms: DFS cycle detection that returns the cycle path, Kahn topological sort, BFS reachability from a trigger. | ✅ done |
| 3 | `engine/registry.go` + `engine/trace.go` | `Node`/`ActionNode`/`TriggerNode` interfaces, node-type registry with `DefaultRegistry`, trace events plus stdout/memory/multi sinks. | ✅ done |
| 4 | `engine/validate.go` + `engine/compile.go` | Validate a flow (known types, valid edges, single inbound edge, DAG) and compile it: instantiate + `Init` nodes, build adjacency and per-trigger topo order. | ✅ done |
| 5 | `engine/executor.go` (per-execution runner) | Run one execution of a trigger's subgraph in topo order; halt on first error, recover panics, honor cancellation, trace every step. | ✅ done |
| 6 | `nodes/log.go` + `nodes/transform.go` | `transform` evaluates an expr-lang expression over `input`/`nodes`/`env`; `log` writes via slog with a template message and passes input through. | ✅ done |
| 7 | `engine/services.go` + `nodes/http_trigger.go` | Shared `HTTPMux` (method + path routes, removable) in `EngineServices`; `http_trigger` turns each request into an execution and answers `202`. | ✅ done |
| 8 | `nodes/http_request.go` | Outbound HTTP action: config `url`, `method`, `headers`, `bodyFrom`; output `{status, headers, body}` with JSON bodies parsed; respects context cancellation. | ✅ done |
| 9 | `nodes/cron_trigger.go` | Scheduled trigger on a shared `robfig/cron` scheduler in `EngineServices`; config `expression`, each tick emits `{firedAt}`. | ✅ done |
| 10 | `cmd/flowforge/main.go` (CLI) | `flowforge run <file>`: parse + compile the flow, implement `engine.Run` (start triggers, wire emit → executor, stop on shutdown), serve `HTTPMux` on a configurable address, trace to stdout. | ✅ done |

**Phase 1 done when:** `flowforge run examples/hello.flow.json` starts a server, accepts a POST to `/webhook`, runs a transform, and logs the result. No UI.

### Phase 2 — Visual Editor MVP (current)
Frontend lives in `frontend/` next to `backend/`. Same repo (monorepo decision documented separately).

| # | Slice | Notes | Status |
|---|---|---|---|
| 1 | `frontend/` scaffold | Vite + LitElement + TS + ESLint + Prettier | ✅ done |
| 2 | Embed a graph library | Rete.js v2 + its Lit renderer, chosen after a spike — see [ADR 0001](adr/0001-graph-library.md). `<ff-canvas>` with pan/zoom, drag and connect; no flow loaded yet | ✅ done |
| 3 | Render a Flow JSON on the canvas | Display-only, proves the JSON → visual mapping | ✅ done |
| 4 | Make nodes draggable + edges connectable | Edits update the Flow held in the frontend; cycles refused | ✅ done |
| 5 | Property panel (per-node config form) | Generic JSON-schema-driven form, one per node type | next |
| 6 | Save/load to local file (download/upload JSON) | No backend required yet | |
| 7 | Node palette (drag a new node onto the canvas) | Pull node types from a hardcoded list | |
| 8 | "Run" button → POST to backend `/flows/run` | Backend stays Phase-1 simple — accepts JSON in, runs once | |
| 9 | Live execution view (trace events streamed via SSE/WS) | First glimpse of the step-debugger | |

**Phase 2 done when:** a user can build the same `hello` flow visually, hit Run, and see the trace stream in real time.

**Frontend conventions** (set in slice 1):
- Lives in `frontend/`. Node 22 (pinned in `frontend/.nvmrc`), npm, with `package-lock.json` committed.
- UI pieces are Lit custom elements prefixed `ff-` (e.g. `<ff-app>`), one per file in `src/components/`. `src/main.ts` is the single entry point and imports them.
- npm scripts: `dev` (Vite dev server), `build` (type-check + bundle to `dist/`), `typecheck`, `lint` (ESLint with type info + Lit/web-component rules), `format` / `format:check` (Prettier).
- TypeScript is pinned to 6.0 because typescript-eslint does not support TypeScript 7 yet. Revisit when it does.
- The frontend talks to the backend only through the Flow JSON schema and the REST API (Layer 1, §5). Once the API exists (slice 8), the Vite dev server proxies `/api` to the Go backend.
- Unit tests use Vitest (`npm test`), next to the code as `*.test.ts`.

**Flow JSON → canvas mapping** (set in slice 3):
- `src/flow/types.ts` mirrors the backend document model (`backend/internal/flow/flow.go`) field for field. JSON field names are the contract.
- Each Flow node becomes one canvas node with the **same ID**, labelled with its `name` (or its `type` when `name` is empty), placed at its `position`.
- Sockets: a trigger type (`http_trigger`, `cron_trigger`) gets one output and no input. Every other type, including unknown ones, gets one input and one output. Edges connect a source's output to a target's input. `sourceHandle` / `targetHandle` are ignored, as in the engine.
- An edge whose source or target is not a node, whose target is a trigger, or that gives a node a second inbound edge is a load error. The canvas shows the error instead of a partial graph. The same rules are enforced by the engine (engine-v1.md §4.2, §10).
- After loading, the view zooms to fit all nodes.

**Editing on the canvas** (set in slice 4):
- The Flow object is the editing state. `<ff-canvas>` never mutates it: each edit produces a new Flow, which the canvas announces with a `flow-change` event (bubbles, composed). The owner stores it and passes it back. The canvas only redraws when it receives a Flow it did not produce itself.
- Edits, each a pure function in `src/flow/edit.ts`:
  - **Move a node** — when a drag ends, the node's `position` is updated, rounded to whole pixels so saved files stay diff-friendly.
  - **Connect** — dragging from an output to an input adds an edge with a fresh ID (`edge-<n>`). Dropping on an input that already has an edge replaces that edge, since a node has at most one inbound edge.
  - **Disconnect** — dragging an edge's end off its input removes the edge.
- A connection that would create a cycle, including a node connected to itself, is refused before anything changes. The canvas shows a short notice, matching the engine's DAG rule (engine-v1.md §6).
- Adding and deleting nodes are later slices (palette, slice 7).

### Phase 3 — Server + Storage

| # | Slice | Notes |
|---|---|---|
| 1 | SQLite layer (`backend/internal/store/`) | Schema: `flows`, `executions`, `trace_events` |
| 2 | REST API: `GET/PUT/POST/DELETE /flows` | Standard CRUD |
| 3 | Multi-flow engine (engine manages N concurrent flows) | Each flow has its own trigger set |
| 4 | `POST /flows/{id}/start`, `/stop` | Lifecycle control |
| 5 | `GET /executions?flow_id=…` | Paginated history |
| 6 | Migrations system | `golang-migrate` or homegrown — decide later |
| 7 | Frontend: flow list, save/load against server | |

### Phase 4 — Plugins

| # | Slice | Notes |
|---|---|---|
| 1 | Plugin SDK doc + example | A `flowforge-plugin-example` repo |
| 2 | Per-node timeouts in config | Engine-level enforcement |
| 3 | Per-node retry policy | Configurable backoff |
| 4 | Connectors: Postgres, Redis | First two non-trivial nodes |
| 5 | Connectors: Kafka producer + consumer | Streaming trigger is the hard part |
| 6 | Connectors: SFTP, SMTP/IMAP, S3 | Pure I/O nodes |

### Phase 5 — Advanced Execution

| # | Slice | Notes |
|---|---|---|
| 1 | Merge node semantics (zip / wait-all / first-wins) | Validator stops rejecting multi-inbound |
| 2 | Error edges in schema (`edge.type: "error"`) | Engine routes errors to handler subgraph |
| 3 | Parallel fan-out within an execution | True concurrent node execution |
| 4 | Sub-flows (`type: "subflow"`) | A node that runs another flow |
| 5 | Streaming triggers (multiple emits per "event") | Kafka consumer benefits most |
| 6 | Hot reload (watch flow files, recompile in place) | |

### Phase 6 — Code Generation

| # | Slice | Notes |
|---|---|---|
| 1 | Codegen package: Flow → Go source AST | Generate readable code |
| 2 | Generated project layout: `go.mod`, `main.go`, runtime stubs | |
| 3 | "Generate" CLI: `flowforge gen flow.json --out ./service` | |
| 4 | Dockerfile generation | |
| 5 | Re-import: parse generated Go back into a Flow | The truly unique feature |
| 6 | Compatibility tests: generated binary matches interpreted behavior | |

### Phase 7 — Observability

- Trace sink → websocket → live UI
- Prometheus metrics endpoint
- Per-flow dashboard (executions, error rate, latency)
- Execution replay

### Phase 8 — Enterprise / Scale

- Auth (OIDC + local users)
- RBAC (flow-level permissions)
- Git-backed flow library
- Cluster runtime (Raft or external coordinator — decide later)
- AI node category

---

## 9. Folder Layout (target)

The full layout when the project is mature. Phase 1 ships only the `backend/` half.

```
flow-forge/
├── README.md
├── LICENSE
├── docs/
│   ├── design.md            ← this document
│   ├── engine-v1.md         ← engine implementation contract
│   ├── schema-v1.md         ← (future) formal JSON schema spec
│   └── plugin-sdk.md        ← (future) plugin author guide
│
├── backend/
│   ├── cmd/
│   │   ├── flowforge/       ← run-a-flow CLI
│   │   └── flowforge-gen/   ← (Phase 6) codegen CLI
│   ├── internal/
│   │   ├── flow/            ← schema + parser + validator
│   │   ├── engine/          ← executor + registry + trace + topo
│   │   ├── nodes/           ← built-in node implementations
│   │   ├── server/          ← (Phase 3) HTTP API + websocket
│   │   ├── store/           ← (Phase 3) SQLite persistence
│   │   └── codegen/         ← (Phase 6) Flow → Go source
│   ├── examples/
│   │   └── hello.flow.json
│   └── go.mod
│
├── frontend/                ← (Phase 2) visual editor
│   ├── src/
│   │   ├── canvas/
│   │   ├── nodes/
│   │   ├── property-panel/
│   │   ├── trace-view/
│   │   └── flow-store.ts
│   ├── public/
│   ├── vite.config.ts
│   └── package.json
│
└── plugins/                 ← (Phase 4+) optional plugins, separate modules
    ├── kafka/
    ├── postgres/
    └── ...
```

---

## 10. Open Questions

Decisions deferred until the relevant phase forces them:

1. **Graph library for the frontend** — React Flow (embed as web component) vs Drawflow vs custom SVG. Spike during Phase 2 slice 2.
2. **Auth model** — defer to Phase 8. Until then, single-user, no auth.
3. **Multi-tenant model** — out of scope for self-hosted v1. Revisit if SaaS emerges.
4. **Flow versioning** — Phase 3 starts with "latest only." Versioning + history is Phase 8.
5. **Cluster runtime coordination** — Raft (embedded), etcd (external), or Postgres advisory locks? Decide in Phase 8.
6. **Codegen target depth** — does the generated code preserve plugin loading, or inline only what's used? Phase 6 design decision.
7. **Schema v2 trigger** — first feature that forces a breaking schema change defines v2. Likely candidates: explicit ports, edge types, subflows.

---

## 11. Risks

The things most likely to derail the project, with how we plan to address them.

| Risk | Mitigation |
|---|---|
| Visual editor UX takes longer than the entire backend | Use an existing graph library. No custom canvas. |
| Schema rewrites cascade through every layer | JSON schema is locked at 1.0 until end of Phase 3. Force-fit features into the existing shape. |
| Plugin API breaks on every release | Keep the `Node` / `ActionNode` / `TriggerNode` interfaces tiny and stable. Plugin SDK = those three interfaces + the registry. |
| Codegen complexity grows without bound | Start with a code generator that handles only the 5 built-in nodes. Plugin codegen is opt-in via plugin SDK additions. |
| Self-hosted ops burden scares users away | Single-binary, single-file SQLite, sensible defaults. No required external services until Phase 8. |
| Feature requests pull us toward Node-RED / n8n parity | Refuse. The wedge is Go-native + compile-to-binary. Catalog breadth is not the goal. |

---

## 12. How to Use This Document

- **New contributor**: read sections 1, 2, 3, 5. That's the orientation.
- **Picking up an unfinished phase**: read the relevant phase in section 8 + the linked detailed doc.
- **Proposing a new feature**: check section 3 (non-goals), section 7 (principles), and section 8 (whether it fits a phase or needs a new one).
- **Considering a schema change**: section 6, then 11 (the "schema rewrites" risk row).

Update this document when the answer to any of those questions changes.
