# FlowForge Production Basics — Design v1

Status: **Being implemented** (slices 1–3 done; §10)
Scope: Phase 4 — what a flow needs to run as a real service: API token auth, health checks, path parameters, request/response HTTP, variables and secrets, per-node timeouts and retries, and error handling.
Audience: implementers of the engine, the nodes, the server and the editor.

This is the contract for Phase 4. If something here is wrong, change the doc first, then the code. It builds on [engine-v1.md](engine-v1.md) (nodes, executor, errors), [server-v1.md](server-v1.md) (the server, listeners, store) and [data-v1.md](data-v1.md) (schemas, mapping). The roadmap is in [design.md](design.md) §8.

---

## 1. Goals & Non-Goals

### Goals
- **Safe to run on a server.** The editor/API can be reached over the network only with a token.
- **Real HTTP services.** A flow can answer the caller: status code, headers and body, chosen by the flow. Routes can have parameters (`/orders/{id}`).
- **The same flow in every environment.** URLs, keys and passwords live in server **variables**, not in the flow file. Secrets never show up in traces or logs.
- **Failures handled by the flow.** A node can time out and retry; a failure can be routed to a handler branch (e.g. to answer `502`).
- **Ready for load balancers and orchestrators.** Health and readiness endpoints.

### Non-Goals (this version)
- **User accounts, roles, OIDC.** One shared token. Users and RBAC are Phase 10.
- **Built-in TLS.** Use a reverse proxy (§3.5).
- **Auth on the flows' own endpoints** (e.g. API keys for `/orders`). Flows can check headers themselves with error handling; a built-in option is an open question (§12).
- **Conditional routing** (if/else branches) and loops. Phase 7. Until then, dynamic decisions are expressions (e.g. a computed status code).
- **Try/catch around groups of nodes.** Error handling is per node (§9).
- **Encrypting secrets at rest.** They are stored in the data directory, which only the server's user can read (§7.5).
- Remote deployment to other servers, central admin. Deferred; the token auth here is its first building block.

---

## 2. Flow JSON 1.1

Two features need new fields in the flow document: per-node run settings (§8) and error edges (§9). The schema goes from `1.0` to **`1.1`** — an additive change (design.md §6):

| Field | Where | Meaning |
|---|---|---|
| `run` | node, optional | `{"timeout": "10s", "retry": {…}}` — §8 |
| `sourceHandle: "error"` | edge | the edge is an **error edge** — §9. The field already exists and was ignored; in 1.1 it must be `""` or `"error"` |

Rules:
- The parser accepts `"1.0"` and `"1.1"`. A `1.0` document that uses a 1.1 feature is rejected (`node n: "run" requires version 1.1`).
- The editor saves every flow as `"1.1"` from Phase 4 on. Older FlowForge builds refuse such documents with their existing "unsupported version" error, which is the safe outcome: an old engine must not run error edges as normal edges.
- Canonical key order per node becomes `id, type, name, position, config, run`.
- The Go parser and the editor's `parseFlow` change together, as before.

---

## 3. API Token Authentication

### 3.1 Configuration
- The token comes from the environment, **`FLOWFORGE_API_TOKEN`**, or from a file: **`-api-token-file <path>`** (contents trimmed). The file wins when both are set; an empty or unreadable file is an error. There is deliberately no flag that takes the token itself: command lines are visible to other users in `ps`.
- **`flowforge token`** prints a new random token (32 random bytes, base64url) to set up with.
- A token shorter than 16 characters is refused at startup (exit 1).

### 3.2 What it protects
- With a token configured, every request under **`/api/`** on the editor listener needs `Authorization: Bearer <token>`. Otherwise: `401 {"error": "…"}` with `WWW-Authenticate: Bearer`. The comparison is constant-time.
- **Exempt:** `GET /api/health` (it reveals nothing), and the editor page and assets at `/` (static files, no data).
- The flows listener is not affected: the flows' own endpoints are public by design (§1).

### 3.3 Binding rules
Phase 3 only warned when the editor listener was bound to a non-loopback address. Now:
- **No token + non-loopback `-addr` → refuse to start** (exit 1) with a message explaining `FLOWFORGE_API_TOKEN`. `-insecure-no-auth` overrides this for people who know what they are doing (e.g. inside a private container network).
- Loopback without a token works as today, for local development.

### 3.4 Editor
- On a `401`, the editor shows a small **sign-in dialog** asking for the token. The token is checked with the server (`GET /api/flows`) before it is kept: in `sessionStorage` (per tab, gone when the tab closes), and also in `localStorage` if *Remember on this computer* is ticked. It is sent on every API call.
- A **Sign out** button clears it everywhere. A wrong token shows "Token not accepted."
- A link to a stored flow (`#/flows/<id>`) opened without a token keeps its hash and opens the flow after signing in.

### 3.5 TLS
The token travels in a header, so on a network it must go over HTTPS. FlowForge does not terminate TLS itself in this version; the docs show a reverse proxy in front of the editor listener (Caddy, two lines with automatic certificates; nginx as an alternative). Binding to a non-loopback address logs a reminder.

For example, with Caddy on the same machine and the editor left on loopback (`flowforge serve` with the defaults and `FLOWFORGE_API_TOKEN` set), a `Caddyfile` of

```
flowforge.example.com {
	reverse_proxy 127.0.0.1:8090
}
```

serves the editor at `https://flowforge.example.com` with an automatic certificate. The flows listener (`:8080`) is proxied the same way, under its own name, if its endpoints should be HTTPS too.

---

## 4. Health and Readiness

### 4.1 Flows listener
For load balancers and Kubernetes probes, on the **flows listener** (where traffic goes):

| Endpoint | Answer |
|---|---|
| `GET /_flowforge/health` | `200 {"status": "ok"}` while the process serves. **Liveness.** |
| `GET /_flowforge/ready` | `200 {"status": "ready", "flows": {"running": 12, "failed": 1}}` once boot is done (all deployed flows started or failed); `503 {"status": "starting"}` before, `503 {"status": "stopping"}` during shutdown. **Readiness.** |

- A failed flow does **not** make the server not-ready: that would take every other flow out of the load balancer too. The counts are there for monitoring.
- Both listeners accept connections from the start, so the probes can see `starting`. Routes of flows that have already started are served during the boot.
- `GET` and `HEAD` only (`405` otherwise); answers carry `Cache-Control: no-store`. Any other path under `/_flowforge/` is `404`.
- **`/_flowforge/` is reserved:** an `http_trigger` whose path starts with it (or is `/_flowforge`) fails to compile, and the editor flags the path.
- `flowforge run` serves the same two endpoints on its `-addr`: ready once the flow's triggers have started, with `{"running": 1, "failed": 0}`.

### 4.2 Editor listener
`GET /api/health` stays as it is, unauthenticated (§3.2). While the server boots, every `/api/` request (health included) answers `503 {"error": "the server is starting"}` with `Retry-After: 1`, so `/api/health` keeps meaning "autostart is done".

---

## 5. Path Parameters

### 5.1 Syntax
- An `http_trigger` path may contain **parameter segments**: `/orders/{id}`, `/customers/{customer}/orders/{order}`.
- A parameter is a whole segment, `{name}` with `name` matching `[A-Za-z_][A-Za-z0-9_]*`. Partial segments (`/orders/id-{id}`) and catch-alls are not supported in this version. Names within one path are unique.

### 5.2 Payload
The trigger's payload gains **`params`**: `{"id": "42"}`, values as strings, URL-decoded; `{}` when the path has none. The output contract in the editor catalog adds it, so the data view and mapper show `input.params.id`.

### 5.3 Matching
- Segment by segment; at each segment a **literal beats a parameter**: with `/orders/new` and `/orders/{id}` both registered, `/orders/new` goes to the first, `/orders/7` to the second.
- Trailing slashes are significant, as today (`/orders/` ≠ `/orders`).
- `404` / `405` as today: `405` when the path matches a route shape but not with this method.
- The method takes part in matching: with `GET /orders/new` and `POST /orders/{id}`, `POST /orders/new` goes to the second.
- Matching uses the escaped path, so `%2F` stays inside its segment (`/orders/a%2Fb` gives `id = "a/b"`). A parameter never matches an empty segment: `/orders/` does not match `/orders/{id}`.
- The editor's test-run payload for an `http_trigger` pre-fills `params` with the path's parameter names.

### 5.4 Conflicts
Two routes conflict when they have the same method and the **same shape**: equal literals and parameters in the same positions, whatever the names. `/orders/{id}` and `/orders/{orderId}` conflict; `/orders/{id}` and `/orders/new` do not. The conflict error names the flow holding the route, as in server-v1.md §4.3.

---

## 6. Request/Response HTTP

Today `http_trigger` answers `202 Accepted` at once and the flow's result never reaches the caller. Phase 4 adds the TIBCO "Send HTTP Response" pattern.

### 6.1 `http_trigger`: respond mode
New config:
- **`respond`**: `"accepted"` (default — today's behaviour) or `"flow"`: the request waits, and the flow's `http_response` node answers it.
- **`timeout`**: for `respond: "flow"`, how long the caller waits, as a duration (`"30s"` default, at most `"5m"`). When it passes: `504 {"error": "timeout", "executionId": "…"}`, and the execution is cancelled.

### 6.2 The `http_response` node
A new action node, the 7th built-in:
- **`status`** — expression (expr-lang, same identifiers as `transform`, §7.3), default `200`. Must evaluate to an integer 100–599. Being an expression, it can decide: `input.body == nil ? 404 : 200`.
- **`headers`** — map of header name → string (variables allowed, §7.3).
- **`body`** — expression, default `input`. An object, array, number or boolean is sent as JSON (`Content-Type: application/json` unless set in `headers`); a string as `text/plain; charset=utf-8`; `nil` sends no body.
- Like `transform`, the body may instead use **mapping mode** (`schema` + `mapping`, data-v1.md §5), so a response is built in the visual mapper and checked against a declared schema.
- **Output:** the response that was sent, `{"status", "headers", "body"}` — so the trace and later nodes see what the caller got.

### 6.3 Semantics
- The response is **sent when the node runs**, not when the execution ends. Nodes after it keep running (e.g. logging) without delaying the caller.
- **One response per execution.** A second `http_response` in the same execution fails with `response already sent` (it can happen with fan-out). The editor warns about two `http_response` nodes on the normal path of one trigger.
- If the execution **ends without a response**: `500 {"error": "the flow finished without an http_response", "executionId": "…"}`.
- If it **fails** (and no error edge handles it, §9): `500 {"error": "execution failed", "executionId": "…"}`. The real error stays in the history and logs; it is not sent to the caller, since it can contain internal details.
- **Validation:** an `http_response` must be downstream of an `http_trigger` with `respond: "flow"`. Anywhere else (after a cron trigger, or an `accepted` trigger) it is a compile error. (Each node descends from exactly one trigger, because a node has at most one inbound edge.)
- **Test runs** capture the response instead of sending it: the run result gains `"response": {"status", "headers", "body"}`, and the results panel shows a *Response 201* row.

### 6.4 Engine change
The executor gains a synchronous path for triggers that wait: the emitter can run an execution and wait for its end, and the `ExecutionContext` carries a **reply slot** that `http_response` fills and the trigger reads. The asynchronous `emit` (today's) stays for `accepted` and cron. Details go into engine-v1.md §4 with the slice.

---

## 7. Variables and Secrets

### 7.1 What a variable is
A **server-level** named value: `API_URL`, `CRM_TOKEN`, `DB_PASSWORD`. Names match `^[A-Z][A-Z0-9_]*$`. A variable is either plain or **secret**. Every flow on the server sees the same variables, so the same flow file behaves correctly on a test server and a production server.

### 7.2 Where values come from
In order of priority:
1. **The environment:** `FLOWFORGE_VAR_<NAME>`, e.g. `FLOWFORGE_VAR_API_URL`. Fits containers and systemd units. Read at startup; shown in the editor as *from environment*, read-only.
2. **The server's variable store:** a `variables` table in the data directory's database, managed through the API and the editor (§7.6).

### 7.3 Using variables in flows
- **Expressions** (`transform`, `http_response` status/body, mapping expressions): `vars.API_URL`.
- **Templates** (`log` messages): `{{.vars.API_URL}}`.
- **Any other string in a node's config** (e.g. `http_request` `url` or a header value): `${vars.API_URL}` is replaced by the value; `$${` writes a literal `${`. Example: `"url": "${vars.CRM_URL}/customers"`, `"headers": {"Authorization": "Bearer ${vars.CRM_TOKEN}"}`.
- Values are resolved when the flow is **compiled** — at deploy and at a test run. An unknown variable is a compile problem (`node node-3: unknown variable CRM_URL`), so a missing setting fails the deploy instead of the first request.
- Changing a variable affects running flows **on their next deploy**. The editor lists the running flows that use a changed variable and offers to redeploy them.

### 7.4 `env` goes away
`transform` expressions currently see **`env`: the whole process environment**. That would expose `FLOWFORGE_API_TOKEN` and every other secret of the server to any flow. **`env` is removed** in favour of `vars`. A flow using `env` fails to compile with a message pointing to variables. (Pre-release, so no compatibility shim.)

### 7.5 Secrets
- The API **never returns a secret's value**: `{"name": "CRM_TOKEN", "secret": true, "value": null}`. It can be replaced or deleted, not read back.
- **Masking:** every occurrence of a secret's value (at least 4 characters) is replaced with `••••` in trace events (run history, test runs and their stream), run log lines and error messages. This is best-effort — a value transformed by the flow (e.g. base64-encoded) is not recognised — and the docs say so.
- **At rest** the values are stored in the database in the data directory, which is only readable by the server's user (directory mode `0700`). Encryption at rest needs key management and is an open question (§12).

### 7.6 API and editor
- `GET /api/variables` — every variable: name, secret, source (`store` or `environment`), value unless secret.
- `PUT /api/variables/{name}` — `{"value": "…", "secret": true}`; creates or replaces. `400` for a bad name; `409` for a variable set by the environment.
- `DELETE /api/variables/{name}` — `204`; `409` if it comes from the environment.
- The editor gets a **Variables** tab in the left column: list, add, edit, delete, with secret values hidden. The data view and mapper list `vars` as a source, so `vars.API_URL` can be clicked or dragged into expressions like any other field.

---

## 8. Timeouts and Retries

### 8.1 Node run settings
The optional node field `run` (Flow JSON 1.1):

```json
"run": {
  "timeout": "10s",
  "retry": { "attempts": 3, "delay": "1s", "maxDelay": "30s" }
}
```

- **`timeout`** — a duration (Go syntax, at most `1h`). Each attempt of the node gets this long; then it fails with `timed out after 10s`.
- **`retry.attempts`** — total tries including the first, 1–10. `delay` (default `1s`) doubles after each failure, capped at `maxDelay` (default `30s`), with ±20% jitter.
- Retries happen on **any node error**, including a timeout. Engine shutdown (or a waiting caller's timeout) cancels the remaining retries.
- **Defaults:** no timeout and no retry. `http_request` keeps its 30 s client timeout as a safety net unless `run.timeout` is set.
- Retrying is **opt-in** because it can repeat side effects: a retried `POST` may create two orders. The editor's retry field says so.

### 8.2 Timeouts in practice
The node's context gets the deadline, so nodes that honour it (`http_request`, future connectors) stop their work. A node that ignores it (a long CPU-bound expression) cannot be killed: the executor stops waiting, fails the node, and discards its late result.

### 8.3 `http_request`: failing on status
Today a non-2xx status is data, not an error, so neither retry nor error edges react to a `503`. New config **`failOn`**: `"none"` (default, today's behaviour), `"5xx"`, or `"4xx+5xx"`. A failing status fails the node with `HTTP 503`; the response is still in the error details (§9.2).

### 8.4 Tracing
Each failed attempt emits `node_failed` with `"attempt": n` and `"willRetry": true`; the next attempt emits a new `node_started`. The final outcome is the last event. The results panel and History show *failed 2×, then completed*.

### 8.5 Editor
The node editor's General tab gets **Run settings**: timeout, retry attempts, delay and maximum delay, with the side-effects note.

---

## 9. Error Handling

### 9.1 Error edges
- Every action node gets a second output, **`error`**. An edge from it (`sourceHandle: "error"`, Flow JSON 1.1) is an **error edge**, drawn red and dashed in the editor.
- When a node **fails** (after its retries), and it has an error edge:
  - its normal downstream is **skipped**;
  - the error edge's target runs instead, and the execution continues from there.
- Without an error edge, the failure ends the execution, as today.
- Triggers have no error output. A node has at most one error edge. The single-inbound-edge rule and the cycle check count error edges like normal ones.

### 9.2 What the handler receives
The handler's input is:

```json
{
  "error": { "message": "HTTP 503", "node": "node-3", "nodeType": "http_request", "attempts": 3 },
  "input": { … the failed node's input … }
}
```

Earlier nodes' outputs remain available as `nodes["…"]`. For `http_request` with `failOn`, `error.response` holds `{status, headers, body}`.

### 9.3 Outcome
- If the handler branch completes, the execution **completes**: the error was handled. Its trace keeps the `node_failed` event, marked `"handled": true`, and the history list shows the run as *completed (handled error)*.
- If a node in the handler branch fails without its own error edge, the execution fails.
- With `respond: "flow"`, the handler branch typically ends in an `http_response` with `502` or `400`, so the caller gets a deliberate answer instead of the generic `500` (§6.3).

### 9.4 Executor
A node runs if its predecessor **completed** and the edge is normal, or its predecessor **failed** and the edge is an error edge; otherwise it is skipped. Nodes reachable only through error edges are skipped in a run without failures. The topological order is unchanged.

---

## 10. Implementation Order

| # | Slice | Notes |
|---|---|---|
| 1 | API token auth | `FLOWFORGE_API_TOKEN` / `-api-token-file`, `flowforge token`, `401` on `/api/` (health exempt), refuse non-loopback without a token unless `-insecure-no-auth`; editor sign-in dialog (§3) |
| 2 | Health and readiness | `/_flowforge/health` and `/ready` on the flows listener and in `flowforge run`; reserved prefix (§4) |
| 3 | Path parameters | `{name}` segments in the trigger mux, literal-beats-parameter matching, shape-based conflicts, `params` in the payload and output contract (§5) |
| 4 | Request/response | `respond` + `timeout` on `http_trigger`, `http_response` node (expressions or mapping mode), reply slot and synchronous emit, validation, response in test runs and the results panel (§6) |
| 5 | Variables and secrets | `variables` table, env overrides, `/api/variables`, `${vars.X}` / `vars` / `{{.vars}}` resolved at compile, `env` removed, secret masking in traces and logs (§7.1–7.5) |
| 6 | Variables in the editor | Variables tab, `vars` as a source in the data view and mapper, "redeploy flows using it" (§7.6) |
| 7 | Flow JSON 1.1, timeouts and retries | Parser and editor for 1.1; `run` settings enforced by the executor; attempt tracing; `http_request` `failOn`; Run settings in the node editor (§2, §8) |
| 8 | Error edges | `error` outputs and red edges in the editor; executor routing, handler input, handled status (§9) |

**Phase 4 done when:** a flow on a server reached only with a token takes `GET /orders/{id}` with `respond: "flow"`, calls a backend whose URL and key are server variables (the key never visible in History), with a 5 s timeout and 3 retries, and answers `200` with a mapped body — or `502` through an error edge when the backend stays down; and a load balancer checks `/_flowforge/ready`.

---

## 11. Decisions Taken

- **One schema bump (1.1) for everything new in the document**, with the editor saving all flows as 1.1, so old engines refuse instead of misreading error edges.
- **Token in env or a file, never on the command line.**
- **Refuse, don't warn,** when the unauthenticated API would be reachable from the network.
- **`/_flowforge/` reserved** on the flows listener, rather than a third listener for health.
- **Literal beats parameter** in routing, as in Go's `ServeMux`.
- **Respond from a node, not at the end**: the caller is answered as soon as the flow knows the answer.
- **Failure details are not sent to callers**; they are in the history.
- **Variables resolved at compile time**, so missing settings fail the deploy.
- **`env` removed**: whole-environment access is incompatible with secrets.
- **Retries opt-in**, because they can repeat side effects.
- **Error edges per node**, reusing the existing `sourceHandle` field.

## 12. Open Questions

1. **Auth for the flows' endpoints.** Should `http_trigger` get a built-in check (API key header, basic auth, a bearer token from a variable)? With error handling a flow can check a header itself, but a built-in option is simpler and harder to get wrong. Could be a small slice at the end of Phase 4.
2. **Secrets at rest.** Encrypt with a key from `FLOWFORGE_SECRET_KEY`? Adds key management (rotation, loss). Left out for now.
3. **Handled errors in History.** Is *completed (handled error)* enough, or should handled runs be filterable?
4. **Variables per flow** in addition to per server? Not planned; server-level variables match the "same flow, different environment" goal, and per-environment variables arrive naturally with remote deployment later.
