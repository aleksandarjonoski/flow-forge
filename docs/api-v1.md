# FlowForge HTTP API — v1

Status: **Draft** (Phase 2 test runs; Phase 3 flow library)
Audience: implementers of the backend API and the frontend client.

This is the contract between the visual editor (Layer 1) and the backend (Layer 2, see [design.md](design.md) §5). If something here is wrong, change the doc first, then the code. Phase 2 only needs test runs. Phase 3 adds the flow library, deploy/stop and run history, designed in [server-v1.md](server-v1.md) §6; those endpoints move into this document as they are built.

## Serving

`flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data dir] [-keep-executions 1000] [-log-format text|json]` serves the API on `-addr`, and the `http_trigger` routes of every deployed flow on `-flows-addr` ([server-v1.md](server-v1.md) §2). Flows and run history live in the data directory: `-data`, else `$FLOWFORGE_DATA`, else `~/.flowforge`.

- **Local use only.** There is no authentication, and a test run executes whatever flow it is given: `http_request` nodes can call any URL, and `transform` expressions can read the server's environment (`env`). So the default address is loopback. Binding to another interface is possible with `-addr`, but should not be done on an untrusted network.
- During development the Vite dev server proxies `/api/*` to `http://127.0.0.1:8090`, so the editor and the API share an origin and no CORS is needed.
- All bodies are JSON (`Content-Type: application/json`). Request bodies are limited to 1 MiB.
- Errors use one shape: `{"error": "<message>", "problems": ["<detail>", …]}`. `problems` is present when there is more than one thing to report (e.g. every validation problem in a flow).

## `POST /api/flows/run` — test run

Runs one execution of a flow, as if one of its triggers had fired once with the given payload. Nothing keeps running afterwards: triggers are compiled (so their config is checked) but never started — no port is opened for `http_trigger`, no schedule for `cron_trigger`. Action nodes do run for real, so an `http_request` node makes its HTTP call.

Request:

```json
{
  "flow": { "id": "flow-hello", "version": "1.0", "nodes": [ … ], "edges": [ … ] },
  "trigger": "node-1",
  "payload": { "headers": {}, "query": {}, "body": { "name": "ada" } }
}
```

- `flow` (required) — a Flow document, checked exactly as `flowforge run` checks a file: parsed (unknown fields rejected), validated and compiled (engine-v1.md §4.1–4.3).
- `trigger` (optional) — the ID of the trigger node to fire. May be omitted when the flow has exactly one trigger.
- `payload` (optional, default `{}`) — what the trigger emits, i.e. the input of the nodes right after it. It should look like what the real trigger would emit (engine-v1.md §11): `{headers, query, body}` for `http_trigger`, `{firedAt}` for `cron_trigger`.
- Unknown request fields are rejected.

Responses:

- **`200 OK`** — the execution ran, whether it completed or a node failed:

  ```json
  {
    "executionId": "af34e49d1bbf08df7c6ac39a1be15db3",
    "status": "completed",
    "trace": [ { "kind": "execution_started", … }, … ]
  }
  ```

  `status` is `completed` or `failed`. When failed, `error` holds the wrapped node error (engine-v1.md §7), e.g. `node node-2 (transform): evaluate expression: …`. `trace` is every trace event of the execution, in order, exactly as `flowforge run` prints them (engine-v1.md §8).
- **`400 Bad Request`** — the request is malformed, the flow does not parse or compile (`problems` lists every validation and config problem), or the trigger is missing, unknown, or ambiguous.
- **`413 Payload Too Large`** — request body over 1 MiB.

A run is limited to 60 seconds; a run that takes longer is cancelled and reported as `failed` (the engine's cancellation, engine-v1.md §9).

### Streaming (`Accept: text/event-stream`)

With `Accept: text/event-stream`, a run that gets past validation is answered with `200` and a [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html) stream instead of one JSON body, so the editor can show the run while it happens:

```
event: trace
data: {"kind":"execution_started","executionId":"af34…","nodeId":"node-1",…}

event: trace
data: {"kind":"node_started",…}

…

event: result
data: {"executionId":"af34…","status":"completed"}
```

- One `trace` event per trace event, sent as soon as the engine emits it. `data` is the same JSON object as an entry of `trace` in the JSON response.
- One final `result` event: the JSON response without `trace`. The stream then ends.
- Errors found before the run starts (`400`, `413`) are still plain JSON responses, as above; the client tells them apart by `Content-Type`.
- If the client disconnects, the run is cancelled (engine-v1.md §9).
- The endpoint stays a `POST`, so the browser reads the stream with `fetch` rather than `EventSource` (which can only `GET`).

## Flow library

Stored flows, deploy and stop ([server-v1.md](server-v1.md) §4, §6). Served by `flowforge serve`.

| Method + path | Answer |
|---|---|
| `GET /api/health` | `200 {"status":"ok"}`. |
| `GET /api/flows` | `200 {"flows": [ … ]}`: every stored flow without its document, ordered by name, then ID. |
| `POST /api/flows` | Body `{"flow": {…}}`. `201` with the stored flow. `400` if the document does not parse or its ID is not allowed; `409` if the ID exists. |
| `GET /api/flows/{id}` | `200` with the stored flow; `404` if unknown. |
| `PUT /api/flows/{id}` | Body `{"flow": {…}, "revision": n}`. `200` with the stored flow at revision n + 1. `409` if the stored revision is not n (below). `400` if the document's ID is not `{id}`, `revision` is missing, or the document does not parse; `404` if unknown. |
| `DELETE /api/flows/{id}` | Stops the flow if it runs and deletes it with its run history. `204`; `404` if unknown. |
| `POST /api/flows/{id}/start` | Deploys the latest saved revision. `200` with the stored flow — its `state` says whether it started (`running`) or not (`failed`, with `error`, e.g. a route held by another flow). `400` with `problems` if the document does not compile; nothing changes then. `404` if unknown. |
| `POST /api/flows/{id}/stop` | Stops the flow and undeploys it. `200` with the stored flow; `404` if unknown. |

**The stored flow:**

```json
{
  "flow": { "id": "order-api", "version": "1.0", "nodes": [ … ], "edges": [ … ] },
  "id": "order-api",
  "name": "Order API",
  "revision": 3,
  "createdAt": "2026-10-30T09:12:44.501Z",
  "updatedAt": "2026-10-31T14:03:07.123Z",
  "state": "running",
  "runningRevision": 2,
  "error": ""
}
```

- `flow` is the document; the list leaves it out. `name` is copied from it.
- `state` is `stopped` (not deployed), `running`, or `failed` (deployed but could not start; `error` says why). `runningRevision` is the revision that runs, `0` unless `running`. `runningRevision` below `revision` means saved changes are not deployed yet.

**Saving:**
- The document must parse (as for Open in the editor), but need not compile: unfinished work can be saved. Start compiles it.
- A flow's ID is fixed; on create it must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`.
- The server stores the document as sent, plus one trailing newline. Responses carry it as a JSON value, so whitespace is not preserved in them.
- **Lost-update guard:** a `PUT` whose `revision` is not the stored one — someone saved in between — is refused with `409` and `{"error": "…", "current": <the stored flow>}`, so the editor can offer to reload theirs or overwrite (save again with `current.revision`).

## Run history

Every execution of a deployed flow is recorded with its trace; test runs are not ([server-v1.md](server-v1.md) §5). The newest `-keep-executions` executions per flow are kept (default 1000, `0` keeps all).

| Method + path | Answer |
|---|---|
| `GET /api/executions?flow={id}&limit=50&before={cursor}` | `200` with one page of the flow's executions, newest first. `flow` is required; `limit` is 1–200, default 50; `before` is the `next` of the previous page. `400` for a missing `flow`, a bad `limit` or a cursor the server did not issue; `404` for an unknown flow. |
| `GET /api/executions/{id}` | `200` with the execution and its trace; `404` if unknown (or pruned). |

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

- `status` is `running`, `completed`, `failed`, or `interrupted` (the server stopped without finishing it — a crash or a kill). `error` is present when failed; `finishedAt` once finished (never for an interrupted execution).
- `next` is absent on the last page. `executions` is `[]` for a flow that never ran.
- `traceComplete` is `false` when trace events of the execution were dropped because the server could not write them fast enough; its status is still exact.
- `GET /api/executions/{id}` adds `"trace": [ … ]`: the events in order, the same shape as a test run's `trace`. A node input or output over 64 KiB is stored as `{"truncated": true, "bytes": <size>}`.
