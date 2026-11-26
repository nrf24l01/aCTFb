import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TaskPage from '@/features/boards/pages/TaskPage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import type { Board, BoardPermissionsResponse, Module, Task } from '@/shared/types/api'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { boardUuid: 'board-1', moduleUuid: 'module-1', taskUuid: 'task-1' }, fullPath: '/x' }),
}))

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoard: vi.fn(),
    getModule: vi.fn(),
    getTask: vi.fn(),
    getMyPermissions: vi.fn(),
    listMySubmissions: vi.fn(),
  },
}))

vi.mock('@/features/auth/session', () => ({
  useSession: () => ({ state: { status: 'authenticated', user: { roles: ['admin'] } } }),
}))

const getBoard = vi.mocked(boardsApi.getBoard)
const getModule = vi.mocked(boardsApi.getModule)
const getTask = vi.mocked(boardsApi.getTask)
const getMyPermissions = vi.mocked(boardsApi.getMyPermissions)
const listMySubmissions = vi.mocked(boardsApi.listMySubmissions)

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

const task: Task = {
  uuid: 'task-1',
  moduleUuid: 'module-1',
  type: 'FLAG',
  title: 'Preview flag',
  category: 'web',
  points: 100,
  status: 'AVAILABLE',
  solved: false,
  instanceRequired: false,
  description: 'Preview description',
  attachments: [],
  submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
  instance: { required: false, status: 'NOT_CREATED', canStart: false },
  preview: true,
  canParticipate: false,
}

const permissions: BoardPermissionsResponse = {
  boardUuid: 'board-1',
  roles: ['board_owner'],
  permissions: ['board.read', 'module.tasks.manage'],
  modules: [],
}

async function mountPage() {
  const wrapper = mount(TaskPage, {
    global: {
      stubs: {
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
        FlagSubmitForm: { template: '<form data-test="flag-form" />' },
        ManualSubmissionForm: { template: '<form data-test="manual-form" />' },
        InstancePanel: { template: '<div data-test="instance-panel" />' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  getBoard.mockReset()
  getModule.mockReset()
  getTask.mockReset()
  getMyPermissions.mockReset()
  listMySubmissions.mockReset()
  getBoard.mockResolvedValue(board)
  getModule.mockResolvedValue(moduleItem)
  getTask.mockResolvedValue(task)
  getMyPermissions.mockResolvedValue(permissions)
  listMySubmissions.mockResolvedValue({ items: [], limit: 100, offset: 0, total: 0 })
})

describe('TaskPage staff preview', () => {
  it('reads the task as staff preview without calling own history or showing fake attempts', async () => {
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Preview flag')
    expect(wrapper.text()).toContain('Organizer preview')
    expect(wrapper.text()).not.toContain('Could not load submission history')
    expect(wrapper.text()).not.toContain('No submissions yet')
    expect(listMySubmissions).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="flag-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="instance-panel"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('treats a joined-but-preview task as preview: no history, no submit, no instance', async () => {
    // The participant row survived a revoked access group: joined is true, but
    // the backend marks the read as a preview.
    getBoard.mockResolvedValue({ ...board, joined: true })
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Organizer preview — participant access is unavailable')
    expect(listMySubmissions).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="flag-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="instance-panel"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Could not load submission history')
    wrapper.unmount()
  })

  it('keeps the participant submission form and history for an eligible participant', async () => {
    getBoard.mockResolvedValue({ ...board, joined: true, preview: false, canParticipate: true })
    getModule.mockResolvedValue({ ...moduleItem, preview: false, canParticipate: true })
    getTask.mockResolvedValue({ ...task, preview: false, canParticipate: true })
    const wrapper = await mountPage()

    expect(listMySubmissions).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="flag-form"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Organizer preview')
    wrapper.unmount()
  })
})

describe('TaskPage description rendering', () => {
  it('renders the description through the Markdown token styles', async () => {
    getTask.mockResolvedValue({
      ...task,
      description: '**bold** and\n\n```python\ndef f():\n  return 1\n```',
    })
    const wrapper = await mountPage()

    const article = wrapper.get('section[aria-labelledby="description-heading"] article')
    expect(article.classes()).toContain('markdown-body')
    expect(article.find('strong').exists()).toBe(true)
    expect(article.find('.hljs-keyword').exists()).toBe(true)
    expect(article.text()).not.toContain('**bold**')
    expect(article.text()).not.toContain('```')
    wrapper.unmount()
  })

  it('escapes raw HTML in the description instead of rendering it', async () => {
    getTask.mockResolvedValue({ ...task, description: '<script>alert(1)</script>' })
    const wrapper = await mountPage()

    const article = wrapper.get('section[aria-labelledby="description-heading"] article')
    expect(article.find('script').exists()).toBe(false)
    expect(article.text()).toContain('<script>alert(1)</script>')
    wrapper.unmount()
  })
})
