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

const firstModule: ModuleListItem = {
  uuid: 'module-a',
  boardUuid: 'board-1',
  name: 'Alpha module',
  type: 'TASKS',
  allowedTaskTypes: ['FLAG'],
  status: 'ACTIVE',
  audienceUuids: [],
  order: 1,
}

const secondModule: ModuleListItem = {
  uuid: 'module-b',
  boardUuid: 'board-1',
  name: 'Beta module',
  type: 'TASKS',
  allowedTaskTypes: ['FLAG'],
  status: 'ACTIVE',
  audienceUuids: [],
  order: 2,
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

describe('BoardPage desktop workspace', () => {
  it('keeps the server module order in the main card list and standing in the rail', async () => {
    listModules.mockResolvedValue({
      items: [secondModule, firstModule],
      limit: 100,
      offset: 0,
      total: 2,
    })
    const wrapper = await mountPage()

    const workspace = wrapper.get('[data-slot="workspace"]')
    const main = workspace.get('[data-slot="workspace-main"]')
    const aside = workspace.get('[data-slot="workspace-aside"]')
    const cards = main.get('[data-slot="board-modules"]')
    const text = cards.text()

    expect(cards.findAll('a')).toHaveLength(2)
    expect(text.indexOf(secondModule.name)).toBeLessThan(text.indexOf(firstModule.name))
    expect(aside.text()).toContain('Your standing')
    expect(
      main.element.compareDocumentPosition(aside.element) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()

    wrapper.unmount()
  })
})
