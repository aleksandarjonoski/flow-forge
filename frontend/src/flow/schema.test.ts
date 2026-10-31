import { describe, expect, it } from 'vitest'
// Shared with the Go implementation, so both report identical problems.
import cases from '../../../backend/internal/schema/testdata/cases.json'
import {
  checkMapping,
  parseSchema,
  problemText,
  schemaFields,
  validate,
} from './schema.ts'

describe('parseSchema (shared cases)', () => {
  it.each(cases.parse.map((c) => [c.name, c] as const))('%s', (_, c) => {
    expect(parseSchema(c.schema).problems.map(problemText)).toEqual(c.problems)
  })
})

describe('validate (shared cases)', () => {
  it.each(cases.validate.map((c) => [c.name, c] as const))('%s', (_, c) => {
    const { schema, problems } = parseSchema(c.schema)
    expect(problems).toEqual([])
    const result = validate(schema!, c.value)
    expect(result.problems.map(problemText)).toEqual(c.problems)
    if ('output' in c) expect(result.value).toEqual(c.output)
  })
})

describe('checkMapping (shared cases)', () => {
  it.each(cases.mapping.map((c) => [c.name, c] as const))('%s', (_, c) => {
    const { schema } = parseSchema(c.schema)
    expect(checkMapping(schema!, c.keys).map(problemText)).toEqual(c.problems)
  })
})

describe('schemaFields', () => {
  it('lists fields depth-first in declared order', () => {
    const { schema } = parseSchema({
      type: 'object',
      properties: {
        b: { type: 'string' },
        a: {
          type: 'object',
          properties: { x: { type: 'number' } },
          required: ['x'],
        },
        list: {
          type: 'array',
          items: { type: 'object', properties: { y: { type: 'string' } } },
        },
      },
      required: ['a'],
    })
    expect(
      schemaFields(schema!).map(
        (f) => `${f.path}:${f.type}:${f.required ? 'r' : ''}:${f.depth}`,
      ),
    ).toEqual([
      'b:string::0',
      'a:object:r:0',
      'a.x:number:r:1',
      'list:array::0',
    ])
  })
})
