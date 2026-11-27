import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TaskCard from '@/features/boards/components/TaskCard.vue'
import type { TaskListItem } from '@/shared/types/api'

const baseTask: TaskListItem = {
  uuid: 'task-1',
  moduleUuid: 'module-1',
  type: 'FLAG',
  title: 'Warmup flag',
  category: 'web',
  points: 100,
  status: 'AVAILABLE',
  solved: false,
  instanceRequired: false,
}

function mountCard(task: TaskListItem) {
  return mount(TaskCard, {
    props: { task, boardUuid: 'board-1', moduleUuid: 'module-1' },
    global: {
      stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } },
    },
  })
}

describe('TaskCard solved presentation', () => {
  it('tints only solved cards with the existing success ink and keeps one Solved badge', () => {
    const wrapper = mountCard({ ...baseTask, status: 'SOLVED', solved: true })
    const link = wrapper.get('a')

    expect(link.classes()).toContain('task-card--solved')
    expect(link.classes()).not.toContain('bg-black')
    expect(wrapper.findAll('svg')).toHaveLength(1)
    const solvedBadges = wrapper
      .findAll('[data-slot="badge"]')
      .filter((badge) => badge.text() === 'Solved')
    expect(solvedBadges).toHaveLength(1)
    wrapper.unmount()
  })

  it('leaves available and locked cards on the existing neutral surface', () => {
    for (const status of ['AVAILABLE', 'LOCKED'] as const) {
      const wrapper = mountCard({ ...baseTask, status })
      expect(wrapper.get('a').classes()).toContain('bg-black')
      expect(wrapper.get('a').classes()).not.toContain('task-card--solved')
      expect(wrapper.text()).not.toContain('Solved')
      wrapper.unmount()
    }
  })

  it('defines the approved 8% default and 12% hover success tint in its scoped CSS', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/features/boards/components/TaskCard.vue'), 'utf8')

    expect(source).toContain('color-mix(in srgb, var(--status-success-ink) 8%, black)')
    expect(source).toContain('color-mix(in srgb, var(--status-success-ink) 12%, black)')
    expect(source).toContain(':hover')
  })
})
