import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import TaskEditorDialog from '@/features/admin/components/TaskEditorDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import type { AdminTask } from '@/shared/types/api'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    createTask: vi.fn(),
    updateTask: vi.fn(),
  },
}))

const createTask = vi.mocked(boardsApi.createTask)
const updateTask = vi.mocked(boardsApi.updateTask)

const existingTask: AdminTask = {
  uuid: '33333333-3333-4333-8333-333333333333',
  moduleUuid: '22222222-2222-4222-8222-222222222222',
  type: 'FLAG',
  title: 'Existing flag',
  category: 'web',
  points: 100,
  status: 'AVAILABLE',
  solved: false,
  instanceRequired: false,
  description: 'Existing description',
  attachments: [],
  submission: {
    mode: 'TEXT',
    checker: 'AUTOMATIC',
    text: { required: true, minLength: 1, maxLength: 1_000_000 },
    files: null,
  },
  instance: { required: false, status: 'NOT_CREATED', canStart: false },
  visible: true,
  answerConfiguration: { configured: true, acceptedAnswersCount: 1 },
  instanceConfig: { enabled: false },
}

async function mountDialog(task?: AdminTask) {
  const wrapper = mount(TaskEditorDialog, {
    props: {
      open: true,
      boardUuid: '11111111-1111-4111-8111-111111111111',
      moduleUuid: '22222222-2222-4222-8222-222222222222',
      ...(task ? { task } : {}),
    },
    attachTo: document.body,
  })
  await nextTick()
  return wrapper
}

async function setField(selector: string, value: string) {
  const element = document.body.querySelector(selector)
  if (!(element instanceof HTMLInputElement) && !(element instanceof HTMLTextAreaElement)) {
    throw new Error(`Field not found: ${selector}`)
  }
  element.value = value
  element.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

async function submitForm() {
  const form = document.body.querySelector('form')
  if (!form) throw new Error('Form not found')
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await flushPromises()
  await nextTick()
}

beforeEach(() => {
  createTask.mockReset()
  updateTask.mockReset()
  createTask.mockResolvedValue(existingTask)
  updateTask.mockResolvedValue(existingTask)
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('TaskEditorDialog FLAG payload', () => {
  it('creates with the backend default TEXT/AUTOMATIC policy only, without text bounds', async () => {
    const wrapper = await mountDialog()

    await setField('#task-title', 'New flag')
    await setField('#task-category', 'misc')
    await setField('#task-answers', 'flag{one}\nflag{two}')
    await submitForm()

    expect(createTask).toHaveBeenCalledTimes(1)
    const [, , body] = createTask.mock.calls[0]
    expect(body.submission).toEqual({ mode: 'TEXT', checker: 'AUTOMATIC' })
    expect(body.submission).not.toHaveProperty('text')
    expect(body.submission).not.toHaveProperty('files')
    expect(JSON.stringify(body)).not.toContain('1024')
    expect(body.instance).toEqual({ enabled: false })
    expect(body.acceptedAnswers).toEqual(['flag{one}', 'flag{two}'])

    wrapper.unmount()
  })

  it('edits with the same policy only and keeps accepted answers out when unchanged', async () => {
    const wrapper = await mountDialog(existingTask)

    await setField('#task-title', 'Existing flag renamed')
    await submitForm()

    expect(updateTask).toHaveBeenCalledTimes(1)
    const [, , , body] = updateTask.mock.calls[0]
    expect(body.submission).toEqual({ mode: 'TEXT', checker: 'AUTOMATIC' })
    expect(body.submission).not.toHaveProperty('text')
    expect(body.submission).not.toHaveProperty('files')
    expect(JSON.stringify(body)).not.toContain('1024')
    expect(body).not.toHaveProperty('acceptedAnswers')
    expect(body.instance).toEqual({ enabled: false })

    wrapper.unmount()
  })
})