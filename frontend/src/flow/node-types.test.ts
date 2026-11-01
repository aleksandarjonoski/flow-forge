import { describe, expect, it } from 'vitest'
import hello from '../../../backend/examples/hello.flow.json'
import {
  NODE_TYPES,
  activeMode,
  nodeType,
  pathParams,
  validateConfig,
  validateNode,
} from './node-types.ts'
import { isTrigger } from './types.ts'

const schema = (type: string) => nodeType(type)!.config

describe('catalog', () => {
  it('covers the five v1 node types', () => {
    expect(NODE_TYPES.map((t) => t.type)).toEqual([
      'http_trigger',
      'cron_trigger',
      'http_request',
      'transform',
      'log',
      'parse_json',
    ])
  })

  it('classifies triggers', () => {
    expect(isTrigger('http_trigger')).toBe(true)
    expect(isTrigger('cron_trigger')).toBe(true)
    expect(isTrigger('transform')).toBe(false)
    expect(isTrigger('unknown_plugin')).toBe(false)
  })

  it('gives every trigger, and only triggers, a sample payload', () => {
    for (const t of NODE_TYPES) {
      expect(typeof t.samplePayload === 'function', t.type).toBe(
        t.kind === 'trigger',
      )
    }
    expect(
      nodeType('http_trigger')!.samplePayload!({ path: '/orders/{id}' }),
    ).toEqual({
      headers: { 'Content-Type': 'application/json' },
      query: {},
      params: { id: '' },
      body: {},
    })
    expect(nodeType('cron_trigger')!.samplePayload!({}).firedAt).toMatch(
      /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/,
    )
  })

  it('reads the parameters of a trigger path', () => {
    expect(pathParams('/c/{customer}/o/{id}')).toEqual(['customer', 'id'])
    expect(pathParams('/orders')).toEqual([])
    expect(pathParams('/o/id-{id}/{1x}/{ok}')).toEqual(['ok'])
    expect(pathParams(undefined)).toEqual([])
  })

  it('only lists required keys that are properties', () => {
    for (const t of NODE_TYPES) {
      for (const key of t.config.required ?? []) {
        expect(t.config.properties, `${t.type}.${key}`).toHaveProperty(key)
      }
    }
  })

  it('accepts the hello example as valid', () => {
    for (const n of hello.nodes) {
      expect(validateNode(nodeType(n.type)!, n.config), n.id).toEqual({})
    }
  })
})

describe('validateConfig', () => {
  it('reports missing required keys', () => {
    expect(validateConfig(schema('http_trigger'), {})).toEqual({
      path: 'Required.',
    })
  })

  it('checks enum, pattern and URL format', () => {
    expect(
      validateConfig(schema('http_trigger'), {
        path: 'webhook',
        method: 'FETCH',
      }),
    ).toEqual({
      path: 'Must start with /.',
      method: 'Must be one of GET, POST, PUT, PATCH, DELETE.',
    })
    expect(
      validateConfig(schema('http_trigger'), { path: '/_flowforge/ready' }),
    ).toEqual({ path: 'Paths under /_flowforge/ are reserved.' })
    expect(
      validateConfig(schema('http_trigger'), { path: '/_flowforgery' }),
    ).toEqual({})
    expect(
      validateConfig(schema('http_request'), { url: '/relative' }),
    ).toEqual({ url: 'Must be an absolute http(s) URL.' })
    expect(
      validateConfig(schema('http_request'), { url: 'ftp://example.com' }),
    ).toEqual({ url: 'Must be an absolute http(s) URL.' })
    expect(
      validateConfig(schema('http_request'), { url: 'https://example.com/x' }),
    ).toEqual({})
  })

  it('checks string maps', () => {
    const s = schema('http_request')
    const url = 'https://example.com'
    expect(validateConfig(s, { url, headers: { A: 'b' } })).toEqual({})
    expect(validateConfig(s, { url, headers: { A: 1 } })).toEqual({
      headers: 'Value of A must be text.',
    })
    expect(validateConfig(s, { url, headers: { '': 'x' } })).toEqual({
      headers: 'Names must not be empty.',
    })
    expect(validateConfig(s, { url, headers: 'A: b' })).toEqual({
      headers: 'Must be a set of name/value pairs.',
    })
  })

  it('checks declared schemas', () => {
    const s = schema('parse_json')
    expect(validateConfig(s, {})).toEqual({ schema: 'Required.' })
    expect(validateConfig(s, { schema: { type: 'object' } })).toEqual({})
    expect(validateConfig(s, { schema: { type: 'array' } })).toEqual({
      schema: 'The top level must be of type object.',
    })
    expect(
      validateConfig(s, {
        schema: { type: 'object', properties: { a: {}, b: { type: 'x' } } },
      }),
    ).toEqual({ schema: 'a: type is required (and 1 more)' })
  })

  it('rejects non-text values and ignores unknown keys', () => {
    expect(
      validateConfig(schema('transform'), { expression: 42, extra: true }),
    ).toEqual({ expression: 'Must be text.' })
  })
})

describe('transform modes', () => {
  const spec = nodeType('transform')!
  const orderSchema = {
    type: 'object',
    properties: { greeting: { type: 'string' }, n: { type: 'number' } },
    required: ['greeting'],
  }

  it('picks the mode from the config keys', () => {
    expect(activeMode(spec, {})?.label).toBe('Expression')
    expect(activeMode(spec, { expression: 'x' })?.label).toBe('Expression')
    expect(activeMode(spec, { schema: {} })?.label).toBe('Mapping')
    expect(activeMode(spec, { mapping: {} })?.label).toBe('Mapping')
  })

  it('requires and checks only the active mode', () => {
    expect(validateNode(spec, {})).toEqual({ expression: 'Required.' })
    expect(validateNode(spec, { mapping: {} })).toEqual({ schema: 'Required.' })
    expect(
      validateNode(spec, {
        schema: orderSchema,
        mapping: { greeting: '"hi"' },
      }),
    ).toEqual({})
  })

  it('reports mapping problems and mixed modes', () => {
    expect(
      validateNode(spec, {
        schema: orderSchema,
        mapping: { zip: '"1"', n: 3 },
      }),
    ).toEqual({
      mapping: 'n: expression must be text (and 2 more)',
    })
    expect(
      validateNode(spec, { expression: 'x', schema: orderSchema }),
    ).toEqual({ schema: 'Belongs to Mapping mode; use one mode.' })
  })
})
