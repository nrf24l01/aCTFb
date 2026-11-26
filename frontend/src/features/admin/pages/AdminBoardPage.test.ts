import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AdminBoardPage from '@/features/admin/pages/AdminBoardPage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { audiencesApi } from '@/features/audiences/api'

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
    getMyPermissions: vi.fn(),
    getBoardJoinCodeStatus: vi.fn(),
    getBoardInviteCode: vi.fn(),
    rotateBoardInviteCode: vi.fn(),
  },
}))

vi.mock('@/features/audiences/api', () => ({
  audiencesApi: { adminList: vi.fn() },
}))

const getAdminBoard = vi.mocked(boardsApi.getAdminBoard)
const listAdminModules = vi.mocked(boardsApi.listAdminModules)
const getMyPermissions = vi.mocked(boardsApi.getMyPermissions)
const getJoinCodeStatus = vi.mocked(boardsApi.getBoardJoinCodeStatus)
const getInviteCode = vi.mocked(boardsApi.getBoardInviteCode)
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
  getMyPermissions.mockReset()
  getJoinCodeStatus.mockReset()
  getInviteCode.mockReset()
  adminList.mockReset()

  getAdminBoard.mockResolvedValue(inviteBoard)
  listAdminModules.mockResolvedValue({ items: [], total: 0, limit: 100, offset: 0 })
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
