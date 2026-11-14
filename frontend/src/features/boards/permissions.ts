import type { BoardPermissionsResponse, BoardRole, ModuleListItem, PermissionCode } from '@/shared/types/api'

export function hasBoardPermission(
  permissions: BoardPermissionsResponse | null,
  code: PermissionCode,
): boolean {
  return permissions?.permissions.includes(code) ?? false
}

/** Board-wide permission or a permission granted for this module. */
export function hasModulePermission(
  permissions: BoardPermissionsResponse | null,
  moduleUuid: string,
  code: PermissionCode,
): boolean {
  if (!permissions) return false
  if (permissions.permissions.includes(code)) return true
  return permissions.modules.some(
    (entry) => entry.moduleUuid === moduleUuid && entry.permissions.includes(code),
  )
}

/**
 * A visible but unjoined board must still offer self-join. Hiding the action
 * left TEAM participants unable to join, so the team panel (which requires a
 * participant row) could never mount. The backend remains authoritative for
 * windows, invitation codes and closed registration.
 */
export function isBoardJoinOffered(
  board: { status: string; registrationMode: string } | null,
  me: { joined: boolean } | null,
): boolean {
  if (!board || me?.joined) return false
  if (board.status !== 'SCHEDULED' && board.status !== 'ACTIVE') return false
  return board.registrationMode !== 'CLOSED'
}

export function isBoardStaff(permissions: BoardPermissionsResponse | null): boolean {
  return Boolean(permissions && permissions.roles.length > 0)
}

export function canCreateModule(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'module.create')
}

export function canManageModule(
  permissions: BoardPermissionsResponse | null,
  moduleUuid: string,
): boolean {
  return hasModulePermission(permissions, moduleUuid, 'module.update')
}

export function canDeleteModule(
  permissions: BoardPermissionsResponse | null,
  moduleUuid: string,
): boolean {
  return hasModulePermission(permissions, moduleUuid, 'module.delete')
}

export function canManageModuleTasks(
  permissions: BoardPermissionsResponse | null,
  moduleUuid: string,
): boolean {
  return hasModulePermission(permissions, moduleUuid, 'module.tasks.manage')
}

export function canReadSubmissions(
  permissions: BoardPermissionsResponse | null,
  moduleUuid?: string,
): boolean {
  if (!permissions) return false
  if (permissions.permissions.includes('module.submissions.read')) return true
  return moduleUuid
    ? permissions.modules.some(
        (entry) =>
          entry.moduleUuid === moduleUuid && entry.permissions.includes('module.submissions.read'),
      )
    : false
}

export function canManageStaff(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'board.staff.manage')
}

export function canManageParticipants(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'board.participants.manage')
}

export function canManageBoard(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'board.update')
}

export function canPublishBoard(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'board.publish')
}

export function canReadAudit(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'board.audit.read')
}

export function canReviewSubmissions(permissions: BoardPermissionsResponse | null): boolean {
  return hasBoardPermission(permissions, 'module.submissions.review')
}

/**
 * Roles the current actor may hand out. A board owner can only grant
 * editor/reviewer in their own board; only a global admin can grant owner.
 */
export function grantableRoles(
  permissions: BoardPermissionsResponse | null,
  globalAdmin: boolean,
): BoardRole[] {
  if (globalAdmin) return ['board_owner', 'board_editor', 'reviewer']
  if (hasBoardPermission(permissions, 'board.staff.manage')) return ['board_editor', 'reviewer']
  return []
}

export interface GrantValidationInput {
  role: BoardRole
  moduleUuid: string | null
  expiresAt: string | null
  modules: ModuleListItem[]
  permissions: BoardPermissionsResponse | null
  globalAdmin: boolean
  now?: Date
}

/** Client-side UX checks. The backend remains authoritative. */
export function validateGrant(input: GrantValidationInput): string | null {
  const now = input.now ?? new Date()
  const allowed = grantableRoles(input.permissions, input.globalAdmin)
  if (!allowed.includes(input.role)) {
    return input.role === 'board_owner'
      ? 'Only a global admin can grant board_owner.'
      : 'You cannot grant this role in this board.'
  }
  if (input.role === 'board_owner' && input.moduleUuid) {
    return 'A board_owner grant must be board-wide; remove the module.'
  }
  if (input.moduleUuid && !input.modules.some((module) => module.uuid === input.moduleUuid)) {
    return 'Choose a module that belongs to this board.'
  }
  if (input.expiresAt) {
    const expiry = new Date(input.expiresAt)
    if (Number.isNaN(expiry.getTime())) return 'Enter a valid expiry date and time.'
    if (expiry.getTime() <= now.getTime()) return 'Expiry must be in the future.'
  }
  return null
}

export function expiryState(
  binding: { expiresAt?: string | null; revokedAt?: string | null },
  now = new Date(),
): 'revoked' | 'expired' | 'active' {
  if (binding.revokedAt) return 'revoked'
  if (binding.expiresAt && new Date(binding.expiresAt).getTime() <= now.getTime()) return 'expired'
  return 'active'
}