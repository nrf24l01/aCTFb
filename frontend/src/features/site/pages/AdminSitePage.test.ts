import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { SITE_INTRO_MAX_CHARACTERS } from '@/shared/lib/text'
import AdminSitePage from './AdminSitePage.vue'

const adminGet = vi.fn()
const update = vi.fn()

vi.mock('@/features/site/api', () => ({
  siteApi: {
    adminGet: (...args: unknown[]) => adminGet(...args),
    update: (...args: unknown[]) => update(...args),
  },
}))

vi.mock('@/features/auth/session', () => ({
  useSession: () => ({ state: { status: 'authenticated', user: { roles: ['admin'] } } }),
}))

async function waitForEditor() {
  await vi.waitFor(() => {
    expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
  }, { timeout: 10_000, interval: 20 })
}

beforeEach(() => {
  adminGet.mockReset()
  update.mockReset()
  adminGet.mockResolvedValue({ title: 'Site', introMarkdown: '**bold**', published: true })
  update.mockResolvedValue({ title: 'Site', introMarkdown: '**bold**', published: true })
})

describe('AdminSitePage Markdown authoring', () => {
  it('edits the introduction with the rich editor and keeps Markdown on save', async () => {
    const wrapper = mount(AdminSitePage, { attachTo: document.body })
    await waitForEditor()

    expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
    expect(document.querySelector('#site-intro')).not.toBeNull()

    await (document.querySelector('[aria-label="Source mode"]') as HTMLButtonElement).click()
    await flushPromises()
    expect((document.querySelector('textarea') as HTMLTextAreaElement).value).toBe('**bold**')

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(update).toHaveBeenCalledTimes(1)
    const [body] = update.mock.calls[0] as [{ introMarkdown: string }]
    expect(body.introMarkdown).toBe('**bold**')
    wrapper.unmount()
  })

  it('blocks save when the introduction exceeds the server limit', async () => {
    adminGet.mockResolvedValue({
      title: 'Site',
      introMarkdown: 'x'.repeat(SITE_INTRO_MAX_CHARACTERS + 1),
      published: true,
    })
    const wrapper = mount(AdminSitePage, { attachTo: document.body })
    await waitForEditor()

    document.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()

    expect(update).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('at most 131,072 characters')
    wrapper.unmount()
  })
})
