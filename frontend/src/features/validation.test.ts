import { describe, expect, it } from 'vitest'
import {
  parseAcceptedAnswers,
  parseDateWindow,
  validatePoints,
  validateSlug,
} from '@/features/admin/validation'
import {
  validateEmail,
  validatePassword,
  validateRegistration,
  validateUsername,
} from '@/features/auth/validation'
import { isUuid, normalizeUuidInput } from '@/shared/lib/validate'

describe('admin validation', () => {
  it('matches the Swagger slug pattern', () => {
    expect(validateSlug('my-board')).toBeUndefined()
    expect(validateSlug('My-Board')).toBeTruthy()
    expect(validateSlug('ab')).toBeTruthy()
    expect(validateSlug('double--hyphen')).toBeTruthy()
    expect(validateSlug('trailing-')).toBeTruthy()
  })

  it('requires unique accepted answers for FLAG create', () => {
    expect(parseAcceptedAnswers('a\nb').problem).toBeUndefined()
    expect(parseAcceptedAnswers('a\na').problem).toContain('unique')
    expect(parseAcceptedAnswers('').problem).toContain('at least one')
  })

  it('rejects a non-increasing or invalid window', () => {
    expect(parseDateWindow('2026-01-02T00:00', '2026-01-01T00:00').problem).toContain('after')
    expect(parseDateWindow('', '').problem).toBeUndefined()
    expect(parseDateWindow('2026-01-01T00:00', '').startsAt).toMatch(/Z$/)
  })

  it('rejects negative or fractional points', () => {
    expect(validatePoints(0)).toBeUndefined()
    expect(validatePoints(100)).toBeUndefined()
    expect(validatePoints(-1)).toBeTruthy()
    expect(validatePoints(1.5)).toBeTruthy()
  })
})

describe('auth validation', () => {
  it('matches username/password/email bounds', () => {
    expect(validateUsername('player_1')).toBeUndefined()
    expect(validateUsername('ab')).toBeTruthy()
    expect(validateUsername('space user')).toBeTruthy()
    expect(validatePassword('12345678')).toBeUndefined()
    expect(validatePassword('short')).toBeTruthy()
    expect(validateEmail('player@example.test')).toBeUndefined()
    expect(validateEmail('not-an-email')).toBeTruthy()
  })

  it('flags mismatched registration confirmation', () => {
    const problems = validateRegistration('a@b.test', 'player', 'password1', 'password2')
    expect(problems.confirmPassword).toBeTruthy()
    expect(problems.email).toBeUndefined()
  })
})
describe('board link input', () => {
  const uuid = '4ff4c0e2-2b59-4a3f-9d9a-2b9f1f2a3c4d'

  it('accepts a UUID or extracts it from a board link', () => {
    expect(isUuid(uuid)).toBe(true)
    expect(normalizeUuidInput(uuid)).toBe(uuid)
    expect(normalizeUuidInput(`https://ctf.example/boards/${uuid}?from=email`)).toBe(uuid)
    expect(normalizeUuidInput(`/boards/${uuid}/`)).toBe(uuid)
  })

  it('rejects a non-UUID value', () => {
    expect(isUuid('board-1')).toBe(false)
    expect(isUuid('4ff4c0e2-2b59-4a3f-9d9a')).toBe(false)
  })
})
