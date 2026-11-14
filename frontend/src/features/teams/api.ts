import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import { withQuery, pageQuery } from '@/shared/api/query'
import type {
  AdminTeamMemberAddRequest,
  Page,
  Team,
  TeamCreateRequest,
  TeamInviteResponse,
  TeamJoinRequest,
  TeamPatchRequest,
  TransferCaptainRequest,
} from '@/shared/types/api'

const boardPath = (boardUuid: string) => `/boards/${boardUuid}`

export const teamsApi = {
  getMyTeam(boardUuid: string): Promise<Team> {
    return apiRequest<Team>(`${boardPath(boardUuid)}/team`)
  },

  updateMyTeam(boardUuid: string, name: string): Promise<Team> {
    const body: TeamPatchRequest = { name }
    return apiPatch<Team>(`${boardPath(boardUuid)}/team`, body)
  },

  createTeam(boardUuid: string, name: string): Promise<Team> {
    const body: TeamCreateRequest = { name }
    return apiPost<Team>(`${boardPath(boardUuid)}/teams`, body)
  },

  joinTeam(boardUuid: string, inviteKey: string): Promise<Team> {
    const body: TeamJoinRequest = { inviteKey }
    return apiPost<Team>(`${boardPath(boardUuid)}/teams/join`, body)
  },

  getInviteKey(boardUuid: string): Promise<TeamInviteResponse> {
    return apiRequest<TeamInviteResponse>(`${boardPath(boardUuid)}/team/invite-key`)
  },

  rotateInviteKey(boardUuid: string): Promise<TeamInviteResponse> {
    return apiPost<TeamInviteResponse>(`${boardPath(boardUuid)}/team/invite-key/rotate`)
  },

  leaveTeam(boardUuid: string): Promise<void> {
    return apiPost<void>(`${boardPath(boardUuid)}/team/leave`)
  },

  removeMember(boardUuid: string, userUuid: string): Promise<void> {
    return apiDelete(`${boardPath(boardUuid)}/team/members/${userUuid}`)
  },

  transferCaptain(boardUuid: string, userUuid: string): Promise<Team> {
    const body: TransferCaptainRequest = { userUuid }
    return apiPost<Team>(`${boardPath(boardUuid)}/team/captain`, body)
  },

  adminListTeams(boardUuid: string, query = '', limit = 100, offset = 0): Promise<Page<Team>> {
    return apiRequest<Page<Team>>(
      withQuery(`/admin/boards/${boardUuid}/teams`, pageQuery(limit, offset, query)),
    )
  },

  adminCreateTeam(boardUuid: string, name: string): Promise<Team> {
    const body: TeamCreateRequest = { name }
    return apiPost<Team>(`/admin/boards/${boardUuid}/teams`, body)
  },

  adminGetTeam(boardUuid: string, teamUuid: string): Promise<Team> {
    return apiRequest<Team>(`/admin/boards/${boardUuid}/teams/${teamUuid}`)
  },

  adminUpdateTeam(boardUuid: string, teamUuid: string, name: string): Promise<Team> {
    const body: TeamPatchRequest = { name }
    return apiPatch<Team>(`/admin/boards/${boardUuid}/teams/${teamUuid}`, body)
  },

  adminDeleteTeam(boardUuid: string, teamUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/teams/${teamUuid}`)
  },

  adminAddMember(boardUuid: string, teamUuid: string, userUuid: string): Promise<Team> {
    const body: AdminTeamMemberAddRequest = { userUuid }
    return apiPost<Team>(`/admin/boards/${boardUuid}/teams/${teamUuid}/members`, body)
  },

  adminRemoveMember(boardUuid: string, teamUuid: string, userUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/teams/${teamUuid}/members/${userUuid}`)
  },
}