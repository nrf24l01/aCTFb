/**
 * Test-only fixture for the real Markdown editor. Not part of the app: no
 * router, no API, no state. It mounts the real component with the real app CSS
 * inside a real form, so Chromium can exercise typing, paste, the toolbar,
 * source mode and native form behaviour without a backend.
 *
 * `?content=` seeds the initial Markdown (URL-encoded).
 */
import '@/style.css'
import { renderMarkdown } from '@/shared/lib/markdown'
import { DESCRIPTION_MAX_CHARACTERS } from '@/shared/lib/text'

const initial = new URLSearchParams(location.search).get('content') ?? ''

async function start() {
  // Vue and the editor are imported dynamically so this fixture stays a
  // test-only module and never becomes a production route dependency.
  const { createApp, h, ref } = await import('vue')
  const { default: MarkdownEditor } = await import('@/shared/components/MarkdownEditor.vue')

  const value = ref(initial)
  const submits = ref(0)

  const App = {
    render() {
      return h('div', { class: 'p-4' }, [
        h('form', {
          id: 'fixture-form',
          onSubmit: (event: Event) => {
            event.preventDefault()
            submits.value += 1
          },
        }, [
          h(MarkdownEditor, {
            modelValue: value.value,
            'onUpdate:modelValue': (next: string) => {
              value.value = next
            },
            label: 'Fixture content',
            id: 'fixture-editor',
            maxLength: DESCRIPTION_MAX_CHARACTERS,
          }),
          h('button', { type: 'submit', id: 'fixture-submit', class: 'mt-2 border border-border px-2 py-1 text-sm' }, 'Save'),
        ]),
        h('output', { id: 'fixture-value', class: 'hidden' }, value.value),
        h('output', { id: 'fixture-submits', class: 'hidden' }, String(submits.value)),
        h('div', { id: 'fixture-reader', class: 'markdown-body mt-4', innerHTML: renderMarkdown(value.value) }),
      ])
    },
  }

  createApp(App).mount('#fixture')

  ;(window as unknown as { __markdownFixture: unknown }).__markdownFixture = {
    renderMarkdown,
    getValue: () => value.value,
    getSubmits: () => submits.value,
  }
}

void start()
