import { describe, expect, it } from 'vitest'
import hello from '../../../backend/examples/hello.flow.json'
import { NODE_TYPES, nodeType, validateConfig } from './node-types.ts'
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
    ])
  })

  it('classifies triggers', () => {
    expect(isTrigger('http_trigger')).toBe(true)
    expect(isTrigger('cron_trigger')).toBe(true)
    expect(isTrigger('transform')).toBe(false)
    expect(isTrigger('unknown_plugin')).toBe(false)
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
      expect(validateConfig(schema(n.type), n.config), n.id).toEqual({})
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

  it('rejects non-text values and ignores unknown keys', () => {
    expect(
      validateConfig(schema('transform'), { expression: 42, extra: true }),
    ).toEqual({ expression: 'Must be text.' })
  })
})
