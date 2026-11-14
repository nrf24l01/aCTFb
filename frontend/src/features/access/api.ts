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
}