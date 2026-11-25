import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import JoinCodePanel from '@/features/boards/components/JoinCodePanel.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { ApiError } from '@/shared/api/errors'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoardJoinCodeStatus: vi.fn(),
    setBoardJoinCode: vi.fn(),
  },
}))

const getStatus = vi.mocked(boardsApi.getBoardJoinCodeStatus)
const setCode = vi.mocked(boardsApi.setBoardJoinCode)
const boardUuid = '11111111-1111-4111-8111-111111111111'

async function mountPanel() {
  const wrapper = mount(JoinCodePanel, {
    props: { boardUuid },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

function buttonByText(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

async function typeCustom(value: string) {
  const input = document.body.querySelector('#join-code-custom')
  if (!(input instanceof HTMLInputElement)) throw new Error('custom field not found')
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

function conflict(): ApiError {
  return new ApiError(409, {
    code: 'BOARD_STATE_CONFLICT',
    message: 'The join code was changed by someone else',
    path: '/api/v2/admin/boards/x/join-code',
    timestamp: '2026-11-25T09:00:00Z',
    traceId: 'trace-code',
  })
}

beforeEach(() => {
  getStatus.mockReset()
  setCode.mockReset()
  getStatus.mockResolvedValue({ configured: false, revision: 0, updatedAt: null })
})

afterEach(() => {
  document.body.innerHTML = ''
  window.localStorage.clear()
  window.sessionStorage.clear()
})

describe('JoinCodePanel', () => {
  it('shows the empty state and a generate action when no code is configured', async () => {
    const wrapper = await mountPanel()
    expect(wrapper.text()).toContain('not configured')
    expect(wrapper.text()).toContain('No code is configured')
    expect(buttonByText(wrapper, 'Generate join code')).toBeTruthy()
    wrapper.unmount()
  })

  it('reveals a generated code once and never persists it', async () => {
    setCode.mockResolvedValue({ configured: true, code: 'ABCDEFGHJKLMNPQRSTVWXYZ012', revision: 1, updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    await buttonByText(wrapper, 'Generate join code')?.trigger('click')
    await flushPromises()

    expect(setCode).toHaveBeenCalledWith(boardUuid, undefined, undefined)
    expect(wrapper.text()).toContain('Copy this code now')
    const revealed = document.body.querySelector('input[aria-label="New board join code"]')
    expect(revealed instanceof HTMLInputElement ? revealed.value : '').toBe('ABCDEFGHJKLMNPQRSTVWXYZ012')
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)

    await buttonByText(wrapper, 'Hide')?.trigger('click')
    expect(document.body.querySelector('input[aria-label="New board join code"]')).toBeNull()
    wrapper.unmount()
  })

  it('requires confirmation before replacing an existing code and sends the revision', async () => {
    getStatus.mockResolvedValue({ configured: true, revision: 4, updatedAt: '2026-11-20T09:00:00Z' })
    setCode.mockResolvedValue({ configured: true, code: 'NEW-RANDOM-CODE-01', revision: 5, updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    expect(wrapper.text()).toContain('configured')
    await buttonByText(wrapper, 'Replace with random code')?.trigger('click')
    await flushPromises()
    expect(setCode).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Replace the current join code?')

    await buttonByText(wrapper, 'Replace code')?.trigger('click')
    await flushPromises()
    expect(setCode).toHaveBeenCalledWith(boardUuid, undefined, 4)
    wrapper.unmount()
  })

  it('commits the frozen intention: typed custom then "replace with random" generates a random code', async () => {
    getStatus.mockResolvedValue({ configured: true, revision: 2, updatedAt: '2026-11-20T09:00:00Z' })
    setCode.mockResolvedValue({ configured: true, code: 'ACTUAL-RANDOM-CODE', revision: 3, updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    await typeCustom('MY-CUSTOM-CODE-01')
    await buttonByText(wrapper, 'Replace with random code')?.trigger('click')
    await flushPromises()
    // The stale form value must not be what gets committed.
    await buttonByText(wrapper, 'Replace code')?.trigger('click')
    await flushPromises()

    expect(setCode).toHaveBeenCalledWith(boardUuid, undefined, 2)
    const revealed = document.body.querySelector('input[aria-label="New board join code"]')
    expect(revealed instanceof HTMLInputElement ? revealed.value : '').toBe('ACTUAL-RANDOM-CODE')
    wrapper.unmount()
  })

  it('cancelling the confirmation performs no write', async () => {
    getStatus.mockResolvedValue({ configured: true, revision: 2, updatedAt: '2026-11-20T09:00:00Z' })
    const wrapper = await mountPanel()

    await buttonByText(wrapper, 'Replace with random code')?.trigger('click')
    await flushPromises()
    await buttonByText(wrapper, 'Cancel')?.trigger('click')
    await flushPromises()

    expect(setCode).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('Replace the current join code?')
    wrapper.unmount()
  })

  it('disables every mutating control while the status is unknown or failed', async () => {
    getStatus.mockRejectedValue(
      new ApiError(500, {
        code: 'INTERNAL_ERROR',
        message: 'Internal server error',
        path: '/x',
        timestamp: '2026-11-25T09:00:00Z',
        traceId: 'trace-status',
      }),
    )
    const wrapper = await mountPanel()

    expect(wrapper.text()).toContain('could not be read')
    expect(buttonByText(wrapper, 'Retry status')).toBeTruthy()
    // No generate/replace control is offered, and the custom field is disabled.
    expect(buttonByText(wrapper, 'Generate join code')).toBeUndefined()
    const customInput = document.body.querySelector('#join-code-custom')
    expect(customInput).toBeNull()
    expect(setCode).not.toHaveBeenCalled()

    // Retry recovers and re-enables the panel.
    getStatus.mockResolvedValue({ configured: false, revision: 0, updatedAt: null })
    await buttonByText(wrapper, 'Retry status')?.trigger('click')
    await flushPromises()
    expect(buttonByText(wrapper, 'Generate join code')).toBeTruthy()
    wrapper.unmount()
  })

  it('keeps the typed custom value on failure but clears it on hide', async () => {
    getStatus.mockResolvedValue({ configured: false, revision: 0, updatedAt: null })
    setCode.mockRejectedValue(conflict())
    const wrapper = await mountPanel()

    await typeCustom('RETRY-ME-CODE-01')
    await buttonByText(wrapper, 'Set custom code')?.trigger('click')
    await flushPromises()

    const customInput = document.body.querySelector('#join-code-custom') as HTMLInputElement | null
    expect(customInput?.value).toBe('RETRY-ME-CODE-01')

    // A successful retry reveals the code and clears the consumed field.
    setCode.mockResolvedValue({ configured: true, code: 'RETRY-ME-CODE-01', revision: 1, updatedAt: '2026-11-25T09:00:00Z' })
    getStatus.mockResolvedValue({ configured: false, revision: 0, updatedAt: null })
    await buttonByText(wrapper, 'Set custom code')?.trigger('click')
    await flushPromises()
    const revealed = document.body.querySelector('input[aria-label="New board join code"]')
    expect(revealed instanceof HTMLInputElement ? revealed.value : '').toBe('RETRY-ME-CODE-01')
    wrapper.unmount()
  })

  it('sets a custom code and passes the revision when replacing', async () => {
    getStatus.mockResolvedValue({ configured: true, revision: 9, updatedAt: '2026-11-20T09:00:00Z' })
    setCode.mockResolvedValue({ configured: true, code: 'TEAM-ALPHA-2026', revision: 10, updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    await typeCustom('team-alpha-2026')
    await buttonByText(wrapper, 'Replace with custom code')?.trigger('click')
    await flushPromises()
    await buttonByText(wrapper, 'Replace code')?.trigger('click')
    await flushPromises()

    expect(setCode).toHaveBeenCalledWith(boardUuid, 'team-alpha-2026', 9)
    wrapper.unmount()
  })
})
