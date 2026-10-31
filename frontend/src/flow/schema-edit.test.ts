import { describe, expect, it } from 'vitest'
import {
  addField,
  enumText,
  mappingKey,
  nodeAt,
  removeField,
  removeMappingKeys,
  renameField,
  renameMappingKeys,
  setDescription,
  setEnum,
  setRequired,
  setType,
  type RawSchema,
} from './schema-edit.ts'

const base: RawSchema = {
  type: 'object',
  properties: {
    name: { type: 'string', description: 'Who' },
    customer: {
      type: 'object',
      properties: { city: { type: 'string' } },
      required: ['city'],
    },
    orders: {
      type: 'array',
      items: { type: 'object', properties: { id: { type: 'integer' } } },
    },
  },
  required: ['name', 'customer'],
}

describe('schema edits', () => {
  it('finds nodes, including array items', () => {
    expect(nodeAt(base, ['customer', 'city'])).toEqual({ type: 'string' })
    expect(nodeAt(base, ['orders', '[]', 'id'])).toEqual({ type: 'integer' })
    expect(nodeAt(base, ['nope'])).toBeUndefined()
  })

  it('adds fields with unique names, also inside array items', () => {
    const one = addField(base, [])
    expect(one.name).toBe('field')
    const two = addField(one.schema, [])
    expect(two.name).toBe('field2')
    const inItems = addField(base, ['orders', '[]'])
    expect(nodeAt(inItems.schema, ['orders', '[]', 'field'])).toEqual({
      type: 'string',
    })
    expect(base.properties).not.toHaveProperty('field')
  })

  it('removes a field and its required entry', () => {
    const s = removeField(base, ['customer', 'city'])
    expect(nodeAt(s, ['customer'])).toEqual({ type: 'object', properties: {} })
    expect(removeField(base, ['name']).required).toEqual(['customer'])
  })

  it('renames in place, keeping position and required', () => {
    const r = renameField(base, ['name'], 'fullName')
    if (!('schema' in r)) throw new Error(r.error)
    expect(Object.keys(nodeAt(r.schema, [])!.properties as object)).toEqual([
      'fullName',
      'customer',
      'orders',
    ])
    expect(r.schema.required).toEqual(['fullName', 'customer'])
  })

  it('refuses bad or taken names', () => {
    expect(renameField(base, ['name'], '')).toHaveProperty('error')
    expect(renameField(base, ['name'], 'a.b')).toHaveProperty('error')
    expect(renameField(base, ['name'], 'customer')).toEqual({
      error: 'There is already a field named "customer".',
    })
  })

  it('changes types, dropping what no longer applies', () => {
    expect(nodeAt(setType(base, ['name'], 'object'), ['name'])).toEqual({
      type: 'object',
      description: 'Who',
      properties: {},
    })
    expect(nodeAt(setType(base, ['customer'], 'array'), ['customer'])).toEqual({
      type: 'array',
      items: { type: 'string' },
    })
    const withEnum = setEnum(
      setType(base, ['name'], 'integer'),
      ['name'],
      '1, 2',
    )
    expect(
      nodeAt(setType(withEnum, ['name'], 'number'), ['name'])?.enum,
    ).toEqual([1, 2])
    expect(
      nodeAt(setType(withEnum, ['name'], 'string'), ['name']),
    ).not.toHaveProperty('enum')
    // Array item type.
    expect(
      nodeAt(setType(base, ['orders', '[]'], 'string'), ['orders']),
    ).toEqual({
      type: 'array',
      items: { type: 'string' },
    })
  })

  it('sets required in property order', () => {
    const s = setRequired(setRequired(base, ['name'], false), ['orders'], true)
    expect(s.required).toEqual(['customer', 'orders'])
    expect(setRequired(s, ['name'], true).required).toEqual([
      'name',
      'customer',
      'orders',
    ])
    expect(setRequired(base, ['customer', 'city'], false)).toEqual({
      ...base,
      properties: {
        ...(base.properties as object),
        customer: { type: 'object', properties: { city: { type: 'string' } } },
      },
    })
  })

  it('sets descriptions and allowed values', () => {
    expect(nodeAt(setDescription(base, ['name'], ' '), ['name'])).toEqual({
      type: 'string',
    })
    const s = setEnum(base, ['name'], 'new, done, ')
    expect(nodeAt(s, ['name'])?.enum).toEqual(['new', 'done'])
    expect(enumText(nodeAt(s, ['name']))).toBe('new, done')
    expect(nodeAt(setEnum(s, ['name'], ''), ['name'])).not.toHaveProperty(
      'enum',
    )
    const n = setEnum(setType(base, ['name'], 'number'), ['name'], '1, x')
    expect(nodeAt(n, ['name'])?.enum).toEqual([1, 'x'])
  })
})

describe('mapping keys', () => {
  const mapping = {
    name: 'a',
    customer: 'b',
    'customer.city': 'c',
    customers: 'd',
  }

  it('only maps fields reached through objects', () => {
    expect(mappingKey(['customer', 'city'])).toBe('customer.city')
    expect(mappingKey(['orders', '[]', 'id'])).toBeUndefined()
    expect(mappingKey([])).toBeUndefined()
  })

  it('renames keys at or below a path', () => {
    expect(renameMappingKeys(mapping, 'customer', 'client')).toEqual({
      name: 'a',
      client: 'b',
      'client.city': 'c',
      customers: 'd',
    })
  })

  it('removes keys at or below a path', () => {
    expect(removeMappingKeys(mapping, 'customer')).toEqual({
      name: 'a',
      customers: 'd',
    })
  })
})
