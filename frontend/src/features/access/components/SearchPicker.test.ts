import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SearchPicker from './SearchPicker.vue'
import TeamPicker from './TeamPicker.vue'
import type { TeamDirectoryEntry } from '@/shared/types/api'

const options = [
  { uuid: '11111111-1111-4111-8111-111111111111', primary: 'alpha', secondary: 'alpha@example.test', hint: '11111111' },
  { uuid: '22222222-2222-4222-8222-222222222222', primary: 'beta', hint: '22222222' },
]

describe('SearchPicker', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('debounces and renders primary/secondary/hint', async () => {
    const search = vi.fn().mockResolvedValue(options)
    const wrapper = mount(SearchPicker, { props: { search } })
    await wrapper.get('input').setValue('al')
    expect(search).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(search).toHaveBeenCalledWith('al', expect.any(AbortSignal))
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('alpha@example.test')
    expect(wrapper.text()).toContain('11111111')
  })

  it('cancels a stale query', async () => {
    let resolveFirst: (value: typeof options) => void = () => undefined
    const first = new Promise<typeof options>((resolve) => { resolveFirst = resolve })
    const search = vi.fn().mockImplementation((query: string) =>
      query === 'first' ? first : Promise.resolve([options[1]]),
    )
    const wrapper = mount(SearchPicker, { props: { search } })
    await wrapper.get('input').setValue('first')
    await vi.advanceTimersByTimeAsync(250)
    await wrapper.get('input').setValue('second')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    resolveFirst(options)
    await flushPromises()
    expect(wrapper.text()).toContain('beta')
    expect(wrapper.text()).not.toContain('alpha')
  })

  it('shows an error with retry', async () => {
    const search = vi.fn().mockRejectedValue(new Error('boom'))
    const wrapper = mount(SearchPicker, { props: { search } })
    await wrapper.get('input').setValue('x')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(wrapper.text()).toContain('boom')
    expect(wrapper.text()).toContain('Retry')
  })
})

describe('TeamPicker', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('maps team name and uuid without any user identity', async () => {
    const teams: TeamDirectoryEntry[] = [{ uuid: '33333333-3333-4333-8333-333333333333', name: 'Red Team' }]
    const search = vi.fn().mockResolvedValue(teams)
    const wrapper = mount(TeamPicker, { props: { search } })
    await wrapper.get('input').setValue('red')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(wrapper.text()).toContain('Red Team')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')?.[0]?.[0]).toEqual(teams[0])
  })
})
