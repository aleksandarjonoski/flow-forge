# ADR 0001 — Graph library for the visual editor

Status: **Accepted** (2026-10-30)
Phase: 2, slice 2

## Context

The visual editor (Phase 2) needs a canvas where users place nodes, drag them, and connect them with edges. The frontend stack is fixed: LitElement + TypeScript + Vite, with every UI piece a Lit custom element rendered in Shadow DOM (see [design.md](../design.md), "Frontend conventions"). The design doc named two candidates, React Flow and Drawflow, to be decided after a spike. A third candidate surfaced during research: Rete.js v2, which has an official Lit renderer.

## Spike

Each library was embedded in a Lit element (Shadow DOM) that rendered the `hello` flow: 3 nodes, 2 edges. A headless Chrome test then checked that all nodes render, that a node can be dragged, and that a new edge can be drawn by dragging from an output to an input. Bundle size is the production build (JS + CSS, gzipped), minus Lit's own ~6.5 KB.

| | Rete.js v2 + `@retejs/lit-plugin` | React Flow (`@xyflow/react`) | Drawflow |
|---|---|---|---|
| Render / drag / connect in Shadow DOM | ✅ / ✅ / ✅ | ✅ / ✅ / ✅ | ✅ / ✅ / ✅ |
| Added size (gzip) | ~30 KB | ~126 KB (includes React) | ~9 KB |
| Maintenance | Active (Lit plugin Jul 2026) | Very active, largest community | 0.0.60, last release Sep 2024 |
| Node rendering | Lit templates / components | React components | HTML strings |
| TypeScript | Built in | Built in | None |
| Node IDs | Settable, so editor IDs = flow IDs | Ours | Its own numeric IDs |
| Second edge into one input | Replaces the first (default) | Allowed | Allowed |
| License | MIT | MIT | MIT |

## Decision

Use **Rete.js v2** with `rete-area-plugin` (pan, zoom, drag), `rete-connection-plugin` (drawing edges) and `@retejs/lit-plugin` (rendering).

- It is the only option where nodes are rendered with Lit, so custom node UIs (slice 5 property panel, slice 9 live execution view) stay in one component model.
- Its inputs accept a single connection by default, which matches the engine's v1 rule that merge nodes are not supported ([engine-v1.md](../engine-v1.md) §10).
- Node IDs can be set to the flow's own IDs, so mapping between the editor and Flow JSON is direct.

React Flow was the most polished option, but it would make the editor a React + Lit hybrid and grow the bundle about 20×. Drawflow was the smallest, but it looks unmaintained, has no types, and renders nodes from HTML strings.

## Consequences

- Rete needs more setup code than the alternatives: an editor plus plugins wired together. This lives in one component, `<ff-canvas>`.
- Rete's API is asynchronous (`await editor.addNode(...)`). Loading and editing flows must await these calls.
- Rete's own model (nodes with named input/output sockets) is not the Flow JSON. Converting between them is the job of slice 3 (render a flow) and slice 6 (save/load). The engine needs one socket in and one out per node.
- If Rete's Lit plugin stops being maintained, the fallback is to render nodes ourselves on top of `rete-area-plugin`, which is framework-independent.
