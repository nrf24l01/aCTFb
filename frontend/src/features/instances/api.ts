import { apiRequest } from '@/shared/api/client'
import { pageQuery, withQuery } from '@/shared/api/query'
import { createIdempotencyKey } from '@/shared/api/idempotency'
import type {
  AdminInstancesResponse,
  Instance,
  InstanceActionRequest,
} from '@/shared/types/api'

export interface ApprovedTemplateParameter {
  key: string
  label: string
  type: string
  options?: string[]
  /** Always blank for a secret parameter; a secret default is never disclosed. */
  default?: string
  required: boolean
  secret: boolean
}

export interface ApprovedInstanceTemplate {
  templateUuid: string
  version: number
  slug: string
  name: string
  /** Operator allowlist restriction. Non-empty values are not supported yet. */
  parametersPolicy?: Record<string, unknown>
  /** Safe projection of the published manifest's parameter definitions. */
  parameters?: ApprovedTemplateParameter[]
}

export interface InstanceListQuery {
  boardUuid?: string
  moduleUuid?: string
  scope?: 'TASK' | 'PENTEST'
  ownerType?: 'USER' | 'TEAM'
  ownerUuid?: string
  status?: string
  limit?: number
  offset?: number
}

export const instancesApi = {
  listApprovedTemplates(boardUuid: string, moduleUuid: string): Promise<{ items: ApprovedInstanceTemplate[] }> {
    return apiRequest<{ items: ApprovedInstanceTemplate[] }>(
      `/boards/${boardUuid}/modules/${moduleUuid}/instance-templates`,
    )
  },

  getTaskInstance(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<Instance> {
    return apiRequest<Instance>(
      `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/instance`,
    )
  },

  startTaskInstance(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<Instance> {
    const key = createIdempotencyKey()
    return apiRequest<Instance>(
      `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/instance`,
      { method: 'POST', headers: { 'Idempotency-Key': key } },
    )
  },

  taskAction(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    action: InstanceActionRequest['action'],
  ): Promise<Instance> {
    const key = createIdempotencyKey()
    return apiRequest<Instance>(
      `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/instance/actions`,
      { method: 'POST', body: { action }, headers: { 'Idempotency-Key': key } },
    )
  },

  adminList(query: InstanceListQuery = {}): Promise<AdminInstancesResponse> {
    return apiRequest<AdminInstancesResponse>(
      withQuery('/admin/instances', {
        ...pageQuery(query.limit ?? 100, query.offset ?? 0),
        boardUuid: query.boardUuid,
        moduleUuid: query.moduleUuid,
        scope: query.scope,
        ownerType: query.ownerType,
        ownerUuid: query.ownerUuid,
        status: query.status,
      }),
    )
  },

  adminGet(instanceUuid: string): Promise<Instance> {
    return apiRequest<Instance>(`/admin/instances/${instanceUuid}`)
  },

  adminAction(instanceUuid: string, action: InstanceActionRequest['action']): Promise<Instance> {
    const key = createIdempotencyKey()
    return apiRequest<Instance>(`/admin/instances/${instanceUuid}/actions`, {
      method: 'POST',
      body: { action },
      headers: { 'Idempotency-Key': key },
    })
  },
}