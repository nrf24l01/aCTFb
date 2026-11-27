import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TaskProgress from '@/features/boards/components/TaskProgress.vue'

function mountProgress(solvedTasks: number, totalTasks: number, compact = false) {
  return mount(TaskProgress, {
    props: { solvedTasks, totalTasks, label: 'Task progress', compact },
  })
}

describe('TaskProgress', () => {
  it('renders an empty 0/0 track without claiming completion', () => {
    const wrapper = mountProgress(0, 0)
    const bar = wrapper.get('[role="progressbar"]')

    expect(bar.attributes('aria-label')).toBe('Task progress')
    expect(bar.attributes('aria-valuemin')).toBe('0')
    expect(bar.attributes('aria-valuemax')).toBe('1')
    expect(bar.attributes('aria-valuenow')).toBe('0')
    expect(bar.attributes('aria-valuetext')).toBe('0 of 0 tasks solved')
    expect(wrapper.text()).toContain('0/0 solved')
    expect(wrapper.text()).not.toContain('Complete')
    expect(wrapper.get('[data-slot="progress-indicator"]').attributes('style')).toContain(
      'translateX(-100%)',
    )
    wrapper.unmount()
  })

  it('exposes the real solved fraction as numeric progress values', () => {
    const wrapper = mountProgress(3, 8)
    const bar = wrapper.get('[role="progressbar"]')

    expect(bar.attributes('aria-valuemax')).toBe('8')
    expect(bar.attributes('aria-valuenow')).toBe('3')
    expect(bar.attributes('aria-valuetext')).toBe('3 of 8 tasks solved')
    expect(wrapper.text()).toContain('3/8 solved')
    expect(wrapper.text()).not.toContain('Complete')
    wrapper.unmount()
  })

  it('shows one short completion label only when a nonempty module is fully solved', () => {
    const wrapper = mountProgress(8, 8)

    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('8')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuemax')).toBe('8')
    expect(wrapper.text()).toContain('8/8 solved')
    expect(wrapper.text()).toContain('Complete')
    const icons = wrapper.findAll('svg')
    expect(icons).toHaveLength(1)
    expect(icons[0].attributes('aria-hidden')).toBe('true')
    wrapper.unmount()
  })

  it('keeps the visible fraction out of the accessible name/value pair', () => {
    const wrapper = mountProgress(4, 6)

    const fraction = wrapper.findAll('span').find((span) => span.text() === '4/6 solved')
    expect(fraction?.attributes('aria-hidden')).toBe('true')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-label')).toBe('Task progress')
    wrapper.unmount()
  })
})
