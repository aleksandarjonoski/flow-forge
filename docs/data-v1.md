# FlowForge Data Contracts & Mapper — Design v1

Status: **Draft — for review**
Scope: declared data shapes (schemas), the `parse_json` node, the transform's mapping mode, and the visual mapper in the editor.
Audience: implementers of the engine nodes and the editor.

This is the contract for this feature. If something here is wrong, change the doc first, then the code. It builds on [engine-v1.md](engine-v1.md) (§10 data flow, §11 nodes) and the editor rules in [design.md](design.md) ("Input data view").

---

## 1. Goals & Non-Goals

### Goals
- **Data contracts.** Let a flow declare what its data looks like, so the rest of the flow can rely on it, the editor can show it without a test run, and bad data fails early with a clear message.
- **Format-neutral triggers.** A trigger only receives data. Turning raw data into checked, typed data is a separate, explicit step: a *parse* node. `parse_json` is the first; XML, CSV, form data can follow with the same pattern.
- **Visual mapping**, in the spirit of TIBCO BusinessWorks: available data on the left, the target structure on the right, lines between them. First for the transform node.
- **Every node shows its data**: what comes in and what goes out, in Input and Output tabs.
- Groundwork for code generation (design.md Phase 6): declared schemas can become Go types.

### Non-Goals (this version)
- Mapping into the inputs of *every* node (TIBCO's "Activity Input"). Sketched in §9, built later.
- Repeating / for-each mappings over arrays. An array field can only be mapped as a whole (§6.3).
- Type conversion (`"42"` → `42`). Validation is strict.
- Parse nodes for other formats. Same pattern, later.
- Full JSON Schema. Only the subset in §3.

---

## 2. Concepts

| Term | Meaning |
|---|---|
| **Payload** | The data passed between nodes, `map[string]any` (engine-v1.md §3.1). Unchanged. |
| **Schema** | A description of a payload's shape, in the JSON Schema subset of §3. |
| **Output contract** | What a node *type* guarantees about its output (e.g. `http_request` → `{status, headers, body: any}`). Fixed, from the catalog. Already exists in the editor. |
| **Declared schema** | A schema written *in the flow*, in a node's config. Only `parse_json` and a transform in mapping mode have one. Their output is guaranteed to match it. |
| **Mapping** | For each field of a declared output schema, an expression that computes its value from available data. |
| **Available data** | What a node may read: `input` (the previous node's output) and the outputs of all its *ancestors* (engine-v1.md §10, "Reading non-predecessor results"). |

**One rule keeps this consistent: a node declares only its output; its input is always the previous node's output.** No node has an "input schema" that could disagree with what the previous node produces. Where data must be checked on the way in, the flow says so with a `parse_json` node.

---

## 3. Schema Subset

A schema is a JSON object using only these keywords. Anything else is rejected when the flow loads, so nobody believes an unsupported rule (`minLength`, `pattern`, …) is being enforced.

| Keyword | Applies to | Meaning |
|---|---|---|
| `type` | all (required) | `object`, `array`, `string`, `number`, `integer`, `boolean`, `null` |
| `properties` | `object` | field name → schema. Field names must not contain `.` (mapping paths use dots) |
| `required` | `object` | field names that must be present |
| `items` | `array` | schema of every element |
| `enum` | `string`, `number`, `integer` | allowed values |
| `title`, `description` | all | documentation, shown in the editor |

Rules:
- The **top level of a declared schema is always an `object`** (payloads are objects).
- **Strict types.** `"42"` is not a number, `1.5` is not an integer, `null` matches only `null`.
- **Extra fields are dropped.** A value with fields not in `properties` is accepted, and those fields are removed from the output, so the output contains exactly what the schema describes.
- **Missing optional fields stay missing** (no defaults are invented).

Validation reports **every** problem, each with its path:

```
body.address.city: expected string, got number
body.items[2].id: required field is missing
body.status: must be one of "new", "done"
```

The validator is written once in Go (`backend/internal/schema`), small and dependency-free. The editor has a TypeScript twin for showing problems while editing; both are tested against the same cases.

---

## 4. The `parse_json` Node

Type: `parse_json` — the 6th built-in node (an action). It turns raw or loosely-shaped data into data that matches a declared schema.

### Config
| Field | Required | Meaning |
|---|---|---|
| `from` | no | Dot path into the input of the value to parse (e.g. `body`). Empty: the whole input. Same path syntax as `http_request`'s `bodyFrom`. |
| `schema` | yes | The declared schema (§3). Its top level must be an `object`. |

### Behaviour
1. Take the value at `from`. If the path does not resolve, the node fails.
2. If the value is a **string**, parse it as JSON (a text body, an `http_request` body without a JSON content type). Invalid JSON fails the node. If it is **already structured** (an `http_trigger` with a JSON body), use it as is.
3. Validate against `schema` (§3). Any problem fails the node, with every problem listed in the error (engine-v1.md §7 — the run stops).
4. Output: the value, with extra fields dropped. It always matches `schema`.

### Example

```
http_trigger  ──►  parse_json (from: body, schema: Order)  ──►  transform  ──►  …
```

Downstream nodes can rely on `input.customer.email` existing and being a string, and the editor shows the `Order` fields before any test run.

Init checks: `schema` is present, uses only the §3 subset, and has an `object` top level; `from` is a valid path.

---

## 5. The Transform's Mapping Mode

Today a transform has one `expression` that computes the whole output. It gains a second mode, chosen per node:

| Mode | Config | Output |
|---|---|---|
| **Expression** (today) | `expression` | Whatever the expression returns (engine-v1.md §11). |
| **Mapping** (new) | `schema` + `mapping` | An object built field by field, matching `schema`. |

A transform has either `expression` or `schema` + `mapping`, never both (load-time error).

### Config

```json
{
  "schema": {
    "type": "object",
    "properties": {
      "greeting": { "type": "string" },
      "customer": {
        "type": "object",
        "properties": { "city": { "type": "string" }, "vip": { "type": "boolean" } }
      }
    },
    "required": ["greeting"]
  },
  "mapping": {
    "greeting": "'hello ' + input.body.name",
    "customer.city": "nodes[\"node-1\"].body.address.city",
    "customer.vip": "input.body.orders > 10"
  }
}
```

- `mapping` keys are **dot paths to fields of `schema`**. Values are expr-lang expressions with the same identifiers as the expression mode: `input`, `nodes`, `env`.
- A key may name a **leaf** field or a **whole object/array** field (the expression then returns that whole value). A key and one of its descendants cannot both be mapped.
- Load-time checks: every key exists in the schema; every expression compiles; every **required** field is mapped (directly, or for an object field through mappings of all its own required fields).

### Execution
1. Evaluate each mapping expression. An evaluation error fails the node, naming the field: `customer.city: evaluate: …`.
2. Build the output object from the results; fields without a mapping are left out.
3. Validate the result against `schema` (§3) — strict, so `customer.vip` must really be a boolean. Problems fail the node.

The output therefore always matches `schema`, just like `parse_json`.

### Why structured config, not a generated expression
The mapping is stored as fields, not as one generated `{ greeting: …, customer: {…} }` expression, because the editor must be able to read it back and redraw the lines. Turning arbitrary expression text back into a mapping is not reliably possible.

---

## 6. References and Lines

### 6.1 How expressions refer to data
- `input` — the previous node's output.
- `nodes["<node id>"]` — the output of any **ancestor** node (the nodes on the path from the trigger). Node IDs are used, not names, because names can change; the editor *shows* names (§7.3).
- Only ancestors are offered: in a flow that branches, a node on another branch may not have run yet.

### 6.2 Direct and computed mappings
- **Direct**: the expression is exactly a path, e.g. `input.body.name` or `nodes["node-1"].body.address.city`. Drawn as a **solid line** from the source field to the target field. Created by dragging.
- **Computed**: anything else (`'hello ' + input.body.name`, `input.body.orders > 10`). The target field shows **ƒ** and the expression; every path the expression mentions is drawn as a **dashed line** (best effort: paths are found by pattern, not by parsing every expression).

### 6.3 Arrays
An array field is mapped as a whole: `"customer.orders": "input.body.orders"`. Mapping element by element ("for each order, build …") is not in this version; the mapping editor shows array fields as single targets.

---

## 7. Editor

### 7.1 Node editor with tabs
Every node keeps the narrow property panel for quick edits. **Double-clicking a node** (or **Open editor** in the panel) opens the **node editor**: a wide overlay over the canvas, in the style of the TIBCO activity editor, with tabs:

| Tab | All nodes | Content |
|---|---|---|
| **General** | ✓ | Name and config form (today's panel form). |
| **Input** | ✓ | Read-only tree of what arrives (today's input data view, full size). |
| **Output** | ✓ | What the node outputs: the type's contract (read-only), or the **declared schema** (editable) for `parse_json` and a mapping-mode transform. Plus the last run's real output when available. |
| **Mapping** | transform (mapping mode) | The visual mapper (§7.2). |

### 7.2 The visual mapper (transform, mapping mode)

```
┌ Available data ──────────────┐        ┌ Output (schema) ───────────────────────────────┐
│ ▾ input  ← Parse Order       │        │ ▾ greeting      string  ƒ 'hello ' + input.…   │
│     ▾ customer               │ ─ ─ ─ ─┼─►                                              │
│         name       string ───┼────────┼─► ▾ customer    object                         │
│         email      string    │        │       city      string    nodes["…"].body.…    │
│ ▾ $HTTP Trigger (node-1)     │        │       vip       boolean ƒ input.body.orders…   │
│     ▾ body                   │        │                                                │
│         ▾ address            │        │  + Add field                                   │
│             city   string ───┼────────┼─► (to customer.city)                           │
└──────────────────────────────┘        └────────────────────────────────────────────────┘
```

- **Left — available data**: `input` (labelled with the previous node's name) and every ancestor as `$<Node name>`, each as a field tree. Shapes come from the last test run when it still matches the flow, otherwise from declared schemas and output contracts (the rules of the input data view).
- **Right — output**: the transform's schema as a tree, each field with its type and its mapping. The tree is editable: add, rename, remove fields; set type; mark required.
- **Lines**: solid for direct mappings, dashed for paths used in computed ones (§6.2). Lines follow scrolling and collapsing.
- **Drag** a left field onto a right field: creates a direct mapping (replacing any existing one).
- **Click** a right field: edit its expression inline; clicking left fields inserts their paths at the cursor (as in the property panel today).
- **Drop a left object onto empty space on the right**: adds a field of that shape to the schema and maps it — the fastest way to start.
- Unmapped required fields are marked, matching the load-time check.

### 7.3 Schema editing
Used for `parse_json`'s schema and the transform's output schema. Slice 2 ships the JSON view and both generators in the property panel; the tree editor comes with the node editor (slice 4). In the JSON view, text that does not parse yet keeps the last valid schema, and every schema problem is listed.
- **Tree editor**: field name, type, required, `enum`, description; nested objects and arrays.
- **Generate from sample**: paste example JSON; a schema is generated (every field typed from the sample, all fields required) and can then be adjusted. Numbers are generated as `number`, never `integer`: one sample cannot show that a value is always whole. A `null` in the sample becomes type `null`, to be corrected by hand.
- **Generate from last run**: the same, from the real data the node received in the last test run, at its `from` path (which must hold an object). Replacing an existing schema asks first.
- **JSON view**: the raw schema, for copy/paste. Edits there are applied when they parse and pass §3.

### 7.4 Data view everywhere
The input data view (design.md) gains a third source: **declared**. A node after `parse_json` or a mapping transform shows the declared fields immediately, labelled "From the declared schema of *Parse Order*", without a test run. Order of preference: last run (when it still applies) → declared schema → type contract.

---

## 8. Flow JSON

No change to the document format: `schema` and `mapping` live inside a node's `config`, which is already free-form (`map[string]any`). Files stay at version `1.0`; an older engine rejects a `parse_json` node as an unknown type, and a mapping-mode transform at load time (no `expression`), which is the right failure.

---

## 9. Later: Mapping Into Every Node's Input

TIBCO maps process data into every activity's *input* (an HTTP request's URL, headers, body). In FlowForge, node settings are fixed text in `config`; only the payload flows. A sketch of how this could be added later, for its own design round:

- Each node type declares an **input schema** in its catalog entry (e.g. `http_request`: `url`, `method`, `headers`, `body`).
- A node gains an optional node-level `inputMapping` (same shape as §5's `mapping`). Before calling `Execute`, the executor evaluates it and the node receives the mapped values instead of only its static config.
- This changes the engine contract (engine-v1.md §3.2, §4.5) and the Flow format (version `1.1`), so it is deliberately not part of this version.

---

## 10. Implementation Order

Each slice ships on its own and is tested end to end:

1. **Schema subset** — `backend/internal/schema` (validate, drop extras, errors with paths) and the TypeScript twin, sharing test cases.
2. **`parse_json` node** — engine node + tests; catalog entry; config field type "schema" (JSON editor with Generate from sample / last run); data view's "declared" source.
3. **Transform mapping mode** — engine (both modes, load-time checks, field-named errors) + tests; catalog/form switch between modes.
4. **Node editor** — wide overlay with General / Input / Output tabs for every node; schema tree editor in the Output tab.
5. **Visual mapper** — Mapping tab: two trees, lines (solid/dashed), drag to map, inline expressions, add-field-by-drop.

## 11. Decisions taken

From the review of this feature (2026-10-31):
- `parse_json` is a separate node, so triggers stay format-neutral; no request schema on `http_trigger`.
- Extra fields are dropped; top level is an object; validation is strict (no conversion).
- `parse_json` becomes the 6th built-in node now.
- A node declares only its output; its input is the previous node's output.
