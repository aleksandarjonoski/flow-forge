import type { SchemaType } from './schema.ts'

// Edits of a declared schema for the tree editor (docs/data-v1.md §7.3).
// Schemas are edited as the raw JSON objects stored in a node's config, so
// unknown or invalid parts survive untouched. Every function returns a new
// schema and leaves its input alone.

/** A raw schema object as stored in config. */
export type RawSchema = Record<string, unknown>

/**
 * Where a field is: property names from the top level down, with "[]" for
 * "the items of this array". [] is the top level itself.
 */
export type FieldPath = string[]

export const ITEMS = '[]'

/** The schema node at a path, if there is one. */
export function nodeAt(
  schema: RawSchema,
  path: FieldPath,
): RawSchema | undefined {
  let cur: unknown = schema
  for (const seg of path) {
    if (!isObject(cur)) return undefined
    cur = seg === ITEMS ? cur.items : asObject(cur.properties)[seg]
  }
  return isObject(cur) ? cur : undefined
}

/** Adds a string field named "field" (or "field2", …) to the object at objectPath. */
export function addField(
  schema: RawSchema,
  objectPath: FieldPath,
): { schema: RawSchema; name: string } {
  const props = asObject(nodeAt(schema, objectPath)?.properties)
  let name = 'field'
  for (let n = 2; name in props; n++) name = `field${n}`
  return {
    schema: updateAt(schema, objectPath, (obj) => ({
      ...obj,
      properties: { ...asObject(obj.properties), [name]: { type: 'string' } },
    })),
    name,
  }
}

/** Removes a field, and its entry in its object's required list. */
export function removeField(schema: RawSchema, path: FieldPath): RawSchema {
  const name = path.at(-1)!
  return updateAt(schema, path.slice(0, -1), (obj) => {
    const props = { ...asObject(obj.properties) }
    delete props[name]
    return withRequired(
      { ...obj, properties: props },
      requiredOf(obj).filter((r) => r !== name),
    )
  })
}

/**
 * Renames a field in place, keeping its position and required flag. Returns
 * an error instead when the name is empty, contains ".", or is taken.
 */
export function renameField(
  schema: RawSchema,
  path: FieldPath,
  newName: string,
): { schema: RawSchema } | { error: string } {
  const old = path.at(-1)!
  if (newName === old) return { schema }
  if (newName === '' || newName.includes('.')) {
    return { error: 'A field name must be non-empty and must not contain ".".' }
  }
  const parent = path.slice(0, -1)
  if (newName in asObject(nodeAt(schema, parent)?.properties)) {
    return { error: `There is already a field named "${newName}".` }
  }
  return {
    schema: updateAt(schema, parent, (obj) => {
      const props: RawSchema = {}
      for (const [k, v] of Object.entries(asObject(obj.properties))) {
        props[k === old ? newName : k] = v
      }
      return withRequired(
        { ...obj, properties: props },
        requiredOf(obj).map((r) => (r === old ? newName : r)),
      )
    }),
  }
}

/**
 * Sets a node's type, dropping settings that no longer apply: an object
 * keeps its fields, an array gets string items if it had none, and allowed
 * values are kept only between string, number and integer of the same kind.
 */
export function setType(
  schema: RawSchema,
  path: FieldPath,
  type: SchemaType,
): RawSchema {
  return updateAt(schema, path, (node) => {
    const next: RawSchema = { type }
    if (typeof node.title === 'string') next.title = node.title
    if (typeof node.description === 'string')
      next.description = node.description
    if (type === 'object') {
      next.properties = node.type === 'object' ? asObject(node.properties) : {}
      const req = node.type === 'object' ? requiredOf(node) : []
      if (req.length) next.required = req
    } else if (type === 'array') {
      next.items =
        node.type === 'array' && isObject(node.items)
          ? node.items
          : { type: 'string' }
    } else if (Array.isArray(node.enum) && sameKind(node.type, type)) {
      next.enum = node.enum
    }
    return next
  })
}

/** Marks a field required or optional in its object. */
export function setRequired(
  schema: RawSchema,
  path: FieldPath,
  required: boolean,
): RawSchema {
  const name = path.at(-1)!
  return updateAt(schema, path.slice(0, -1), (obj) => {
    const others = requiredOf(obj).filter((r) => r !== name)
    if (!required) return withRequired(obj, others)
    // Keep required in property order, so the JSON stays tidy.
    const order = Object.keys(asObject(obj.properties))
    return withRequired(
      obj,
      [...others, name].sort((a, b) => order.indexOf(a) - order.indexOf(b)),
    )
  })
}

/** Sets a node's description; empty text removes it. */
export function setDescription(
  schema: RawSchema,
  path: FieldPath,
  text: string,
): RawSchema {
  return updateAt(schema, path, (node) => {
    const next = { ...node }
    if (text.trim() === '') delete next.description
    else next.description = text
    return next
  })
}

/**
 * Sets a node's allowed values from comma-separated text; numbers for
 * number and integer fields. Empty text removes the restriction. A value
 * that is not a number is kept as text, so the schema check reports it.
 */
export function setEnum(
  schema: RawSchema,
  path: FieldPath,
  text: string,
): RawSchema {
  return updateAt(schema, path, (node) => {
    const next = { ...node }
    const values = text
      .split(',')
      .map((v) => v.trim())
      .filter((v) => v !== '')
    if (!values.length) {
      delete next.enum
      return next
    }
    const numeric = node.type === 'number' || node.type === 'integer'
    next.enum = values.map((v) =>
      numeric && v !== '' && !Number.isNaN(Number(v)) ? Number(v) : v,
    )
    return next
  })
}

/** The allowed values of a node as comma-separated text. */
export function enumText(node: RawSchema | undefined): string {
  return Array.isArray(node?.enum)
    ? node.enum.map((v) => String(v)).join(', ')
    : ''
}

/**
 * The mapping key (dot path) of a field, or undefined if the field is inside
 * an array: mappings only reach fields through objects (§5).
 */
export function mappingKey(path: FieldPath): string | undefined {
  return path.length && !path.includes(ITEMS) ? path.join('.') : undefined
}

/** Renames mapping keys at or below `from` to start with `to` instead. */
export function renameMappingKeys(
  mapping: Record<string, unknown>,
  from: string,
  to: string,
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(mapping)) {
    out[
      k === from ? to : k.startsWith(`${from}.`) ? to + k.slice(from.length) : k
    ] = v
  }
  return out
}

/** Removes mapping keys at or below `path`. */
export function removeMappingKeys(
  mapping: Record<string, unknown>,
  path: string,
): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(mapping).filter(
      ([k]) => k !== path && !k.startsWith(`${path}.`),
    ),
  )
}

// updateAt replaces the node at path with fn(node), copying the way down.
function updateAt(
  schema: RawSchema,
  path: FieldPath,
  fn: (node: RawSchema) => RawSchema,
): RawSchema {
  if (path.length === 0) return fn(schema)
  const [seg, ...rest] = path
  if (seg === ITEMS) {
    return { ...schema, items: updateAt(asObject(schema.items), rest, fn) }
  }
  const props = asObject(schema.properties)
  return {
    ...schema,
    properties: { ...props, [seg]: updateAt(asObject(props[seg]), rest, fn) },
  }
}

function requiredOf(node: RawSchema): string[] {
  return Array.isArray(node.required)
    ? node.required.filter((r): r is string => typeof r === 'string')
    : []
}

function withRequired(node: RawSchema, required: string[]): RawSchema {
  const next = { ...node }
  if (required.length) next.required = required
  else delete next.required
  return next
}

function sameKind(from: unknown, to: SchemaType): boolean {
  const numeric = (t: unknown) => t === 'number' || t === 'integer'
  return from === to || (numeric(from) && numeric(to))
}

function isObject(v: unknown): v is RawSchema {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function asObject(v: unknown): RawSchema {
  return isObject(v) ? v : {}
}
