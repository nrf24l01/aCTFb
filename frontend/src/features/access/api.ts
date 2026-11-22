import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import { withQuery, pageQuery } from '@/shared/api/query'
import type {
  AccessGroup,
  AccessGroupCreateRequest,
  AccessGroupPatchRequest,
  AccessGroupsResponse,
  AdminUser,
  AdminUserCreateRequest,
  AdminUserPatchRequest,
  AdminUsersResponse,
  CaptainRosterResponse,
  TeamDirectoryResponse,
  UserDirectoryResponse,
} from '@/shared/types/api'

export const accessApi = {
  listGroups(limit = 100, offset = 0, query = ''): Promise<AccessGroupsResponse> {
    return apiRequest<AccessGroupsResponse>(
      withQuery('/admin/access-groups', pageQuery(limit, offset, query)),
    )
  },

  createGroup(name: string): Promise<AccessGroup> {
    const body: AccessGroupCreateRequest = { name }
    return apiPost<AccessGroup>('/admin/access-groups', body)
  },

  getGroup(groupUuid: string): Promise<AccessGroup> {
    return apiRequest<AccessGroup>(`/admin/access-groups/${groupUuid}`)
  },

  updateGroup(groupUuid: string, name: string): Promise<AccessGroup> {
    const body: AccessGroupPatchRequest = { name }
    return apiPatch<AccessGroup>(`/admin/access-groups/${groupUuid}`, body)
  },

  deleteGroup(groupUuid: string): Promise<void> {
    return apiDelete(`/admin/access-groups/${groupUuid}`)
  },

  listUsers(limit = 100, offset = 0, query = ''): Promise<AdminUsersResponse> {
    return apiRequest<AdminUsersResponse>(withQuery('/admin/users', pageQuery(limit, offset, query)))
  },

  createUser(body: AdminUserCreateRequest): Promise<AdminUser> {
    return apiPost<AdminUser>('/admin/users', body)
  },

  getUser(userUuid: string): Promise<AdminUser> {
    return apiRequest<AdminUser>(`/admin/users/${userUuid}`)
  },

  updateUser(userUuid: string, body: AdminUserPatchRequest): Promise<AdminUser> {
    return apiPatch<AdminUser>(`/admin/users/${userUuid}`, body)
  },

  searchUsers(query: string, limit = 20, offset = 0): Promise<UserDirectoryResponse> {
    return apiRequest<UserDirectoryResponse>(withQuery('/admin/user-search', pageQuery(limit, offset, query)))
  },

  /**
   * Board user search. `scope` is explicit so a caller cannot accidentally
   * enumerate the global directory: PARTICIPANTS (default) returns only current
   * board participants, DIRECTORY requires participant/staff management.
   */
  searchBoardUsers(
    boardUuid: string,
    query: string,
    scope: 'PARTICIPANTS' | 'DIRECTORY' = 'PARTICIPANTS',
    limit = 20,
    offset = 0,
  ): Promise<UserDirectoryResponse> {
    return apiRequest<UserDirectoryResponse>(
      withQuery(`/admin/boards/${boardUuid}/user-search`, {
        ...pageQuery(limit, offset, query),
        scope,
      }),
    )
  },

  searchBoardTeams(boardUuid: string, query: string, limit = 20, offset = 0): Promise<TeamDirectoryResponse> {
    return apiRequest<TeamDirectoryResponse>(
      withQuery(`/admin/boards/${boardUuid}/team-search`, pageQuery(limit, offset, query)),
    )
  },

  searchCaptainRoster(boardUuid: string, query: string, limit = 20, offset = 0): Promise<CaptainRosterResponse> {
    return apiRequest<CaptainRosterResponse>(
      withQuery(`/boards/${boardUuid}/team/roster-search`, pageQuery(limit, offset, query)),
    )
  },

  listGroupMembers(groupUuid: string, limit = 50, offset = 0, query = ''): Promise<UserDirectoryResponse> {
    return apiRequest<UserDirectoryResponse>(
      withQuery(`/admin/access-groups/${groupUuid}/members`, pageQuery(limit, offset, query)),
    )
  },

  addGroupMember(groupUuid: string, userUuid: string): Promise<void> {
    return apiPost<void>(`/admin/access-groups/${groupUuid}/members`, { userUuid })
  },

  removeGroupMember(groupUuid: string, userUuid: string): Promise<void> {
    return apiDelete(`/admin/access-groups/${groupUuid}/members/${userUuid}`)
  },
}