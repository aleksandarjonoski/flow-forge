import { parseSchema, problemText } from './schema.ts'
import type { InsertStyle, Shape } from './shape.ts'

// The node type catalog: what the editor knows about each built-in node type.
// Config schemas use the JSON Schema subset described in docs/design.md
// ("Node type catalog and property panel") and must match engine-v1.md §11
// until the backend serves them.

export interface StringProperty {
  type: 'string'
  title: string
  description?: string
  default?: string
  enum?: string[]
  pattern?: string
  format?: 'uri'
  'x-multiline'?: boolean
  /** How a clicked input field's path is written into this field. */
  'x-insert'?: InsertStyle
}

export interface StringMapProperty {
  type: 'object'
  title: string
  description?: string
  additionalProperties: { type: 'string' }
}

/** A declared data schema (docs/data-v1.md §3), edited as JSON in the panel. */
export interface SchemaProperty {
  type: 'schema'
  title: string
  description?: string
}

export type ConfigProperty = StringProperty | StringMapProperty | SchemaProperty

export interface ConfigSchema {
  type: 'object'
  properties: Record<string, ConfigProperty>
  required?: string[]
}

export interface NodeTypeSpec {
  type: string
  label: string
  kind: 'trigger' | 'action'
  description: string
  config: ConfigSchema
  /**
   * Triggers only: an example of what the trigger emits, used to pre-fill
   * the payload of a test run (engine-v1.md §11).
   */
  samplePayload?: () => Record<string, unknown>
  /**
   * What the node outputs, as far as its type knows (engine-v1.md §11);
   * "input" for nodes that pass their input through unchanged; "declared"
   * for nodes whose output matches the schema in their `schema` config
   * (docs/data-v1.md §4).
   */
  output: Shape | 'input' | 'declared'
}

// Shapes shared by several contracts.
const STRING_MAP: Shape = { type: 'object', values: { type: 'string' } }
const ANY_BODY = (note: string): Shape => ({ type: 'any', note })

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

export const NODE_TYPES: readonly NodeTypeSpec[] = [
  {
    type: 'http_trigger',
    label: 'HTTP Trigger',
    kind: 'trigger',
    description: 'Starts the flow for each HTTP request on a path.',
    config: {
      type: 'object',
      properties: {
        path: {
          type: 'string',
          title: 'Path',
          description: 'URL path to listen on, e.g. /webhook.',
          pattern: '^/',
        },
        method: {
          type: 'string',
          title: 'Method',
          enum: HTTP_METHODS,
          default: 'POST',
        },
      },
      required: ['path'],
    },
    samplePayload: () => ({
      headers: { 'Content-Type': 'application/json' },
      query: {},
      body: {},
    }),
    output: {
      type: 'object',
      fields: {
        headers: STRING_MAP,
        query: STRING_MAP,
        body: ANY_BODY('whatever the caller sends'),
      },
    },
  },
  {
    type: 'cron_trigger',
    label: 'Cron Trigger',
    kind: 'trigger',
    description: 'Starts the flow on a schedule.',
    config: {
      type: 'object',
      properties: {
        expression: {
          type: 'string',
          title: 'Schedule',
          description:
            '5-field cron (e.g. */5 * * * *) or a descriptor such as @hourly or @every 30s. Optional CRON_TZ=<zone> prefix.',
        },
      },
      required: ['expression'],
    },
    samplePayload: () => ({
      firedAt: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
    }),
    output: { type: 'object', fields: { firedAt: { type: 'string' } } },
  },
  {
    type: 'http_request',
    label: 'HTTP Request',
    kind: 'action',
    description: 'Calls an HTTP endpoint and outputs {status, headers, body}.',
    config: {
      type: 'object',
      properties: {
        url: {
          type: 'string',
          title: 'URL',
          description: 'Absolute http(s) URL.',
          format: 'uri',
        },
        method: {
          type: 'string',
          title: 'Method',
          enum: HTTP_METHODS,
          default: 'GET',
        },
        headers: {
          type: 'object',
          title: 'Headers',
          additionalProperties: { type: 'string' },
        },
        bodyFrom: {
          type: 'string',
          title: 'Body from',
          description:
            'Dot path into the input (e.g. body.user), sent as JSON. Leave empty for no body.',
          'x-insert': 'path',
        },
      },
      required: ['url'],
    },
    output: {
      type: 'object',
      fields: {
        status: { type: 'number' },
        headers: STRING_MAP,
        body: ANY_BODY('whatever the API returns'),
      },
    },
  },
  {
    type: 'transform',
    label: 'Transform',
    kind: 'action',
    description: 'Computes a new payload with an expression.',
    config: {
      type: 'object',
      properties: {
        expression: {
          type: 'string',
          title: 'Expression',
          description:
            'expr-lang expression over input, nodes and env, e.g. { greeting: "hello " + input.body.name }.',
          'x-multiline': true,
          'x-insert': 'expr',
        },
      },
      required: ['expression'],
    },
    output: ANY_BODY('whatever the expression returns'),
  },
  {
    type: 'log',
    label: 'Log',
    kind: 'action',
    description: 'Logs its input and passes it through unchanged.',
    config: {
      type: 'object',
      properties: {
        level: {
          type: 'string',
          title: 'Level',
          enum: ['debug', 'info', 'warn', 'error'],
          default: 'info',
        },
        message: {
          type: 'string',
          title: 'Message',
          description:
            'Go template over .input and .nodes, e.g. hello {{.input.name}}.',
          default: 'payload',
          'x-multiline': true,
          'x-insert': 'template',
        },
      },
    },
    output: 'input',
  },
  {
    type: 'parse_json',
    label: 'Parse JSON',
    kind: 'action',
    description:
      'Parses JSON and checks it against a schema; outputs exactly the schema fields.',
    config: {
      type: 'object',
      properties: {
        from: {
          type: 'string',
          title: 'From',
          description:
            'Dot path into the input of the value to parse (e.g. body). Empty: the whole input.',
          'x-insert': 'path',
        },
        schema: {
          type: 'schema',
          title: 'Schema',
          description:
            'What the data must look like. Extra fields are dropped; a mismatch stops the run.',
        },
      },
      required: ['schema'],
    },
    output: 'declared',
  },
]

const byType = new Map(NODE_TYPES.map((t) => [t.type, t]))

/** Returns the catalog entry for a node type, or undefined for unknown types. */
export function nodeType(type: string): NodeTypeSpec | undefined {
  return byType.get(type)
}

/**
 * Checks a node's config against its schema and returns a message per
 * offending key. An empty object means the config is valid. Keys not in the
 * schema are left alone: the engine decides what to do with them.
 */
export function validateConfig(
  schema: ConfigSchema,
  config: Record<string, unknown>,
): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const key of schema.required ?? []) {
    if (config[key] === undefined) errors[key] = 'Required.'
  }
  for (const [key, prop] of Object.entries(schema.properties)) {
    const value = config[key]
    if (value === undefined) continue
    const error =
      prop.type === 'string'
        ? checkString(prop, value)
        : prop.type === 'schema'
          ? checkSchema(value)
          : checkMap(value)
    if (error) errors[key] = error
  }
  return errors
}

function checkString(prop: StringProperty, value: unknown): string | undefined {
  if (typeof value !== 'string') return 'Must be text.'
  if (prop.enum && !prop.enum.includes(value)) {
    return `Must be one of ${prop.enum.join(', ')}.`
  }
  if (prop.pattern && !new RegExp(prop.pattern).test(value)) {
    return prop.pattern === '^/'
      ? 'Must start with /.'
      : `Must match ${prop.pattern}.`
  }
  if (prop.format === 'uri' && !isHttpURL(value)) {
    return 'Must be an absolute http(s) URL.'
  }
  return undefined
}

// checkSchema reports the first problem of a declared schema, with a count
// of the rest; its top level must be an object (docs/data-v1.md §3).
function checkSchema(value: unknown): string | undefined {
  const { schema, problems } = parseSchema(value)
  if (problems.length) {
    const more = problems.length > 1 ? ` (and ${problems.length - 1} more)` : ''
    return `${problemText(problems[0])}${more}`
  }
  if (schema?.type !== 'object') return 'The top level must be of type object.'
  return undefined
}

function checkMap(value: unknown): string | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return 'Must be a set of name/value pairs.'
  }
  for (const [k, v] of Object.entries(value)) {
    if (k === '') return 'Names must not be empty.'
    if (typeof v !== 'string') return `Value of ${k} must be text.`
  }
  return undefined
}

function isHttpURL(value: string): boolean {
  try {
    const u = new URL(value)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== ''
  } catch {
    return false
  }
}
