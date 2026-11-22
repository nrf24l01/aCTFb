import type { BoardAccessMode, ParticipationMode } from '@/shared/types/api'

const accessModeLabels: Record<BoardAccessMode, string> = {
  PUBLIC: 'Public',
  AUDIENCE: 'Selected audiences',
  INVITE: 'Invite code',
  LEGACY: 'Legacy access',
}

const participationLabels: Record<ParticipationMode, string> = {
  INDIVIDUAL: 'Individual',
  TEAM: 'Teams',
}

export function accessModeLabel(mode: BoardAccessMode | undefined): string {
  return mode ? accessModeLabels[mode] : 'Legacy access'
}

export function participationLabel(mode: ParticipationMode): string {
  return participationLabels[mode] ?? mode
}

export function isInviteBoard(mode: BoardAccessMode | undefined): boolean {
  return mode === 'INVITE'
}

/**
 * Whether a board requires an invite code to join. This is deliberately keyed on
 * the authoritative persisted `registrationMode`, not on the derived
 * `accessMode`: a legacy combination such as PUBLIC+INVITE_ONLY or
 * PRIVATE+INVITE_ONLY+groups projects LEGACY but still needs the code, so gating
 * on the derived mode would silently remove the join input and the staff
 * reveal/rotate controls.
 */
export function requiresInviteCode(board: { registrationMode: string } | null | undefined): boolean {
  return board?.registrationMode === 'INVITE_ONLY'
}
