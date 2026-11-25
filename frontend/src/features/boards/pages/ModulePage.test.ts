import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModulePage from '@/features/boards/pages/ModulePage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { ApiError } from '@/shared/api/errors'
import type { Board, Module, TaskListItem } from '@/shared/types/api'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { boardUuid: 'board-1', moduleUuid: 'module-1' } }),
}))

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoard: vi.fn(),
    getModule: vi.fn(),
    listTasks: vi.fn(),
  },
}))

const getBoard = vi.mocked(boardsApi.getBoard)
const getModule = vi.mocked(boardsApi.getModule)
const listTasks = vi.mocked(boardsApi.listTasks)

const board: Board = {
  uuid: 'board-1',
  slug: 'staff-board',
  name: 'Staff board',
  moduleTypes: ['TASKS'],
  modulesCount: 1,
  participationMode: 'INDIVIDUAL',
  registrationMode: 'OPEN',
  status: 'ACTIVE',
  rating: { visibility: 'PUBLIC' },
  joined: false,
  description: 'Private board',
  visibility: 'PRIVATE',
  preview: true,
  canParticipate: false,
}

const moduleItem: Module = {
  uuid: 'module-1',
  boardUuid: 'board-1',
  name: 'Flag module',
  type: 'TASKS',
  allowedTaskTypes: ['FLAG'],
  status: 'ACTIVE',
  audienceUuids: [],
  order: 0,
  description: 'Flag tasks',
  preview: true,
  canParticipate: false,
}

const task: TaskListItem = {
  uuid: 'task-1',
  moduleUuid: 'module-1',
  type: 'FLAG',
  title: 'Preview flag',
  category: 'web',
  points: 100,
  status: 'AVAILABLE',
  solved: false,
  instanceRequired: false,
  preview: true,
  canParticipate: false,
}

function forbidden(): ApiError {
  return new ApiError(403, {
    code: 'BOARD_ACCESS_DENIED',
    message: 'Board access denied',
    path: '/api/v2/boards/board-1/modules/module-1/tasks',
    timestamp: '2026-11-25T09:00:00Z',
    traceId: 'trace-board',
  })
}

async function mountPage() {
  const wrapper = mount(ModulePage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  getBoard.mockReset()
  getModule.mockReset()
  listTasks.mockReset()
  getBoard.mockResolvedValue(board)
  getModule.mockResolvedValue(moduleItem)
})

describe('ModulePage staff preview', () => {
  it('renders the real staff task list with an organizer preview banner when not joined', async () => {
    listTasks.mockResolvedValue({ items: [task], limit: 100, offset: 0, total: 1 })
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Organizer preview')
    expect(wrapper.text()).toContain('Preview flag')
    expect(wrapper.text()).not.toContain('Could not load module')
    wrapper.unmount()
  })

  it('shows a preview state, not a fatal error, when a joined staff reader is denied the list', async () => {
    // The participant row survived a revoked access group: board.joined is true
    // but the module read is a management preview.
    getBoard.mockResolvedValue({ ...board, joined: true })
    listTasks.mockRejectedValue(forbidden())
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Preview only')
    expect(wrapper.text()).not.toContain('Could not load module')
    wrapper.unmount()
  })

  it('shows a participation state, not an error, for an unjoined non-staff reader', async () => {
    getBoard.mockResolvedValue({ ...board, joined: false, preview: false, canParticipate: false })
    getModule.mockResolvedValue({ ...moduleItem, preview: false, canParticipate: false })
    listTasks.mockRejectedValue(forbidden())
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Join the board to view tasks')
    expect(wrapper.text()).not.toContain('Could not load module')
    wrapper.unmount()
  })

  it('keeps a real error for an eligible participant whose list failed', async () => {
    getBoard.mockResolvedValue({ ...board, joined: true, preview: false, canParticipate: true })
    getModule.mockResolvedValue({ ...moduleItem, preview: false, canParticipate: true })
    listTasks.mockRejectedValue(forbidden())
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Could not load module')
    expect(wrapper.text()).not.toContain('Join the board to view tasks')
    wrapper.unmount()
  })
})
