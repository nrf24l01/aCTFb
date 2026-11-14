import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import { pageQuery, withQuery } from '@/shared/api/query'
import { createIdempotencyKey } from '@/shared/api/idempotency'
import type {
  ADConfig,
  ADProgress,
  ADRepositoriesResponse,
  ADRepositoryAssignment,
  ADRepositoryAssignmentRequest,
  ADRepositoryAssignmentsResponse,
  ADRun,
  ADRunAccepted,
  ADRunAdminActionRequest,
  ADRunCreateRequest,
  ADRunListResponse,
  ADVulnerabilityAdmin,
  ADVulnerabilityCreateRequest,
  ADVulnerabilityPatchRequest,
  ADVulnerabilitiesResponse,
  RunLogsResponse,
} from '@/shared/types/api'

const adPath = (boardUuid: string, moduleUuid: string) =>
  `/boards/${boardUuid}/modules/${moduleUuid}/attack-defense`
const adminAdPath = (boardUuid: string, moduleUuid: string) =>
  `/admin/boards/${boardUuid}/modules/${moduleUuid}/attack-defense`

export const adApi = {
  getRepositories(boardUuid: string, moduleUuid: string): Promise<ADRepositoriesResponse> {
    return apiRequest<ADRepositoriesResponse>(`${adPath(boardUuid, moduleUuid)}/repositories`)
  },

  getProgress(boardUuid: string, moduleUuid: string): Promise<ADProgress> {
    return apiRequest<ADProgress>(`${adPath(boardUuid, moduleUuid)}/progress`)
  },

  listRuns(boardUuid: string, moduleUuid: string, limit = 100, offset = 0): Promise<ADRunListResponse> {
    return apiRequest<ADRunListResponse>(
      withQuery(`${adPath(boardUuid, moduleUuid)}/runs`, pageQuery(limit, offset)),
    )
  },

  createRun(boardUuid: string, moduleUuid: string, body: ADRunCreateRequest): Promise<ADRunAccepted> {
    return apiRequest<ADRunAccepted>(`${adPath(boardUuid, moduleUuid)}/runs`, {
      method: 'POST',
      body,
      headers: { 'Idempotency-Key': createIdempotencyKey() },
    })
  },

  getRun(boardUuid: string, moduleUuid: string, runUuid: string): Promise<ADRun> {
    return apiRequest<ADRun>(`${adPath(boardUuid, moduleUuid)}/runs/${runUuid}`)
  },

  getRunLogs(boardUuid: string, moduleUuid: string, runUuid: string): Promise<RunLogsResponse> {
    return apiRequest<RunLogsResponse>(`${adPath(boardUuid, moduleUuid)}/runs/${runUuid}/logs`)
  },

  adminGetConfig(boardUuid: string, moduleUuid: string): Promise<ADConfig> {
    return apiRequest<ADConfig>(`${adminAdPath(boardUuid, moduleUuid)}/config`)
  },

  adminPutConfig(boardUuid: string, moduleUuid: string, body: ADConfig): Promise<ADConfig> {
    return apiRequest<ADConfig>(`${adminAdPath(boardUuid, moduleUuid)}/config`, {
      method: 'PUT',
      body,
    })
  },

  adminListVulnerabilities(
    boardUuid: string,
    moduleUuid: string,
    limit = 100,
    offset = 0,
  ): Promise<ADVulnerabilitiesResponse> {
    return apiRequest<ADVulnerabilitiesResponse>(
      withQuery(`${adminAdPath(boardUuid, moduleUuid)}/vulnerabilities`, pageQuery(limit, offset)),
    )
  },

  adminCreateVulnerability(
    boardUuid: string,
    moduleUuid: string,
    body: ADVulnerabilityCreateRequest,
  ): Promise<ADVulnerabilityAdmin> {
    return apiPost<ADVulnerabilityAdmin>(`${adminAdPath(boardUuid, moduleUuid)}/vulnerabilities`, body)
  },

  adminGetVulnerability(
    boardUuid: string,
    moduleUuid: string,
    vulnerabilityUuid: string,
  ): Promise<ADVulnerabilityAdmin> {
    return apiRequest<ADVulnerabilityAdmin>(
      `${adminAdPath(boardUuid, moduleUuid)}/vulnerabilities/${vulnerabilityUuid}`,
    )
  },

  adminUpdateVulnerability(
    boardUuid: string,
    moduleUuid: string,
    vulnerabilityUuid: string,
    body: ADVulnerabilityPatchRequest,
  ): Promise<ADVulnerabilityAdmin> {
    return apiPatch<ADVulnerabilityAdmin>(
      `${adminAdPath(boardUuid, moduleUuid)}/vulnerabilities/${vulnerabilityUuid}`,
      body,
    )
  },

  adminDeleteVulnerability(boardUuid: string, moduleUuid: string, vulnerabilityUuid: string): Promise<void> {
    return apiDelete(`${adminAdPath(boardUuid, moduleUuid)}/vulnerabilities/${vulnerabilityUuid}`)
  },

  adminListRepositories(
    boardUuid: string,
    moduleUuid: string,
    limit = 100,
    offset = 0,
  ): Promise<ADRepositoryAssignmentsResponse> {
    return apiRequest<ADRepositoryAssignmentsResponse>(
      withQuery(`${adminAdPath(boardUuid, moduleUuid)}/repositories`, pageQuery(limit, offset)),
    )
  },

  adminCreateRepository(
    boardUuid: string,
    moduleUuid: string,
    body: ADRepositoryAssignmentRequest,
  ): Promise<ADRepositoryAssignment> {
    return apiPost<ADRepositoryAssignment>(`${adminAdPath(boardUuid, moduleUuid)}/repositories`, body)
  },

  adminGetRepository(
    boardUuid: string,
    moduleUuid: string,
    repositoryBindingUuid: string,
  ): Promise<ADRepositoryAssignment> {
    return apiRequest<ADRepositoryAssignment>(
      `${adminAdPath(boardUuid, moduleUuid)}/repositories/${repositoryBindingUuid}`,
    )
  },

  adminUpdateRepository(
    boardUuid: string,
    moduleUuid: string,
    repositoryBindingUuid: string,
    body: ADRepositoryAssignmentRequest,
  ): Promise<ADRepositoryAssignment> {
    return apiPatch<ADRepositoryAssignment>(
      `${adminAdPath(boardUuid, moduleUuid)}/repositories/${repositoryBindingUuid}`,
      body,
    )
  },

  adminDeleteRepository(boardUuid: string, moduleUuid: string, repositoryBindingUuid: string): Promise<void> {
    return apiDelete(`${adminAdPath(boardUuid, moduleUuid)}/repositories/${repositoryBindingUuid}`)
  },

  adminListRuns(boardUuid: string, moduleUuid: string, limit = 100, offset = 0): Promise<ADRunListResponse> {
    return apiRequest<ADRunListResponse>(
      withQuery(`${adminAdPath(boardUuid, moduleUuid)}/runs`, pageQuery(limit, offset)),
    )
  },

  adminGetRun(boardUuid: string, moduleUuid: string, runUuid: string): Promise<ADRun> {
    return apiRequest<ADRun>(`${adminAdPath(boardUuid, moduleUuid)}/runs/${runUuid}`)
  },

  adminRunAction(
    boardUuid: string,
    moduleUuid: string,
    runUuid: string,
    action: ADRunAdminActionRequest['action'],
  ): Promise<ADRunAccepted> {
    return apiRequest<ADRunAccepted>(`${adminAdPath(boardUuid, moduleUuid)}/runs/${runUuid}/actions`, {
      method: 'POST',
      body: { action },
      headers: { 'Idempotency-Key': createIdempotencyKey() },
    })
  },
}