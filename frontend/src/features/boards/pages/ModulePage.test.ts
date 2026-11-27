import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModulePage from '@/features/boards/pages/ModulePage.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { ApiError } from '@/shared/api/errors'
import type { Board, Module, TaskListItem, TaskListResponse } from '@/shared/types/api'

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({
    params: { boardUuid: 'board-1', moduleUuid: 'module-1' },
  })
  return { useRoute: () => route }
})

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    getBoard: vi.fn(),
    getModule: vi.fn(),
    listTasks: vi.fn(),
  },
}))

const route = useRoute() as unknown as {
  params: { boardUuid: string; moduleUuid: string }
}
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

const participantBoard: Board = {
  ...board,
  name: 'Participant board',
  joined: true,
  preview: false,
  canParticipate: true,
}

const participantModule: Module = {
  ...moduleItem,
  preview: false,
  canParticipate: true,
  taskProgress: { solvedTasks: 3, totalTasks: 8 },
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

function page(items: TaskListItem[], total = items.length, offset = 0): TaskListResponse {
  return { items, limit: 100, offset, total }
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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function mountPage() {
  const wrapper = mount(ModulePage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

function filterButton(wrapper: Awaited<ReturnType<typeof mountPage>>, name: string) {
  return wrapper.findAll('button').find((button) => button.text() === name)
}

function filterButtons(wrapper: Awaited<ReturnType<typeof mountPage>>) {
  return ['All', 'Unsolved', 'Solved']
    .map((name) => filterButton(wrapper, name))
    .filter((button) => button !== undefined)
}

beforeEach(() => {
  route.params.boardUuid = 'board-1'
  route.params.moduleUuid = 'module-1'
  getBoard.mockReset()
  getModule.mockReset()
  listTasks.mockReset()
  getBoard.mockResolvedValue(board)
  getModule.mockResolvedValue(moduleItem)
})

describe('ModulePage staff preview', () => {
  it('renders the back link with an SVG chevron instead of a text glyph', async () => {
    listTasks.mockResolvedValue(page([task]))
    const wrapper = await mountPage()

    const backLink = wrapper.findAll('a').find((link) => link.text().includes('Staff board'))
    expect(backLink).toBeDefined()
    expect(backLink!.find('svg').exists()).toBe(true)
    expect(backLink!.text()).toContain('Staff board')
    expect(wrapper.text()).not.toContain('‹')
    wrapper.unmount()
  })

  it('renders the real staff task list with an organizer preview banner when not joined', async () => {
    listTasks.mockResolvedValue(page([task]))
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Organizer preview')
    expect(wrapper.text()).toContain('Preview flag')
    expect(wrapper.text()).not.toContain('Could not load module')
    expect(filterButtons(wrapper)).toHaveLength(0)
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
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
    expect(filterButtons(wrapper)).toHaveLength(0)
    wrapper.unmount()
  })

  it('shows a participation state, not an error, for an unjoined non-staff reader', async () => {
    getBoard.mockResolvedValue({ ...board, joined: false, preview: false, canParticipate: false })
    getModule.mockResolvedValue({ ...moduleItem, preview: false, canParticipate: false })
    listTasks.mockRejectedValue(forbidden())
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Join the board to view tasks')
    expect(wrapper.text()).not.toContain('Could not load module')
    expect(filterButtons(wrapper)).toHaveLength(0)
    wrapper.unmount()
  })

  it('keeps a real error for an eligible participant whose list failed', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    listTasks.mockRejectedValue(forbidden())
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('Could not load module')
    expect(wrapper.text()).not.toContain('Join the board to view tasks')
    wrapper.unmount()
  })
})

describe('ModulePage description rendering', () => {
  it('renders the stored description as safe Markdown in the header', async () => {
    getModule.mockResolvedValue({
      ...moduleItem,
      description: '**bold** and\n\n```python\ndef f():\n  return 1\n```',
    })
    listTasks.mockResolvedValue(page([]))
    const wrapper = await mountPage()

    expect(wrapper.find('.markdown-body').exists()).toBe(true)
    expect(wrapper.find('strong').exists()).toBe(true)
    expect(wrapper.find('.hljs-keyword').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('**bold**')
    expect(wrapper.text()).not.toContain('```')
    wrapper.unmount()
  })

  it('escapes raw HTML in the description instead of rendering it', async () => {
    getModule.mockResolvedValue({ ...moduleItem, description: '<script>alert(1)</script>' })
    listTasks.mockResolvedValue(page([]))
    const wrapper = await mountPage()

    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).toContain('<script>alert(1)</script>')
    wrapper.unmount()
  })
})

describe('ModulePage desktop task grid', () => {
  it('keeps the server task order inside the task card grid', async () => {
    const firstTask: TaskListItem = { ...task, uuid: 'task-a', title: 'Alpha task' }
    const secondTask: TaskListItem = { ...task, uuid: 'task-b', title: 'Beta task' }
    listTasks.mockResolvedValue(page([secondTask, firstTask], 2))

    const wrapper = await mountPage()

    const grid = wrapper.get('[data-slot="module-tasks"]')
    const text = grid.text()

    expect(grid.findAll('a')).toHaveLength(2)
    expect(text.indexOf(secondTask.title)).toBeLessThan(text.indexOf(firstTask.title))

    wrapper.unmount()
  })
})

describe('ModulePage participant completion filters', () => {
  it('defaults to All and sends each explicit completion query', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    listTasks.mockResolvedValue(page([task]))

    const wrapper = await mountPage()
    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 0 },
      expect.any(AbortSignal),
    )
    expect(filterButton(wrapper, 'All')!.attributes('aria-pressed')).toBe('true')
    expect(filterButton(wrapper, 'Unsolved')!.attributes('aria-pressed')).toBe('false')

    await filterButton(wrapper, 'Unsolved')!.trigger('click')
    await flushPromises()
    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 0, completion: 'UNSOLVED' },
      expect.any(AbortSignal),
    )
    expect(filterButton(wrapper, 'Unsolved')!.attributes('aria-pressed')).toBe('true')
    expect(filterButton(wrapper, 'All')!.attributes('aria-pressed')).toBe('false')

    await filterButton(wrapper, 'Solved')!.trigger('click')
    await flushPromises()
    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 0, completion: 'SOLVED' },
      expect.any(AbortSignal),
    )
    expect(filterButton(wrapper, 'Solved')!.attributes('aria-pressed')).toBe('true')
    wrapper.unmount()
  })

  it('keeps filters out of PENTEST and ATTACK_DEFENSE modules', async () => {
    for (const type of ['PENTEST', 'ATTACK_DEFENSE'] as const) {
      getBoard.mockResolvedValue(participantBoard)
      getModule.mockResolvedValue({
        ...participantModule,
        type,
        allowedTaskTypes: type === 'PENTEST' ? ['PENTEST_REPORT'] : ['EXPLOIT'],
        taskProgress: undefined,
      })
      listTasks.mockResolvedValue(page([]))

      const wrapper = await mountPage()
      expect(filterButtons(wrapper)).toHaveLength(0)
      expect(listTasks).not.toHaveBeenCalled()
      wrapper.unmount()
      listTasks.mockReset()
    }
  })

  it('loads beyond the first 100 tasks and resets the offset when the filter changes', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    const firstItems = Array.from({ length: 100 }, (_, index) => ({
      ...task,
      uuid: `task-${index}`,
      title: `Task ${index}`,
    }))
    const secondItems = Array.from({ length: 50 }, (_, index) => ({
      ...task,
      uuid: `task-${index + 100}`,
      title: `Task ${index + 100}`,
    }))
    listTasks
      .mockResolvedValueOnce(page(firstItems, 150, 0))
      .mockResolvedValueOnce(page(secondItems, 150, 100))
      .mockResolvedValue(page([], 0, 0))

    const wrapper = await mountPage()
    expect(wrapper.get('[data-slot="module-tasks"]').findAll('a')).toHaveLength(100)

    const loadMore = filterButton(wrapper, 'Load more')
    expect(loadMore).toBeDefined()
    await loadMore!.trigger('click')
    await flushPromises()

    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 100 },
      expect.any(AbortSignal),
    )
    expect(wrapper.get('[data-slot="module-tasks"]').findAll('a')).toHaveLength(150)
    expect(filterButton(wrapper, 'Load more')).toBeUndefined()

    await filterButton(wrapper, 'Unsolved')!.trigger('click')
    await flushPromises()
    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 0, completion: 'UNSOLVED' },
      expect.any(AbortSignal),
    )
    wrapper.unmount()
  })

  it('deduplicates a shifted server page instead of creating duplicate card keys', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    const firstTask = { ...task, uuid: 'task-1', title: 'First task' }
    const newTask = { ...task, uuid: 'task-2', title: 'Second task' }
    listTasks
      .mockResolvedValueOnce(page([firstTask], 2, 0))
      .mockResolvedValueOnce(page([firstTask, newTask], 2, 1))

    const wrapper = await mountPage()
    await filterButton(wrapper, 'Load more')!.trigger('click')
    await flushPromises()

    const cards = wrapper.get('[data-slot="module-tasks"]').findAll('a')
    expect(cards).toHaveLength(2)
    expect(cards.map((card) => card.text())).toEqual([
      expect.stringContaining('First task'),
      expect.stringContaining('Second task'),
    ])
    wrapper.unmount()
  })

  it('never lets a slower old filter response replace the selected filter', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    const unsolved = deferred<TaskListResponse>()
    const solved = deferred<TaskListResponse>()
    listTasks
      .mockResolvedValueOnce(page([task]))
      .mockImplementationOnce(() => unsolved.promise)
      .mockImplementationOnce(() => solved.promise)

    const wrapper = await mountPage()
    await filterButton(wrapper, 'Unsolved')!.trigger('click')
    await filterButton(wrapper, 'Solved')!.trigger('click')

    solved.resolve(
      page([{ ...task, uuid: 'solved-1', title: 'Solved target', status: 'SOLVED', solved: true }]),
    )
    await flushPromises()
    unsolved.resolve(page([{ ...task, uuid: 'unsolved-1', title: 'Unsolved stale' }]))
    await flushPromises()

    expect(wrapper.text()).toContain('Solved target')
    expect(wrapper.text()).not.toContain('Unsolved stale')
    expect(filterButton(wrapper, 'Solved')!.attributes('aria-pressed')).toBe('true')
    wrapper.unmount()
  })

  it('preserves the current filter and retries the actual query after an error', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    listTasks
      .mockResolvedValueOnce(page([task]))
      .mockRejectedValueOnce(forbidden())
      .mockResolvedValueOnce(page([{ ...task, uuid: 'recovered', title: 'Recovered task' }]))

    const wrapper = await mountPage()
    await filterButton(wrapper, 'Unsolved')!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Could not load tasks')
    expect(filterButton(wrapper, 'Unsolved')!.attributes('aria-pressed')).toBe('true')
    const retry = filterButton(wrapper, 'Retry')
    expect(retry).toBeDefined()

    await retry!.trigger('click')
    await flushPromises()

    expect(listTasks).toHaveBeenLastCalledWith(
      'board-1',
      'module-1',
      { limit: 100, offset: 0, completion: 'UNSOLVED' },
      expect.any(AbortSignal),
    )
    expect(wrapper.text()).toContain('Recovered task')
    expect(wrapper.text()).not.toContain('Could not load tasks')
    wrapper.unmount()
  })

  it('keeps the unfiltered all-solved module total when a filtered page is empty', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue({
      ...participantModule,
      taskProgress: { solvedTasks: 8, totalTasks: 8 },
    })
    const solvedItems = Array.from({ length: 8 }, (_, index) => ({
      ...task,
      uuid: `solved-${index}`,
      title: `Solved ${index}`,
      status: 'SOLVED' as const,
      solved: true,
    }))
    listTasks
      .mockResolvedValueOnce(page(solvedItems, 8, 0))
      .mockResolvedValueOnce(page([], 0, 0))

    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('8/8 solved')
    expect(wrapper.text()).toContain('Complete')

    await filterButton(wrapper, 'Unsolved')!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('No unsolved tasks')
    expect(wrapper.text()).not.toContain('No tasks available')
    expect(wrapper.text()).toContain('8/8 solved')
    expect(wrapper.text()).toContain('Complete')
    expect(filterButton(wrapper, 'Unsolved')).toBeDefined()
    wrapper.unmount()
  })

  it('uses the ruled empty label for a Solved filter with no matches', async () => {
    getBoard.mockResolvedValue(participantBoard)
    getModule.mockResolvedValue(participantModule)
    listTasks.mockResolvedValueOnce(page([task])).mockResolvedValue(page([], 0, 0))

    const wrapper = await mountPage()
    await filterButton(wrapper, 'Solved')!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('No solved tasks')
    expect(wrapper.text()).not.toContain('No unsolved tasks')
    wrapper.unmount()
  })
})

describe('ModulePage route changes', () => {
  it('reloads once for a new route and ignores the older GetModule response', async () => {
    const first = deferred<Module>()
    const second = deferred<Module>()
    getModule.mockReset()
    getModule.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
    listTasks.mockResolvedValue(page([]))

    const wrapper = mount(ModulePage, {
      global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } },
    })
    await flushPromises()

    route.params.moduleUuid = 'module-2'
    await nextTick()
    await flushPromises()
    expect(getModule).toHaveBeenCalledTimes(2)

    second.resolve({ ...participantModule, uuid: 'module-2', name: 'Second module' })
    await flushPromises()
    expect(wrapper.text()).toContain('Second module')

    first.resolve({ ...participantModule, uuid: 'module-1', name: 'First module' })
    await flushPromises()
    expect(wrapper.text()).toContain('Second module')
    expect(wrapper.text()).not.toContain('First module')
    wrapper.unmount()
  })
})
