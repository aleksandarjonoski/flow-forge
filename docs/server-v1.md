# FlowForge Server & Storage — Design v1

Status: **Draft — for review**
Scope: Phase 3 — one `flowforge serve` process that stores flows, runs many of them at once, keeps their run history, and serves the editor.
Audience: implementers of the store, the flow host, the API and the editor's flow library.

This is the contract for Phase 3. If something here is wrong, change the doc first, then the code. It builds on [engine-v1.md](engine-v1.md) (§4.4 Run, §8 tracing, §12 services) and [api-v1.md](api-v1.md) (test runs, error shape). The roadmap is in [design.md](design.md) §8.

---

## 1. Goals & Non-Goals

### Goals
- **Install once, deploy many flows.** One binary, copied onto a machine, hosts any number of flows. Nothing else to install: no Go, no Node, no database server.
- **A flow library.** Flows are saved on the server, not only as files on the user's machine.
- **Deploy and stop from the editor.** A deployed flow keeps running, and starts again when the server restarts.
- **Run history.** Every execution of a deployed flow is recorded with its trace, and can be opened and replayed in the editor.
- **One log line per execution**, so a standard log shipper gives basic monitoring with no work in the flows.
- **Static, cross-platform build.** `CGO_ENABLED=0`; the same code builds for Linux, macOS and Windows.

### Non-Goals (this version)
- **Authentication.** The editor/API stays local-only, as in Phase 2 (§9). Token auth is the first item of Phase 4.
- **Flow versioning and history.** The library keeps the latest saved document only; a revision number guards against lost updates (§3.2). Git-backed history is Phase 10.
- **Hot swap.** Redeploying a running flow stops it and starts the new revision; requests arriving in between get `404` (§4.4). Zero-downtime reload is Phase 7.
- **Live push of run history to the editor.** The editor polls (§7.3). Streaming is Phase 9.
- **Clustering.** One process. Phase 10.
- **Storing test runs.** Runs started from the editor's Run button stay ephemeral (§5.1).

---

## 2. Overview

```
                    flowforge serve
┌────────────────────────────────────────────────────────────┐
│  editor listener  (-addr, default 127.0.0.1:8090)          │
│    /            the editor (embedded frontend/dist)        │
│    /api/...     flow library, deploy/stop, history, runs   │
│                                                            │
│  flows listener   (-flows-addr, default :8080)             │
│    every http_trigger route of every running flow          │
│                                                            │
│  host  ── runs N flows, each with its own engine instance  │
│  store ── SQLite file in the data directory                │
└────────────────────────────────────────────────────────────┘
```

**Two listeners, on purpose.** The editor/API has no authentication and can run arbitrary flows, so it binds to loopback by default. The flows' own endpoints are what the outside world calls, so they bind to all interfaces by default, like `flowforge run`. Keeping them apart means opening one never exposes the other.

New code:

| Package | Owns |
|---|---|
| `internal/store` | SQLite: schema migrations, flows, executions, trace events, retention (§3, §5) |
| `internal/host` | The running flows: start, stop, state, shared trigger mux, autostart (§4) |
| `internal/api` | Grows from test runs to the full API (§6) |
| `internal/webui` | The embedded editor (§8) |

`flowforge run <file>` stays as it is — one flow, no database — apart from the run log line (§5.3).

---

## 3. Storage

### 3.1 Database

- **One SQLite file**, `flowforge.db`, in the **data directory**: `-data <dir>`, else `$FLOWFORGE_DATA`, else `~/.flowforge`. Created with mode `0700` if missing.
- **Driver: `modernc.org/sqlite`** — pure Go, so the build stays `CGO_ENABLED=0` and cross-compiles. (`mattn/go-sqlite3` needs CGO.)
- Opened with WAL journal mode, `foreign_keys=ON` and a 5 s busy timeout. A single connection is used for writes (`SetMaxOpenConns(1)` in v1), which is far more than one server needs and rules out write contention.
- **Only one server per data directory.** The store takes an exclusive lock file (`flowforge.lock`) on open; a second `flowforge serve` on the same directory exits with a clear error.

### 3.2 Schema

```sql
CREATE TABLE flows (
  id         TEXT PRIMARY KEY,      -- the document's "id"
  name       TEXT NOT NULL,         -- copied from the document, for listing
  document   TEXT NOT NULL,         -- the flow JSON, stored verbatim
  revision   INTEGER NOT NULL,      -- 1 on create, +1 on every save
  enabled    INTEGER NOT NULL DEFAULT 0, -- deployed: should run while the server runs
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE executions (
  id             TEXT PRIMARY KEY,  -- the engine's execution ID
  flow_id        TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
  revision       INTEGER NOT NULL,  -- the flow revision that ran
  trigger_id     TEXT NOT NULL,
  status         TEXT NOT NULL,     -- running | completed | failed | interrupted
  error          TEXT,
  started_at     TEXT NOT NULL,
  finished_at    TEXT,
  trace_complete INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX executions_by_flow ON executions(flow_id, started_at DESC, id DESC);

CREATE TABLE trace_events (
  execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,    -- order within the execution, from 0
  event        TEXT NOT NULL,       -- the TraceEvent as JSON, same shape as the API
  PRIMARY KEY (execution_id, seq)
);
```

- **Timestamps** are RFC 3339 UTC with milliseconds (`2026-10-31T14:03:07.123Z`), so text order is time order.
- **The document is stored verbatim** — the exact bytes the editor sent, which is the canonical form (design.md §7.2), plus one trailing newline (a JSON value inside a request body cannot carry the newline a canonical file ends with). The server parses it (§6.2) but never re-serializes it, so the stored text stays diff-friendly. API responses carry the document as a JSON value, which Go's encoder compacts; the editor writes files in canonical form itself.
- **What lives outside the document:** `revision`, `enabled` and the timestamps are server state about a flow, not part of the flow. This keeps design.md §7.1: everything that defines *what a flow does* is in its JSON; the database only adds *where it is deployed*.
- **Trace events are stored as JSON blobs** in the API shape, so reading a trace back is one query and no mapping.

### 3.3 Migrations

Homegrown and small:
- Migrations are numbered SQL files, `internal/store/migrations/0001_init.sql`, …, embedded with `go:embed`.
- The applied version is SQLite's `PRAGMA user_version`. On open, each newer migration runs in its own transaction, then `user_version` is set.
- A database whose version is **newer** than the binary knows is refused ("database was created by a newer FlowForge"), so an old binary never damages a new database.
- No down-migrations. Downgrades restore a backup.

### 3.4 Backup

The data directory *is* the installation's state. Backup is `sqlite3 flowforge.db ".backup copy.db"` or, with the server stopped, copying the file. Documented in the README; a built-in backup command can come later.

---

## 4. The Flow Host

### 4.1 States

Each stored flow is in one of these states, shown in the editor:

| State | Meaning |
|---|---|
| `stopped` | Not deployed (`enabled = 0`). |
| `running` | Deployed and running. Reports `runningRevision`. |
| `failed` | Deployed, but it could not start: compile error, route conflict, port problem. Reports the error. Stays `enabled`, so fixing and redeploying is enough. |

`running` with `runningRevision < revision` means **saved changes are not deployed yet**; the editor says so (§7.2).

### 4.2 One engine instance per flow

Each running flow is compiled into its own `CompiledFlow` with its own `EngineServices`:
- **HTTP:** every flow shares **one trigger mux**, served on the flows listener. A route belongs to one flow at a time.
- **Cron:** each flow gets its own scheduler (`cron.New()`), started and stopped with it. Schedulers are cheap and this keeps `Run`'s start/stop rules unchanged.

**Engine change.** `engine.Run` blocks until its context ends, so a host could not tell "started" from "still starting". engine-v1.md §4.4 gains a split form:

```go
// Start starts every trigger of cf and returns once they are all started,
// or the first start error (after stopping the ones already started).
func Start(cf *CompiledFlow, svc *EngineServices, sink TraceSink) (*Instance, error)

// Stop stops the scheduler, the triggers and waits for in-flight executions,
// exactly as Run's shutdown does (§4.4 step 5).
func (i *Instance) Stop() error
```

`Run` becomes `Start`, wait for `ctx`, `Stop` — same behaviour, so `flowforge run` is unaffected.

### 4.3 Route ownership

- Two running flows cannot claim the same method + path. The shared mux already refuses a second registration (engine-v1.md §12); the error is extended to **name the flow that holds the route**: `route POST /orders is used by flow order-api`. Each flow registers through a view of the mux tagged with its flow ID, so the mux knows owners without knowing node types.
- A conflict fails the start: the flow becomes `failed` with that message, and the other flow is untouched.
- The check happens at start, not at save, so two flows may *both* be saved with the same route; only one can run.

### 4.4 Start, redeploy, stop

**Start** (`POST /api/flows/{id}/start`) always means "run the latest saved revision":
1. Parse and compile the latest document. A compile error answers `400` with the problems and changes nothing — a running older revision keeps running.
2. If the flow is running, stop it (step 4).
3. `engine.Start` the new instance. On success: `running`. On failure (route conflict, …): `failed` with the error.
4. Set `enabled = 1`.

Compiling first means the common mistakes (bad config, invalid expression) never take a running flow down. Only a start-time failure, like a route conflict, can leave a previously running flow stopped; the state says why.

**Stop** (`POST /api/flows/{id}/stop`) stops the instance (in-flight executions finish or are cancelled, engine-v1.md §9) and sets `enabled = 0`.

**Delete** stops the flow if it is running, then deletes it with all its executions.

### 4.5 Server start and shutdown

- **On start:** first, any execution still `running` in the database was cut off by a crash or kill; it is marked `interrupted`. Then every `enabled` flow is started, one by one. A flow that fails to start becomes `failed`; the server keeps going. A summary is logged (`started 12 flows, 1 failed`).
- **On `SIGINT` / `SIGTERM`:** both listeners stop accepting requests (5 s grace, as today), then every flow is stopped, then pending history writes are flushed (§5.2), then the store closes. `enabled` is left as it is, so the same flows come back on the next start.

---

## 5. Run History and Run Logs

### 5.1 What is recorded

Every execution of a **deployed** flow: one `executions` row and all its trace events. Test runs from the editor (`POST /api/flows/run`) are not recorded; they are already shown in full in the editor.

Each flow's engine instance traces to a sink that tags events with the flow ID and revision and hands them to the store writer (§5.2) and the run logger (§5.3).

### 5.2 The writer

Executions can be frequent; the hot path must not wait for the disk.
- The sink puts events on a **bounded queue** (10 000 events). One writer goroutine drains it and writes in batches, one transaction per batch.
- `execution_started` creates the row (`running`); `execution_completed` / `execution_failed` sets `status`, `error` and `finished_at`.
- **When the queue is full**, node-level events are **dropped**, and their execution is marked `trace_complete = 0` (the editor shows "trace incomplete"). Execution-level events are never dropped — the sink waits for room — so every execution always gets its final status. A warning with the number of dropped events is logged at most once a minute.
- **Large payloads:** a node's input or output whose JSON is over **64 KiB** is stored as `{"truncated": true, "bytes": <size>}`. The engine already allows sampling here (engine-v1.md §8).

### 5.3 Run log line

Every finished execution — in `flowforge serve` and `flowforge run` — writes one structured log line through `slog`:

```
level=INFO msg=execution flow=order-api revision=3 execution=af34… trigger=node-1 status=completed duration=12ms
level=ERROR msg=execution flow=order-api revision=3 execution=b71c… trigger=node-1 status=failed duration=4ms error="node node-2 (transform): …"
```

- `-log-format text|json` chooses the slog handler; `text` is the default (as today), `json` is for log shippers (Fluent Bit, Vector, Promtail → Loki, Elasticsearch, …). In JSON, `duration` is in nanoseconds (slog's encoding of a duration); text shows it as `12ms`.
- `flowforge run` has no revision, so its line has no `revision` attribute.
- Server and run logs go to **stderr**. `flowforge run` keeps writing trace events to **stdout**.
- This is the technical log of every run, at no cost to flow authors. `log` nodes remain for business messages.

### 5.4 Retention

The newest **1000 executions per flow** are kept (`-keep-executions`, `0` = keep everything). The writer prunes older ones every minute; trace events go with them (`ON DELETE CASCADE`).

---

## 6. API

The editor's API, on the editor listener. Phase 2's rules carry over ([api-v1.md](api-v1.md)): JSON bodies, 1 MiB request limit, unknown request fields rejected, and the error shape `{"error": "…", "problems": [ … ]}`. These endpoints move into api-v1.md as they are built: the flow library and run history are there now.

### 6.1 Endpoints

| Method + path | Does |
|---|---|
| `GET /api/health` | `200 {"status":"ok"}` once the store is open and autostart is done. |
| `GET /api/flows` | List every flow: summary + state (§6.3), ordered by name. |
| `POST /api/flows` | Create a flow from `{"flow": {…}}`. `201` with the stored flow. `409` if the ID exists. |
| `GET /api/flows/{id}` | The stored flow (§6.3). `404` if unknown. |
| `PUT /api/flows/{id}` | Save `{"flow": {…}, "revision": n}`. `200` with the stored flow, `revision` + 1. |
| `DELETE /api/flows/{id}` | Stop if running, delete with its history. `204`. |
| `POST /api/flows/{id}/start` | Deploy the latest revision (§4.4). `200` with the flow; `400` + problems if it does not compile. |
| `POST /api/flows/{id}/stop` | Stop and undeploy. `200` with the flow. |
| `GET /api/executions?flow={id}&limit=50&before={cursor}` | One page of executions, newest first (§6.4). |
| `GET /api/executions/{id}` | One execution with its full trace. |
| `POST /api/flows/run` | Test run — unchanged (api-v1.md). |

### 6.2 Saving rules

- **A save must parse; it need not compile.** The document must pass `flow.Parse` — what the editor's Open already requires — but a flow with a missing required config or a bad expression can still be saved, so work in progress is never lost. Compiling is checked on **start**.
- **The ID is fixed.** On `PUT`, the document's `id` must equal the path's `{id}`, else `400`. IDs must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, checked on create, because they appear in URLs and log lines.
- **Lost-update guard.** `PUT` carries the `revision` the editor loaded. If the stored revision differs — another tab or person saved meanwhile — the answer is `409` with `{"error": "…", "current": <the stored flow, §6.3>}`, and nothing is written. The editor then offers to reload or overwrite (overwrite = save again with the new revision).

### 6.3 The stored flow

`GET /api/flows/{id}`, and every endpoint that returns "the flow":

```json
{
  "flow": { "id": "order-api", "version": "1.0", "nodes": [ … ], "edges": [ … ] },
  "revision": 3,
  "createdAt": "2026-10-30T09:12:44.501Z",
  "updatedAt": "2026-10-31T14:03:07.123Z",
  "state": "running",
  "runningRevision": 2,
  "error": ""
}
```

`GET /api/flows` returns `{"flows": [ … ]}` with the same objects **without `flow`**, plus its `name`.

### 6.4 Executions

```json
{
  "executions": [
    {
      "id": "af34e49d1bbf08df7c6ac39a1be15db3",
      "flowId": "order-api",
      "revision": 2,
      "trigger": "node-1",
      "status": "failed",
      "error": "node node-2 (transform): …",
      "startedAt": "2026-10-31T14:03:07.123Z",
      "finishedAt": "2026-10-31T14:03:07.127Z",
      "traceComplete": true
    }
  ],
  "next": "<cursor>"
}
```

- `flow` is required; `limit` defaults to 50, at most 200. `next` is an opaque cursor for the following (older) page, absent on the last page.
- `GET /api/executions/{id}` returns the same object plus `"trace": [ … ]`, the events in order — the same shape as a test run's `trace`, so the editor shows and replays it with the existing results panel.

---

## 7. Editor

The editor gains a server-backed **flow library**. It still works without a server for files (Open file… / Download), as in Phase 2.

### 7.1 Flow library

- The left column has two tabs: **Nodes** (the palette) and **Flows**. Flows lists every stored flow: name, ID, state badge (running / stopped / failed, with the reason as a tooltip), and "changes not deployed" when the running revision is older than the saved one. It is refreshed when shown, after every save/deploy/stop, and every 5 seconds while shown.
- Clicking a flow opens it, asking first if the open flow has unsaved changes. Each row has a **×** that deletes the flow and its run history, after a confirmation. Deleting the open flow keeps it in the editor as an unsaved flow that is no longer stored.
- The editor still starts with the hello example, not stored. **New** starts a flow that is not stored either; its first Save creates it on the server. The header says "not stored" for such a flow.
- The URL follows the open stored flow (`#/flows/order-api`), so a flow can be bookmarked and a page reload reopens it. Editing the hash opens that flow.
- If the API is unreachable, the list says to start `flowforge serve`, and files can still be opened and downloaded.

### 7.2 Header actions

| Action | Does |
|---|---|
| **Save** (Ctrl/⌘+S) | Saves to the server: create the first time, then `PUT` at the loaded revision. A flow with changes gets a new `metadata.updatedAt`. Edits made while the request is out stay unsaved. |
| **Deploy** | Save if needed, then start (§4.4). Compile problems are shown in the results panel, headed *Deploy*. A start failure (e.g. a route taken) is shown in the message bar. |
| **Stop** | Stop the flow. Shown while it is running or failed. |
| **History** | Opens the History tab of the bottom panel. Enabled for stored flows. |
| **Run…** | Test run, unchanged. |
| **Open file…** (Ctrl/⌘+O) | Import a local `.json` file as an unsaved flow. If its ID is already stored, the message bar offers *Open as a copy* (a free ID, `<id>-copy`, `<id>-copy-2`, …) or *Replace the stored one* (it becomes the stored flow's unsaved content; Save overwrites). |
| **Download** | Export the editor's flow as a canonical file. It does not count as saving. |

The header shows the state badge next to the flow name, and "changes not deployed" when the saved revision is ahead of the running one.

**The message bar** shows errors (red) and choices (blue) with buttons:
- A save refused with `409` (someone else saved): *Reload theirs* or *Overwrite with mine* (save again at their revision).
- A first save whose ID is already stored: *Open the stored one* or *Save mine as a copy*.
- An unreachable backend on Save: use Download to keep the flow as a file.

The editor never takes a newer revision from the library list: the revision stays the one it loaded or saved, so the lost-update guard holds.

### 7.3 Run history

- The bottom panel has two tabs, **Results** and **History**. History lists the open flow's executions, newest first: start time, status, duration, trigger, revision and error, with *Load more* for older pages. It is refreshed every 5 seconds while shown.
- Clicking an execution loads its trace into Results and plays it back on the canvas, exactly like a test run (playback speed and Replay included); the heading is *Run of <start time>*. A note says when the run used another revision than the one open, or when the canvas has unsaved changes, since the canvas may then differ from what ran.
- "Trace incomplete" and `interrupted` executions are marked as such; an interrupted one shows as failed with the reason. Durations come from stored times with millisecond precision; shorter runs show "< 1 ms".

---

## 8. One Binary

- `frontend/dist` is embedded in the Go binary (`internal/webui`, `go:embed`), and `flowforge serve` serves it at `/` on the editor listener. One file to copy; nothing else.
- `go:embed` cannot reach outside the module, so the build copies `frontend/dist` into `backend/internal/webui/dist/` first. That directory is git-ignored except a placeholder `index.html` which says the editor was not built; a plain `go build` therefore still works and serves the API.
- A **`Makefile`** at the root: `make build` (frontend build, copy, `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`), `make release` (the same for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64).
- The binary embeds **`time/tzdata`**, so `CRON_TZ=Europe/Skopje` schedules work on Windows and minimal containers that have no zoneinfo.
- During development nothing changes: Vite serves the editor and proxies `/api` to `flowforge serve`.

---

## 9. Security

Unchanged from Phase 2, and stated again because the server now runs flows permanently:
- The **editor listener has no authentication** and can deploy any flow, including ones that call any URL or read the server's environment. It binds to `127.0.0.1` by default. To use the editor on a remote server, use an SSH tunnel (`ssh -L 8090:127.0.0.1:8090 server`). Binding it elsewhere logs a warning at startup.
- The **flows listener** serves only the routes of running flows; nothing on it changes or reveals the server's state.
- Token authentication for the API is the first item of Phase 4.

---

## 10. CLI

```
flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data ~/.flowforge]
                [-keep-executions 1000] [-log-format text|json]
flowforge run [-addr :8080] [-log-format text|json] <flow.json>
```

Exit codes as today: `0` clean shutdown, `1` load or runtime error (including a locked data directory or a too-new database), `2` usage error.

---

## 11. Implementation Order

| # | Slice | Notes |
|---|---|---|
| 1 | Store | `internal/store`: SQLite via modernc, lock file, embedded migrations with `user_version`, flows with revisions and the lost-update guard, executions + trace events, retention (§3, §5.4) |
| 2 | Flow host | `engine.Start` / `Instance.Stop`; `internal/host`: one instance per flow, shared trigger mux with route owners, per-flow cron, states, autostart, `interrupted` marking (§4) |
| 3 | Flow API | CRUD, start/stop, health; `flowforge serve` gains `-data` and `-flows-addr` and the two listeners (§6.1–6.3, §10) |
| 4 | Run history | History sink + batched writer with drop policy and truncation; `GET /api/executions…` (§5.1–5.2, §6.4) |
| 5 | Run logs | One `slog` line per execution in `serve` and `run`; `-log-format` (§5.3) |
| 6 | Editor flow library | Flows list, Save/Deploy/Stop, state badges, `409` handling, import/export, History tab with replay (§7) |
| 7 | One binary | Embedded editor, placeholder, `Makefile`, `time/tzdata`, cross-compile check for the five targets (§8) |

**Phase 3 done when:** on a machine with nothing installed, copying the `flowforge` binary and running `flowforge serve` gives an editor at `http://127.0.0.1:8090`; a flow built there can be deployed, called on `:8080`, and its executions opened in History; after restarting the server, the flow is running again.

---

## 12. Decisions Taken

- **SQLite, pure Go (`modernc.org/sqlite`).** No database server, static build. Slower than the CGO driver in benchmarks, which does not matter at this scale.
- **Homegrown migrations** over `golang-migrate`: ~50 lines, no dependency, and SQLite's `user_version` does the bookkeeping.
- **Documents stored verbatim**, not as columns: the JSON stays the source of truth and the stored text stays diff-friendly.
- **Two listeners**, so the unauthenticated editor is never exposed by exposing the flows.
- **Save does not require compile; start does.** Work in progress is never lost, and broken flows never deploy.
- **Compile before stopping** on redeploy, so a broken revision does not take down a running one.
- **Polling, not push**, for history in the editor. Simple; push comes with Phase 9.

## 13. Open Questions

1. **Importing many files at once.** Teams with dozens of flow files will want `flowforge push <dir>` (or a folder import in the editor). Small; add it once Phase 3 is in use and the need is concrete.
2. **Per-flow listener ports.** All flows share the flows listener. Some teams may want a flow on its own port. Not planned; revisit on demand.
3. **Retention by age.** Count-based retention is simple and bounds disk use; an age limit (`-keep-days`) can be added when someone needs it.
