import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import InviteCodePanel from '@/features/boards/components/InviteCodePanel.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { ApiError } from '@/shared/api/errors'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoardInviteCode: vi.fn(),
    rotateBoardInviteCode: vi.fn(),
  },
}))

const getInvite = vi.mocked(boardsApi.getBoardInviteCode)
const boardUuid = '11111111-1111-4111-8111-111111111111'

async function mountPanel() {
  const wrapper = mount(InviteCodePanel, {
    props: { boardUuid },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

function buttonByText(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

/** The documented no-code answer for a board created after join codes shipped. */
function noLegacyCode(): ApiError {
  return new ApiError(422, {
    code: 'INVALID_INVITE_CODE',
    message: 'Invalid invite code',
    path: `/api/v2/admin/boards/${boardUuid}/invite-code`,
    timestamp: '2026-11-26T09:00:00Z',
    traceId: 'trace-legacy',
  })
}

beforeEach(() => {
  getInvite.mockReset()
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('InviteCodePanel', () => {
  it('is labelled as the legacy code, not the primary join code', async () => {
    const wrapper = await mountPanel()
    const text = document.body.textContent ?? ''
    expect(text).toContain('Legacy invite code')
    expect(text).toContain('Join code')
    // The old copy claimed this code was needed to join; that is the canonical code now.
    expect(text).not.toContain('Needed to join this board')
    wrapper.unmount()
  })

  it('never reads the deprecated route until the organizer asks', async () => {
    const wrapper = await mountPanel()
    expect(getInvite).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a neutral empty state when the board has no legacy code', async () => {
    getInvite.mockRejectedValue(noLegacyCode())
    const wrapper = await mountPanel()

    await buttonByText(wrapper, 'Reveal')!.trigger('click')
    await flushPromises()

    const text = document.body.textContent ?? ''
    expect(text).toContain('no legacy invite code')
    // A documented "no code configured" answer is not a destructive failure.
    expect(text).not.toContain('Could not read invite code')
    wrapper.unmount()
  })

  it('still reveals a configured legacy code on demand', async () => {
    getInvite.mockResolvedValue({
      inviteCode: 'LEGACY-CODE-0001',
      expiresAt: '2026-12-26T09:00:00Z',
    })
    const wrapper = await mountPanel()

    await buttonByText(wrapper, 'Reveal')!.trigger('click')
    await flushPromises()

    const input = document.body.querySelector('input[readonly]')
    expect(input instanceof HTMLInputElement ? input.value : '').toBe('LEGACY-CODE-0001')
    wrapper.unmount()
  })
})
