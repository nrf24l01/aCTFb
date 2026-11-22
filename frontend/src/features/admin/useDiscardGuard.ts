import { ref } from 'vue'

/**
 * Guards an editor dialog against accidental dismissal. Every close path
 * (Cancel, the close button, Escape, click outside) goes through
 * `attemptClose`, which only asks for confirmation when the form actually
 * changed. Nothing is ever discarded without an explicit choice.
 *
 * The snapshot is a JSON string of the caller's form state. It intentionally
 * includes secret inputs so a typed secret also counts as unsaved work; the
 * snapshot is never persisted or logged.
 */
export function useDiscardGuard<T>(options: {
  snapshot: () => T
  close: () => void
}) {
  const baseline = ref('')
  const discardOpen = ref(false)

  function reset() {
    baseline.value = JSON.stringify(options.snapshot())
    discardOpen.value = false
  }

  function isDirty(): boolean {
    return JSON.stringify(options.snapshot()) !== baseline.value
  }

  function attemptClose() {
    if (!isDirty()) {
      options.close()
      return
    }
    discardOpen.value = true
  }

  function keepEditing() {
    discardOpen.value = false
  }

  function discardAndClose() {
    discardOpen.value = false
    options.close()
  }

  return { discardOpen, reset, isDirty, attemptClose, keepEditing, discardAndClose }
}
