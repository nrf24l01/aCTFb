import { describe, expect, it } from 'vitest'
import { fromTemplateParameters, hasUnsupportedPolicy } from './taskParameters'
import type { ApprovedTemplateParameter } from '@/features/instances/api'

describe('fromTemplateParameters', () => {
  it('maps the published manifest projection to typed form fields', () => {
    const declared: ApprovedTemplateParameter[] = [
      { key: 'title', label: 'Title', type: 'string', required: true, secret: false, default: 'hello' },
      { key: 'replicas', label: 'Replicas', type: 'int', required: false, secret: false, default: '2' },
      { key: 'tls', label: 'TLS', type: 'bool', required: false, secret: false, default: 'true' },
      { key: 'size', label: 'Size', type: 'choice', options: ['s', 'm'], required: true, secret: false },
      { key: 'apiKey', label: 'API key', type: 'string', required: true, secret: true, default: 'leaked-default' },
    ]
    const fields = fromTemplateParameters(declared)
    expect(fields.map((field) => [field.key, field.type])).toEqual([
      ['title', 'string'],
      ['replicas', 'number'],
      ['tls', 'boolean'],
      ['size', 'select'],
      ['apiKey', 'string'],
    ])
    expect(fields[3].options).toEqual(['s', 'm'])
    // A secret default is never carried into the form.
    expect(fields[4].secret).toBe(true)
    expect(fields[4].defaultValue).toBe('')
    expect(fields[0].defaultValue).toBe('hello')
  })

  it('renders a manifest `text` parameter as a multiline textarea', () => {
    const fields = fromTemplateParameters([
      { key: 'notes', label: 'Notes', type: 'text', required: false, secret: false },
    ])
    expect(fields).toHaveLength(1)
    expect(fields[0].type).toBe('textarea')
  })

  it('keeps an unknown declared type usable as a single-line string', () => {
    // The server rejects an unknown manifest type, so this is only a defensive
    // UI fallback: it must not throw or produce a control the form cannot bind.
    const fields = fromTemplateParameters([
      { key: 'mystery', label: 'Mystery', type: 'provider-credential', required: false, secret: false },
    ])
    expect(fields[0].type).toBe('string')
  })

  it('treats a non-empty allowlist policy as unsupported', () => {
    expect(hasUnsupportedPolicy(undefined)).toBe(false)
    expect(hasUnsupportedPolicy({})).toBe(false)
    expect(hasUnsupportedPolicy({ maxReplicas: 3 })).toBe(true)
  })
})
