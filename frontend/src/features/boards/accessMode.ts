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

// Whether a board has a *legacy* sealed invite code cannot be derived from the
// board DTO: it depends on `boards.invite_code_hash`, which the API does not
// expose. The admin page therefore treats the canonical join code panel as the
// only automatic surface and offers the legacy reveal as an explicit action.
