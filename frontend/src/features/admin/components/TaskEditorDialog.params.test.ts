import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const createTask = vi.fn().mockResolvedValue({})
const listApprovedTemplates = vi.fn()

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    createTask: (...args: unknown[]) => createTask(...args),
    updateTask: vi.fn().mockResolvedValue({}),
  },
}))

vi.mock('@/features/instances/api', () => ({
  instancesApi: {
    listApprovedTemplates: (...args: unknown[]) => listApprovedTemplates(...args),
  },
}))

import TaskEditorDialog from './TaskEditorDialog.vue'

describe('TaskEditorDialog typed instance payload', () => {
  beforeEach(() => {
    createTask.mockClear()
    listApprovedTemplates.mockResolvedValue({
      items: [
        {
          templateUuid: '11111111-1111-4111-8111-111111111111',
          version: 1,
          slug: 'typed',
          name: 'Typed',
          parametersPolicy: {},
          parameters: [
            { key: 'marker', label: 'Marker', type: 'string', required: true, secret: false, default: 'default-marker' },
            { key: 'replicas', label: 'Replicas', type: 'int', required: true, secret: false, default: '1' },
            { key: 'tls', label: 'TLS', type: 'bool', required: false, secret: false, default: 'false' },
            { key: 'size', label: 'Size', type: 'choice', options: ['small', 'large'], required: true, secret: false },
            { key: 'flag', label: 'Flag', type: 'string', required: true, secret: true },
          ],
        },
      ],
    })
  })

  it('sends every parameter value as a string', async () => {
    const wrapper = mount(TaskEditorDialog, {
      props: {
        open: true,
        boardUuid: '22222222-2222-4222-8222-222222222222',
        moduleUuid: '33333333-3333-4333-8333-333333333333',
        allowedTaskTypes: ['INSTANCE'],
      },
      attachTo: document.body,
    })
    await flushPromises()

    // The dialog is teleported, so query the document.
    const field = (selector: string) => {
      const element = document.querySelector(selector) as HTMLInputElement | HTMLTextAreaElement | null
      expect(element, `missing ${selector}`).not.toBeNull()
      return element!
    }
    const setValue = async (selector: string, value: string) => {
      const element = field(selector)
      element.value = value
      element.dispatchEvent(new Event('input', { bubbles: true }))
      // A <select> v-model listens to change, an <input> to input.
      element.dispatchEvent(new Event('change', { bubbles: true }))
      await flushPromises()
    }

    // Fill the basics and choose the approved template.
    await setValue('#task-title', 'Typed task')
    await setValue('#task-category', 'web')
    const approved = field('#task-instance-approved') as unknown as HTMLSelectElement
    approved.value = '11111111-1111-4111-8111-111111111111:1'
    approved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    // A number input is auto-cast by Vue, so the payload must be normalised.
    await setValue('#param-replicas', '2')
    await setValue('#param-marker', 'marker-value')
    await setValue('#param-size', 'large')
    await setValue('#secret-flag', 'actfb{typed-secret}')

    const form = document.querySelector('form')!
    form.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(createTask).toHaveBeenCalledTimes(1)
    const body = createTask.mock.calls[0][2] as {
      instance: { parameters?: Record<string, unknown>; secretParameters?: Record<string, unknown> }
    }
    for (const [key, value] of Object.entries(body.instance.parameters ?? {})) {
      expect(typeof value, `parameter ${key} must be a string`).toBe('string')
    }
    expect(body.instance.parameters).toMatchObject({ marker: 'marker-value', replicas: '2', size: 'large' })
    expect(typeof body.instance.parameters?.replicas).toBe('string')
    expect(body.instance.secretParameters?.flag).toBe('actfb{typed-secret}')
    expect(typeof body.instance.secretParameters?.flag).toBe('string')
    wrapper.unmount()
  })
})
