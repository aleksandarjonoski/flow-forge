# FlowForge Execution Engine — Design v1

Status: **Draft**
Scope: backend runtime that loads a Flow JSON document and executes it.
Audience: implementers of the Go engine.

This document is the contract for v1. If something here is wrong, change the doc first, then the code.

---

## 1. Goals & Non-Goals

### Goals (v1)
- Load a Flow JSON (per the v1 schema) and execute it.
- Support the 5 starter node types: `http_trigger`, `cron_trigger`, `http_request`, `transform`, `log`.
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

Why `NodeResults` is on the context: it lets a node read upstream results beyond its immediate predecessor without changing the `Execute` signature. Useful for transforms that reference multiple nodes (`$nodes.node-1.body.id`).

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
`engine.Run(cf *CompiledFlow) error`:
1. For each trigger, instantiate an `Emitter` bound to that trigger's downstream subgraph.
2. Call `trigger.Start(ctx, emit)`.
3. Block until `ctx` is cancelled, then call `Stop()` on each trigger.

### 4.5 Execute (per event)
When a trigger calls `emit(payload)`:
1. Create a fresh `ExecutionContext`.
2. Walk the trigger's downstream subgraph in topological order (§6).
3. For each action node, call `Execute(ctx, input)`. `input` = output of the predecessor node (see §10 for the "multiple predecessors" rule).
4. Store each result in `ctx.NodeResults[nodeID]`.
5. On error, halt the execution (§7).
6. Emit trace events at every step (§8).

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
Nodes that need data from elsewhere in the graph read `ctx.NodeResults[someNodeID]`. The `transform` node uses this for expressions like `$nodes.node-1.body.user`.

---

## 11. Built-in Nodes (v1)

Each lives in `backend/internal/nodes/<name>.go` and registers itself via `init()`.

### `http_trigger`
- Config: `path` (string, required), `method` (string, default `POST`).
- Lifecycle: registers a handler with the engine's shared HTTP server on `Start`. Each request becomes an `emit({"headers": ..., "body": ..., "query": ...})`.
- The HTTP server is owned by the engine, not the node — see §12.

### `cron_trigger`
- Config: `expression` (cron string, required).
- Lifecycle: uses `github.com/robfig/cron/v3`. Each tick → `emit({"firedAt": <RFC3339>})`.

### `http_request`
- Config: `url`, `method`, `headers` (map), `bodyFrom` (string, optional — JSON path into input).
- Output: `{"status": int, "headers": map, "body": any}`. Body is parsed as JSON if `Content-Type` is JSON, else as string.

### `transform`
- Config: `expression` (string, required) — an expression in [expr-lang/expr](https://github.com/expr-lang/expr) syntax.
- Available identifiers: `input`, `nodes`, `env`.
- Output: whatever the expression returns, coerced into `Payload`.

### `log`
- Config: `level` (string, default `info`), `message` (string, optional template).
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
