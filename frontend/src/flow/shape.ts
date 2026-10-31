import type { Schema } from './schema.ts'

// Shapes describe what data looks like, for the input data view (docs/
// design.md, "Input data view"). They come from a node type's output
// contract, from a declared schema (docs/data-v1.md), or are inferred from
// real data seen in a test run.

export type Shape =
  | {
      type: 'object'
      /** Known fields, in order. */
      fields?: Record<string, Shape>
      /** For maps with arbitrary keys (e.g. headers): the type of every value. */
      values?: Shape
      /**
       * Which fields are required, when known (from a declared schema).
       * Unknown for shapes inferred from data.
       */
      required?: string[]
    }
  | { type: 'array'; items?: Shape }
  | {
      type: 'string' | 'number' | 'integer' | 'boolean' | 'null'
      sample?: unknown
    }
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

/** The shape a declared schema describes. */
export function shapeFromSchema(schema: Schema): Shape {
  switch (schema.type) {
    case 'object': {
      const fields: Record<string, Shape> = {}
      for (const [k, v] of Object.entries(schema.properties ?? {})) {
        fields[k] = shapeFromSchema(v)
      }
      return { type: 'object', fields, required: schema.required ?? [] }
    }
    case 'array':
      return schema.items
        ? { type: 'array', items: shapeFromSchema(schema.items) }
        : { type: 'array' }
    default:
      return { type: schema.type }
  }
}

/**
 * Generates a schema from a shape: every field typed as seen, ready to be
 * adjusted (docs/data-v1.md §7.3). Required fields are kept when the shape
 * knows them (it came from a declared schema); a shape inferred from a
 * sample marks every field required. Inferred numbers become "number", never
 * "integer", since one sample cannot show that a value is always whole.
 */
export function schemaFromShape(shape: Shape): Schema {
  switch (shape.type) {
    case 'object': {
      const fields = Object.entries(shape.fields ?? {})
      const properties: Record<string, Schema> = {}
      for (const [k, v] of fields) properties[k] = schemaFromShape(v)
      const required = shape.required ?? fields.map(([k]) => k)
      if (!fields.length) return { type: 'object' }
      return required.length
        ? { type: 'object', properties, required }
        : { type: 'object', properties }
    }
    case 'array':
      return shape.items
        ? { type: 'array', items: schemaFromShape(shape.items) }
        : { type: 'array' }
    case 'integer':
      // Only declared schemas produce integer shapes; keep them.
      return { type: 'integer' }
    case 'any':
      return { type: 'object' }
    default:
      return { type: shape.type }
  }
}

/** The part of a shape at a dot path, if the path leads through objects. */
export function shapeAt(shape: Shape, path: string[]): Shape | undefined {
  let cur: Shape | undefined = shape
  for (const key of path) {
    if (cur?.type !== 'object') return undefined
    cur = cur.fields?.[key]
  }
  return cur
}
