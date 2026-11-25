/**
 * Test-only visual fixture. Not part of the app: no router, no API, no state.
 * It mounts the real StatusBadge/StatusPill components with the real app CSS so
 * the browser can measure computed colours and contrast.
 */
import { createApp, h } from 'vue'
import '@/style.css'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import StatusPill from '@/components/layout/StatusPill.vue'

interface Row {
  tone: string
  statuses: string[]
}

const rows: Row[] = [
  { tone: 'success', statuses: ['ACCEPTED', 'SOLVED', 'PASSED'] },
  { tone: 'active', statuses: ['ACTIVE', 'RUNNING', 'CHECKING'] },
  { tone: 'attention', statuses: ['PENDING', 'PENDING_REVIEW', 'SCHEDULED'] },
  { tone: 'error', statuses: ['REJECTED', 'ERROR', 'FAILED'] },
  { tone: 'restricted', statuses: ['LOCKED', 'RESTRICTED'] },
  { tone: 'neutral', statuses: ['DRAFT', 'PROVIDER_TIMEOUT'] },
  { tone: 'muted', statuses: ['FINISHED', 'EXPIRED', 'CANCELED'] },
]

const legacy = [
  { label: 'ok', tone: 'ok' },
  { label: 'look', tone: 'look' },
  { label: 'blocked', tone: 'blocked' },
  { label: 'broken', tone: 'broken' },
  { label: 'unknown', tone: 'unknown' },
  { label: 'off', tone: 'off' },
  { label: 'running', tone: 'running' },
  { label: 'gone', tone: 'gone' },
] as const

function row(tone: string, children: ReturnType<typeof h>[]) {
  return h('div', { class: 'flex flex-wrap items-center gap-2', 'data-tone': tone }, children)
}

function badges(statuses: string[]) {
  return statuses.map((status) =>
    h('div', { 'data-tone': status, 'data-kind': 'status' }, [h(StatusPill, { status })]),
  )
}

const App = {
  render() {
    return h('div', { class: 'p-4 space-y-4' }, [
      h('h1', { class: 'text-sm font-semibold', 'data-surface': 'black' }, 'Status colours (visual fixture)'),
      // Black page background.
      h('section', { class: 'space-y-2', 'data-surface': 'black' }, rows.map((r) => row(r.tone, badges(r.statuses)))),
      // Card/secondary surface (#191724).
      h(
        'section',
        { class: 'space-y-2 bg-secondary p-3', 'data-surface': 'card' },
        rows.map((r) => row(r.tone, badges(r.statuses))),
      ),
      // Table-like surface.
      h('section', { class: 'bg-card p-3', 'data-surface': 'table' }, [
        h('table', { class: 'w-full text-sm' }, [
          h(
            'tbody',
            {},
            rows.map((r) =>
              h('tr', { class: 'border-b border-border' }, [
                h('td', { class: 'py-1 pr-3 text-muted-foreground' }, r.tone),
                h('td', { class: 'py-1' }, row(r.tone, badges(r.statuses))),
              ]),
            ),
          ),
        ]),
      ]),
      // Legacy tone props must keep working.
      h('section', { class: 'space-y-2', 'data-surface': 'black' }, [
        h(
          'div',
          { class: 'flex flex-wrap items-center gap-2', 'data-kind': 'legacy' },
          legacy.map(({ label, tone }) => h('div', { 'data-tone': tone }, [h(StatusBadge, { label, tone })])),
        ),
      ]),
    ])
  },
}

createApp(App).mount('#fixture')
