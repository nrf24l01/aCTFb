import { apiDelete, apiPost, apiRequest } from '@/shared/api/client'
import { pageQuery, withQuery } from '@/shared/api/query'
import type {
  PermissionGrant,
  PermissionGrantCreateRequest,
  PermissionGrantListResponse,
  Plugin,
  PluginEventDeliveryListResponse,
  PluginInstallRequest,
  PluginListResponse,
  PluginLogsResponse,
  PluginUpgradeRequest,
  PluginValidateRequest,
  PluginValidation,
  PluginVisibleListResponse,
} from '@/shared/types/api'

const admin = (id?: string) => (id ? `/admin/plugins/${id}` : '/admin/plugins')

export const pluginsApi = {
  list(limit = 100, offset = 0): Promise<PluginListResponse> {
    return apiRequest<PluginListResponse>(withQuery('/admin/plugins', pageQuery(limit, offset)))
  },

  get(id: string): Promise<Plugin> {
    return apiRequest<Plugin>(admin(id))
  },

  validate(packagePath: string): Promise<PluginValidation> {
    const body: PluginValidateRequest = { packagePath }
    return apiPost<PluginValidation>('/admin/plugins/validate', body)
  },

  install(body: PluginInstallRequest): Promise<Plugin> {
    return apiPost<Plugin>('/admin/plugins/install', { ...body, enable: false })
  },

  enable(id: string): Promise<Plugin> {
    return apiPost<Plugin>(`${admin(id)}/enable`)
  },

  disable(id: string): Promise<Plugin> {
    return apiPost<Plugin>(`${admin(id)}/disable`)
  },

  upgrade(id: string, body: PluginUpgradeRequest): Promise<Plugin> {
    return apiPost<Plugin>(`${admin(id)}/upgrade`, body)
  },

  remove(id: string): Promise<void> {
    return apiDelete(admin(id))
  },

  logs(id: string, limit = 1000): Promise<PluginLogsResponse> {
    return apiRequest<PluginLogsResponse>(withQuery(`${admin(id)}/logs`, { limit }))
  },

  eventDeliveries(id: string, limit = 100, offset = 0): Promise<PluginEventDeliveryListResponse> {
    return apiRequest<PluginEventDeliveryListResponse>(
      withQuery(`${admin(id)}/event-deliveries`, pageQuery(limit, offset)),
    )
  },

  replayEvent(id: string, eventId: string): Promise<void> {
    return apiPost<void>(`${admin(id)}/event-deliveries/${eventId}/replay`)
  },

  listGrants(id: string, limit = 100, offset = 0): Promise<PermissionGrantListResponse> {
    return apiRequest<PermissionGrantListResponse>(
      withQuery(`${admin(id)}/permission-grants`, pageQuery(limit, offset)),
    )
  },

  createGrant(id: string, body: PermissionGrantCreateRequest): Promise<PermissionGrant> {
    return apiPost<PermissionGrant>(`${admin(id)}/permission-grants`, body)
  },

  revokeGrant(id: string, grantUuid: string): Promise<void> {
    return apiDelete(`${admin(id)}/permission-grants/${grantUuid}`)
  },

  visible(): Promise<PluginVisibleListResponse> {
    return apiRequest<PluginVisibleListResponse>('/plugins')
  },
}