import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import StatusBadge from '@/components/layout/StatusBadge.vue'

function badge(wrapper: ReturnType<typeof mount>) {
  return wrapper.get('[data-slot="badge"]')
}

describe('StatusBadge', () => {
  it('always renders a visible text label next to a decorative icon', () => {
    const wrapper = mount(StatusBadge, { props: { label: 'Approved', tone: 'ok' } })

    expect(wrapper.text()).toContain('Approved')
    const icon = wrapper.get('svg')
    expect(icon.attributes('aria-hidden')).toBe('true')
  })

  it('keeps legacy tone names working', () => {
    const cases = [
      ['ok', '--status-success-ink'],
      ['look', '--status-attention-ink'],
      ['blocked', '--status-restricted-ink'],
      ['broken', '--status-error-ink'],
      ['unknown', '--status-neutral-ink'],
      ['off', '--status-neutral-ink'],
      ['running', '--status-active-ink'],
      ['gone', '--status-muted-ink'],
    ] as const

    for (const [tone, token] of cases) {
      const wrapper = mount(StatusBadge, { props: { label: 'State', tone } })
      expect(badge(wrapper).attributes('style'), tone).toContain(`var(${token})`)
    }
  })

  it('defaults to the neutral palette', () => {
    const wrapper = mount(StatusBadge, { props: { label: 'Unknown' } })

    expect(badge(wrapper).attributes('style')).toContain('var(--status-neutral-ink)')
  })

  it('derives label and tone from a status when given', () => {
    const wrapper = mount(StatusBadge, { props: { status: 'PENDING_REVIEW' } })

    expect(wrapper.text()).toContain('Needs review')
    expect(badge(wrapper).attributes('style')).toContain('var(--status-attention-ink)')
  })

  it('renders expired and canceled as muted, not active', () => {
    for (const status of ['EXPIRED', 'CANCELED']) {
      const wrapper = mount(StatusBadge, { props: { status } })
      const style = badge(wrapper).attributes('style')
      expect(style, status).toContain('var(--status-muted-ink)')
      expect(style, status).not.toContain('var(--status-active-ink)')
    }
  })

  it('does not animate or encode meaning only by colour', () => {
    const wrapper = mount(StatusBadge, { props: { status: 'RUNNING' } })
    const html = wrapper.html()

    expect(html).not.toContain('animate-pulse')
    expect(html).not.toContain('animation')
    // The label is real text, so the colour is decorative rather than the only cue.
    expect(wrapper.text()).toContain('Running')
  })

  it('uses the tokenised surface and border, not raw colours', () => {
    const wrapper = mount(StatusBadge, { props: { status: 'ACCEPTED' } })
    const style = badge(wrapper).attributes('style') ?? ''

    expect(style).toContain('var(--status-success-ink)')
    expect(style).toContain('var(--status-success-surface)')
    expect(style).toContain('var(--status-success-border)')
    expect(style).not.toMatch(/#[0-9a-f]{3,8}/i)
  })
})
