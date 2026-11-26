import { describe, expect, it } from 'vitest'
import type { JSONContent } from '@tiptap/core'
import { generateJSON } from '@tiptap/core'
import {
  markdownExtensions,
  markdownSemanticsEqual,
  parseMarkdown,
  serializeMarkdown,
} from './markdownDocument'
import { renderMarkdown } from './markdown'

/**
 * Every construct the approved spec promises to preserve through
 * open → edit → save. Anything not listed here is protected in Source mode by
 * `inspectMarkdownSupport`, so this corpus is the positive half of the contract.
 */
const CORPUS = [
  '# H1\n\n###### H6',
  'Some **bold**, *italic*, ~~strike~~ and `inline code`.',
  '- one\n- two\n  - nested\n\n1. first\n2. second',
  '> quoted\n>\n> > nested quote',
  '```python\ndef f():\n  return 1\n```',
  '```brainfuck\n+++[>+<-]\n```',
  '[link](/boards "title") and ![img](/x.png "t")',
  '| a | b |\n| :- | -: |\n| 1 | 2 |',
  '3. starts at three\n4. then four',
  '---',
  'first line  \nsecond line',
  'literal < and > and & signs',
  '```brainfuck title=extra\n+++\n```',
  '> - quoted list\n> - second',
  '# Heading with **bold** and `code`',
]

const roundTrip = (source: string): string => serializeMarkdown(parseMarkdown(source))

const firstNode = (source: string): JSONContent => parseMarkdown(source).content?.[0] ?? {}

describe('markdown round-trip', () => {
  it.each(CORPUS)('preserves the editor document for %s', (source) => {
    expect(markdownSemanticsEqual(source, roundTrip(source))).toBe(true)
  })

  /**
   * The reader path is what users actually see, so the round-trip must not
   * change it. Comparing the rendered HTML of both sides catches loss that a
   * parse/parse comparison of the same parser cannot: both sides would be
   * equally wrong.
   */
  it.each(CORPUS)('renders the same HTML before and after for %s', (source) => {
    expect(renderMarkdown(roundTrip(source))).toBe(renderMarkdown(source))
  })

  it('is idempotent after one serialisation', () => {
    const once = roundTrip(CORPUS.join('\n\n'))
    expect(roundTrip(once)).toBe(once)
  })

  it('keeps an unknown fence language instead of dropping it', () => {
    expect(roundTrip('```brainfuck\n+++\n```')).toContain('```brainfuck')
  })
})

/**
 * Explicit expected structures. These are written by hand from the approved
 * subset, so they fail if the schema silently degrades a construct — which the
 * same-parser comparison above cannot detect on its own.
 */
describe('explicit document structures', () => {
  it('keeps all six heading levels', () => {
    for (const level of [1, 2, 3, 4, 5, 6]) {
      const node = firstNode(`${'#'.repeat(level)} heading ${level}`)
      expect(node.type).toBe('heading')
      expect(node.attrs?.level).toBe(level)
    }
  })

  it('represents a hard break as a hardBreak node, not a space', () => {
    expect(firstNode('first line  \nsecond line').content).toEqual([
      { type: 'text', text: 'first line' },
      { type: 'hardBreak' },
      { type: 'text', text: 'second line' },
    ])
  })

  it('keeps the link title and href', () => {
    const paragraph = firstNode('[link](/boards "Board list")')
    // Only what Markdown can express: `target`/`rel` come from the extension's
    // render options and must never be baked into the stored string.
    expect(paragraph.content?.[0].marks).toEqual([
      { type: 'link', attrs: { href: '/boards', title: 'Board list' } },
    ])
  })

  it('keeps the image alt, title and source', () => {
    expect(firstNode('![alt text](/x.png "the title")')).toEqual({
      type: 'image',
      attrs: { src: '/x.png', alt: 'alt text', title: 'the title' },
    })
  })

  it('keeps GFM table alignment per column', () => {
    const table = firstNode('| a | b | c |\n| :- | :-: | -: |\n| 1 | 2 | 3 |')
    expect(table.type).toBe('table')
    const [header, row] = table.content ?? []
    expect(header.content?.map((cell) => cell.attrs?.align)).toEqual(['left', 'center', 'right'])
    expect(row.content?.map((cell) => cell.attrs?.align)).toEqual(['left', 'center', 'right'])
  })

  it('keeps the ordered list start number', () => {
    expect(firstNode('3. starts at three\n4. then four').attrs?.start).toBe(3)
  })

  it('keeps a fenced block language, including an unknown one with extra info', () => {
    expect(firstNode('```python\ndef f():\n  return 1\n```').attrs?.language).toBe('python')
    expect(firstNode('```brainfuck title=extra\n+++\n```').attrs?.language)
      .toBe('brainfuck title=extra')
  })

  it('keeps nested lists and nested quotes', () => {
    const list = firstNode('- one\n- two\n  - nested')
    expect(list.type).toBe('bulletList')
    const nested = list.content?.[1].content?.[1]
    expect(nested?.type).toBe('bulletList')

    const quote = firstNode('> quoted\n>\n> > nested quote')
    expect(quote.type).toBe('blockquote')
    expect(quote.content?.[1].type).toBe('blockquote')
  })

  it('keeps inline marks on their exact text', () => {
    const paragraph = firstNode('a **b** *c* ~~d~~ `e`')
    const marked = (paragraph.content ?? []).map((node) => ({
      text: node.text,
      marks: node.marks?.map((mark) => mark.type) ?? [],
    }))
    expect(marked).toEqual([
      { text: 'a ', marks: [] },
      { text: 'b', marks: ['bold'] },
      { text: ' ', marks: [] },
      { text: 'c', marks: ['italic'] },
      { text: ' ', marks: [] },
      { text: 'd', marks: ['strike'] },
      { text: ' ', marks: [] },
      { text: 'e', marks: ['code'] },
    ])
  })
})

/**
 * Reader-path assertions for the constructs named in the review ruling:
 * hard break, link title, table alignment. They are checked on rendered HTML
 * because a document that keeps a node but renders nothing is still a loss.
 */
describe('rendered semantics', () => {
  it('renders a hard break as <br>', () => {
    expect(renderMarkdown(roundTrip('first line  \nsecond line'))).toContain('<br>')
  })

  it('renders the link title', () => {
    expect(renderMarkdown(roundTrip('[link](/boards "Board list")')))
      .toContain('title="Board list"')
  })

  it('renders the image alt and title', () => {
    const html = renderMarkdown(roundTrip('![alt text](/x.png "the title")'))
    expect(html).toContain('alt="alt text"')
    expect(html).toContain('title="the title"')
  })

  it('renders literal angle brackets as text instead of dropping them', () => {
    const html = renderMarkdown(roundTrip('literal < and > and & signs'))
    expect(html).toContain('&lt;')
    expect(html).toContain('&gt;')
    expect(html).toContain('&amp;')
    expect(html).not.toContain('<script')
  })
})

describe('empty and invalid input', () => {
  it('parses an empty source as an empty paragraph', () => {
    expect(parseMarkdown('')).toEqual({ type: 'doc', content: [{ type: 'paragraph' }] })
    expect(parseMarkdown('   \n  ')).toEqual({ type: 'doc', content: [{ type: 'paragraph' }] })
  })

  it('serialises an empty document to an empty string', () => {
    expect(serializeMarkdown({ type: 'doc', content: [{ type: 'paragraph' }] })).toBe('')
  })

  it('treats two spellings of the same document as equal', () => {
    expect(markdownSemanticsEqual('__bold__', '**bold**')).toBe(true)
    expect(markdownSemanticsEqual('* item', '- item')).toBe(true)
    expect(markdownSemanticsEqual('**bold**', 'plain')).toBe(false)
  })
})

/**
 * Pasted HTML goes through the schema, never through `v-html`. These tests use
 * `generateJSON`, which is exactly the ProseMirror parse the editor performs on
 * paste, so the assertions cover the real path and not a stand-in.
 */
describe('pasted HTML filtering', () => {
  const paste = (html: string): JSONContent => generateJSON(html, markdownExtensions)

  it('keeps supported marks but drops style, class and event handlers', () => {
    const doc = paste('<b style="color:red" onclick="alert(1)" class="x">bold</b>')
    expect(doc).toEqual({
      type: 'doc',
      content: [{
        type: 'paragraph',
        content: [{ type: 'text', marks: [{ type: 'bold' }], text: 'bold' }],
      }],
    })
    expect(serializeMarkdown(doc)).toBe('**bold**')
    expect(JSON.stringify(doc)).not.toMatch(/style|class|onclick/i)
  })

  it('drops marks outside the supported subset', () => {
    const doc = paste('<p><span style="color:red">red</span> <em>em</em> <u>under</u> <s>strike</s></p>')
    expect(serializeMarkdown(doc)).toBe('red *em* under ~~strike~~')
  })

  it('drops a link whose href is a javascript: URL', () => {
    const doc = paste('<a href="javascript:alert(1)">click</a>')
    expect(serializeMarkdown(doc)).toBe('click')
    expect(JSON.stringify(doc)).not.toContain('javascript')
  })

  it('drops a link with an entity-encoded or control-character scheme', () => {
    expect(serializeMarkdown(paste('<a href="java&#9;script:alert(1)">click</a>'))).toBe('click')
    expect(serializeMarkdown(paste('<a href="JaVaScRiPt:alert(1)">click</a>'))).toBe('click')
    expect(serializeMarkdown(paste('<a href="vbscript:msgbox(1)">click</a>'))).toBe('click')
  })

  it('keeps a safe link', () => {
    expect(serializeMarkdown(paste('<a href="https://example.com">ok</a>')))
      .toBe('[ok](https://example.com)')
  })

  it('drops data: and javascript: images', () => {
    expect(serializeMarkdown(paste('<img src="data:image/svg+xml;base64,PHN2Zz4=" alt="x">'))).toBe('')
    expect(serializeMarkdown(paste('<img src="javascript:alert(1)" alt="x">'))).toBe('')
    expect(serializeMarkdown(paste('<img src="DATA:text/html;base64,PHNjcmlwdD4=" alt="x">'))).toBe('')
  })

  it('keeps a safe image', () => {
    expect(serializeMarkdown(paste('<img src="/ok.png" alt="ok">'))).toBe('![ok](/ok.png)')
  })

  it('drops script content instead of importing it', () => {
    const doc = paste('<script>alert(1)</script>')
    expect(JSON.stringify(doc)).not.toContain('alert')
    expect(serializeMarkdown(doc)).toBe('')
  })
})
