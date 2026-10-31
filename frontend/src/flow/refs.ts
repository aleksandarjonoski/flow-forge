import { formatPath, type PathSegment } from './shape.ts'

// References from mapping expressions to available data (docs/data-v1.md
// §6): `input…` for the previous node's output and `nodes["<id>"]…` for an
// ancestor's. Used to write paths for dragged fields and to find which
// fields an expression uses, so the mapper can draw lines (§6.2).

/** Where a reference starts: the input, or an ancestor's output by node ID. */
export type RefRoot = 'input' | { node: string }

export interface Ref {
  root: RefRoot
  segments: PathSegment[]
}

/** Writes a reference as expr-lang: input.a.b, nodes["node-1"].a[0]. */
export function refExpr(ref: Ref): string {
  const inputForm = formatPath(ref.segments, 'expr')!
  if (ref.root === 'input') return inputForm
  return `nodes[${JSON.stringify(ref.root.node)}]${inputForm.slice('input'.length)}`
}

// One reference: input or nodes["…"], then .name, ["key"] or [n] steps. Not
// preceded by a name character or a dot, so `myinput` or `x.input` don't
// count.
const REF =
  /(?<![\w$.])(?:input|nodes\["((?:[^"\\]|\\.)*)"\])((?:\.[A-Za-z_][A-Za-z0-9_]*|\[(?:\d+|"(?:[^"\\]|\\.)*")\])*)/g

const STEP = /\.([A-Za-z_][A-Za-z0-9_]*)|\[(\d+)\]|\["((?:[^"\\]|\\.)*)"\]/g

/**
 * Finds every reference in an expression. `direct` is set when the whole
 * expression is exactly one reference — a direct mapping, drawn as a solid
 * line; otherwise the references are drawn dashed. This is a best-effort
 * pattern match, not a parser (§6.2): text inside string literals that
 * looks like a reference also counts.
 */
export function findRefs(expression: string): { direct?: Ref; refs: Ref[] } {
  const refs: Ref[] = []
  let direct: Ref | undefined
  const trimmed = expression.trim()
  for (const m of trimmed.matchAll(REF)) {
    const ref: Ref = {
      root: m[1] === undefined ? 'input' : { node: unquote(m[1]) },
      segments: steps(m[2]),
    }
    refs.push(ref)
    if (m.index === 0 && m[0].length === trimmed.length) direct = ref
  }
  return { direct, refs }
}

function steps(text: string): PathSegment[] {
  const out: PathSegment[] = []
  for (const m of text.matchAll(STEP)) {
    if (m[1] !== undefined) out.push(m[1])
    else if (m[2] !== undefined) out.push(Number(m[2]))
    else out.push(unquote(m[3]))
  }
  return out
}

function unquote(s: string): string {
  return JSON.parse(`"${s}"`) as string
}

/** Identifies a root for lookups: "input" or the node ID. */
export function rootKey(root: RefRoot): string {
  return root === 'input' ? 'input' : `node:${root.node}`
}
