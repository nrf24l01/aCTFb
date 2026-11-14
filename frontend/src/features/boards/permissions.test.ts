import { describe, expect, it } from 'vitest'
import {
  expiryState,
  grantableRoles,
  hasModulePermission,
  validateGrant,
} from '@/features/boards/permissions'
import type { BoardPermissionsResponse, ModuleListItem } from '@/shared/types/api'

const perms: BoardPermissionsResponse = {
  boardUuid: 'board-1',
  roles: ['board_owner'],
  permissions: ['board.read', 'board.staff.manage', 'module.create'],
  modules: [
    {
      moduleUuid: 'module-a',
      roles: ['reviewer'],
      permissions: ['module.submissions.read'],
    },
  ],
}

const modules: ModuleListItem[] = [
  {
    uuid: 'module-a',
    boardUuid: 'board-1',
    name: 'A',
    type: 'TASKS',
    allowedTaskTypes: ['FLAG'],
    status: 'ACTIVE',
    audienceUuids: [],
    order: 0,
  },
]

describe('scoped permissions', () => {
  it('treats board-wide grants as applying to every module', () => {
    expect(hasModulePermission(perms, 'module-z', 'module.create')).toBe(true)
  })

  it('does not leak a module grant to a sibling module', () => {
    expect(hasModulePermission(perms, 'module-a', 'module.submissions.read')).toBe(true)
    expect(hasModulePermission(perms, 'module-b', 'module.submissions.read')).toBe(false)
  })

  it('lets an owner grant editor/reviewer but never owner', () => {
    expect(grantableRoles(perms, false)).toEqual(['board_editor', 'reviewer'])
    expect(grantableRoles(perms, true)).toEqual(['board_owner', 'board_editor', 'reviewer'])
  })

  it('rejects owner-in-module and past expiry', () => {
    expect(
      validateGrant({
        role: 'board_editor',
        moduleUuid: 'module-a',
        expiresAt: null,
        modules,
        permissions: perms,
        globalAdmin: false,
      }),
    ).toBeNull()
    expect(
      validateGrant({
        role: 'board_owner',
        moduleUuid: 'module-a',
        expiresAt: null,
        modules,
        permissions: perms,
        globalAdmin: false,
      }),
    ).toContain('Only a global admin')
    expect(
      validateGrant({
        role: 'board_editor',
        moduleUuid: null,
        expiresAt: '2020-01-01T00:00:00Z',
        modules,
        permissions: perms,
        globalAdmin: false,
      }),
    ).toContain('future')
  })

  it('reports revoked, expired and active grants from the response fields', () => {
    expect(expiryState({ revokedAt: '2026-01-01T00:00:00Z' })).toBe('revoked')
    expect(expiryState({ expiresAt: '2020-01-01T00:00:00Z' })).toBe('expired')
    expect(expiryState({ expiresAt: '2999-01-01T00:00:00Z' })).toBe('active')
    expect(expiryState({})).toBe('active')
  })
})