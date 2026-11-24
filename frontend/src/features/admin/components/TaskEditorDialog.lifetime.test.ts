import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const createTask = vi.fn().mockResolvedValue({})
const updateTask = vi.fn().mockResolvedValue({})
const listApprovedTemplates = vi.fn()

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    createTask: (...args: unknown[]) => createTask(...args),
    updateTask: (...args: unknown[]) => updateTask(...args),
  },
}))

vi.mock('@/features/instances/api', () => ({
  instancesApi: {
    listApprovedTemplates: (...args: unknown[]) => listApprovedTemplates(...args),
  },
}))

import TaskEditorDialog from './TaskEditorDialog.vue'

const moduleSettings = {
  maxConcurrentInstancesPerSubject: 2,
  defaultTtlSeconds: 3600,
  maxTtlSeconds: 14400,
  resetEnabled: true,
  extension: { enabled: true, seconds: 1800, maxCount: 3 },
}

function field(selector: string): HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement {
  const element = document.querySelector(selector) as HTMLInputElement | null
  expect(element, `missing ${selector}`).not.toBeNull()
  return element!
}

async function setValue(selector: string, value: string) {
  const element = field(selector)
  element.value = value
  element.dispatchEvent(new Event('input', { bubbles: true }))
  element.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}

async function submit() {
  document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
  await flushPromises()
}

function mountDialog(extra: Record<string, unknown> = {}) {
  return mount(TaskEditorDialog, {
    props: {
      open: true,
      boardUuid: '22222222-2222-4222-8222-222222222222',
      moduleUuid: '33333333-3333-4333-8333-333333333333',
      allowedTaskTypes: ['INSTANCE'],
      instanceSettings: moduleSettings,
      ...extra,
    },
    attachTo: document.body,
  })
}

describe('TaskEditorDialog instance lifetime', () => {
  beforeEach(() => {
    createTask.mockClear()
    updateTask.mockClear()
    listApprovedTemplates.mockResolvedValue({
      items: [
        {
          templateUuid: '11111111-1111-4111-8111-111111111111',
          version: 1,
          slug: 'lab',
          name: 'Lab',
          parametersPolicy: {},
          parameters: [],
        },
      ],
    })
  })

  it('inherits the module policy by sending explicit nulls', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await setValue('#task-title', 'Inherit task')
    await setValue('#task-category', 'web')
    const approved = field('#task-instance-approved')
    approved.value = '11111111-1111-4111-8111-111111111111:1'
    approved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await submit()

    expect(createTask).toHaveBeenCalledTimes(1)
    const body = createTask.mock.calls[0][2] as { instance: Record<string, unknown> }
    expect(body.instance.ttlSeconds).toBeNull()
    expect(body.instance.maxTtlSeconds).toBeNull()
    expect(body.instance.extension).toBeNull()
    wrapper.unmount()
  })

  it('sends the custom policy in seconds', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await setValue('#task-title', 'Custom task')
    await setValue('#task-category', 'web')
    const approved = field('#task-instance-approved')
    approved.value = '11111111-1111-4111-8111-111111111111:1'
    approved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    const customButton = Array.from(document.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Custom'),
    )!
    customButton.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()

    await setValue('#task-lifetime-initial', '30')
    await setValue('#task-lifetime-max', '120')
    await setValue('#task-extension-step', '15')
    await setValue('#task-extension-count', '3')
    await submit()

    expect(createTask).toHaveBeenCalledTimes(1)
    const body = createTask.mock.calls[0][2] as {
      instance: {
        ttlSeconds: number
        maxTtlSeconds: number
        extension: { enabled: boolean; seconds: number; maxCount: number }
      }
    }
    expect(body.instance.ttlSeconds).toBe(1800)
    expect(body.instance.maxTtlSeconds).toBe(7200)
    expect(body.instance.extension).toEqual({ enabled: true, seconds: 900, maxCount: 3 })
    wrapper.unmount()
  })

  it('blocks an extension count above the module limit', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await setValue('#task-title', 'Too many')
    await setValue('#task-category', 'web')
    const approved = field('#task-instance-approved')
    approved.value = '11111111-1111-4111-8111-111111111111:1'
    approved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    const customButton = Array.from(document.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Custom'),
    )!
    customButton.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()
    await setValue('#task-extension-count', '6')
    await submit()

    expect(createTask).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('must not exceed the module maximum of 3')
    wrapper.unmount()
  })

  it('edits only the lifetime policy and never resends the binding', async () => {
    const wrapper = mountDialog({
      task: {
        uuid: '44444444-4444-4444-8444-444444444444',
        moduleUuid: '33333333-3333-4333-8333-333333333333',
        type: 'INSTANCE',
        title: 'Existing',
        category: 'web',
        description: '',
        points: 10,
        status: 'AVAILABLE',
        solved: false,
        visible: true,
        attachments: [],
        submission: { mode: 'TEXT', checker: 'MANUAL' },
        instance: { required: true, status: 'NOT_CREATED', canStart: true },
        instanceConfig: {
          enabled: true,
          ttlSeconds: 1800,
          maxTtlSeconds: 7200,
          extension: { enabled: true, seconds: 900, maxCount: 2 },
        },
      },
    })
    await flushPromises()
    await submit()

    expect(updateTask).toHaveBeenCalledTimes(1)
    const body = updateTask.mock.calls[0][3] as { instance: Record<string, unknown> }
    expect(body.instance.deploymentTemplateUuid).toBeUndefined()
    expect(body.instance.templateVersion).toBeUndefined()
    expect(body.instance.parameters).toBeUndefined()
    expect(body.instance.ttlSeconds).toBe(1800)
    expect(body.instance.maxTtlSeconds).toBe(7200)
    expect(body.instance.extension).toEqual({ enabled: true, seconds: 900, maxCount: 2 })
    wrapper.unmount()
  })
})
