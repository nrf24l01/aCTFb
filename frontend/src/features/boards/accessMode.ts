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
