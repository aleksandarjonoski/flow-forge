import { describe, expect, it } from 'vitest'
// Shared with the Go implementation, so both report identical problems.
import cases from '../../../backend/internal/schema/testdata/cases.json'
import { parseSchema, problemText, validate } from './schema.ts'

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
