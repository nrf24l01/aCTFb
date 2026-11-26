import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { Editor, EditorContent } from '@tiptap/vue-3'
import { lowlight } from '@/shared/lib/highlight'
import { createMarkdownExtensions } from '@/shared/lib/markdownDocument'
import MarkdownEditor from './MarkdownEditor.vue'

const mountEditor = (modelValue: string, maxLength?: number) =>
  mount(MarkdownEditor, {
    props: { modelValue, label: 'Task content', maxLength },
    attachTo: document.body,
  })

const ariaLabels = (): string[] =>
  Array.from(document.querySelectorAll('[aria-label]')).map((element) => element.getAttribute('aria-label') ?? '')

describe('toolbar', () => {
  it('exposes a named toolbar and a source toggle', async () => {
    const wrapper = mountEditor('**bold**')
    await flushPromises()

    expect(ariaLabels()).toEqual(
      expect.arrayContaining(['Bold', 'Italic', 'Bullet list', 'Code block', 'Source mode']),
    )
    wrapper.unmount()
  })

  it('reflects the active mark in aria-pressed instead of a stale value', async () => {
    // The caret starts in the leading plain paragraph, outside any mark.
    const wrapper = mountEditor('plain **bold**')
    await flushPromises()

    const bold = (): HTMLButtonElement =>
      document.querySelector<HTMLButtonElement>('[aria-label="Bold"]')!

    expect(bold().getAttribute('aria-pressed')).toBe('false')

    bold().click()
    await flushPromises()
    expect(bold().getAttribute('aria-pressed')).toBe('true')

    bold().click()
    await flushPromises()
    expect(bold().getAttribute('aria-pressed')).toBe('false')

    wrapper.unmount()
  })

  it('does not render the code-language picker outside a code block', async () => {
    const wrapper = mountEditor('plain text\n\n```python\nx = 1\n```')
    await flushPromises()

    expect(ariaLabels()).not.toContain('Code language')
    wrapper.unmount()
  })

  it('highlights a known fence language with the shared registry', async () => {
    const wrapper = mountEditor('```python\ndef f():\n  return 1\n```')
    await flushPromises()

    expect(wrapper.html()).toContain('hljs-keyword')
    wrapper.unmount()
  })

  it('never auto-detects an unknown fence language in the editor', async () => {
    const spy = vi.spyOn(lowlight, 'highlightAuto')
    const wrapper = mountEditor('```brainfuck\n+++[>+<-]\n```')
    await flushPromises()

    expect(spy).not.toHaveBeenCalled()
    expect(wrapper.html()).not.toContain('hljs-')
    wrapper.unmount()
  })

  it('shows an unknown fence language readably without rewriting the document', async () => {
    const wrapper = mountEditor('```brainfuck title=x\n+++\n```')
    await flushPromises()

    expect(ariaLabels()).toContain('Code language')
    expect(document.body.textContent).toContain('Unknown (brainfuck title=x)')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })
})

describe('source mode lifecycle', () => {
  it('keeps exact bytes when switching to source and back without editing', async () => {
    const source = '__bold__\n\n* item'
    const wrapper = mountEditor(source)
    await flushPromises()

    await wrapper.get('[aria-label="Source mode"]').trigger('click')
    const textarea = wrapper.get('textarea')
    expect((textarea.element as HTMLTextAreaElement).value).toBe(source)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    await wrapper.get('[aria-label="Visual mode"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.ProseMirror').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })

  it('does not emit on mount, focus or selection', async () => {
    const wrapper = mountEditor('# Title\n\nSome **bold** text.')
    await flushPromises()

    const surface = wrapper.get('.ProseMirror')
    await surface.trigger('focus')
    await surface.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })

  it('emits the edited source and does not reset the caret when the parent echoes it', async () => {
    const wrapper = mountEditor('original')
    await flushPromises()
    await wrapper.get('[aria-label="Source mode"]').trigger('click')

    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement
    textarea.value = 'edited source'
    textarea.setSelectionRange(4, 4)
    await wrapper.get('textarea').trigger('input')

    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['edited source'])

    // The parent echoes the value back through v-model.
    await wrapper.setProps({ modelValue: 'edited source' })
    await flushPromises()

    expect(textarea.value).toBe('edited source')
    expect(textarea.selectionStart).toBe(4)
    expect(textarea.selectionEnd).toBe(4)
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)

    wrapper.unmount()
  })
})

describe('visual mode', () => {
  const editorOf = (wrapper: ReturnType<typeof mountEditor>): Editor =>
    wrapper.findComponent(EditorContent).props('editor') as Editor

  it('emits Markdown, not HTML, after a real edit', async () => {
    const wrapper = mountEditor('plain')
    await flushPromises()

    editorOf(wrapper).chain().focus('end').insertContent(' tail').run()
    await flushPromises()

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as string
    expect(emitted).toBe('plain tail')
    expect(emitted).not.toContain('<p>')

    wrapper.unmount()
  })

  it('applies an external model change without emitting', async () => {
    const wrapper = mountEditor('one')
    await flushPromises()

    await wrapper.setProps({ modelValue: 'two' })
    await flushPromises()

    expect(wrapper.text()).toContain('two')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })

  it('switches to source when the parent supplies unsupported content', async () => {
    const wrapper = mountEditor('one')
    await flushPromises()

    await wrapper.setProps({ modelValue: '<div>x</div>' })
    await flushPromises()

    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect(document.body.textContent).toMatch(/raw HTML/i)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })
})

describe('protected content', () => {
  it('opens unsupported content in source with a warning and no visual editor', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    expect(document.body.textContent).toContain('Source')
    expect(document.body.textContent).toMatch(/raw HTML/i)
    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('<div>legacy</div>')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })

  it('names every unsupported reason', async () => {
    const wrapper = mountEditor('<div>x</div>\n\n- [ ] todo\n\n[x](javascript:alert(1))')
    await flushPromises()

    const text = document.body.textContent ?? ''
    expect(text).toMatch(/raw HTML/i)
    expect(text).toMatch(/task list/i)
    expect(text).toMatch(/unsafe link or image URLs/i)

    wrapper.unmount()
  })

  it('converts to visual only after an explicit confirmation', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    const anyway = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('Edit visually anyway'))
    expect(anyway).toBeTruthy()
    anyway!.click()
    await flushPromises()

    // The dialog asks first; the visual editor must not appear yet.
    expect(document.body.textContent).toMatch(/Edit visually anyway\?/i)
    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })

  it('keeps source edits when the conversion is cancelled', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement
    textarea.value = '<div>legacy</div>\n\nmore'
    await wrapper.get('textarea').trigger('input')
    await flushPromises()

    // Attempting to switch asks for confirmation; cancelling keeps the draft.
    await wrapper.get('[aria-label="Visual mode"]').trigger('click')
    await flushPromises()

    expect(document.body.textContent).toMatch(/Edit visually anyway\?/i)
    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value)
      .toBe('<div>legacy</div>\n\nmore')

    const cancel = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Cancel')
    cancel!.click()
    await flushPromises()

    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value)
      .toBe('<div>legacy</div>\n\nmore')

    wrapper.unmount()
  })

  it('allows visual editing after the confirmation', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    const anyway = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('Edit visually anyway'))
    anyway!.click()
    await flushPromises()

    const confirm = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Edit visually')
    confirm!.click()
    await flushPromises()

    expect(wrapper.find('.ProseMirror').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })
})

describe('length validation', () => {
  it('shows the code-point counter and blocks over-limit content', async () => {
    const wrapper = mountEditor('🎯'.repeat(3), 2)
    await flushPromises()

    expect(document.body.textContent).toContain('3 / 2')
    expect(document.body.textContent).toContain('at most 2 characters')

    wrapper.unmount()
  })

  it('shows no message at exactly the limit', async () => {
    const wrapper = mountEditor('🎯🎯', 2)
    await flushPromises()

    expect(document.body.textContent).toContain('2 / 2')
    expect(document.body.textContent).not.toContain('at most')

    wrapper.unmount()
  })
})

describe('link editing', () => {
  it('opens an inline URL row on Mod-K without invoking the browser default', async () => {
    const wrapper = mountEditor('some text')
    await flushPromises()

    const shell = wrapper.get('.markdown-editor-shell')
    const event = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, cancelable: true })
    shell.element.dispatchEvent(event)
    await flushPromises()

    expect(event.defaultPrevented).toBe(true)
    expect(document.querySelector('[aria-label="Link URL"]')).not.toBeNull()

    wrapper.unmount()
  })

  it('rejects an unsafe URL with an inline message and no dialog', async () => {
    const wrapper = mountEditor('some text')
    await flushPromises()

    const event = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, cancelable: true })
    wrapper.get('.markdown-editor-shell').element.dispatchEvent(event)
    await flushPromises()

    const input = document.querySelector<HTMLInputElement>('[aria-label="Link URL"]')!
    input.value = 'javascript:alert(1)'
    input.dispatchEvent(new Event('input'))
    await flushPromises()

    const apply = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Apply')!
    apply.click()
    await flushPromises()

    expect(document.body.textContent).toContain('Enter a safe URL')
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    wrapper.unmount()
  })
})

describe('teardown', () => {
  it('destroys the editor on unmount', async () => {
    const destroy = vi.spyOn(Editor.prototype, 'destroy')
    const wrapper = mountEditor('**bold**')
    await flushPromises()

    wrapper.unmount()
    expect(destroy).toHaveBeenCalled()
    expect(document.querySelector('.ProseMirror')).toBeNull()
  })
})

describe('source mode trust and notice lifecycle', () => {
  it('keeps visual edits when the parent withholds the echo', async () => {
    const wrapper = mountEditor('plain')
    await flushPromises()

    const editor = wrapper.findComponent(EditorContent).props('editor') as Editor
    editor.chain().focus('end').insertContent(' tail').run()
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['plain tail'])

    // A validating parent does not echo the new value back.
    await wrapper.get('[aria-label="Source mode"]').trigger('click')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('plain tail')

    wrapper.unmount()
  })

  it('shows the warning again when returning to source after an override', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('Edit visually anyway'))!
      .click()
    await flushPromises()
    Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Edit visually')!
      .click()
    await flushPromises()
    expect(wrapper.find('.ProseMirror').exists()).toBe(true)

    await wrapper.get('[aria-label="Source mode"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect(document.body.textContent).toMatch(/raw HTML/i)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('<div>legacy</div>')

    wrapper.unmount()
  })

  it('marks the lossy conversion as destructive', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('Edit visually anyway'))!
      .click()
    await flushPromises()

    const confirm = Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === 'Edit visually')!
    expect(confirm.getAttribute('data-variant')).toBe('destructive')

    wrapper.unmount()
  })
})

describe('default cancel', () => {
  /**
   * The confirm is the only path that can drop markup, so the *default* action
   * must be Cancel. `ConfirmDialog` renders Cancel first, as a ghost button, and
   * nothing autofocuses the destructive action; Escape and the close button both
   * map to cancel too. reka-ui's Escape handling needs a real focus trap, which
   * happy-dom does not provide, so the Escape path is verified in Chromium
   * (Task 3) and is not claimed here.
   */
  it('offers Cancel first and the destructive action second', async () => {
    const wrapper = mountEditor('<div>legacy</div>')
    await flushPromises()

    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement
    textarea.value = '<div>legacy</div>\n\nedited'
    await wrapper.get('textarea').trigger('input')
    await flushPromises()

    await wrapper.get('[aria-label="Visual mode"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toMatch(/Edit visually anyway\?/i)

    const dialog = document.querySelector('[role="dialog"]')!
    const actions = Array.from(dialog.querySelectorAll('button'))
      .filter((button) => button.textContent?.trim() === 'Cancel'
        || button.textContent?.trim() === 'Edit visually')

    expect(actions.map((button) => button.textContent?.trim())).toEqual(['Cancel', 'Edit visually'])
    expect((actions[0] as HTMLButtonElement).disabled).toBe(false)
    expect(actions[0].getAttribute('data-variant')).toBe('ghost')

    ;(actions[0] as HTMLButtonElement).click()
    await flushPromises()

    expect(wrapper.find('.ProseMirror').exists()).toBe(false)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value)
      .toBe('<div>legacy</div>\n\nedited')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['<div>legacy</div>\n\nedited'])

    wrapper.unmount()
  })
})

describe('concurrent editors', () => {
  it('keeps two mounted editors independent', async () => {
    const first = mountEditor('first **one**')
    const second = mountEditor('second *two*')
    await flushPromises()

    const firstEditor = first.findComponent(EditorContent).props('editor') as Editor
    const secondEditor = second.findComponent(EditorContent).props('editor') as Editor

    firstEditor.chain().focus('end').insertContent(' edited').run()
    await flushPromises()

    expect(first.emitted('update:modelValue')?.at(-1)?.[0]).toContain('edited')
    expect(second.emitted('update:modelValue')).toBeUndefined()
    expect(secondEditor.getMarkdown()).toBe('second *two*')

    first.unmount()
    second.unmount()
  })

  it('gives each editor its own extension instances', () => {
    const left = createMarkdownExtensions()
    const right = createMarkdownExtensions()
    expect(left).not.toBe(right)
    left.forEach((extension, index) => {
      expect(extension).not.toBe(right[index])
    })
  })
})

describe('blockquote shortcut', () => {
  it('binds Mod-Shift-. in addition to the upstream Mod-Shift-b', async () => {
    const wrapper = mountEditor('quoted text')
    await flushPromises()

    const surface = wrapper.get('.ProseMirror').element
    surface.dispatchEvent(new KeyboardEvent('keydown', {
      key: '.',
      code: 'Period',
      ctrlKey: true,
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    }))
    await flushPromises()

    const editor = wrapper.findComponent(EditorContent).props('editor') as Editor
    expect(editor.isActive('blockquote')).toBe(true)

    wrapper.unmount()
  })
})

describe('visual echo does not reset the document', () => {
  it('keeps the selection and emits nothing when the parent echoes the value', async () => {
    const wrapper = mountEditor('one two three')
    await flushPromises()

    const editor = wrapper.findComponent(EditorContent).props('editor') as Editor
    editor.chain().focus('end').insertContent(' four').run()
    await flushPromises()

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as string
    expect(emitted).toBe('one two three four')
    const before = { from: editor.state.selection.from, to: editor.state.selection.to }
    const html = editor.getHTML()

    await wrapper.setProps({ modelValue: emitted })
    await flushPromises()

    // A parent echo must not be treated as an external replacement.
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
    expect(editor.getHTML()).toBe(html)
    expect({ from: editor.state.selection.from, to: editor.state.selection.to }).toEqual(before)

    wrapper.unmount()
  })
})

describe('notice styling hooks', () => {
  /**
   * happy-dom cannot evaluate computed colour, so these assertions only pin the
   * class hooks that the Task 3 Chromium gate checks for real contrast. They
   * exist because the `text-status-*`/`bg-status-*`/`border-status-*` utilities
   * previously generated no CSS at all and the warning looked like body text.
   */
  it('uses the status tokens for the notice and the limit message', async () => {
    const wrapper = mountEditor('<div>legacy</div>', 2)
    await flushPromises()

    const notice = document.querySelector<HTMLElement>('[data-slot="markdown-editor-notice"]')!
    expect(notice.getAttribute('class') ?? '').toContain('text-status-attention-ink')
    expect(notice.getAttribute('class') ?? '').toContain('bg-status-attention-surface')
    expect(notice.getAttribute('class') ?? '').toContain('border-status-attention-border')

    const limit = wrapper.get('[role="alert"]')
    expect(limit.attributes('class')).toContain('text-status-error-ink')

    wrapper.unmount()
  })
})
