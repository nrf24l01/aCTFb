import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BoardSettingsDialog from './BoardSettingsDialog.vue'

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
