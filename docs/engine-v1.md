# FlowForge Execution Engine — Design v1

Status: **Draft**
Scope: backend runtime that loads a Flow JSON document and executes it.
Audience: implementers of the Go engine.

This document is the contract for v1. If something here is wrong, change the doc first, then the code.

---

## 1. Goals & Non-Goals

### Goals (v1)
- Load a Flow JSON (per the v1 schema) and execute it.
- Support the starter node types: `http_trigger`, `cron_trigger`, `http_request`, `transform`, `log`, and `parse_json` (added with [data-v1.md](data-v1.md)).
- Plugin-friendly: external packages can register new node types via a registry.
- Deterministic, debuggable execution: every step emits a trace event.
- Validate flows before running (cycles, dangling edges, unknown node types).
- Cancellation and timeouts via `context.Context`.

### Non-Goals (v1)
- Code generation (Go source emission). Interpreted execution only.
- Distributed/clustered execution.
- Persistent execution state, retries, durable queues.
- Parallel fan-out within a single execution. Sequential topo order is fine for v1.
- Merge-strategy semantics for nodes with multiple inbound edges (deferred to v2 — see §10).
- Subflows, error edges, streaming outputs.

---

## 2. Mental Model

A Flow is a DAG with two kinds of nodes:

- **Triggers** (sources) — emit events asynchronously. No input. They are the *entry points* of execution. Examples: `http_trigger`, `cron_trigger`.
- **Actions** (transforms / sinks) — consume an input payload, produce an output payload. Examples: `http_request`, `transform`, `log`.

A Flow may contain multiple triggers. Each trigger event spawns **one execution** — an independent run of the subgraph reachable from that trigger. Executions do not share state.

```
            [http_trigger /webhook]      [cron_trigger @hourly]
                     │                            │
                     ▼                            ▼
              [transform]                    [http_request]
                     │                            │
                     ▼                            ▼
                  [log]                        [log]
```

Two triggers → two independent execution paths. One incoming HTTP request → one execution down the left arm. Cron tick → one execution down the right arm.

---

## 3. Core Types

All types live in `backend/internal/engine` unless noted.

### 3.1 Payload

```go
// Payload is the data that flows between nodes.
// map[string]any is intentional: keeps the schema-less node config
// philosophy consistent at runtime, and serializes cleanly to JSON.
type Payload map[string]any
```

### 3.2 Node interfaces

Two interfaces, because triggers and actions have fundamentally different lifecycles.

```go
// Node is the base contract every node type implements.
type Node interface {
    Type() string                          // e.g. "http_request"
    Init(config map[string]any) error      // called once at flow load
}

// ActionNode is invoked synchronously during execution.
// Input is the upstream node's output (or nil for the first action after a trigger).
type ActionNode interface {
    Node
    Execute(ctx *ExecutionContext, input Payload) (Payload, error)
}

// TriggerNode runs for the lifetime of the flow.
// It calls `emit` whenever an event occurs; the engine spawns
// an execution per call.
type TriggerNode interface {
    Node
    Start(ctx context.Context, emit Emitter) error
    Stop() error
}

type Emitter func(Payload) error
```

A type must satisfy exactly one of `ActionNode` / `TriggerNode`. The registry enforces this.

### 3.3 ExecutionContext

One per execution. Passed by pointer to every action node.

```go
type ExecutionContext struct {
    FlowID      string
    ExecutionID string                  // UUID, unique per run
    NodeResults map[string]Payload      // nodeID → output, populated as we go
    Logger      Logger
    Trace       TraceSink
    GoCtx       context.Context         // for cancellation/timeouts
}
```

Why `NodeResults` is on the context: it lets a node read upstream results beyond its immediate predecessor without changing the `Execute` signature. Useful for transforms that reference multiple nodes (`nodes["node-1"].body.id`).

### 3.4 Flow model (in `internal/flow`)

Mirrors the JSON schema 1:1.

```go
type Flow struct {
    ID       string
    Name     string
    Version  string
    Nodes    []NodeDef
    Edges    []Edge
    Metadata Metadata
}

type NodeDef struct {
    ID       string
    Type     string
    Name     string
    Position Position
    Config   map[string]any
}

type Edge struct {
    ID           string
    Source       string
    SourceHandle string  // unused in v1, reserved
    Target       string
    TargetHandle string  // unused in v1, reserved
}
```

---

## 4. Lifecycle

```
JSON file ──parse──► Flow struct ──validate──► CompiledFlow ──run──► Engine running
                                                                      │
                                                  trigger fires ──────┤
                                                                      ▼
                                                              Execution (goroutine)
```

### 4.1 Parse
`flow.Parse(r io.Reader) (*Flow, error)` — straight `encoding/json` decode.

### 4.2 Validate
`engine.Validate(f *flow.Flow, reg *Registry) error` checks (without
instantiating any node):
1. All `Node.Type` values are registered.
2. Every edge endpoint references an existing node.
3. No node has more than one inbound edge (merge nodes are deferred — §10).
4. The graph is a **DAG** (cycle detection — see §6).

All errors are joined via `errors.Join` so a single pass surfaces every
problem.

Instantiation-dependent checks are performed by `engine.Compile` (§4.3):
- There is at least one trigger node.
- Triggers have no inbound edges.
- Every node implements exactly one of `ActionNode` / `TriggerNode`.

**Layering note:** The validator lives in `engine/`, not `flow/`, because
cycle detection depends on the graph algorithms in `engine/topo.go`. Putting
the validator in `flow/` would force either a circular import or a third
shared package. `flow` stays purely the document model; `engine` owns all
graph-level semantics.

Duplicate-node-ID checks happen earlier, in `flow.Parse`, since they don't
require any registry context.

### 4.3 Compile
Validation produces a `CompiledFlow`:

```go
type CompiledFlow struct {
    Flow      *flow.Flow
    Nodes     map[string]Node       // nodeID → instantiated node (Init'd)
    Adjacency map[string][]string   // nodeID → downstream nodeIDs
    Triggers  []string              // nodeIDs of triggers (sorted)
    TopoOrder map[string][]string   // triggerID → topo-sorted reachable subgraph (incl. trigger)
}
```

`Init(config)` is called here, **once per node**, so config errors surface at load time, not at execution time.

### 4.4 Run
`engine.Run(ctx context.Context, cf *CompiledFlow, svc *EngineServices, sink TraceSink) error`:
1. Build one `Executor` for `cf` that traces to `sink`.
2. For each trigger, build an `Emitter` bound to that trigger's subgraph. Each `emit` call starts one execution in its own goroutine (§9) and returns immediately; execution errors are reported through the trace, not to the trigger. Once shutdown begins, `emit` returns `ErrEngineStopped`.
3. Call `trigger.Start(ctx, emit)` for each trigger. If one fails, the triggers already started are stopped and `Run` returns the error.
4. Start `svc.Cron`.
5. Block until `ctx` is cancelled. Then: stop the scheduler (waiting for running cron jobs), call `Stop()` on each trigger, and wait for in-flight executions — the cancelled context makes them abort between nodes. Returns the joined `Stop()` errors.

`svc` must be the same services passed to `Compile`. `Run` does not listen on a port: the caller mounts `svc.HTTP` in an `http.Server` (§4.6).

`Run` is built from two halves, for callers that run many flows and must know whether each one started (the server, [server-v1.md](server-v1.md) §4.2):
- `engine.Start(cf, svc, sink) (*Instance, error)` does steps 1–4 and returns once every trigger is started, or with the first start error after stopping the ones already started.
- `Instance.Stop() error` does step 5: it cancels the instance's context (in-flight executions abort between nodes), stops the scheduler and the triggers, waits for the executions, and returns the joined `Stop()` errors. Calling it again returns the same result.

`Run` is `Start`, then wait for `ctx`, then `Stop`.

### 4.5 Execute (per event)
When a trigger calls `emit(payload)`:
1. Create a fresh `ExecutionContext`.
2. Walk the trigger's downstream subgraph in topological order (§6).
3. For each action node, call `Execute(ctx, input)`. `input` = output of the predecessor node (see §10 for the "multiple predecessors" rule).
4. Store each result in `ctx.NodeResults[nodeID]`.
5. On error, halt the execution (§7).
6. Emit trace events at every step (§8).

### 4.6 CLI
`flowforge run [-addr :8080] <flow.json>`:
- Parses and compiles the flow against `DefaultRegistry` (all built-in nodes), serves `svc.HTTP` on `-addr`, and calls `engine.Run`.
- Trace events go to **stdout** as JSON lines (`StdoutTraceSink`). `slog` output — including the `log` node — goes to **stderr** as text, with all levels enabled.
- `SIGINT` / `SIGTERM` shut down gracefully: the HTTP server stops accepting requests (5 s grace), then the engine stops.
- Exit codes: `0` clean shutdown, `1` load/compile/runtime error, `2` usage error.

`flowforge serve [-addr 127.0.0.1:8090]` serves the HTTP API used by the visual editor instead of running one flow; see [api-v1.md](api-v1.md). It uses the same exit codes.

---

## 5. Plugin Registry

`internal/engine/registry.go`:

```go
type NodeFactory func() Node

type Registry struct {
    factories map[string]NodeFactory
}

func NewRegistry() *Registry
func (r *Registry) Register(typeName string, f NodeFactory)
func (r *Registry) Create(typeName string) (Node, error)
func (r *Registry) Has(typeName string) bool
```

- A `DefaultRegistry` global is provided for convenience; built-in nodes register themselves via `init()`.
- External plugins register against a passed-in registry or the default. We do **not** use Go's `plugin` package — too fragile across platforms. Plugins are compile-time linked in v1.
- `Create` returns a new instance each time so node state cannot leak between flows.

---

## 6. Graph Algorithms

### 6.1 Cycle detection

DFS with three-color marking on the full graph. Any back-edge into a `GRAY` node is a cycle. Implemented in `engine/topo.go`:

```go
func detectCycle(adj map[string][]string) (cycle []string, ok bool)
```

Returns the cycle path on failure so the error message is actionable.

### 6.2 Topological sort

Kahn's algorithm (BFS with in-degree counting) — stable, easy to debug. Returns nodes in execution order.

For v1 we compute a topo order **per trigger's reachable subgraph**, not the whole flow. This matters because two triggers might share downstream nodes (rare but legal), and we want each execution to walk its own slice in correct order.

```go
func reachable(adj map[string][]string, from string) map[string]bool
func topoSort(adj map[string][]string, only map[string]bool) ([]string, error)
```

### 6.3 Reachability

Plain BFS from the trigger node, following outgoing edges.

---

## 7. Error Handling

v1 policy: **halt on first error**.

When `node.Execute` returns a non-nil error:
1. Emit `NodeFailed` trace event with the error and elapsed time.
2. Wrap the error: `fmt.Errorf("node %s (%s): %w", nodeID, nodeType, err)`.
3. Emit `ExecutionFailed` trace event.
4. The execution goroutine returns. Other concurrent executions are unaffected.

There is no automatic retry, no error-edge routing, no compensating actions. Those are v2.

If a node panics, the executor recovers and treats it as `ExecutionFailed` with the panic value as the error.

---

## 8. Tracing

Every observable step emits a `TraceEvent`. The engine never logs directly — it writes to the `TraceSink`. This is the hook the UI will eventually consume over a websocket.

```go
type TraceEvent struct {
    Kind        TraceKind   // ExecutionStarted | NodeStarted | NodeCompleted | NodeFailed | ExecutionCompleted | ExecutionFailed
    ExecutionID string
    NodeID      string      // empty for execution-level events
    NodeType    string
    Timestamp   time.Time
    Duration    time.Duration  // for *Completed/*Failed
    Input       Payload        // sampled; may be truncated
    Output      Payload        // for NodeCompleted
    Err         string         // for *Failed
}

type TraceSink interface {
    Emit(TraceEvent)
}
```

Provided sinks:
- `StdoutTraceSink` — one JSON line per event. Default for the CLI.
- `MemoryTraceSink` — ring buffer, useful in tests and for the future step-debugger.
- `MultiSink(...)` — fan-out.

Input/output payloads are recorded by reference in v1 (no deep copy). If a node mutates a map it received, the trace will reflect the mutated value. We accept this trade-off in v1 and revisit if it bites.

---

## 9. Concurrency Model

- The engine runs `len(triggers)` long-lived goroutines (one per trigger).
- Each trigger event spawns **one** execution goroutine. No fan-out within an execution in v1.
- All cancellation flows through `GoCtx`. Stopping the engine cancels the root context, which propagates: triggers stop emitting, in-flight executions check `ctx.GoCtx.Err()` between nodes and abort.
- Nodes are expected to respect `ctx.GoCtx` for any blocking I/O (`http.NewRequestWithContext`, etc.).
- The registry is read-only after `Run` starts. Concurrent reads are safe.

---

## 10. Data Flow Between Nodes

### Single inbound edge (the common case)
`input` to `Execute` is the predecessor's output payload.

### No inbound edges (first action after a trigger)
`input` is the trigger's emitted payload.

### Multiple inbound edges (merge node)
**Deferred to v2.** Validator rejects this configuration in v1 with a clear error:
`"node %s has %d inbound edges; merge nodes are not supported in v1"`.

Rationale: choosing merge semantics (zip, join-on-key, wait-all, race) is a design decision that should not be made implicitly. We'd rather block it than ship a surprising default.

### Reading non-predecessor results
Nodes that need data from elsewhere in the graph read `ctx.NodeResults[someNodeID]`. The `transform` node uses this for expressions like `nodes["node-1"].body.user`.

---

## 11. Built-in Nodes (v1)

Each lives in `backend/internal/nodes/<name>.go` and registers itself via `init()`.

### `http_trigger`
- Config: `path` (string, required, must start with `/`), `method` (string, default `POST`, case-insensitive).
- `Init` requires `services.HTTP`; a missing mux is a load-time error.
- Lifecycle: registers a handler with the engine's shared `HTTPMux` on `Start` and removes it on `Stop`. Each request becomes an `emit({"headers": ..., "body": ..., "query": ...})`:
  - `headers` — `map[string]any`, canonical header name → values joined with `", "`.
  - `query` — `map[string]any`, parameter → first value.
  - `body` — parsed JSON if `Content-Type` is `application/json` (or `+json`), otherwise the raw body as a string; `nil` if empty.
- Responses: `202 Accepted` once `emit` returns without error. The flow's output is **not** returned to the caller — request/response flows are v2. Invalid JSON → `400`, body over 1 MiB → `413`, `emit` error → `500`.
- The HTTP server is owned by the engine, not the node — see §12.

### `cron_trigger`
- Config: `expression` (cron string, required). Standard 5-field syntax (`minute hour dom month dow`) plus descriptors (`@hourly`, `@daily`, `@every 30s`, …). An optional `CRON_TZ=Europe/Amsterdam ` prefix sets the time zone; otherwise the process's local zone applies.
- The expression is parsed at `Init`, so an invalid schedule fails at load time. `Init` requires `services.Cron`.
- Lifecycle: uses `github.com/robfig/cron/v3`. `Start` adds a job to the engine's shared scheduler, `Stop` removes it. Each tick → `emit({"firedAt": <RFC3339, UTC>})`.
- Ticks run in the scheduler's own goroutines, so a slow execution never delays the next tick. An `emit` error has no caller to report to, so it is logged via `slog`.

### `http_request`
- Config: `url` (string, required, absolute `http`/`https` URL), `method` (string, default `GET`, case-insensitive), `headers` (map of string → string, optional), `bodyFrom` (string, optional).
- `bodyFrom` is a dot-separated path into the input payload (e.g. `body` or `body.user`). The value found there is JSON-encoded as the request body, and `Content-Type: application/json` is set unless `headers` sets it. A path that does not resolve fails the node. Without `bodyFrom` the request has no body.
- The request uses `ctx.GoCtx`, so engine shutdown cancels in-flight calls. A 30 s client timeout is a safety net until per-node timeouts exist (§14).
- Output: `{"status": int, "headers": map, "body": any}`. `headers` uses the same flattening as `http_trigger`. Body is parsed as JSON if `Content-Type` is JSON, else returned as a string; `nil` if empty. Response bodies over 10 MiB fail the node.
- A non-2xx status is **not** an error: the status is data in the output. Transport failures (DNS, connection refused, timeout, invalid JSON response) fail the node (§7).

### `transform`
Two modes, chosen per node: **expression** (below) or **mapping** — `schema` + `mapping` config that builds the output field by field and validates it against a declared schema; see [data-v1.md](data-v1.md) §5. A node uses one mode, never both.

Expression mode:
- Config: `expression` (string, required) — an expression in [expr-lang/expr](https://github.com/expr-lang/expr) syntax.
- The expression is compiled once at `Init`, so syntax errors and unknown identifiers fail at load time. The compiled program is immutable and shared by concurrent executions.
- Available identifiers:
  - `input` — the node's input payload.
  - `nodes` — `ctx.NodeResults` (nodeID → output). Node IDs usually contain `-`, so use index syntax: `nodes["node-1"].body.id`.
  - `env` — process environment variables (`map[string]string`), snapshotted at `Init`.
- Output: whatever the expression returns, coerced into `Payload`:
  - a map (`map[string]any`) is used as-is;
  - `nil` yields a `nil` payload;
  - any other value (string, number, slice, …) is wrapped as `{"value": <result>}`.
- A runtime evaluation error fails the node (§7).

### `parse_json`
- Config: `from` (dot path into the input, optional — the whole input when empty), `schema` (required — a declared schema, [data-v1.md](data-v1.md) §3).
- Takes the value at `from`; a string is parsed as JSON, structured data is used as is. Validates it against `schema` (strict types, extra fields dropped) and outputs it. Any problem fails the node with every problem listed.
- Full specification: [data-v1.md](data-v1.md) §4.

### `log`
- Config: `level` (string, one of `debug`/`info`/`warn`/`error`, default `info`; anything else is an `Init` error), `message` (string, optional).
- `message` is a Go `text/template`, parsed at `Init`, rendered with `.input` (the input payload) and `.nodes` (`ctx.NodeResults`). Example: `"hello {{.input.body.name}}"`. Default message: `"payload"`. A render error fails the node.
- Writes one record through `log/slog`'s default logger with attributes `flow_id`, `execution_id` and `payload` (the input). A dedicated `Logger` on `ExecutionContext` (§3.3) is deferred until the CLI needs to route it.
- Output: passes input through unchanged (so `log` can be inserted mid-flow without breaking downstream nodes).

---

## 12. Engine-Owned Services

Some node types need shared infrastructure. The engine owns:
- **HTTP server** — one `http.Server` shared by all `http_trigger` nodes. Each trigger registers its `path`+`method` on `Start`. Listen address comes from engine config.
- **Cron scheduler** — one `cron.Cron` instance.

These are exposed to nodes via the `EngineServices` struct injected at `Init`:

```go
type EngineServices struct {
    HTTP *HTTPMux       // wrapper around http.ServeMux with method awareness
    Cron *cron.Cron
}

// Node Init receives services as well as config.
// This means the interface from §3.2 is actually:
type Node interface {
    Type() string
    Init(config map[string]any, services *EngineServices) error
}
```

(Yes, this is a refinement of §3.2. I'm leaving §3.2 minimal so the *concept* is clear, and adding the services parameter here where the rationale is.)

`HTTPMux` is a plain `http.Handler` keyed by exact method + path. Unlike `http.ServeMux` it supports removing routes, which `Stop` needs. A request to an unknown path gets `404`; a known path with the wrong method gets `405` with an `Allow` header. Registering the same method + path twice is an error. The mux does not listen itself: whoever runs the engine (the CLI, slice 10) mounts it in an `http.Server` on the configured address.

A server running many flows shares one route table between them. `mux.Owned(flowID)` returns a view of the mux that registers routes as that flow's: a clash with another flow's route names it (`route POST /orders is used by flow order-api`), and `Remove` only removes the view's own routes. Serving any view serves every route.

The cron scheduler works the same way: nodes only add and remove jobs, and whoever runs the engine calls `Cron.Start()` / `Cron.Stop()`.

---

## 13. Folder Layout (engine slice only)

```
backend/
├── cmd/flowforge/
│   └── main.go                # CLI: `flowforge run <flow.json>`
├── internal/
│   ├── flow/
│   │   ├── flow.go            # structs
│   │   └── parser.go          # JSON → Flow (+ basic structural checks)
│   ├── engine/
│   │   ├── engine.go          # Engine, Run, Stop
│   │   ├── validate.go        # graph-level validation (cycles, edges, types)
│   │   ├── compile.go         # Flow → CompiledFlow
│   │   ├── executor.go        # per-execution runner
│   │   ├── context.go         # ExecutionContext
│   │   ├── registry.go        # NodeFactory registry
│   │   ├── services.go        # EngineServices, HTTPMux
│   │   ├── topo.go            # cycle detection, topo sort, reachability
│   │   └── trace.go           # TraceEvent, TraceSink, sinks
│   └── nodes/
│       ├── http_trigger.go
│       ├── cron_trigger.go
│       ├── http_request.go
│       ├── transform.go
│       └── log.go
├── examples/
│   └── hello.flow.json
└── go.mod
```

---

## 14. Open Questions

These are explicitly punted to v2 but worth flagging so the v1 code doesn't paint us into a corner:

1. **Merge semantics** (§10). The validator rejects multi-inbound for now; the model needs design before we lift that.
2. **Streaming triggers**. `Emitter` is a `func(Payload) error` — fine for discrete events. Kafka consumers that want to emit a continuous stream are awkward. Probably needs `EmitterContext` later.
3. **Per-node timeouts**. v1 uses the engine-wide context. Per-node `timeout` config is trivially additive.
4. **Hot reload**. v1 reads the JSON once at startup. Watching the file and recompiling is a small, separable feature.
5. **Persistent execution state**. v1 is fully in-memory; restart loses everything. v2 needs a journal.

---

## 15. Implementation Order

Suggested commit slices, each independently runnable/testable:

1. `flow` package — structs + parser + tests against the v1 JSON schema.
2. `engine/topo.go` — cycle detection + topo sort + reachability + tests on hand-built graphs.
3. `engine/registry.go` + `engine/trace.go` — registry, trace events, stdout sink.
4. `engine/validate.go` + `engine/compile.go` — Flow → CompiledFlow with full validation.
5. `engine/executor.go` — single-execution runner against a hardcoded in-memory trigger that fires once.
6. `nodes/log.go` + `nodes/transform.go` — pure nodes, no I/O. Wire end-to-end with a fake trigger.
7. `engine/services.go` + `nodes/http_trigger.go` — real HTTP server, real trigger.
8. `nodes/http_request.go` — outbound HTTP.
9. `nodes/cron_trigger.go` — scheduled trigger.
10. `cmd/flowforge/main.go` — CLI wiring, `flowforge run <file>`.

After step 10, this is runnable end-to-end:

```bash
flowforge run examples/hello.flow.json
# POST localhost:8080/webhook → transform → log
```

That is the v1 milestone.
