import { describe, expect, it } from 'vitest'
import { formatPath, hasUnknowns, inferShape } from './shape.ts'

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
