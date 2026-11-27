import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SubmissionHistory from '@/features/boards/components/SubmissionHistory.vue'
import type { Submission } from '@/shared/types/api'

const submission: Submission = {
  uuid: 'submission-1',
  status: 'ACCEPTED',
  text: null,
  files: [],
  scoreAwarded: 100,
  createdAt: '2026-01-01T00:00:00Z',
}

describe('SubmissionHistory', () => {
  it('renders one accessible caption and no repeated visible heading', () => {
    const wrapper = mount(SubmissionHistory, { props: { submissions: [submission] } })

    expect(wrapper.find('h2').exists()).toBe(false)
    expect(wrapper.get('table caption').text()).toBe('Your submissions')
    expect(wrapper.text()).not.toContain('Your submission history')
  })
})
