import { describe, expect, it } from 'vitest'
import {
  formatPath,
  hasUnknowns,
  inferShape,
  schemaFromShape,
  shapeAt,
  shapeFromSchema,
} from './shape.ts'

describe('inferShape', () => {
  it('describes nested data and keeps primitive samples', () => {
    expect(
      inferShape({
        body: { name: 'ada', age: 36, tags: ['x'], ok: true, n: null },
      }),
    ).toEqual({
      type: 'object',
      fields: {
        body: {
          type: 'object',
          fields: {
            name: { type: 'string', sample: 'ada' },
            age: { type: 'number', sample: 36 },
            tags: { type: 'array', items: { type: 'string', sample: 'x' } },
            ok: { type: 'boolean', sample: true },
            n: { type: 'null' },
          },
        },
      },
    })
    expect(inferShape([])).toEqual({ type: 'array' })
  })
})

describe('hasUnknowns', () => {
  it('finds "any" anywhere in a shape', () => {
    expect(hasUnknowns(inferShape({ a: { b: [1] } }))).toBe(false)
    expect(
      hasUnknowns({ type: 'object', fields: { body: { type: 'any' } } }),
    ).toBe(true)
    expect(hasUnknowns({ type: 'object', values: { type: 'any' } })).toBe(true)
  })
})

describe('formatPath', () => {
  it('writes expr-lang paths, quoting non-identifier keys', () => {
    expect(formatPath(['body', 'name'], 'expr')).toBe('input.body.name')
    expect(formatPath(['headers', 'Content-Type'], 'expr')).toBe(
      'input.headers["Content-Type"]',
    )
    expect(formatPath(['items', 0, 'id'], 'expr')).toBe('input.items[0].id')
    expect(formatPath([], 'expr')).toBe('input')
  })

  it('writes Go template actions, using index when needed', () => {
    expect(formatPath(['body', 'name'], 'template')).toBe(
      '{{.input.body.name}}',
    )
    expect(formatPath(['headers', 'Content-Type'], 'template')).toBe(
      '{{index .input "headers" "Content-Type"}}',
    )
    expect(formatPath(['items', 0], 'template')).toBe(
      '{{index .input "items" 0}}',
    )
  })

  it('writes dot paths, refusing what they cannot express', () => {
    expect(formatPath(['body', 'user'], 'path')).toBe('body.user')
    expect(formatPath(['headers', 'Content-Type'], 'path')).toBe(
      'headers.Content-Type',
    )
    expect(formatPath(['items', 0], 'path')).toBeUndefined()
    expect(formatPath(['a.b'], 'path')).toBeUndefined()
    expect(formatPath([], 'path')).toBeUndefined()
  })
})

describe('schemas and shapes', () => {
  it('generates a schema from a sample, every field required', () => {
    const shape = inferShape({
      id: 7,
      tags: ['a'],
      customer: { email: 'x' },
      note: null,
    })
    expect(schemaFromShape(shape)).toEqual({
      type: 'object',
      properties: {
        id: { type: 'number' },
        tags: { type: 'array', items: { type: 'string' } },
        customer: {
          type: 'object',
          properties: { email: { type: 'string' } },
          required: ['email'],
        },
        note: { type: 'null' },
      },
      required: ['id', 'tags', 'customer', 'note'],
    })
    expect(schemaFromShape(inferShape({}))).toEqual({ type: 'object' })
  })

  it('turns a schema back into a shape', () => {
    expect(
      shapeFromSchema({
        type: 'object',
        properties: {
          n: { type: 'integer' },
          list: { type: 'array', items: { type: 'string' } },
          any: { type: 'array' },
        },
      }),
    ).toEqual({
      type: 'object',
      fields: {
        n: { type: 'integer' },
        list: { type: 'array', items: { type: 'string' } },
        any: { type: 'array' },
      },
    })
  })

  it('finds the part of a shape at a path', () => {
    const shape = inferShape({ body: { user: { name: 'a' } } })
    expect(shapeAt(shape, ['body', 'user'])).toEqual(inferShape({ name: 'a' }))
    expect(shapeAt(shape, [])).toBe(shape)
    expect(shapeAt(shape, ['body', 'user', 'name', 'x'])).toBeUndefined()
    expect(shapeAt(shape, ['nope'])).toBeUndefined()
  })
})
