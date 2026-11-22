import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ModuleEditorDialog from './ModuleEditorDialog.vue'

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
