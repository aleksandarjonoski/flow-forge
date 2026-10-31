// Shapes describe what data looks like, for the input data view (docs/
// design.md, "Input data view"). They come from a node type's output
// contract or are inferred from real data seen in a test run.

export type Shape =
  | {
      type: 'object'
      /** Known fields, in order. */
      fields?: Record<string, Shape>
      /** For maps with arbitrary keys (e.g. headers): the type of every value. */
      values?: Shape
    }
  | { type: 'array'; items?: Shape }
  | { type: 'string' | 'number' | 'boolean' | 'null'; sample?: unknown }
  | { type: 'any'; note?: string }

/** Infers a shape from a value, keeping primitive values as samples. */
export function inferShape(value: unknown): Shape {
  if (value === null || value === undefined) return { type: 'null' }
  if (Array.isArray(value)) {
    return value.length
      ? { type: 'array', items: inferShape(value[0]) }
      : { type: 'array' }
  }
  switch (typeof value) {
    case 'string':
    case 'number':
    case 'boolean':
      return { type: typeof value, sample: value } as Shape
    case 'object': {
      const fields: Record<string, Shape> = {}
      for (const [k, v] of Object.entries(value)) fields[k] = inferShape(v)
      return { type: 'object', fields }
    }
    default:
      return { type: 'any' }
  }
}

/** True when a shape still has parts nobody knows ("any"). */
export function hasUnknowns(shape: Shape): boolean {
  switch (shape.type) {
    case 'any':
      return true
    case 'array':
      return shape.items ? hasUnknowns(shape.items) : false
    case 'object':
      return (
        Object.values(shape.fields ?? {}).some(hasUnknowns) ||
        (shape.values ? hasUnknowns(shape.values) : false)
      )
    default:
      return false
  }
}

/** One step of a path into the data: a map key or an array index. */
export type PathSegment = string | number

/**
 * How a path is written into a config field:
 * - expr: an expr-lang expression over `input` (transform)
 * - template: a Go text/template action over `.input` (log message)
 * - path: a plain dot path into the input (http_request bodyFrom)
 */
export type InsertStyle = 'expr' | 'template' | 'path'

const IDENT = /^[A-Za-z_][A-Za-z0-9_]*$/

/**
 * Writes a path into the input in the given style, or returns undefined when
 * the style cannot express it (a dot path cannot index arrays or hold dots).
 */
export function formatPath(
  segments: PathSegment[],
  style: InsertStyle,
): string | undefined {
  switch (style) {
    case 'expr':
      return segments.reduce<string>(
        (acc, s) =>
          typeof s === 'number'
            ? `${acc}[${s}]`
            : IDENT.test(s)
              ? `${acc}.${s}`
              : `${acc}[${JSON.stringify(s)}]`,
        'input',
      )
    case 'template':
      if (segments.every((s) => typeof s === 'string' && IDENT.test(s))) {
        return `{{.input${segments.map((s) => `.${s}`).join('')}}}`
      }
      return `{{index .input ${segments
        .map((s) => (typeof s === 'number' ? String(s) : JSON.stringify(s)))
        .join(' ')}}}`
    case 'path':
      if (
        segments.length === 0 ||
        segments.some(
          (s) => typeof s === 'number' || s === '' || s.includes('.'),
        )
      ) {
        return undefined
      }
      return segments.join('.')
  }
}
