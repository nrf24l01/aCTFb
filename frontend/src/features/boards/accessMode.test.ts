import { describe, expect, it } from 'vitest'
import { accessModeLabel, requiresInviteCode } from './accessMode'

describe('invite affordances', () => {
  it('keys the legacy invite panel on the registration field, not the derived mode', () => {
    // A legacy PUBLIC+INVITE_ONLY board projects LEGACY but keeps its sealed code.
    expect(requiresInviteCode({ registrationMode: 'INVITE_ONLY' })).toBe(true)
    // A legacy PRIVATE+INVITE_ONLY+groups board keeps both gates.
    expect(requiresInviteCode({ registrationMode: 'INVITE_ONLY' })).toBe(true)
    // The canonical INVITE mode is also INVITE_ONLY underneath.
    expect(requiresInviteCode({ registrationMode: 'INVITE_ONLY' })).toBe(true)
    // OPEN boards have no legacy sealed invite code.
    expect(requiresInviteCode({ registrationMode: 'OPEN' })).toBe(false)
    // CLOSED boards are staff-add only.
    expect(requiresInviteCode({ registrationMode: 'CLOSED' })).toBe(false)
  })

  it('labels PUBLIC as discovery, not free enrollment', () => {
    expect(accessModeLabel('PUBLIC')).toBe('Public discovery')
  })
})
