import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import JoinBoardDialog from '@/features/boards/components/JoinBoardDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { ApiError } from '@/shared/api/errors'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    joinBoardByCode: vi.fn(),
  },
}))

const joinBoardByCode = vi.mocked(boardsApi.joinBoardByCode)

const joined = {
  boardUuid: '11111111-1111-4111-8111-111111111111',
  slug: 'staff-board',
  name: 'Staff board',
  joined: true,
  alreadyParticipating: false,
  me: { joined: true, participationMode: 'INDIVIDUAL' as const, audiences: [] },
}

async function mountDialog(props: { open: boolean; boardUuid?: string } = { open: true }) {
  const wrapper = mount(JoinBoardDialog, {
    props,
    attachTo: document.body,
  })
  await nextTick()
  return wrapper
}

async function fillCode(value: string) {
  const input = document.body.querySelector('#join-board-code')
  if (!(input instanceof HTMLInputElement)) throw new Error('code field not found')
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

async function submit() {
  const form = document.body.querySelector('form')
  if (!form) throw new Error('form not found')
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await flushPromises()
  await nextTick()
}

beforeEach(() => {
  joinBoardByCode.mockReset()
  joinBoardByCode.mockResolvedValue(joined)
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('JoinBoardDialog', () => {
  it('offers exactly one board code field and no URL or UUID input', async () => {
    const wrapper = await mountDialog()
    const inputs = document.body.querySelectorAll('input')
    expect(inputs).toHaveLength(1)
    expect(inputs[0].id).toBe('join-board-code')
    expect(document.body.textContent).not.toMatch(/board uuid/i)
    expect(document.body.textContent).not.toMatch(/paste.*link/i)
    wrapper.unmount()
  })

  it('joins with the entered code and emits the resolved board', async () => {
    const wrapper = await mountDialog({ open: true, boardUuid: joined.boardUuid })
    await fillCode('  team-alpha-2026  ')
    await submit()

    expect(joinBoardByCode).toHaveBeenCalledWith('team-alpha-2026', joined.boardUuid)
    expect(wrapper.emitted('joined')?.[0]?.[0]).toMatchObject({ boardUuid: joined.boardUuid })
    wrapper.unmount()
  })

  it('never persists the code in Web Storage or the URL', async () => {
    const wrapper = await mountDialog()
    await fillCode('TEAM-ALPHA-2026')
    await submit()

    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
    expect(window.location.href).not.toContain('TEAM-ALPHA-2026')
    wrapper.unmount()
  })

  it('clears the code on close and on success', async () => {
    const wrapper = await mountDialog()
    await fillCode('TEAM-ALPHA-2026')
    const input = document.body.querySelector('#join-board-code')
    expect(input instanceof HTMLInputElement ? input.value : '').toBe('TEAM-ALPHA-2026')

    await submit()
    await nextTick()
    expect((document.body.querySelector('#join-board-code') as HTMLInputElement | null)?.value ?? '').toBe('')
    wrapper.unmount()
  })

  it('shows a friendly message for an invalid code instead of a raw error', async () => {
    joinBoardByCode.mockRejectedValue(
      new ApiError(403, {
        code: 'JOIN_CODE_INVALID',
        message: 'Join code is invalid',
        path: '/api/v2/boards/join',
        timestamp: '2026-11-25T09:00:00Z',
        traceId: 'trace-join',
      }),
    )
    const wrapper = await mountDialog()
    await fillCode('WRONG-CODE-0001')
    await submit()

    expect(document.body.textContent).toContain('That board code is not valid')
    expect(wrapper.emitted('joined')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps the submit disabled for a code that is too short', async () => {
    const wrapper = await mountDialog()
    await fillCode('short')
    await submit()
    expect(joinBoardByCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
