import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from '@/shared/api/client'
import { boardsApi } from '@/features/boards/api/boards'

vi.mock('@/shared/api/client', () => ({
  apiRequest: vi.fn(),
  apiPost: vi.fn(),
  apiPatch: vi.fn(),
  apiDelete: vi.fn(),
}))

const request = vi.mocked(apiRequest)

beforeEach(() => {
  request.mockReset()
  request.mockResolvedValue({ items: [], limit: 100, offset: 0, total: 0 })
})

describe('boardsApi task completion query', () => {
  it('forwards the completion filter as an explicit query parameter', async () => {
    await boardsApi.listTasks('board-1', 'module-1', {
      limit: 100,
      offset: 0,
      completion: 'SOLVED',
    })

    expect(request).toHaveBeenCalledWith(
      '/boards/board-1/modules/module-1/tasks?limit=100&offset=0&completion=SOLVED',
      expect.objectContaining({ signal: undefined }),
    )
  })

  it('omits completion for the default All selection', async () => {
    await boardsApi.listTasks('board-1', 'module-1', { limit: 100, offset: 0 })

    expect(request).toHaveBeenCalledWith(
      '/boards/board-1/modules/module-1/tasks?limit=100&offset=0',
      expect.anything(),
    )
  })

  it('keeps an explicit abort signal for stale-request protection', async () => {
    const controller = new AbortController()
    await boardsApi.listTasks('board-1', 'module-1', { completion: 'UNSOLVED' }, controller.signal)

    expect(request).toHaveBeenCalledWith(
      '/boards/board-1/modules/module-1/tasks?completion=UNSOLVED',
      expect.objectContaining({ signal: controller.signal }),
    )
  })
})
