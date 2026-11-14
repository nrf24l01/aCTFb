import type { Team } from '@/shared/types/api'

/**
 * Captain authority comes from the returned captainUuid, not from a generic
 * user role or a client-selected UUID.
 */
export function isTeamCaptain(team: Team | null, userUuid: string): boolean {
  return Boolean(team && userUuid && team.captainUuid === userUuid)
}

export function canManageTeam(team: Team | null, userUuid: string): boolean {
  return isTeamCaptain(team, userUuid)
}