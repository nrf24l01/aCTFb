import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getTaskInstance = vi.fn()
const taskAction = vi.fn()
const startTaskInstance = vi.fn()

vi.mock('@/features/instances/api', () => ({
  instancesApi: {
    getTaskInstance: (...args: unknown[]) => getTaskInstance(...args),
    taskAction: (...args: unknown[]) => taskAction(...args),
    startTaskInstance: (...args: unknown[]) => startTaskInstance(...args),
  },
}))

import InstancePanel from './InstancePanel.vue'

const runningInstance = {
  uuid: '55555555-5555-4555-8555-555555555555',
  boardUuid: '22222222-2222-4222-8222-222222222222',
  moduleUuid: '33333333-3333-4333-8333-333333333333',
  scope: 'TASK',
  owner: { type: 'USER', uuid: '44444444-4444-4444-8444-444444444444', name: 'student' },
  status: 'RUNNING',
  createdAt: new Date().toISOString(),
  expiresAt: new Date(Date.now() + 30 * 60 * 1000).toISOString(),
  extensionCount: 0,
  canExtend: true,
  canReset: true,
  extensionSeconds: 900,
  extensionsRemaining: 3,
  nodes: [],
}

const task = {
  uuid: '66666666-6666-4666-8666-666666666666',
  moduleUuid: '33333333-3333-4333-8333-333333333333',
  type: 'INSTANCE' as const,
  title: 'Lab',
  category: 'web',
  description: '',
  points: 10,
  status: 'AVAILABLE' as const,
  solved: false,
  visible: true,
  attachments: [],
  submission: { mode: 'TEXT' as const, checker: 'MANUAL' as const },
  instanceRequired: true,
  instance: { required: true, status: 'NOT_CREATED' as const, canStart: true },
}

function mountPanel() {
  return mount(InstancePanel, {
    props: {
      boardUuid: '22222222-2222-4222-8222-222222222222',
      moduleUuid: '33333333-3333-4333-8333-333333333333',
      task,
    },
    attachTo: document.body,
  })
}

async function clickExtend() {
  const button = Array.from(document.querySelectorAll('button')).find((candidate) =>
    candidate.textContent?.includes('Extend'),
  )
  expect(button, 'missing Extend button').toBeTruthy()
  button!.dispatchEvent(new Event('click', { bubbles: true }))
  await flushPromises()
}

describe('InstancePanel extension idempotency', () => {
  beforeEach(() => {
    getTaskInstance.mockReset()
    taskAction.mockReset()
    startTaskInstance.mockReset()
    getTaskInstance.mockResolvedValue({ ...runningInstance })
  })

  it('retries a failed extension with the same Idempotency-Key', async () => {
    taskAction.mockRejectedValueOnce(new Error('network lost')).mockResolvedValueOnce({ ...runningInstance })
    const wrapper = mountPanel()
    await flushPromises()

    await clickExtend()
    expect(taskAction).toHaveBeenCalledTimes(1)
    const firstKey = (taskAction.mock.calls[0][4] as string | undefined) ?? null
    expect(firstKey, 'the first attempt must send an explicit key').toBeTruthy()

    await clickExtend()
    expect(taskAction).toHaveBeenCalledTimes(2)
    const secondKey = (taskAction.mock.calls[1][4] as string | undefined) ?? null
    expect(secondKey).toBe(firstKey)

    wrapper.unmount()
  })

  it('uses a fresh key after a confirmed success', async () => {
    taskAction.mockResolvedValue({ ...runningInstance })
    const wrapper = mountPanel()
    await flushPromises()

    await clickExtend()
    const firstKey = (taskAction.mock.calls[0][4] as string | undefined) ?? null
    expect(firstKey).toBeTruthy()

    await clickExtend()
    const secondKey = (taskAction.mock.calls[1][4] as string | undefined) ?? null
    expect(secondKey).toBeTruthy()
    expect(secondKey).not.toBe(firstKey)

    wrapper.unmount()
  })

  it('renders the server extension step and remaining count', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const text = document.body.textContent ?? ''
    expect(text).toContain('Extend +15 min')
    expect(text).toContain('3 left')
    wrapper.unmount()
  })

  it('disables Extend with a readable reason when the server says no', async () => {
    getTaskInstance.mockResolvedValue({
      ...runningInstance,
      canExtend: false,
      extensionsRemaining: 0,
    })
    const wrapper = mountPanel()
    await flushPromises()
    const button = Array.from(document.querySelectorAll('button')).find((candidate) =>
      candidate.textContent?.includes('Extend'),
    )
    expect(button).toBeTruthy()
    expect(button!.hasAttribute('disabled')).toBe(true)
    expect(document.body.textContent).toContain('No extensions left')
    wrapper.unmount()
  })
})
