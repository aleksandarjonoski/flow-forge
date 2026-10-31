// The JSON Schema subset for declared data shapes (docs/data-v1.md §3): the
// editor's twin of backend/internal/schema. Both are tested against
// backend/internal/schema/testdata/cases.json, so they accept the same
// schemas and report the same problems with the same messages.

export type SchemaType =
  'object' | 'array' | 'string' | 'number' | 'integer' | 'boolean' | 'null'

export interface Schema {
  type: SchemaType
  properties?: Record<string, Schema>
  required?: string[]
  items?: Schema
  enum?: unknown[]
  title?: string
  description?: string
}

/** One way a schema or a value is wrong, at a field path ("" = top level). */
export interface SchemaProblem {
  path: string
  message: string
}

export function problemText(p: SchemaProblem): string {
  return p.path ? `${p.path}: ${p.message}` : p.message
}

const TYPES: readonly string[] = [
  'object',
  'array',
  'string',
  'number',
  'integer',
  'boolean',
  'null',
]

const KEYWORDS: readonly string[] = [
  'type',
  'properties',
  'required',
  'items',
  'enum',
  'title',
  'description',
]

/** Reads a schema, reporting every problem (unsupported keywords, bad types, …). */
export function parseSchema(raw: unknown): {
  schema?: Schema
  problems: SchemaProblem[]
} {
  const problems: SchemaProblem[] = []
  const schema = parse(raw, '', problems)
  return problems.length ? { problems } : { schema, problems }
}

function parse(
  raw: unknown,
  path: string,
  problems: SchemaProblem[],
): Schema | undefined {
  const add = (message: string) => problems.push({ path, message })
  if (!isObject(raw)) {
    add(`must be a schema object, got ${typeName(raw)}`)
    return undefined
  }
  for (const k of sortedKeys(raw)) {
    if (!KEYWORDS.includes(k)) add(`unsupported keyword ${quote(k)}`)
  }

  const s: Partial<Schema> = {}
  const t = raw.type
  if (t === undefined || t === null) add('type is required')
  else if (typeof t !== 'string') add('type must be a string')
  else if (!TYPES.includes(t)) add(`unknown type ${quote(t)}`)
  else s.type = t as SchemaType
  for (const key of ['title', 'description'] as const) {
    if (key in raw) {
      if (typeof raw[key] === 'string') s[key] = raw[key]
      else add(`${key} must be a string`)
    }
  }

  if ('properties' in raw) {
    const props = raw.properties
    if (s.type !== undefined && s.type !== 'object') {
      add('properties only apply to type object')
    } else if (!isObject(props)) {
      add('properties must be an object')
    } else {
      // Check in sorted order (problems match the Go twin), but keep the
      // fields in the order they were written, for display.
      const parsed = new Map<string, Schema>()
      for (const name of sortedKeys(props)) {
        if (name === '' || name.includes('.')) {
          add(
            `field name ${quote(name)} must be non-empty and must not contain "."`,
          )
          continue
        }
        const child = parse(props[name], join(path, name), problems)
        if (child) parsed.set(name, child)
      }
      s.properties = {}
      for (const name of Object.keys(props)) {
        const child = parsed.get(name)
        if (child) s.properties[name] = child
      }
    }
  }
  if ('required' in raw) {
    const req = raw.required
    if (s.type !== undefined && s.type !== 'object') {
      add('required only applies to type object')
    } else if (!Array.isArray(req)) {
      add('required must be a list of field names')
    } else {
      s.required = []
      for (const r of req as unknown[]) {
        if (typeof r !== 'string') add('required must be a list of field names')
        else if (!s.properties?.[r]) {
          add(`required field ${quote(r)} is not in properties`)
        } else s.required.push(r)
      }
    }
  }
  if ('items' in raw) {
    if (s.type !== undefined && s.type !== 'array') {
      add('items only apply to type array')
    } else {
      s.items = parse(raw.items, `${path}[]`, problems)
    }
  }
  if ('enum' in raw) {
    const list = raw.enum
    if (
      s.type !== undefined &&
      s.type !== 'string' &&
      s.type !== 'number' &&
      s.type !== 'integer'
    ) {
      add('enum only applies to types string, number and integer')
    } else if (!Array.isArray(list) || list.length === 0) {
      add('enum must be a non-empty list')
    } else {
      for (const v of list as unknown[]) {
        if (s.type !== undefined && !matches(s.type, v)) {
          add(`enum value ${show(v)} is not of type ${s.type}`)
        }
      }
      s.enum = list as unknown[]
    }
  }
  return s as Schema
}

/**
 * Checks value against schema strictly. Returns the value with fields the
 * schema does not describe removed, and every problem found.
 */
export function validate(
  schema: Schema,
  value: unknown,
): { value: unknown; problems: SchemaProblem[] } {
  const problems: SchemaProblem[] = []
  const out = check(schema, value, '', problems)
  return { value: out, problems }
}

function check(
  s: Schema,
  v: unknown,
  path: string,
  problems: SchemaProblem[],
): unknown {
  const add = (message: string) => problems.push({ path, message })
  if (!matches(s.type, v)) {
    add(`expected ${s.type}, got ${typeName(v)}`)
    return v
  }
  if (s.enum?.length && !s.enum.some((e) => e === v)) {
    add(`must be one of ${s.enum.map(show).join(', ')}`)
    return v
  }
  if (s.type === 'object') {
    const obj = v as Record<string, unknown>
    const out: Record<string, unknown> = {}
    for (const name of sortedKeys(s.properties ?? {})) {
      if (!(name in obj)) {
        if (s.required?.includes(name)) {
          problems.push({
            path: join(path, name),
            message: 'required field is missing',
          })
        }
        continue
      }
      out[name] = check(
        s.properties![name],
        obj[name],
        join(path, name),
        problems,
      )
    }
    return out
  }
  if (s.type === 'array') {
    const items = v as unknown[]
    if (!s.items) return items
    return items.map((item, i) =>
      check(s.items!, item, `${path}[${i}]`, problems),
    )
  }
  return v
}

function matches(t: string, v: unknown): boolean {
  switch (t) {
    case 'object':
      return isObject(v)
    case 'array':
      return Array.isArray(v)
    case 'string':
      return typeof v === 'string'
    case 'number':
      return typeof v === 'number'
    case 'integer':
      return Number.isInteger(v)
    case 'boolean':
      return typeof v === 'boolean'
    case 'null':
      return v === null
  }
  return false
}

/** Names a value's type in schema terms, for messages. */
export function typeName(v: unknown): string {
  if (v === null || v === undefined) return 'null'
  if (Array.isArray(v)) return 'array'
  switch (typeof v) {
    case 'boolean':
      return 'boolean'
    case 'string':
      return 'string'
    case 'number':
      return 'number'
    case 'object':
      return 'object'
  }
  return typeof v
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function sortedKeys(o: object): string[] {
  return Object.keys(o).sort()
}

function join(path: string, name: string): string {
  return path ? `${path}.${name}` : name
}

function show(v: unknown): string {
  return JSON.stringify(v)
}

function quote(s: string): string {
  return JSON.stringify(s)
}

/**
 * Checks the keys of a mapping (docs/data-v1.md §5) against a schema, like
 * the Go CheckMapping: every key is a dot path to a field through object
 * fields; no key lies inside another mapped key; every required field is
 * covered, directly or through its own fields.
 */
export function checkMapping(schema: Schema, keys: string[]): SchemaProblem[] {
  const problems: SchemaProblem[] = []
  const sorted = [...keys].sort()
  const mapped = new Set<string>()
  for (const key of sorted) {
    const err = fieldError(schema, key)
    if (err) problems.push({ path: key, message: err })
    else mapped.add(key)
  }
  for (const key of sorted) {
    if (!mapped.has(key)) continue // already reported as not a field
    const outer = [...mapped].find((o) => o !== key && key.startsWith(`${o}.`))
    if (outer) {
      problems.push({
        path: key,
        message: `is inside ${quote(outer)}, which is mapped as a whole`,
      })
    }
  }
  checkRequired(schema, '', mapped, problems)
  return problems
}

/** The schema of the field at a dot path, if the path is valid. */
export function fieldAt(schema: Schema, path: string): Schema | undefined {
  return fieldError(schema, path) ? undefined : walk(schema, path.split('.'))
}

function walk(schema: Schema, names: string[]): Schema {
  let cur = schema
  for (const name of names) cur = cur.properties![name]
  return cur
}

function fieldError(schema: Schema, path: string): string | undefined {
  const names = path.split('.')
  let cur = schema
  for (let i = 0; i < names.length; i++) {
    if (cur.type !== 'object') {
      return `${quote(names.slice(0, i).join('.'))} is not an object`
    }
    const next = cur.properties?.[names[i]]
    if (!next) return 'no such field in the schema'
    cur = next
  }
  return undefined
}

function checkRequired(
  s: Schema,
  path: string,
  mapped: Set<string>,
  problems: SchemaProblem[],
) {
  for (const name of s.required ?? []) {
    const child = join(path, name)
    if (!covers(s.properties![name], child, mapped)) {
      problems.push({ path: child, message: 'required field is not mapped' })
    }
  }
  for (const name of sortedKeys(s.properties ?? {})) {
    const child = join(path, name)
    const field = s.properties![name]
    if (
      field.type === 'object' &&
      !mapped.has(child) &&
      mappedBelow(child, mapped)
    ) {
      checkRequired(field, child, mapped, problems)
    }
  }
}

function covers(field: Schema, path: string, mapped: Set<string>): boolean {
  if (mapped.has(path)) return true
  if (field.type !== 'object' || !mappedBelow(path, mapped)) return false
  return (field.required ?? []).every((name) =>
    covers(field.properties![name], join(path, name), mapped),
  )
}

function mappedBelow(path: string, mapped: Set<string>): boolean {
  return [...mapped].some((k) => k.startsWith(`${path}.`))
}

/** One field of a schema, for listing fields in the editor. */
export interface SchemaField {
  path: string
  name: string
  type: SchemaType
  required: boolean
  depth: number
}

/**
 * Lists a schema's fields depth-first in the order they are declared,
 * descending into object fields (not into arrays, which are mapped whole).
 */
export function schemaFields(schema: Schema): SchemaField[] {
  const out: SchemaField[] = []
  const walkFields = (s: Schema, path: string, depth: number) => {
    for (const [name, field] of Object.entries(s.properties ?? {})) {
      const child = join(path, name)
      out.push({
        path: child,
        name,
        type: field.type,
        required: s.required?.includes(name) ?? false,
        depth,
      })
      if (field.type === 'object') walkFields(field, child, depth + 1)
    }
  }
  walkFields(schema, '', 0)
  return out
}
