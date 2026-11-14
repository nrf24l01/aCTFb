import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import { withQuery, pageQuery } from '@/shared/api/query'
import type {
  AdminAudiencesResponse,
  Audience,
  AudienceCreateRequest,
  AudienceListResponse,
  AudienceMember,
  AudienceMemberCreateRequest,
  AudienceMembersResponse,
  AudiencePatchRequest,
  SubjectType,
} from '@/shared/types/api'

export const audiencesApi = {
  listMine(boardUuid: string): Promise<AudienceListResponse> {
    return apiRequest<AudienceListResponse>(`/boards/${boardUuid}/audiences/me`)
  },

  adminList(boardUuid: string, limit = 100, offset = 0, query = ''): Promise<AdminAudiencesResponse> {
    return apiRequest<AdminAudiencesResponse>(
      withQuery(`/admin/boards/${boardUuid}/audiences`, pageQuery(limit, offset, query)),
    )
  },

  adminCreate(boardUuid: string, body: AudienceCreateRequest): Promise<Audience> {
    return apiPost<Audience>(`/admin/boards/${boardUuid}/audiences`, body)
  },

  adminGet(boardUuid: string, audienceUuid: string): Promise<Audience> {
    return apiRequest<Audience>(`/admin/boards/${boardUuid}/audiences/${audienceUuid}`)
  },

  adminUpdate(boardUuid: string, audienceUuid: string, body: AudiencePatchRequest): Promise<Audience> {
    return apiPatch<Audience>(`/admin/boards/${boardUuid}/audiences/${audienceUuid}`, body)
  },

  adminDelete(boardUuid: string, audienceUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/audiences/${audienceUuid}`)
  },

  listMembers(
    boardUuid: string,
    audienceUuid: string,
    limit = 100,
    offset = 0,
  ): Promise<AudienceMembersResponse> {
    return apiRequest<AudienceMembersResponse>(
      withQuery(`/admin/boards/${boardUuid}/audiences/${audienceUuid}/members`, pageQuery(limit, offset)),
    )
  },

  addMember(
    boardUuid: string,
    audienceUuid: string,
    subjectType: SubjectType,
    subjectUuid: string,
  ): Promise<AudienceMember> {
    const body: AudienceMemberCreateRequest = { subjectType, subjectUuid }
    return apiPost<AudienceMember>(
      `/admin/boards/${boardUuid}/audiences/${audienceUuid}/members`,
      body,
    )
  },

  removeMember(
    boardUuid: string,
    audienceUuid: string,
    subjectType: SubjectType,
    subjectUuid: string,
  ): Promise<void> {
    return apiDelete(
      `/admin/boards/${boardUuid}/audiences/${audienceUuid}/members/${subjectType}/${subjectUuid}`,
    )
  },
}