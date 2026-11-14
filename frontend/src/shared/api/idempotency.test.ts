import { describe, expect, it } from 'vitest'
import { createIdempotencyKey, isIdempotencyKey } from '@/shared/api/idempotency'

describe('idempotency keys', () => {
  it('produces keys accepted by the backend header pattern', () => {
    for (let i = 0; i < 20; i += 1) {
      const key = createIdempotencyKey()
      expect(isIdempotencyKey(key)).toBe(true)
      expect(key.length).toBeGreaterThanOrEqual(16)
    }
  })

  it('produces distinct keys for distinct attempts', () => {
    const keys = new Set(Array.from({ length: 50 }, () => createIdempotencyKey()))
    expect(keys.size).toBe(50)
  })

  it('rejects short or illegal keys', () => {
    expect(isIdempotencyKey('short')).toBe(false)
    expect(isIdempotencyKey('valid-key_but has spaces')).toBe(false)
    expect(isIdempotencyKey('a'.repeat(15))).toBe(false)
    expect(isIdempotencyKey('a'.repeat(16))).toBe(true)
  })
})