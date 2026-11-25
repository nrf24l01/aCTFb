import type { BoardAccessMode, ParticipationMode } from '@/shared/types/api'

const accessModeLabels: Record<BoardAccessMode, string> = {
  PUBLIC: 'Public discovery',
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

/**
 * Whether a board still carries a legacy sealed invite code.
 *
 * Every self-join now needs a board join code, so this helper no longer decides
 * whether the join form appears. It decides whether the deprecated legacy
 * invite panel (which needs the credential sealer) is worth showing for a board
 * that already had one. It is keyed on the authoritative persisted
 * `registrationMode`, not on the derived `accessMode`.
 */
export function requiresInviteCode(board: { registrationMode: string } | null | undefined): boolean {
  return board?.registrationMode === 'INVITE_ONLY'
}
