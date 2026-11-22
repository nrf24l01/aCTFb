import { describe, expect, it, vi } from 'vitest'
import { useDiscardGuard } from './useDiscardGuard'

function harness(initial: Record<string, unknown>) {
  let form = { ...initial }
  const close = vi.fn()
  const guard = useDiscardGuard({ snapshot: () => form, close })
  guard.reset()
  return { guard, close, set: (next: Record<string, unknown>) => { form = next } }
}

describe('useDiscardGuard', () => {
  it('closes immediately when nothing changed', () => {
    const { guard, close } = harness({ title: 'a', answers: '' })
    guard.attemptClose()
    expect(close).toHaveBeenCalledTimes(1)
    expect(guard.discardOpen.value).toBe(false)
  })

  it('asks for confirmation instead of closing a dirty form', () => {
    const { guard, close, set } = harness({ title: 'a', answers: '' })
    set({ title: 'b', answers: '' })
    guard.attemptClose()
    expect(close).not.toHaveBeenCalled()
    expect(guard.discardOpen.value).toBe(true)
  })

  it('keeps editing without closing', () => {
    const { guard, close, set } = harness({ title: 'a' })
    set({ title: 'b' })
    guard.attemptClose()
    guard.keepEditing()
    expect(guard.discardOpen.value).toBe(false)
    expect(close).not.toHaveBeenCalled()
  })

  it('discards only after the explicit choice, including typed secrets', () => {
    const { guard, close, set } = harness({ title: 'a', secretParameters: {} })
    set({ title: 'a', secretParameters: { flag: 's3cret' } })
    guard.attemptClose()
    expect(guard.discardOpen.value).toBe(true)
    guard.discardAndClose()
    expect(guard.discardOpen.value).toBe(false)
    expect(close).toHaveBeenCalledTimes(1)
  })
})
