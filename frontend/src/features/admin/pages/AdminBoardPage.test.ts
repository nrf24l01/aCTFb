import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AdminBoardPage from '@/features/admin/pages/AdminBoardPage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { audiencesApi } from '@/features/audiences/api'
import type { AdminModule } from '@/shared/types/api'

const boardUuid = '11111111-1111-4111-8111-111111111111'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { boardUuid } }),
}))

vi.mock('@/features/auth/session', () => ({
  useSession: () => ({ state: { user: { uuid: '22222222-2222-4222-8222-222222222222', roles: ['admin'] } } }),
}))

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getAdminBoard: vi.fn(),
    listAdminModules: vi.fn(),
    listAdminTasks: vi.fn(),
    getMyPermissions: vi.fn(),
    getBoardJoinCodeStatus: vi.fn(),
    getBoardInviteCode: vi.fn(),
    rotateBoardInviteCode: vi.fn(),
    boardAction: vi.fn(),
  },
}))

vi.mock('@/features/audiences/api', () => ({
  audiencesApi: { adminList: vi.fn() },
}))

const getAdminBoard = vi.mocked(boardsApi.getAdminBoard)
const listAdminModules = vi.mocked(boardsApi.listAdminModules)
const listAdminTasks = vi.mocked(boardsApi.listAdminTasks)
const getMyPermissions = vi.mocked(boardsApi.getMyPermissions)
const getJoinCodeStatus = vi.mocked(boardsApi.getBoardJoinCodeStatus)
const getInviteCode = vi.mocked(boardsApi.getBoardInviteCode)
const boardAction = vi.mocked(boardsApi.boardAction)
const adminList = vi.mocked(audiencesApi.adminList)

/** A board created with accessMode INVITE after join codes replaced invite codes. */
const inviteBoard = {
  uuid: boardUuid,
  slug: 'invite-board',
  name: 'Invite board',
  description: '',
  moduleTypes: [],
  modulesCount: 0,
  participationMode: 'INDIVIDUAL' as const,
  registrationMode: 'INVITE_ONLY' as const,
  visibility: 'PRIVATE' as const,
  accessMode: 'INVITE' as const,
  status: 'DRAFT' as const,
  rating: { visibility: 'PUBLIC' as const },
  joined: false,
  teamSettings: null,
  accessGroupUuids: [],
  canParticipate: false,
}

const moduleItem: AdminModule = {
  uuid: '33333333-3333-4333-8333-333333333333',
  boardUuid,
  name: 'Workspace FLAG',
  type: 'TASKS',
  allowedTaskTypes: ['FLAG'],
  status: 'ACTIVE',
  audienceUuids: [],
  order: 0,
  description: 'Flag tasks',
  enabled: true,
  preview: false,
  canParticipate: false,
  taskInstanceSettings: null,
}

async function mountPage() {
  const wrapper = mount(AdminBoardPage, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

function buttonByText(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

beforeEach(() => {
  getAdminBoard.mockReset()
  listAdminModules.mockReset()
  listAdminTasks.mockReset()
  getMyPermissions.mockReset()
  getJoinCodeStatus.mockReset()
  getInviteCode.mockReset()
  boardAction.mockReset()
  adminList.mockReset()

  getAdminBoard.mockResolvedValue(inviteBoard)
  listAdminModules.mockResolvedValue({ items: [], total: 0, limit: 100, offset: 0 })
  listAdminTasks.mockResolvedValue({ items: [], total: 0, limit: 100, offset: 0 })
  getMyPermissions.mockResolvedValue({ permissions: [], modules: [] } as never)
  getJoinCodeStatus.mockResolvedValue({ configured: false, revision: 0, updatedAt: null })
  adminList.mockResolvedValue({ items: [], total: 0, limit: 100, offset: 0 })
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('AdminBoardPage invite board', () => {
  it('opens the canonical join code panel without reading the deprecated invite code', async () => {
    const wrapper = await mountPage()

    // The canonical panel is the primary code management surface.
    expect(getJoinCodeStatus).toHaveBeenCalledWith(boardUuid)
    expect(getInviteCode).not.toHaveBeenCalled()

    const text = document.body.textContent ?? ''
    expect(text).toContain('Join code')
    expect(text).toContain('not configured')
    expect(text).toContain('Generate join code')
    expect(text).toContain('Set custom code')

    // The legacy panel is not mounted automatically and shows no error.
    expect(document.querySelector('#invite-code-heading')).toBeNull()
    expect(text).not.toContain('Could not read invite code')
    wrapper.unmount()
  })

  it('offers the legacy reveal only as an explicit advanced action', async () => {
    const wrapper = await mountPage()

    const legacyButton = buttonByText(wrapper, 'Legacy invite code')
    expect(legacyButton).toBeDefined()

    await legacyButton!.trigger('click')
    await flushPromises()

    // Only now is the deprecated panel mounted, and still without a read.
    expect(document.querySelector('#invite-code-heading')).not.toBeNull()
    expect(getInviteCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

function withModule() {
  listAdminModules.mockResolvedValue({ items: [moduleItem], total: 1, limit: 100, offset: 0 })
}

describe('AdminBoardPage desktop workspace', () => {
  it('puts the module work before the management rail in document order', async () => {
    withModule()
    const wrapper = await mountPage()

    const main = wrapper.get('[data-slot="workspace-main"]')
    const aside = wrapper.get('[data-slot="workspace-aside"]')

    expect(main.find('#admin-modules-heading').exists()).toBe(true)
    expect(main.find('#join-code-heading').exists()).toBe(false)
    expect(main.find('#lifecycle-heading').exists()).toBe(false)
    expect(aside.find('#join-code-heading').exists()).toBe(true)
    expect(aside.find('#lifecycle-heading').exists()).toBe(true)
    expect(
      main.element.compareDocumentPosition(aside.element) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()

    wrapper.unmount()
  })

  it('keeps every lifecycle control and note inside a labelled disclosure that starts collapsed', async () => {
    withModule()
    const wrapper = await mountPage()

    const disclosure = wrapper.get('[data-slot="lifecycle-disclosure"]')
    const toggle = disclosure.get('button[aria-label="Show lifecycle actions"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    // The referenced body is removed by v-if while collapsed, so the control
    // must not advertise an IDREF that does not resolve.
    expect(toggle.attributes('aria-controls')).toBeUndefined()
    expect(wrapper.find('#lifecycle-body').exists()).toBe(false)
    expect(buttonByText(wrapper, 'Publish')).toBeUndefined()
    expect(buttonByText(wrapper, 'Archive board')).toBeUndefined()
    expect(buttonByText(wrapper, 'Delete board')).toBeUndefined()

    await toggle.trigger('click')

    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(toggle.attributes('aria-controls')).toBe('lifecycle-body')
    const body = wrapper.get('#lifecycle-body')
    expect(body.text()).toContain('Only a global admin can archive or delete a board.')
    expect(buttonByText(wrapper, 'Publish')).toBeDefined()
    expect(buttonByText(wrapper, 'Archive board')).toBeDefined()
    expect(buttonByText(wrapper, 'Delete board')).toBeDefined()

    wrapper.unmount()
  })

  it('keeps the archive confirmation inside the existing destructive flow', async () => {
    withModule()
    const wrapper = await mountPage()

    await wrapper.get('[data-slot="lifecycle-disclosure"] button[aria-label="Show lifecycle actions"]').trigger('click')
    await buttonByText(wrapper, 'Archive board')!.trigger('click')
    await flushPromises()

    expect(document.body.textContent).toContain(
      'Archiving is a global-admin action and hides the board from participants.',
    )
    expect(boardAction).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a board action error outside the collapsed lifecycle region', async () => {
    withModule()
    boardAction.mockRejectedValue(new Error('server refused'))
    const wrapper = await mountPage()

    await wrapper.get('[data-slot="lifecycle-disclosure"] button[aria-label="Show lifecycle actions"]').trigger('click')
    await buttonByText(wrapper, 'Publish')!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Board action failed')
    expect(wrapper.get('#lifecycle-body').text()).not.toContain('Board action failed')
    wrapper.unmount()
  })

  it('keeps one Create module action in the page header when modules exist', async () => {
    withModule()
    const wrapper = await mountPage()

    const createButtons = wrapper
      .findAll('button')
      .filter((button) => button.text().includes('Create module'))
    expect(createButtons).toHaveLength(1)

    wrapper.unmount()
  })

  it('keeps the only Create module action in the page header when there are no modules', async () => {
    const wrapper = await mountPage()

    const createButtons = wrapper
      .findAll('button')
      .filter((button) => button.text().includes('Create module'))
    expect(createButtons).toHaveLength(1)

    wrapper.unmount()
  })
})
