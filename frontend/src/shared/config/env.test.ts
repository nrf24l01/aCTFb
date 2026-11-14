import { describe, expect, it } from 'vitest'
import { normalizeApiBaseUrl } from '@/shared/config/env'

describe('normalizeApiBaseUrl', () => {
  it('defaults to /api', () => {
    expect(normalizeApiBaseUrl(undefined)).toBe('/api')
    expect(normalizeApiBaseUrl('')).toBe('/api')
  })

  it('strips trailing slashes', () => {
    expect(normalizeApiBaseUrl('/gateway/api/')).toBe('/gateway/api')
  })

  it('removes a version segment so /v2 is appended exactly once', () => {
    expect(normalizeApiBaseUrl('/api/v2')).toBe('/api')
    expect(normalizeApiBaseUrl('https://example.test/api/v2/')).toBe('https://example.test/api')
  })
})