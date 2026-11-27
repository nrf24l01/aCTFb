import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ModuleCard from '@/features/boards/components/ModuleCard.vue'
import type { ModuleListItem } from '@/shared/types/api'

const baseModule: ModuleListItem = {
  uuid: 'module-1',
  boardUuid: 'board-1',
  name: 'Flag module',
  type: 'TASKS',
  allowedTaskTypes: ['FLAG'],
  status: 'ACTIVE',
  audienceUuids: [],
  order: 1,
  canParticipate: true,
}

function mountCard(module: ModuleListItem) {
  return mount(ModuleCard, {
    props: { module },
    global: {
      stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } },
    },
  })
}

describe('ModuleCard task progress', () => {
  it('omits progress when the server does not publish an aggregate', () => {
    const wrapper = mountCard(baseModule)

    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('solved')
    wrapper.unmount()
  })

  it('renders the unfiltered server fraction inside the existing card link', () => {
    const wrapper = mountCard({ ...baseModule, taskProgress: { solvedTasks: 3, totalTasks: 8 } })

    expect(wrapper.findAll('a')).toHaveLength(1)
    expect(wrapper.text()).toContain('3/8 solved')
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('3')
    expect(bar.attributes('aria-valuemax')).toBe('8')
    expect(bar.attributes('aria-valuetext')).toBe('3 of 8 tasks solved')
    expect(bar.attributes('aria-label')).toBe('Task progress for Flag module')
    wrapper.unmount()
  })

  it('never shows own progress in a staff management preview', () => {
    const wrapper = mountCard({
      ...baseModule,
      preview: true,
      canParticipate: false,
      taskProgress: { solvedTasks: 3, totalTasks: 8 },
    })

    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('adds the check-circle label only for a fully solved nonempty module', () => {
    const wrapper = mountCard({ ...baseModule, taskProgress: { solvedTasks: 5, totalTasks: 5 } })

    expect(wrapper.text()).toContain('5/5 solved')
    // The Complete text itself is the assertion: an unrelated StatusBadge icon
    // must not be able to satisfy it.
    const complete = wrapper.findAll('span').find((span) => span.text() === 'Complete')
    expect(complete).toBeDefined()
    expect(complete!.find('svg').exists()).toBe(true)
    wrapper.unmount()
  })

  it('never labels a partially solved or empty module Complete', () => {
    const partial = mountCard({ ...baseModule, taskProgress: { solvedTasks: 3, totalTasks: 8 } })
    expect(partial.text()).toContain('3/8 solved')
    expect(partial.text()).not.toContain('Complete')
    partial.unmount()

    const empty = mountCard({ ...baseModule, taskProgress: { solvedTasks: 0, totalTasks: 0 } })
    expect(empty.text()).toContain('0/0 solved')
    expect(empty.text()).not.toContain('Complete')
    empty.unmount()
  })
})
