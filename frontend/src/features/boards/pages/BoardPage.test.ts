import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BoardPage from '@/features/boards/pages/BoardPage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import type { Board, BoardMe, BoardPermissionsResponse, ModuleListItem } from '@/shared/types/api'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { boardUuid: 'board-1' }, fullPath: '/boards/board-1' }),
}))

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoard: vi.fn(),
    getBoardMe: vi.fn(),
    listModules: vi.fn(),
    getMyPermissions: vi.fn(),
    leaveBoard: vi.fn(),
  },
}))

vi.mock('@/features/auth/session', () => ({
  useSession: () => ({ state: { status: 'authenticated', user: { roles: ['user'] } } }),
}))

const getBoard = vi.mocked(boardsApi.getBoard)
const getBoardMe = vi.mocked(boardsApi.getBoardMe)
const listModules = vi.mocked(boardsApi.listModules)
const getMyPermissions = vi.mocked(boardsApi.getMyPermissions)

const board: Board = {
  uuid: 'board-1',
  slug: 'demo-board',
  name: 'Demo board',
  moduleTypes: ['TASKS'],
  modulesCount: 0,
  participationMode: 'INDIVIDUAL',
  registrationMode: 'OPEN',
  status: 'ACTIVE',
  rating: { visibility: 'PUBLIC' },
  joined: true,
  description: '**bold** board',
  visibility: 'PUBLIC',
  preview: false,
  canParticipate: true,
}

const me: BoardMe = {
  joined: true,
  joinedAt: '2026-11-26T09:00:00Z',
  participationMode: 'INDIVIDUAL',
  audiences: [],
  score: 0,
  place: null,
}

const permissions: BoardPermissionsResponse = {
  boardUuid: 'board-1',
  roles: [],
  permissions: ['board.read'],
  modules: [],
}

async function mountPage() {
  const wrapper = mount(BoardPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  getBoard.mockReset()
  getBoardMe.mockReset()
  listModules.mockReset()
  getMyPermissions.mockReset()
  getBoard.mockResolvedValue(board)
  getBoardMe.mockResolvedValue(me)
  listModules.mockResolvedValue({ items: [] as ModuleListItem[], limit: 100, offset: 0, total: 0 })
  getMyPermissions.mockResolvedValue(permissions)
})

describe('BoardPage description rendering', () => {
  it('renders the stored description as safe Markdown in the header', async () => {
    getBoard.mockResolvedValue({
      ...board,
      description: '**bold** and\n\n```python\ndef f():\n  return 1\n```',
    })
    const wrapper = await mountPage()

    expect(wrapper.find('.markdown-body').exists()).toBe(true)
    expect(wrapper.find('strong').exists()).toBe(true)
    expect(wrapper.find('.hljs-keyword').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('**bold**')
    expect(wrapper.text()).not.toContain('```')
    wrapper.unmount()
  })

  it('escapes raw HTML in the description instead of rendering it', async () => {
    getBoard.mockResolvedValue({ ...board, description: '<script>alert(1)</script>' })
    const wrapper = await mountPage()

    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).toContain('<script>alert(1)</script>')
    wrapper.unmount()
  })
})
