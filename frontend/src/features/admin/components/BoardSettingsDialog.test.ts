import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BoardSettingsDialog from './BoardSettingsDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { DESCRIPTION_MAX_CHARACTERS } from '@/shared/lib/text'

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    createBoard: vi.fn(),
    updateBoard: vi.fn(),
    getBoardInviteCode: vi.fn(),
    rotateBoardInviteCode: vi.fn(),
  },
}))

const getInviteCode = vi.mocked(boardsApi.getBoardInviteCode)

const boardBase = {
  uuid: '11111111-1111-4111-8111-111111111111',
  slug: 'demo',
  name: 'Demo',
  description: '',
  moduleTypes: [],
  modulesCount: 0,
  participationMode: 'INDIVIDUAL' as const,
  registrationMode: 'OPEN' as const,
  visibility: 'PUBLIC' as const,
  status: 'DRAFT' as const,
  rating: { visibility: 'PUBLIC' as const },
  joined: false,
  teamSettings: null,
  accessGroupUuids: [],
}

describe('BoardSettingsDialog', () => {
  it('shows exactly the three access modes and no legacy visibility/registration controls', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: { open: true, board: null },
      attachTo: document.body,
    })
    await Promise.resolve()

    const text = document.body.textContent ?? ''
    expect(text).toContain('Public')
    expect(text).toContain('Selected audiences')
    expect(text).toContain('Invite code')
    expect(text).not.toContain('Visibility')
    expect(text).not.toContain('Registration')

    const radios = Array.from(document.querySelectorAll('input[name="board-access-mode"]')) as HTMLInputElement[]
    expect(radios.map((radio) => radio.value)).toEqual(['PUBLIC', 'AUDIENCE', 'INVITE'])
    expect(radios[0].checked).toBe(true)
    wrapper.unmount()
  })

  it('preserves a legacy board and asks for an explicit mode', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: {
        open: true,
        board: { ...boardBase, visibility: 'PRIVATE', registrationMode: 'CLOSED', accessMode: 'LEGACY' },
      },
      attachTo: document.body,
    })
    await Promise.resolve()

    const text = document.body.textContent ?? ''
    expect(text).toContain('legacy access combination')

    const radios = Array.from(document.querySelectorAll('input[name="board-access-mode"]')) as HTMLInputElement[]
    expect(radios.every((radio) => !radio.checked)).toBe(true)
    wrapper.unmount()
  })
})

describe('BoardSettingsDialog gate-change confirmation', () => {
  const legacyWithGroup = {
    ...boardBase,
    visibility: 'PRIVATE' as const,
    registrationMode: 'INVITE_ONLY' as const,
    accessMode: 'LEGACY' as const,
    accessGroupUuids: ['99999999-9999-4999-8999-999999999999'],
  }

  it('asks before converting a LEGACY board with a live gate to Public', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: { open: true, board: legacyWithGroup },
      attachTo: document.body,
    })
    await Promise.resolve()

    const publicRadio = document.querySelector('input[name="board-access-mode"][value="PUBLIC"]') as HTMLInputElement
    publicRadio.click()
    await Promise.resolve()
    const form = document.querySelector('form')!
    form.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await Promise.resolve()
    await Promise.resolve()

    // The explicit conversion must be confirmed, not saved immediately.
    const dialogText = Array.from(document.querySelectorAll('[role="dialog"]'))
      .map((node) => node.textContent ?? '')
      .join(' ')
    expect(dialogText).toContain('Change board access?')
    wrapper.unmount()
  })

  it('does not prompt for a metadata-only save of the same LEGACY board', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: { open: true, board: legacyWithGroup },
      attachTo: document.body,
    })
    await Promise.resolve()

    const nameInput = document.querySelector('#board-name') as HTMLInputElement
    nameInput.value = 'Renamed legacy board'
    nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    await Promise.resolve()
    const form = document.querySelector('form')!
    form.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await Promise.resolve()
    await Promise.resolve()

    const dialogText = Array.from(document.querySelectorAll('[role="dialog"]'))
      .map((node) => node.textContent ?? '')
      .join(' ')
    expect(dialogText).not.toContain('Change board access?')
    wrapper.unmount()
  })
})

describe('BoardSettingsDialog join code hand-off', () => {
  beforeEach(() => {
    getInviteCode.mockReset()
  })

  it('never reads the deprecated invite code when an INVITE board is opened', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: {
        open: true,
        board: {
          ...boardBase,
          visibility: 'PRIVATE',
          registrationMode: 'INVITE_ONLY',
          accessMode: 'INVITE',
        },
      },
      attachTo: document.body,
    })
    await flushPromises()

    expect(getInviteCode).not.toHaveBeenCalled()
    const text = document.body.textContent ?? ''
    expect(text).not.toContain('Could not load the invite code')
    // Code management lives in the board's canonical join code panel.
    expect(text).toContain('Join code panel')
    wrapper.unmount()
  })

  it('points a new INVITE board at the join code panel after saving', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: { open: true, board: null },
      attachTo: document.body,
    })
    await flushPromises()

    const inviteRadio = document.querySelector('input[name="board-access-mode"][value="INVITE"]') as HTMLInputElement
    inviteRadio.click()
    await flushPromises()

    const text = document.body.textContent ?? ''
    expect(text).toContain('Join code panel')
    expect(getInviteCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('BoardSettingsDialog Markdown authoring', () => {
  const updateBoard = vi.mocked(boardsApi.updateBoard)

  async function waitForEditor() {
    await vi.waitFor(() => {
      expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
    }, { timeout: 10_000, interval: 20 })
  }

  beforeEach(() => {
    updateBoard.mockReset()
    updateBoard.mockResolvedValue(boardBase)
  })

  it('edits the description with the rich editor and keeps Markdown on save', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: { open: true, board: { ...boardBase, description: '**bold**' } },
      attachTo: document.body,
    })
    await waitForEditor()

    expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
    expect(document.querySelector('#board-description')).not.toBeNull()

    await (document.querySelector('[aria-label="Source mode"]') as HTMLButtonElement).click()
    await flushPromises()
    expect((document.querySelector('textarea') as HTMLTextAreaElement).value).toBe('**bold**')

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateBoard).toHaveBeenCalledTimes(1)
    const [, body] = updateBoard.mock.calls[0] as [string, { description: string }]
    expect(body.description).toBe('**bold**')
    wrapper.unmount()
  })

  it('blocks submit when the description exceeds the server limit', async () => {
    const wrapper = mount(BoardSettingsDialog, {
      props: {
        open: true,
        board: { ...boardBase, description: 'x'.repeat(DESCRIPTION_MAX_CHARACTERS + 1) },
      },
      attachTo: document.body,
    })
    await waitForEditor()

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(updateBoard).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('at most 50,000 characters')

    // Exactly one message owns the error, and it stays visible on Preview.
    const alerts = Array.from(document.querySelectorAll('[role="alert"]'))
      .filter((element) => element.textContent?.includes('at most 50,000 characters'))
    expect(alerts).toHaveLength(1)
    const field = document.getElementById('board-description')!
    expect(field.getAttribute('aria-invalid')).toBe('true')
    const describedBy = field.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy!)?.textContent).toContain('at most 50,000 characters')

    const previewTab = Array.from(document.querySelectorAll('[role="tab"]'))
      .find((element) => element.textContent?.trim() === 'Preview') as HTMLElement
    previewTab.click()
    await flushPromises()
    const previewAlerts = Array.from(document.querySelectorAll('[role="alert"]'))
      .filter((element) => element.textContent?.includes('at most 50,000 characters'))
    expect(previewAlerts).toHaveLength(1)
    wrapper.unmount()
  })
})
