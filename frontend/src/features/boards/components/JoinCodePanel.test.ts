import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import JoinCodePanel from '@/features/boards/components/JoinCodePanel.vue'
import { boardsApi } from '@/features/boards/api/boards'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoardJoinCodeStatus: vi.fn(),
    setBoardJoinCode: vi.fn(),
  },
}))

const getStatus = vi.mocked(boardsApi.getBoardJoinCodeStatus)
const setCode = vi.mocked(boardsApi.setBoardJoinCode)

async function mountPanel() {
  const wrapper = mount(JoinCodePanel, {
    props: { boardUuid: '11111111-1111-4111-8111-111111111111' },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  getStatus.mockReset()
  setCode.mockReset()
  getStatus.mockResolvedValue({ configured: false, updatedAt: null })
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
    expect(wrapper.findAll('button').some((button) => button.text().includes('Generate join code'))).toBe(true)
    wrapper.unmount()
  })

  it('reveals a generated code once and never persists it', async () => {
    setCode.mockResolvedValue({ configured: true, code: 'ABCDEFGHJKLMNPQRSTVWXYZ012', updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    const generate = wrapper.findAll('button').find((button) => button.text().includes('Generate join code'))
    await generate?.trigger('click')
    await flushPromises()

    expect(setCode).toHaveBeenCalledWith('11111111-1111-4111-8111-111111111111', undefined)
    expect(wrapper.text()).toContain('Copy this code now')
    const revealed = document.body.querySelector('input[aria-label="New board join code"]')
    expect(revealed instanceof HTMLInputElement ? revealed.value : '').toBe('ABCDEFGHJKLMNPQRSTVWXYZ012')
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)

    const hide = wrapper.findAll('button').find((button) => button.text() === 'Hide')
    await hide?.trigger('click')
    expect(document.body.querySelector('input[aria-label="New board join code"]')).toBeNull()
    wrapper.unmount()
  })

  it('requires confirmation before replacing an existing code', async () => {
    getStatus.mockResolvedValue({ configured: true, updatedAt: '2026-11-20T09:00:00Z' })
    const wrapper = await mountPanel()

    expect(wrapper.text()).toContain('configured')
    const replace = wrapper.findAll('button').find((button) => button.text().includes('Replace with random code'))
    await replace?.trigger('click')
    await flushPromises()

    expect(setCode).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Replace the current join code?')
    expect(wrapper.text()).toContain('stops working immediately')
    wrapper.unmount()
  })

  it('sets a custom code and normalizes through the API contract', async () => {
    setCode.mockResolvedValue({ configured: true, code: 'TEAM-ALPHA-2026', updatedAt: '2026-11-25T09:00:00Z' })
    const wrapper = await mountPanel()

    const input = document.body.querySelector('#join-code-custom')
    if (!(input instanceof HTMLInputElement)) throw new Error('custom field not found')
    input.value = 'team-alpha-2026'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()

    const set = wrapper.findAll('button').find((button) => button.text().includes('Set custom code'))
    await set?.trigger('click')
    await flushPromises()

    expect(setCode).toHaveBeenCalledWith('11111111-1111-4111-8111-111111111111', 'team-alpha-2026')
    wrapper.unmount()
  })
})
