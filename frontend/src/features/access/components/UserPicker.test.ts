import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import UserPicker from './UserPicker.vue'
import type { UserDirectoryEntry } from '@/shared/types/api'

const alice: UserDirectoryEntry = { uuid: '11111111-1111-4111-8111-111111111111', username: 'alice', email: 'alice@example.test' }
const bob: UserDirectoryEntry = { uuid: '22222222-2222-4222-8222-222222222222', username: 'bob', email: 'bob@example.test' }

describe('UserPicker', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('debounces the server search by 250ms', async () => {
    const search = vi.fn().mockResolvedValue([alice, bob])
    const wrapper = mount(UserPicker, { props: { search } })

    await wrapper.get('input').setValue('al')
    expect(search).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(249)
    expect(search).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(search).toHaveBeenCalledTimes(1)
    expect(search).toHaveBeenCalledWith('al', expect.any(AbortSignal))
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('bob')
  })

  it('selects with keyboard and returns focus to the input', async () => {
    const search = vi.fn().mockResolvedValue([alice, bob])
    const wrapper = mount(UserPicker, { props: { search }, attachTo: document.body })

    const input = wrapper.get('input')
    await input.setValue('a')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    const emitted = wrapper.emitted('select')
    expect(emitted).toBeTruthy()
    expect((emitted?.[0]?.[0] as UserDirectoryEntry).username).toBe('bob')
    expect(document.activeElement).toBe(input.element)
    wrapper.unmount()
  })

  it('cancels a stale response when the query changes', async () => {
    let resolveFirst: (value: UserDirectoryEntry[]) => void = () => undefined
    const first = new Promise<UserDirectoryEntry[]>((resolve) => {
      resolveFirst = resolve
    })
    const search = vi.fn().mockImplementation((query: string) => {
      if (query === 'first') return first
      return Promise.resolve([bob])
    })
    const wrapper = mount(UserPicker, { props: { search } })

    await wrapper.get('input').setValue('first')
    await vi.advanceTimersByTimeAsync(250)
    await wrapper.get('input').setValue('second')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    resolveFirst([alice])
    await flushPromises()

    expect(wrapper.text()).toContain('bob')
    expect(wrapper.text()).not.toContain('alice')
  })

  it('marks already-added users as disabled', async () => {
    const search = vi.fn().mockResolvedValue([alice, bob])
    const wrapper = mount(UserPicker, { props: { search, exclude: [alice.uuid] } })

    await wrapper.get('input').setValue('a')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    const options = wrapper.findAll('[role="option"]')
    expect(options).toHaveLength(2)
    expect(options[0].attributes('aria-disabled')).toBe('true')
    await options[0].trigger('click')
    expect(wrapper.emitted('select')).toBeFalsy()
  })
})
