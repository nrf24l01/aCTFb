import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminTemplatesPage from '@/features/admin/pages/AdminTemplatesPage.vue'
import { templatesApi } from '@/features/templates/api'
import { ApiError } from '@/shared/api/errors'
import type { DeploymentTemplate } from '@/shared/types/api'

const push = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

vi.mock('@/features/templates/api', () => ({
  templatesApi: {
    list: vi.fn(),
    create: vi.fn(),
  },
}))

const roles = { value: ['admin'] }
vi.mock('@/features/auth/session', () => ({
  useSession: () => ({ state: { user: { roles: roles.value } } }),
}))

const list = vi.mocked(templatesApi.list)

const sample: DeploymentTemplate = {
  uuid: '11111111-1111-4111-8111-111111111111',
  slug: 'lab-template',
  name: 'Lab template',
  description: 'Bounded lab',
  createdAt: '2026-11-25T09:00:00Z',
  updatedAt: '2026-11-25T09:30:00Z',
}

function apiError(status: number, code: string, message: string): ApiError {
  return new ApiError(status, {
    code,
    message,
    path: '/api/v2/admin/deployment-templates',
    timestamp: '2026-11-25T09:00:00Z',
    traceId: 'trace-template',
  })
}

async function mountPage() {
  const wrapper = mount(AdminTemplatesPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  list.mockReset()
  push.mockReset()
  roles.value = ['admin']
})

describe('AdminTemplatesPage catalog states', () => {
  it('renders the empty catalog as a normal empty state, never as an error', async () => {
    list.mockResolvedValue({ items: [], limit: 100, offset: 0, total: 0 })
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('No deployment templates yet')
    expect(wrapper.text()).not.toContain('Could not load deployment templates')
    expect(wrapper.findAll('button').some((button) => button.text().includes('Create template'))).toBe(true)
    wrapper.unmount()
  })

  it('keeps a real 403 as an error with a retry action', async () => {
    list.mockRejectedValue(apiError(403, 'FORBIDDEN', 'Access denied'))
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Could not load deployment templates')
    expect(wrapper.text()).toContain('Access denied')
    expect(wrapper.text()).not.toContain('No deployment templates yet')
    expect(wrapper.findAll('button').some((button) => button.text().includes('Retry'))).toBe(true)
    wrapper.unmount()
  })

  it('keeps an unexpected 404 as an error instead of a quiet empty list', async () => {
    list.mockRejectedValue(apiError(404, 'NOT_FOUND', 'Not found'))
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Could not load deployment templates')
    expect(wrapper.text()).not.toContain('No deployment templates yet')
    wrapper.unmount()
  })

  it('keeps a 500 as an error', async () => {
    list.mockRejectedValue(apiError(500, 'INTERNAL_ERROR', 'Internal server error'))
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Could not load deployment templates')
    expect(wrapper.text()).not.toContain('No deployment templates yet')
    wrapper.unmount()
  })

  it('shows the explicit feature-disabled state without a fake empty catalog', async () => {
    list.mockRejectedValue(apiError(503, 'FEATURE_DISABLED', 'Deployment templates are disabled'))
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Template management is disabled')
    expect(wrapper.text()).not.toContain('No deployment templates yet')
    expect(wrapper.text()).not.toContain('Could not load deployment templates')
    wrapper.unmount()
  })

  it('renders a filled catalog normally', async () => {
    list.mockResolvedValue({ items: [sample], limit: 100, offset: 0, total: 1 })
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Lab template')
    expect(wrapper.text()).toContain('lab-template')
    expect(wrapper.text()).not.toContain('No deployment templates yet')
    wrapper.unmount()
  })

  it('does not call the catalog for a non-admin and keeps the access notice', async () => {
    roles.value = ['user']
    const wrapper = await mountPage()

    expect(list).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Administrator access required')
    wrapper.unmount()
  })

  it('keeps catalog order inside the shared admin card grid', async () => {
    const second: DeploymentTemplate = {
      ...sample,
      uuid: '22222222-2222-4222-8222-222222222222',
      slug: 'second-template',
      name: 'Second template',
    }
    list.mockResolvedValue({ items: [sample, second], limit: 100, offset: 0, total: 2 })
    const wrapper = await mountPage()

    const scope = wrapper.get('[data-slot="admin-cards"]')
    const text = scope.text()

    expect(scope.findAll('a')).toHaveLength(2)
    expect(text.indexOf(sample.name)).toBeLessThan(text.indexOf(second.name))
    wrapper.unmount()
  })
})
