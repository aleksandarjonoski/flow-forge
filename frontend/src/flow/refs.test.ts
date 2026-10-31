import { describe, expect, it } from 'vitest'
import { findRefs, refExpr } from './refs.ts'

describe('refExpr', () => {
  it('writes input and ancestor references', () => {
    expect(refExpr({ root: 'input', segments: ['body', 'name'] })).toBe(
      'input.body.name',
    )
    expect(
      refExpr({
        root: { node: 'node-1' },
        segments: ['headers', 'Content-Type'],
      }),
    ).toBe('nodes["node-1"].headers["Content-Type"]')
    expect(refExpr({ root: { node: 'n' }, segments: [] })).toBe('nodes["n"]')
  })
})

describe('findRefs', () => {
  it('recognises a direct mapping', () => {
    expect(findRefs(' input.body.name ')).toEqual({
      direct: { root: 'input', segments: ['body', 'name'] },
      refs: [{ root: 'input', segments: ['body', 'name'] }],
    })
    expect(findRefs('nodes["node-1"].items[2]["a b"]').direct).toEqual({
      root: { node: 'node-1' },
      segments: ['items', 2, 'a b'],
    })
  })

  it('finds every reference in a computed expression', () => {
    const r = findRefs(`'hello ' + input.body.name + nodes["node-1"].x > 1`)
    expect(r.direct).toBeUndefined()
    expect(r.refs).toEqual([
      { root: 'input', segments: ['body', 'name'] },
      { root: { node: 'node-1' }, segments: ['x'] },
    ])
  })

  it('ignores lookalikes', () => {
    expect(findRefs('myinput.a + x.input.b').refs).toEqual([])
    expect(findRefs('len(input)').refs).toEqual([
      { root: 'input', segments: [] },
    ])
  })

  it('round-trips with refExpr', () => {
    const ref = {
      root: { node: 'n"1' },
      segments: ['a', 0, 'b-c'] as (string | number)[],
    }
    expect(findRefs(refExpr(ref)).direct).toEqual(ref)
  })
})
