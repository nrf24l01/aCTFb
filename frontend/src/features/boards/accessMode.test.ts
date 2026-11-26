import { describe, expect, it } from 'vitest'
import { accessModeLabel } from './accessMode'

describe('access mode labels', () => {
  it('labels PUBLIC as discovery, not free enrollment', () => {
    expect(accessModeLabel('PUBLIC')).toBe('Public discovery')
  })

  it('labels INVITE by the canonical join code, not the deprecated invite credential', () => {
    expect(accessModeLabel('INVITE')).toBe('Invite code')
  })

  it('falls back to legacy access for an unknown mode', () => {
    expect(accessModeLabel(undefined)).toBe('Legacy access')
  })
})
