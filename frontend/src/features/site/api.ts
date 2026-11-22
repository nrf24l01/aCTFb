import { apiPatch, apiRequest } from '@/shared/api/client'
import type { AdminSite, AdminSitePatchRequest, SitePublic } from '@/shared/types/api'

export const siteApi = {
  getPublic(): Promise<SitePublic> {
    return apiRequest<SitePublic>('/site')
  },

  adminGet(): Promise<AdminSite> {
    return apiRequest<AdminSite>('/admin/site')
  },

  update(body: AdminSitePatchRequest): Promise<AdminSite> {
    return apiPatch<AdminSite>('/admin/site', body)
  },
}
