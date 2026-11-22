import { describe, expect, it } from 'vitest'
import { initialParameterValues, parseParameterPolicy } from './taskParameters'

describe('parseParameterPolicy', () => {
  it('parses the manifest parameter array', () => {
    const fields = parseParameterPolicy([
      { key: 'region', label: 'Region', type: 'string', default: 'local', required: true },
      { key: 'size', label: 'Size', type: 'select', options: ['small', 'large'], default: 'small' },
      { key: 'flag', label: 'Flag', type: 'string', secret: true, required: true },
    ])
    expect(fields).toHaveLength(3)
    expect(fields[0]).toMatchObject({ key: 'region', type: 'string', required: true, secret: false })
    expect(fields[1].type).toBe('select')
    expect(fields[1].options).toEqual(['small', 'large'])
    expect(fields[2].secret).toBe(true)
  })

  it('parses an object map of defaults and declarations', () => {
    const fields = parseParameterPolicy({ region: 'local', replicas: { type: 'number', default: 2 } })
    expect(fields).toHaveLength(2)
    expect(fields[0]).toMatchObject({ key: 'region', defaultValue: 'local' })
    expect(fields[1]).toMatchObject({ key: 'replicas', type: 'number', defaultValue: '2' })
  })

  it('returns no fields for unknown policy shapes', () => {
    expect(parseParameterPolicy(null)).toEqual([])
    expect(parseParameterPolicy('raw')).toEqual([])
    expect(parseParameterPolicy([{ nope: true }])).toEqual([])
  })
})

describe('initialParameterValues', () => {
  it('keeps only non-empty defaults', () => {
    const fields = parseParameterPolicy([
      { key: 'a', default: 'x' },
      { key: 'b' },
    ])
    expect(initialParameterValues(fields)).toEqual({ a: 'x' })
  })
})
