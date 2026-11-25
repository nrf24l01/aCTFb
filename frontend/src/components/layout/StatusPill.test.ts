import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import StatusPill from '@/components/layout/StatusPill.vue'

function styleFor(status: string): string {
  const wrapper = mount(StatusPill, { props: { status } })
  return wrapper.get('[data-slot="badge"]').attributes('style') ?? ''
}

describe('StatusPill', () => {
  it('renders a friendly label for every status', () => {
    expect(mount(StatusPill, { props: { status: 'ACCEPTED' } }).text()).toContain('Accepted')
    expect(mount(StatusPill, { props: { status: 'PENDING_REVIEW' } }).text()).toContain('Needs review')
    expect(mount(StatusPill, { props: { status: 'DRAFT' } }).text()).toContain('Draft')
  })

  it('uses the semantic palette for the core lifecycle states', () => {
    expect(styleFor('ACCEPTED')).toContain('var(--status-success-ink)')
    expect(styleFor('ACTIVE')).toContain('var(--status-active-ink)')
    expect(styleFor('PENDING_REVIEW')).toContain('var(--status-attention-ink)')
    expect(styleFor('REJECTED')).toContain('var(--status-error-ink)')
    expect(styleFor('LOCKED')).toContain('var(--status-restricted-ink)')
    expect(styleFor('DRAFT')).toContain('var(--status-neutral-ink)')
    expect(styleFor('FINISHED')).toContain('var(--status-muted-ink)')
  })

  it('treats expired, canceled, and finished as finished rather than running', () => {
    for (const status of ['EXPIRED', 'CANCELED', 'FINISHED']) {
      const style = styleFor(status)
      expect(style, status).toContain('var(--status-muted-ink)')
      expect(style, status).not.toContain('var(--status-active-ink)')
    }
  })

  it('keeps unknown statuses neutral and readable', () => {
    const wrapper = mount(StatusPill, { props: { status: 'PROVIDER_TIMEOUT' } })

    expect(wrapper.text()).toContain('Provider timeout')
    expect(wrapper.get('[data-slot="badge"]').attributes('style')).toContain('var(--status-neutral-ink)')
  })
})
