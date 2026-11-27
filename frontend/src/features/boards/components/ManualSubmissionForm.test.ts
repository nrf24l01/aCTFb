import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ManualSubmissionForm from '@/features/boards/components/ManualSubmissionForm.vue'
import { NetworkError } from '@/shared/api/errors'
import type { Submission, Task } from '@/shared/types/api'

const submitTaskBody = vi.fn()

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    submitTaskBody: (...args: unknown[]) => submitTaskBody(...args),
  },
}))

const task: Task = {
  uuid: 'task-manual',
  moduleUuid: 'module-1',
  type: 'MANUAL',
  title: 'Report',
  category: 'forensics',
  points: 50,
  status: 'AVAILABLE',
  solved: false,
  solvesCount: 0,
  instanceRequired: false,
  description: 'Write a report.',
  attachments: [],
  submission: {
    mode: 'TEXT',
    checker: 'MANUAL',
    text: { required: true, minLength: 120, maxLength: 4000 },
    files: null,
  },
  instance: { required: false, status: 'NOT_CREATED', canStart: false },
}

function pendingReview(): Submission {
  return {
    uuid: 'submission-manual',
    status: 'PENDING_REVIEW',
    text: null,
    files: [],
    scoreAwarded: null,
    createdAt: '2026-01-01T00:00:00Z',
  }
}

function mountForm() {
  return mount(ManualSubmissionForm, {
    props: { boardUuid: 'board-1', moduleUuid: 'module-1', task },
  })
}

const draft = 'x'.repeat(120)

beforeEach(() => {
  submitTaskBody.mockReset()
})

describe('ManualSubmissionForm', () => {
  it('shows Awaiting review instead of the raw enum or the routine points paragraph', async () => {
    submitTaskBody.mockResolvedValueOnce(pendingReview())
    const wrapper = mountForm()

    await wrapper.get('#manual-submission-text').setValue(draft)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('Awaiting review')
    expect(wrapper.text()).not.toContain('PENDING_REVIEW')
    expect(wrapper.text()).not.toContain('Points are awarded only when a reviewer accepts the submission.')
  })

  it('keeps the submitted text and replays the same body and key after a network error', async () => {
    submitTaskBody.mockRejectedValueOnce(new NetworkError())
    const wrapper = mountForm()

    await wrapper.get('#manual-submission-text').setValue(draft)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(submitTaskBody).toHaveBeenCalledTimes(1)
    const firstKey = submitTaskBody.mock.calls[0][3]
    const firstBody = submitTaskBody.mock.calls[0][4]
    expect((wrapper.get('#manual-submission-text').element as HTMLTextAreaElement).value).toBe(draft)
    expect(wrapper.text()).toContain('Network error')

    const retry = wrapper.findAll('button').find((item) => item.text().includes('Retry same submission'))
    expect(retry, 'missing Retry same submission').toBeTruthy()
    await retry!.trigger('click')
    await flushPromises()

    expect(submitTaskBody).toHaveBeenCalledTimes(2)
    expect(submitTaskBody.mock.calls[1][3]).toBe(firstKey)
    expect(submitTaskBody.mock.calls[1][4]).toEqual(firstBody)
  })

  it('clears the text only after the submission succeeds', async () => {
    submitTaskBody.mockResolvedValueOnce(pendingReview())
    const wrapper = mountForm()

    await wrapper.get('#manual-submission-text').setValue(draft)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect((wrapper.get('#manual-submission-text').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.text()).toContain('Awaiting review')
  })

  it('keeps the real submission constraints visible', () => {
    const wrapper = mountForm()

    expect(wrapper.text()).toContain('120–4000 characters. Required.')
    expect(wrapper.get('#manual-submission-text').attributes('maxlength')).toBe('4000')
  })

  it('renders an accepted review outcome with the semantic success ink, not the brand pine', async () => {
    submitTaskBody.mockResolvedValueOnce({
      ...pendingReview(),
      uuid: 'submission-accepted',
      status: 'ACCEPTED',
      scoreAwarded: 50,
    })
    const wrapper = mountForm()

    await wrapper.get('#manual-submission-text').setValue(draft)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const line = wrapper
      .findAll('[data-slot="alert"]')
      .find((alert) => alert.text().includes('Accepted · +50 points'))
    expect(line, 'missing accepted outcome alert').toBeTruthy()
    expect(line!.classes()).toContain('text-status-success-ink')
    expect(line!.classes()).not.toContain('text-pine')
    expect(wrapper.html()).not.toContain('text-pine')
  })
})
