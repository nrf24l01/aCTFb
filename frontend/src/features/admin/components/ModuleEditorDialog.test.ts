import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, vi } from 'vitest'
import { DESCRIPTION_MAX_CHARACTERS } from '@/shared/lib/text'

const updateModule = vi.fn().mockResolvedValue({})

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    updateModule: (...args: unknown[]) => updateModule(...args),
    createModule: vi.fn().mockResolvedValue({}),
  },
}))

import ModuleEditorDialog from './ModuleEditorDialog.vue'

beforeEach(() => {
  updateModule.mockClear()
})

describe('ModuleEditorDialog', () => {
  it('offers one format dropdown and no allowed-task-types checklist', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: { open: true, boardUuid: '11111111-1111-4111-8111-111111111111', audiences: [] },
      attachTo: document.body,
    })
    await Promise.resolve()

    const text = document.body.textContent ?? ''
    expect(text).toContain('Format')
    expect(text).toContain('Flag challenges')
    expect(text).not.toContain('Allowed task types')

    const format = document.getElementById('module-format') as HTMLSelectElement | null
    expect(format).not.toBeNull()
    expect(Array.from(format!.options).map((option) => option.textContent)).toEqual([
      'Flag challenges',
      'Manual review',
      'Instance tasks',
      'Pentest',
      'Attack & Defense',
    ])
    wrapper.unmount()
  })

  it('keeps the format immutable when editing and shows a legacy notice for mixed modules', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid: '11111111-1111-4111-8111-111111111111',
        audiences: [],
        module: {
          uuid: '22222222-2222-4222-8222-222222222222',
          boardUuid: '11111111-1111-4111-8111-111111111111',
          name: 'Legacy',
          description: '',
          type: 'TASKS',
          allowedTaskTypes: ['FLAG', 'MANUAL'],
          status: 'ACTIVE',
          audienceUuids: [],
          order: 0,
          enabled: true,
          legacyMixed: true,
        },
      },
      attachTo: document.body,
    })
    await Promise.resolve()

    const format = document.getElementById('module-format') as HTMLSelectElement | null
    expect(format?.disabled).toBe(true)
    expect(document.body.textContent).toContain('mixes')
    wrapper.unmount()
  })
})

describe('ModuleEditorDialog EXTENSION and legacy formats', () => {
  const boardUuid = '11111111-1111-4111-8111-111111111111'

  it('shows an installed plugin module read-only and preserves its task types', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid,
        audiences: [],
        module: {
          uuid: '44444444-4444-4444-8444-444444444444',
          boardUuid,
          name: 'Plugin module',
          description: '',
          type: 'EXTENSION',
          allowedTaskTypes: ['EXTENSION'],
          status: 'ACTIVE',
          audienceUuids: [],
          order: 0,
          enabled: true,
          extensionKey: 'example.hello',
        },
      },
      attachTo: document.body,
    })
    await Promise.resolve()

    const text = document.body.textContent ?? ''
    // It must never be presented as a native flag module.
    expect(text).not.toContain('Flag challenges')
    expect(text).toContain('Extension: example.hello (unavailable)')
    // No native format select is offered for an installed plugin module.
    expect(document.getElementById('module-format')?.tagName).toBe('P')

    // Saving must preserve the extension identity and task types. Submit the
    // teleported form directly and inspect the captured update payload.
    const form = document.querySelector('form')
    expect(form).not.toBeNull()
    form!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()
    expect(updateModule).toHaveBeenCalledTimes(1)
    const [calledBoard, calledModule, payload] = updateModule.mock.calls[0]
    expect(calledBoard).toBe(boardUuid)
    expect(calledModule).toBe('44444444-4444-4444-8444-444444444444')
    expect(payload.allowedTaskTypes).toEqual(['EXTENSION'])
    expect(payload).not.toHaveProperty('type')
    expect(payload).not.toHaveProperty('extensionKey')
    wrapper.unmount()
  })

  it('opens a legacy mixed module on "Keep current mixed types"', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid,
        audiences: [],
        module: {
          uuid: '55555555-5555-4555-8555-555555555555',
          boardUuid,
          name: 'Legacy mixed',
          description: '',
          type: 'TASKS',
          allowedTaskTypes: ['FLAG', 'MANUAL'],
          status: 'ACTIVE',
          audienceUuids: [],
          order: 0,
          enabled: true,
          legacyMixed: true,
        },
      },
      attachTo: document.body,
    })
    await Promise.resolve()

    const select = document.getElementById('module-legacy-type') as HTMLSelectElement | null
    expect(select).not.toBeNull()
    // The empty "keep" option must be the selected default, never the first type.
    expect(select!.value).toBe('')
    wrapper.unmount()
  })
})

describe('ModuleEditorDialog instance settings', () => {
  it('reloads the persisted instance policy and sends it back', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid: '11111111-1111-4111-8111-111111111111',
        audiences: [],
        module: {
          uuid: '33333333-3333-4333-8333-333333333333',
          boardUuid: '11111111-1111-4111-8111-111111111111',
          name: 'Lab',
          description: '',
          type: 'TASKS',
          allowedTaskTypes: ['INSTANCE'],
          status: 'ACTIVE',
          audienceUuids: [],
          order: 0,
          enabled: true,
          taskInstanceSettings: {
            maxConcurrentInstancesPerSubject: 2,
            defaultTtlSeconds: 1800,
            maxTtlSeconds: 7200,
            resetEnabled: false,
            extension: { enabled: true, seconds: 900, maxCount: 6 },
          },
        },
      },
      attachTo: document.body,
    })
    await flushPromises()

    const value = (id: string) => (document.getElementById(id) as HTMLInputElement | null)?.value
    // 1800 s -> 0.5 h, 7200 s -> 2 h, 900 s -> 15 min, count 6.
    expect(value('module-instance-default-ttl')).toBe('0.5')
    expect(value('module-instance-max-ttl')).toBe('2')
    expect(value('module-instance-extension-minutes')).toBe('15')
    expect(value('module-instance-extension-count')).toBe('6')

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateModule).toHaveBeenCalledTimes(1)
    const body = updateModule.mock.calls[0][2] as {
      taskInstanceSettings: {
        defaultTtlSeconds: number
        maxTtlSeconds: number
        resetEnabled: boolean
        extension: { enabled: boolean; seconds: number; maxCount: number }
      }
    }
    expect(body.taskInstanceSettings.defaultTtlSeconds).toBe(1800)
    expect(body.taskInstanceSettings.maxTtlSeconds).toBe(7200)
    expect(body.taskInstanceSettings.resetEnabled).toBe(false)
    expect(body.taskInstanceSettings.extension).toEqual({ enabled: true, seconds: 900, maxCount: 6 })
    wrapper.unmount()
  })
})

describe('ModuleEditorDialog instance limit validation', () => {
  it('blocks an out-of-range max concurrency instead of sending it', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid: '11111111-1111-4111-8111-111111111111',
        audiences: [],
        module: {
          uuid: '33333333-3333-4333-8333-333333333333',
          boardUuid: '11111111-1111-4111-8111-111111111111',
          name: 'Lab',
          description: '',
          type: 'TASKS',
          allowedTaskTypes: ['INSTANCE'],
          status: 'ACTIVE',
          audienceUuids: [],
          order: 0,
          enabled: true,
          taskInstanceSettings: {
            maxConcurrentInstancesPerSubject: 2,
            defaultTtlSeconds: 3600,
            maxTtlSeconds: 7200,
            resetEnabled: true,
            extension: { enabled: true, seconds: 900, maxCount: 3 },
          },
        },
      },
      attachTo: document.body,
    })
    await flushPromises()

    const input = document.getElementById('module-instance-concurrent') as HTMLInputElement
    input.value = '0'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateModule).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Max concurrent per participant must be a whole number between 1 and 100')
    wrapper.unmount()
  })
})

describe('ModuleEditorDialog Markdown authoring', () => {
  const boardUuid = '11111111-1111-4111-8111-111111111111'

  const moduleWithDescription = (description: string) => ({
    uuid: '22222222-2222-4222-8222-222222222222',
    boardUuid,
    name: 'Rich module',
    description,
    type: 'TASKS' as const,
    allowedTaskTypes: ['FLAG' as const],
    status: 'ACTIVE' as const,
    audienceUuids: [],
    order: 0,
    enabled: true,
  })

  async function waitForEditor() {
    await vi.waitFor(() => {
      expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
    }, { timeout: 10_000, interval: 20 })
  }

  it('edits the description with the rich editor and keeps Markdown on save', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: { open: true, boardUuid, audiences: [], module: moduleWithDescription('**bold**') },
      attachTo: document.body,
    })
    await waitForEditor()

    expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
    expect(document.querySelector('#module-description')).not.toBeNull()

    await (document.querySelector('[aria-label="Source mode"]') as HTMLButtonElement).click()
    await flushPromises()
    expect((document.querySelector('textarea') as HTMLTextAreaElement).value).toBe('**bold**')

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateModule).toHaveBeenCalledTimes(1)
    const [, , body] = updateModule.mock.calls[0] as [string, string, { description: string }]
    expect(body.description).toBe('**bold**')
    wrapper.unmount()
  })

  it('blocks submit when the description exceeds the server limit', async () => {
    const wrapper = mount(ModuleEditorDialog, {
      props: {
        open: true,
        boardUuid,
        audiences: [],
        module: moduleWithDescription('x'.repeat(DESCRIPTION_MAX_CHARACTERS + 1)),
      },
      attachTo: document.body,
    })
    await waitForEditor()

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateModule).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('at most 50,000 characters')

    // Exactly one message owns the error, and it stays visible on Preview.
    const alerts = Array.from(document.querySelectorAll('[role="alert"]'))
      .filter((element) => element.textContent?.includes('at most 50,000 characters'))
    expect(alerts).toHaveLength(1)
    const field = document.getElementById('module-description')!
    expect(field.getAttribute('aria-invalid')).toBe('true')
    const describedBy = field.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy!)?.textContent).toContain('at most 50,000 characters')

    const previewTab = Array.from(document.querySelectorAll('[role="tab"]'))
      .find((element) => element.textContent?.trim() === 'Preview') as HTMLElement
    previewTab.click()
    await flushPromises()
    const previewAlerts = Array.from(document.querySelectorAll('[role="alert"]'))
      .filter((element) => element.textContent?.includes('at most 50,000 characters'))
    expect(previewAlerts).toHaveLength(1)
    wrapper.unmount()
  })
})
