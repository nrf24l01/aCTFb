import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import type {
  IntegrationCreateRequest,
  IntegrationPatchRequest,
  IntegrationSummary,
  IntegrationTestResponse,
  IntegrationsResponse,
} from '@/shared/types/api'
import { pageQuery, withQuery } from '@/shared/api/query'

export const integrationsApi = {
  list(limit = 100, offset = 0, query = ''): Promise<IntegrationsResponse> {
    return apiRequest<IntegrationsResponse>(
      withQuery('/admin/integrations', pageQuery(limit, offset, query)),
    )
  },

  create(body: IntegrationCreateRequest): Promise<IntegrationSummary> {
    return apiPost<IntegrationSummary>('/admin/integrations', body)
  },

  get(uuid: string): Promise<IntegrationSummary> {
    return apiRequest<IntegrationSummary>(`/admin/integrations/${uuid}`)
  },

  update(uuid: string, body: IntegrationPatchRequest): Promise<IntegrationSummary> {
    return apiPatch<IntegrationSummary>(`/admin/integrations/${uuid}`, body)
  },

  remove(uuid: string): Promise<void> {
    return apiDelete(`/admin/integrations/${uuid}`)
  },

  test(uuid: string): Promise<IntegrationTestResponse> {
    return apiPost<IntegrationTestResponse>(`/admin/integrations/${uuid}/test`)
  },
}