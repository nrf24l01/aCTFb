import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import FlagSubmitForm from '@/features/boards/components/FlagSubmitForm.vue'
import { ApiError, NetworkError } from '@/shared/api/errors'
import type { Submission, Task } from '@/shared/types/api'

const submitTask = vi.fn()

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    submitTask: (...args: unknown[]) => submitTask(...args),
  },
}))

const task: Task = {
  uuid: 'task-1',
  moduleUuid: 'module-1',
  type: 'FLAG',
  title: 'Warm up',
  category: 'web',
  points: 100,
  status: 'AVAILABLE',
  solved: false,
  solvesCount: 0,
  instanceRequired: false,
  description: 'Find the flag.',
  attachments: [],
  submission: {
    mode: 'TEXT',
    checker: 'AUTOMATIC',
    text: { required: true, minLength: 1, maxLength: 1_000_000 },
    files: null,
  },
  instance: { required: false, status: 'NOT_CREATED', canStart: false },
}

function accepted(): Submission {
  return {
    uuid: 'submission-1',
    status: 'ACCEPTED',
    text: null,
    files: [],
    scoreAwarded: 100,
    createdAt: '2026-01-01T00:00:00Z',
  }
}

function mountForm() {
  return mount(FlagSubmitForm, {
    props: { boardUuid: 'board-1', moduleUuid: 'module-1', task },
  })
}

function findButton(wrapper: ReturnType<typeof mountForm>, text: string) {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  if (!button) throw new Error(`Button not found: ${text}`)
  return button
}

beforeEach(() => {
  submitTask.mockReset()
})

describe('FlagSubmitForm', () => {
  it('clears the input immediately and never renders the submitted flag', async () => {
    submitTask.mockResolvedValueOnce(accepted())
    const wrapper = mountForm()

    await wrapper.get('#task-flag').setValue('flag{top-secret}')
    await wrapper.get('form').trigger('submit')

    expect((wrapper.get('#task-flag').element as HTMLInputElement).value).toBe('')
    await flushPromises()

    expect(wrapper.text()).toContain('Accepted')
    expect(wrapper.text()).not.toContain('flag{top-secret}')
    expect(submitTask).toHaveBeenCalledTimes(1)
  })

  it('reuses the same idempotency key and payload on a network retry', async () => {
    submitTask.mockRejectedValueOnce(new NetworkError())
    submitTask.mockResolvedValueOnce(accepted())
    const wrapper = mountForm()

    await wrapper.get('#task-flag').setValue('flag{retry-me}')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(submitTask).toHaveBeenCalledTimes(1)
    const firstKey = submitTask.mock.calls[0][3]
    const firstText = submitTask.mock.calls[0][4]
    expect(wrapper.text()).toContain('Network error')

    await findButton(wrapper, 'Retry same submission').trigger('click')
    await flushPromises()

    expect(submitTask).toHaveBeenCalledTimes(2)
    expect(submitTask.mock.calls[1][3]).toBe(firstKey)
    expect(submitTask.mock.calls[1][4]).toBe(firstText)
  })

  it('uses a new idempotency key for a new attempt after a server verdict', async () => {
    submitTask.mockRejectedValueOnce(
      new ApiError(200, {
        code: 'REJECTED',
        message: 'That flag is not correct',
        traceId: 'trace-1',
        timestamp: '2026-01-01T00:00:00Z',
        path: '/api/v2/boards/b/modules/m/tasks/t/submissions',
      }),
    )
    submitTask.mockResolvedValueOnce(accepted())
    const wrapper = mountForm()

    await wrapper.get('#task-flag').setValue('flag{wrong}')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    await wrapper.get('#task-flag').setValue('flag{right}')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(submitTask).toHaveBeenCalledTimes(2)
    expect(submitTask.mock.calls[0][3]).not.toBe(submitTask.mock.calls[1][3])
    expect((wrapper.get('#task-flag').element as HTMLInputElement).value).toBe('')
  })

  it('does not offer a manual accept control for FLAG tasks', () => {
    const wrapper = mountForm()
    const text = wrapper.text().toLowerCase()
    expect(text).not.toContain('accept submission')
    expect(text).not.toContain('manual review')
  })

  it('uses the server text policy and does not impose a 1024-character client cap', async () => {
    submitTask.mockResolvedValueOnce(accepted())
    const wrapper = mountForm()
    const input = wrapper.get('#task-flag')
    expect(input.attributes('maxlength')).toBe('1000000')

    const longFlag = 'a'.repeat(2048)
    await input.setValue(longFlag)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(submitTask).toHaveBeenCalledTimes(1)
    expect(submitTask.mock.calls[0][4]).toHaveLength(2048)
    expect((wrapper.get('#task-flag').element as HTMLInputElement).value).toBe('')
  })

  it('falls back to the backend default 1..1000000 bounds when the response omits text policy', () => {
    const wrapper = mount(FlagSubmitForm, {
      props: {
        boardUuid: 'board-1',
        moduleUuid: 'module-1',
        task: {
          ...task,
          submission: { mode: 'TEXT', checker: 'AUTOMATIC', text: null, files: null },
        },
      },
    })
    expect(wrapper.get('#task-flag').attributes('maxlength')).toBe('1000000')
  })

  it('shows server field errors for the text body and still clears the flag', async () => {
    submitTask.mockRejectedValueOnce(
      new ApiError(422, {
        code: 'VALIDATION_FAILED',
        message: 'Validation failed',
        traceId: 'trace-field',
        timestamp: '2026-01-01T00:00:00Z',
        path: '/api/v2/boards/b/modules/m/tasks/t/submissions',
        fieldErrors: [{ field: 'body.text', issue: 'Flag exceeds the server limit', code: 'OUT_OF_RANGE' }],
      }),
    )
    const wrapper = mountForm()

    await wrapper.get('#task-flag').setValue('flag{too-long}')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('Flag exceeds the server limit')
    expect(wrapper.text()).not.toContain('flag{too-long}')
    expect((wrapper.get('#task-flag').element as HTMLInputElement).value).toBe('')
  })

  it('does not render a flag form for an unsupported task type', () => {
    const wrapper = mount(FlagSubmitForm, {
      props: {
        boardUuid: 'board-1',
        moduleUuid: 'module-1',
        task: { ...task, type: 'MANUAL', submission: { mode: 'TEXT', checker: 'MANUAL', text: null, files: null } },
      },
    })
    expect(wrapper.find('#task-flag').exists()).toBe(false)
    expect(wrapper.text()).toContain('not supported in the first slice')
  })
})