import { apiDelete, apiPost, apiRequest } from '@/shared/api/client'
import type {
  DeploymentTemplate,
  DeploymentTemplateAllowlist,
  DeploymentTemplateAllowlistCreateRequest,
  DeploymentTemplateAllowlistListResponse,
  DeploymentTemplateDiff,
  DeploymentTemplateDraft,
  DeploymentTemplateDraftRequest,
  DeploymentTemplateListResponse,
  DeploymentTemplatePreview,
  DeploymentTemplatePublishRequest,
  DeploymentTemplateValidation,
  DeploymentTemplateVersion,
  DeploymentTemplateVersionListResponse,
} from '@/shared/types/api'
import { pageQuery, withQuery } from '@/shared/api/query'

const base = (templateUuid: string) => `/admin/deployment-templates/${templateUuid}`

export const templatesApi = {
  create(): Promise<DeploymentTemplate> {
    const slug = `tpl-${Date.now().toString(36)}`
    const manifestYaml = [
      'apiVersion: actfb.dev/v1alpha1',
      'kind: DeploymentTemplate',
      'metadata:',
      '  name: Untitled deployment template',
      `  slug: ${slug}`,
      "  description: ''",
      'nodes: []',
      '',
    ].join('\n')
    return apiPost<DeploymentTemplate>('/admin/deployment-templates', {
      manifestYaml,
      files: [],
    })
  },

  list(limit = 100, offset = 0, query = ''): Promise<DeploymentTemplateListResponse> {
    return apiRequest<DeploymentTemplateListResponse>(
      withQuery('/admin/deployment-templates', pageQuery(limit, offset, query)),
    )
  },

  get(templateUuid: string): Promise<DeploymentTemplate> {
    return apiRequest<DeploymentTemplate>(base(templateUuid))
  },

  getDraft(templateUuid: string): Promise<DeploymentTemplateDraft> {
    return apiRequest<DeploymentTemplateDraft>(`${base(templateUuid)}/draft`)
  },

  putDraft(templateUuid: string, body: DeploymentTemplateDraftRequest): Promise<DeploymentTemplateDraft> {
    return apiRequest<DeploymentTemplateDraft>(`${base(templateUuid)}/draft`, { method: 'PUT', body })
  },

  validate(templateUuid: string): Promise<DeploymentTemplateValidation> {
    return apiPost<DeploymentTemplateValidation>(`${base(templateUuid)}/draft/validate`)
  },

  preview(templateUuid: string): Promise<DeploymentTemplatePreview> {
    return apiPost<DeploymentTemplatePreview>(`${base(templateUuid)}/draft/preview`)
  },

  publish(templateUuid: string, versionNote: string): Promise<DeploymentTemplateVersion> {
    const body: DeploymentTemplatePublishRequest = { versionNote }
    return apiPost<DeploymentTemplateVersion>(`${base(templateUuid)}/publish`, body)
  },

  listVersions(templateUuid: string, limit = 100, offset = 0): Promise<DeploymentTemplateVersionListResponse> {
    return apiRequest<DeploymentTemplateVersionListResponse>(
      withQuery(`${base(templateUuid)}/versions`, pageQuery(limit, offset)),
    )
  },

  getVersion(templateUuid: string, version: number): Promise<DeploymentTemplateVersion> {
    return apiRequest<DeploymentTemplateVersion>(`${base(templateUuid)}/versions/${version}`)
  },

  diff(templateUuid: string, version: number, from: number): Promise<DeploymentTemplateDiff> {
    return apiRequest<DeploymentTemplateDiff>(
      withQuery(`${base(templateUuid)}/versions/${version}/diff`, { from }),
    )
  },

  listAllowlists(templateUuid: string): Promise<DeploymentTemplateAllowlistListResponse> {
    return apiRequest<DeploymentTemplateAllowlistListResponse>(`${base(templateUuid)}/allowlist`)
  },

  createAllowlist(
    templateUuid: string,
    body: DeploymentTemplateAllowlistCreateRequest,
  ): Promise<DeploymentTemplateAllowlist> {
    return apiPost<DeploymentTemplateAllowlist>(`${base(templateUuid)}/allowlist`, body)
  },

  revokeAllowlist(templateUuid: string, allowlistUuid: string): Promise<void> {
    return apiDelete(`${base(templateUuid)}/allowlist/${allowlistUuid}`)
  },
}