<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { Editor, EditorContent } from '@tiptap/vue-3'
import { Placeholder } from '@tiptap/extensions'
import { EyeIcon, PencilIcon } from '@lucide/vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { createMarkdownExtensions } from '@/shared/lib/markdownDocument'
import type { UnsupportedReason } from '@/shared/lib/markdownUnsupported'
import { inspectMarkdownSupport } from '@/shared/lib/markdownUnsupported'
import { isSafeUrl } from '@/shared/lib/markdown'
import { countCharacters, validateDescriptionLength } from '@/shared/lib/text'
import MarkdownEditorToolbar from './MarkdownEditorToolbar.vue'

/**
 * Markdown authoring control.
 *
 * Two modes share one `v-model` string:
 *
 * - **Visual** — Tiptap over the shared `markdownExtensions` schema. The
 *   document is created from the stored Markdown and the model is written back
 *   only when ProseMirror reports a real document change.
 * - **Source** — a plain textarea holding the exact bytes, used for content the
 *   visual schema cannot represent (raw HTML, task lists, unsafe URLs).
 *
 * Lifecycle contract: mounting, focusing, selecting, switching modes and
 * cancelling never emit. A stored description therefore stays byte-identical
 * until the user actually types, and an unsupported description is never
 * silently rewritten.
 */

const model = defineModel<string>({ default: '' })

const props = withDefaults(
  defineProps<{
    label: string
    id?: string
    maxLength?: number
    placeholder?: string
    /**
     * When the surrounding form owns the over-limit message (it must stay
     * visible on the Preview tab too), set this to `false` so the control does
     * not repeat the same sentence. The counter stays visible either way.
     */
    showLengthError?: boolean
    /** Mirrors the form's live validity on the editable surface. */
    invalid?: boolean
    /** Id of the form-owned error message, for `aria-describedby`. */
    describedBy?: string
  }>(),
  {
    id: undefined,
    maxLength: undefined,
    placeholder: undefined,
    showLengthError: true,
    invalid: false,
    describedBy: undefined,
  },
)

type Mode = 'visual' | 'source'

const mode = ref<Mode>('visual')
const report = ref(inspectMarkdownSupport(model.value))
/** Set only after the author confirms dropping unsupported markup. */
const visualOverride = ref(false)
const sourceDraft = ref(model.value)
const lastEmitted = ref(model.value)
const editor = shallowRef<Editor | null>(null)
/** Content handed to the next visual editor instance (source edits included). */
const pendingContent = ref<string | null>(null)
const textarea = ref<HTMLTextAreaElement | null>(null)
const linkInput = ref<ComponentPublicInstance | null>(null)

const confirmVisualOpen = ref(false)
const linkRowOpen = ref(false)
const linkDraft = ref('')
const linkError = ref<string | undefined>(undefined)
/** Selection captured when the link row opened, so the URL applies to it. */
const linkSelection = ref<{ from: number; to: number } | null>(null)

let initialising = false

const REASON_LABELS: Record<UnsupportedReason, string> = {
  'raw-html': 'raw HTML',
  'task-list': 'task list items',
  'unsafe-url': 'unsafe link or image URLs',
}

const reasonText = computed(() =>
  report.value.reasons.map((reason) => REASON_LABELS[reason]).join(', '),
)

const showUnsupportedNotice = computed(() => !report.value.supported && !visualOverride.value)

const currentValue = computed(() => (mode.value === 'source' ? sourceDraft.value : model.value))
const characterCount = computed(() => countCharacters(currentValue.value))
const limitMessage = computed(() =>
  props.maxLength === undefined
    ? undefined
    : validateDescriptionLength(currentValue.value, props.maxLength),
)

/** Trailing blank lines carry no meaning in Markdown; do not accumulate them. */
const normalizeOutput = (value: string): string => value.replace(/\n+$/, '')

const emitValue = (value: string): void => {
  lastEmitted.value = value
  if (value !== model.value) model.value = value
}

const destroyEditor = (): void => {
  editor.value?.destroy()
  editor.value = null
}

const createEditor = (content: string): void => {
  if (editor.value) return
  initialising = true
  const instance = new Editor({
    // Fresh instances per editor: Tiptap keeps per-editor state on the extension
    // object, so a shared list would couple two mounted editors.
    extensions: [
      ...createMarkdownExtensions(),
      Placeholder.configure({ placeholder: () => props.placeholder ?? '' }),
    ],
    content,
    contentType: 'markdown',
    editorProps: {
      attributes: {
        ...(props.id ? { id: props.id } : {}),
        role: 'textbox',
        'aria-multiline': 'true',
        'aria-label': props.label,
        'aria-invalid': props.invalid ? 'true' : 'false',
        ...(props.describedBy ? { 'aria-describedby': props.describedBy } : {}),
      },
    },
    onUpdate: ({ editor: current }) => {
      if (initialising) return
      emitValue(normalizeOutput(current.getMarkdown()))
    },
  })
  initialising = false
  editor.value = instance
}

const toSource = (): void => {
  // The last value this control actually produced. Before any edit that is the
  // original prop, so the bytes are preserved exactly; after an edit it is the
  // edit, even if the parent has not echoed it yet (a parent that validates and
  // withholds the update must not roll the author's work back).
  sourceDraft.value = lastEmitted.value
  // The warning tracks the content, not a one-time decision: coming back to
  // Source with unsupported bytes must show it again.
  visualOverride.value = false
  mode.value = 'source'
  destroyEditor()
  linkRowOpen.value = false
}

const canShowVisual = computed(() => report.value.supported || visualOverride.value)

const enterVisual = (content: string): void => {
  pendingContent.value = content
  mode.value = 'visual'
}

const toVisual = (): void => {
  const candidate = mode.value === 'source' ? sourceDraft.value : model.value
  const nextReport = inspectMarkdownSupport(candidate)
  report.value = nextReport
  visualOverride.value = false
  if (!nextReport.supported) {
    // The source now contains something the schema cannot represent. Keep the
    // bytes and stay put; converting is an explicit, confirmed choice, and
    // cancelling it must not lose the unsaved source edits.
    mode.value = 'source'
    sourceDraft.value = candidate
    confirmVisualOpen.value = true
    return
  }
  enterVisual(candidate)
}

const confirmVisualAnyway = (): void => {
  confirmVisualOpen.value = false
  visualOverride.value = true
  enterVisual(mode.value === 'source' ? sourceDraft.value : model.value)
}

const toggleMode = (): void => {
  if (mode.value === 'visual') toSource()
  else toVisual()
}

const focus = (): void => {
  if (mode.value === 'source') {
    textarea.value?.focus()
    return
  }
  editor.value?.commands.focus()
}

defineExpose({ focus })

const onSourceInput = (event: Event): void => {
  const value = (event.target as HTMLTextAreaElement).value
  sourceDraft.value = value
  emitValue(value)
  // Re-inspect only while the warning is visible, so the notice never claims
  // the content is unsupported after the author has already fixed it. The
  // common (supported) case pays nothing per keystroke.
  if (!report.value.supported) {
    report.value = inspectMarkdownSupport(value)
    if (report.value.supported) visualOverride.value = false
  }
}

const openLinkRow = (): void => {
  const instance = editor.value
  const current = instance?.getAttributes('link').href
  linkDraft.value = typeof current === 'string' ? current : ''
  linkError.value = undefined
  // Keep the editor selection: focusing the URL input blurs the contenteditable,
  // and the URL must apply to the text the author had selected.
  linkSelection.value = instance
    ? { from: instance.state.selection.from, to: instance.state.selection.to }
    : null
  linkRowOpen.value = true
  void nextTick(() => {
    const element = linkInput.value?.$el
    if (element instanceof HTMLInputElement) element.focus()
  })
}

const closeLinkRow = (): void => {
  linkRowOpen.value = false
  linkError.value = undefined
  editor.value?.commands.focus()
}

const applyLink = (): void => {
  const instance = editor.value
  if (!instance) return
  const href = linkDraft.value.trim()
  if (!isSafeUrl(href)) {
    linkError.value = 'Enter a safe URL: http, https, mailto or a site path.'
    return
  }
  const chain = instance.chain().focus()
  if (linkSelection.value) chain.setTextSelection(linkSelection.value)
  chain.extendMarkRange('link').setLink({ href }).run()
  closeLinkRow()
}

const removeLink = (): void => {
  editor.value?.chain().focus().extendMarkRange('link').unsetLink().run()
  closeLinkRow()
}

/**
 * The wrapper intercepts Mod-K so the browser's address-bar shortcut never
 * fires while the author is inside the editor, and Escape closes the link row.
 */
const onKeydown = (event: KeyboardEvent): void => {
  const modifier = event.metaKey || event.ctrlKey
  if (modifier && !event.altKey && event.key.toLowerCase() === 'k') {
    if (mode.value !== 'visual' || !editor.value) return
    event.preventDefault()
    openLinkRow()
    return
  }
  if (event.key === 'Escape' && linkRowOpen.value) {
    event.preventDefault()
    closeLinkRow()
  }
}

watch(mode, (next) => {
  if (next === 'visual' && canShowVisual.value) {
    createEditor(pendingContent.value ?? model.value)
    pendingContent.value = null
    return
  }
  destroyEditor()
})

/**
 * The editor DOM is created once; `editorProps.attributes` is not reactive. Keep
 * the validity state in sync so a form-owned error is announced against the
 * editable surface while the author types.
 */
watch(
  () => [props.invalid, props.describedBy] as const,
  () => {
    const element = editor.value?.view.dom
    if (!element) return
    element.setAttribute('aria-invalid', props.invalid ? 'true' : 'false')
    if (props.describedBy) element.setAttribute('aria-describedby', props.describedBy)
    else element.removeAttribute('aria-describedby')
  },
)

/**
 * Parent-driven updates. Our own emissions are ignored (that is the feedback
 * loop guard), so typing never resets the selection or the caret: in Source the
 * textarea keeps its own value unless the parent really changed it, and in
 * Visual the document is only replaced for an external change.
 */
watch(model, (value) => {
  if (value === lastEmitted.value) return
  lastEmitted.value = value

  if (mode.value === 'source') {
    if (value !== sourceDraft.value) sourceDraft.value = value
    const nextReport = inspectMarkdownSupport(value)
    report.value = nextReport
    visualOverride.value = false
    return
  }

  const nextReport = inspectMarkdownSupport(value)
  if (!nextReport.supported) {
    report.value = nextReport
    visualOverride.value = false
    toSource()
    sourceDraft.value = value
    return
  }

  const instance = editor.value
  if (!instance) return
  initialising = true
  instance.commands.setContent(value, { contentType: 'markdown', emitUpdate: false })
  initialising = false
})

onMounted(() => {
  if (!report.value.supported) {
    toSource()
    return
  }
  createEditor(model.value)
})

onBeforeUnmount(() => {
  destroyEditor()
})
</script>

<template>
  <div class="markdown-editor-shell space-y-1.5" @keydown="onKeydown">
    <div
      v-if="showUnsupportedNotice"
      data-slot="markdown-editor-notice"
      role="status"
      class="border border-status-attention-border bg-status-attention-surface px-2.5 py-2 text-xs text-status-attention-ink"
    >
      This description contains {{ reasonText }}, which the visual editor cannot represent. It stays
      in Source mode so the stored text is not changed.
      <Button
        variant="link"
        size="xs"
        type="button"
        class="h-auto px-1 text-xs"
        @click="confirmVisualOpen = true"
      >
        Edit visually anyway
      </Button>
    </div>

    <div class="markdown-editor flex flex-col border border-input bg-transparent focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50">
      <div
        v-if="mode === 'visual' && editor"
        class="flex flex-wrap items-center gap-1 border-b border-border"
      >
        <MarkdownEditorToolbar
          :editor="editor"
          class="flex-1"
          @request-link="openLinkRow"
        />
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="sm"
                type="button"
                aria-label="Source mode"
                class="m-1"
                @click="toggleMode"
              >
                <EyeIcon />
                Source
              </Button>
            </TooltipTrigger>
            <TooltipContent>Edit the raw Markdown instead</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>

      <div v-if="mode === 'visual' && editor" class="markdown-editor-body">
        <EditorContent :editor="editor" />
      </div>

      <div v-if="mode === 'source'" class="flex flex-col">
        <div class="flex flex-wrap items-center justify-between gap-1 border-b border-border px-1 py-1">
          <span class="px-1 text-xs text-muted-foreground">Markdown source</span>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="sm"
                  type="button"
                  aria-label="Visual mode"
                  @click="toggleMode"
                >
                  <PencilIcon />
                  Visual
                </Button>
              </TooltipTrigger>
              <TooltipContent>Return to the formatted editor</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
        <textarea
          ref="textarea"
          :id="id"
          :value="sourceDraft"
          :aria-label="`${label} (Markdown source)`"
          :aria-invalid="invalid ? 'true' : 'false'"
          :aria-describedby="describedBy"
          spellcheck="false"
          class="markdown-editor-source min-h-40 w-full resize-y bg-transparent px-2.5 py-2 font-mono text-sm outline-none"
          @input="onSourceInput"
        />
      </div>

      <div
        v-if="linkRowOpen && mode === 'visual'"
        data-slot="markdown-editor-link-row"
        class="flex flex-wrap items-start gap-1 border-t border-border px-1.5 py-1.5"
      >
        <Input
          ref="linkInput"
          v-model="linkDraft"
          aria-label="Link URL"
          placeholder="/boards or https://example.com"
          class="h-8 min-w-40 flex-1"
          @keydown.enter.prevent="applyLink"
          @keydown.esc.prevent="closeLinkRow"
        />
        <Button size="sm" type="button" @click="applyLink">
          Apply
        </Button>
        <Button variant="ghost" size="sm" type="button" @click="removeLink">
          Remove
        </Button>
        <Button variant="ghost" size="sm" type="button" @click="closeLinkRow">
          Cancel
        </Button>
        <p
          v-if="linkError"
          role="alert"
          class="w-full text-xs text-status-error-ink"
        >
          {{ linkError }}
        </p>
      </div>
    </div>

    <div class="flex flex-wrap items-center justify-between gap-1 text-xs">
      <p v-if="limitMessage && showLengthError" role="alert" class="text-status-error-ink">
        {{ limitMessage }}
      </p>
      <span v-else class="text-muted-foreground" />
      <span
        v-if="maxLength !== undefined"
        class="tabular-nums"
        :class="limitMessage ? 'text-status-error-ink' : 'text-muted-foreground'"
      >
        {{ characterCount }} / {{ maxLength }}
      </span>
    </div>

    <ConfirmDialog
      :open="confirmVisualOpen"
      title="Edit visually anyway?"
      :description="`This description contains ${reasonText}. The visual editor cannot represent it, so saving from visual mode will drop that markup.`"
      confirm-label="Edit visually"
      destructive
      @update:open="confirmVisualOpen = $event"
      @confirm="confirmVisualAnyway"
    />
  </div>
</template>
