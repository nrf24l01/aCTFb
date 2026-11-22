import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, vi } from 'vitest'

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
