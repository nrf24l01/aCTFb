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

describe('BoardPage task progress summary', () => {
  it('reads the server aggregate instead of summing the visible module cards', async () => {
    getBoardMe.mockResolvedValue({
      ...me,
      taskProgress: { solvedTasks: 42, totalTasks: 150 },
    })
    listModules.mockResolvedValue({
      items: [
        { ...firstModule, taskProgress: { solvedTasks: 1, totalTasks: 2 } },
        { ...secondModule, taskProgress: { solvedTasks: 2, totalTasks: 2 } },
      ],
      limit: 100,
      offset: 0,
      total: 2,
    })
    const wrapper = await mountPage()

    const summary = wrapper.get('[data-slot="board-task-progress"]')
    expect(summary.text()).toContain('Task progress')
    expect(summary.text()).toContain('42/150 solved')
    expect(summary.text()).not.toContain('3/4 solved')
    expect(summary.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('42')
    expect(summary.get('[role="progressbar"]').attributes('aria-valuemax')).toBe('150')
    wrapper.unmount()
  })

  it('omits the summary when the server publishes no own aggregate', async () => {
    const wrapper = await mountPage()

    expect(wrapper.find('[data-slot="board-task-progress"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not expose own progress in a staff preview read', async () => {
    getBoardMe.mockResolvedValue({
      ...me,
      joined: false,
      preview: true,
      taskProgress: { solvedTasks: 42, totalTasks: 150 },
    })
    const wrapper = await mountPage()

    expect(wrapper.find('[data-slot="board-task-progress"]').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('BoardPage module pagination', () => {
  it('loads modules beyond the first 100 with the server total', async () => {
    const firstItems = Array.from({ length: 100 }, (_, index) => ({
      ...firstModule,
      uuid: `module-${index}`,
      name: `Module ${index}`,
      order: index,
    }))
    const secondItems = Array.from({ length: 20 }, (_, index) => ({
      ...firstModule,
      uuid: `module-${index + 100}`,
      name: `Module ${index + 100}`,
      order: index + 100,
    }))
    listModules
      .mockResolvedValueOnce({ items: firstItems, limit: 100, offset: 0, total: 120 })
      .mockResolvedValueOnce({ items: secondItems, limit: 100, offset: 100, total: 120 })

    const wrapper = await mountPage()
    expect(wrapper.get('[data-slot="board-modules"]').findAll('a')).toHaveLength(100)

    const loadMore = wrapper.findAll('button').find((button) => button.text() === 'Load more')
    expect(loadMore).toBeDefined()
    await loadMore!.trigger('click')
    await flushPromises()

    expect(listModules).toHaveBeenLastCalledWith('board-1', { limit: 100, offset: 100 })
    expect(wrapper.get('[data-slot="board-modules"]').findAll('a')).toHaveLength(120)
    expect(wrapper.findAll('button').find((button) => button.text() === 'Load more')).toBeUndefined()
    wrapper.unmount()
  })
})
