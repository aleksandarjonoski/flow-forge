# FlowForge HTTP API — v1

Status: **Draft** (Phase 2)
Audience: implementers of the backend API and the frontend client.

This is the contract between the visual editor (Layer 1) and the backend (Layer 2, see [design.md](design.md) §5). If something here is wrong, change the doc first, then the code. Phase 3 adds flow storage and start/stop; Phase 2 only needs test runs.

## Serving

`flowforge serve [-addr 127.0.0.1:8090]` serves the API.

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
