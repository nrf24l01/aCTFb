<script setup lang="ts">
import type { Editor } from '@tiptap/core'
import type { Component } from 'vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  BoldIcon,
  CodeIcon,
  ItalicIcon,
  LinkIcon,
  ListIcon,
  ListOrderedIcon,
  Redo2Icon,
  SquareCodeIcon,
  TextQuoteIcon,
  Undo2Icon,
} from '@lucide/vue'
import { Toggle } from '@/components/ui/toggle'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DEFAULT_CODE_LANGUAGE, HIGHLIGHT_LANGUAGES } from '@/shared/lib/highlight'

/**
 * Toolbar for the visual editor.
 *
 * Every control is a real `button` (via `Toggle`/`Button`) with an accessible
 * name and a tooltip, so keyboard and screen-reader users can reach all of
 * them; nothing depends on colour alone. Active marks come from
 * `editor.isActive(...)` re-read on each transaction, so the state never goes
 * stale after a keyboard shortcut or a selection change.
 */

const props = withDefaults(
  defineProps<{
    editor: Editor
    disabled?: boolean
  }>(),
  { disabled: false },
)

const emit = defineEmits<{ (event: 'requestLink'): void }>()

/**
 * `useEditor` from `@tiptap/vue-3` does not re-render on transactions, so the
 * toolbar subscribes itself. Reading `version` inside the template helpers
 * makes the render effect depend on it, which keeps `aria-pressed` current.
 */
const version = ref(0)
const bump = (): void => {
  version.value += 1
}

onMounted(() => {
  props.editor.on('transaction', bump)
  props.editor.on('selectionUpdate', bump)
  props.editor.on('update', bump)
})

onBeforeUnmount(() => {
  props.editor.off('transaction', bump)
  props.editor.off('selectionUpdate', bump)
  props.editor.off('update', bump)
})

const isDisabled = computed(() => props.disabled || !props.editor.isEditable)

const isActive = (name: string, attributes?: Record<string, unknown>): boolean => {
  // Touch the reactive counter so the surrounding template re-renders.
  void version.value
  return props.editor.isActive(name, attributes)
}

const canRun = (action: 'undo' | 'redo'): boolean => {
  void version.value
  const chain = props.editor.can().chain().focus()
  return action === 'undo' ? chain.undo().run() : chain.redo().run()
}

const run = (command: (chain: ReturnType<Editor['chain']>) => ReturnType<Editor['chain']>): void => {
  command(props.editor.chain().focus()).run()
}

const toggleBold = (): void => run((chain) => chain.toggleBold())
const toggleItalic = (): void => run((chain) => chain.toggleItalic())
const toggleCode = (): void => run((chain) => chain.toggleCode())
const toggleBulletList = (): void => run((chain) => chain.toggleBulletList())
const toggleOrderedList = (): void => run((chain) => chain.toggleOrderedList())
const toggleBlockquote = (): void => run((chain) => chain.toggleBlockquote())
const toggleCodeBlock = (): void => run((chain) => chain.toggleCodeBlock())
const undo = (): void => run((chain) => chain.undo())
const redo = (): void => run((chain) => chain.redo())

const HEADING_LEVELS = [1, 2, 3, 4, 5, 6] as const

const headingValue = computed<string>(() => {
  void version.value
  for (const level of HEADING_LEVELS) {
    if (props.editor.isActive('heading', { level })) return String(level)
  }
  return 'paragraph'
})

const setHeading = (value: unknown): void => {
  const level = Number(value)
  if (!HEADING_LEVELS.includes(level as (typeof HEADING_LEVELS)[number])) {
    run((chain) => chain.setParagraph())
    return
  }
  run((chain) => chain.setHeading({ level: level as (typeof HEADING_LEVELS)[number] }))
}

/** The language stored on the active code block, exactly as written. */
const codeLanguage = computed<string>(() => {
  void version.value
  const raw = props.editor.getAttributes('codeBlock').language
  return typeof raw === 'string' && raw !== '' ? raw : ''
})

/**
 * Registry languages plus the current value when it is not one of them.
 * An unknown fence language (```` ```brainfuck ```` or ```` ```brainfuck title=x ````)
 * is shown readably as `Unknown (…)` and is never rewritten just by opening the
 * picker: only an explicit choice updates the node.
 */
const languageOptions = computed<{ value: string, label: string }[]>(() => {
  const options = HIGHLIGHT_LANGUAGES.map((language) => ({
    value: language.id,
    label: language.label,
  }))
  const current = codeLanguage.value
  if (current !== '' && !options.some((option) => option.value === current)) {
    options.push({ value: current, label: `Unknown (${current})` })
  }
  return options
})

const languageValue = computed<string>(() => codeLanguage.value || DEFAULT_CODE_LANGUAGE)

const setLanguage = (value: unknown): void => {
  // Choosing "Plain text" stores no language, so the fence stays a bare ```
  // instead of gaining a `plaintext` info string the author never typed.
  const language = value === DEFAULT_CODE_LANGUAGE ? null : String(value)
  run((chain) => chain.updateAttributes('codeBlock', { language }))
}

interface ToolbarControl {
  label: string
  icon: Component
  /** Re-read on every render, so `aria-pressed` follows the real selection. */
  pressed?: () => boolean
  disabled: () => boolean
  activate: () => void
}

const markControls: ToolbarControl[] = [
  { label: 'Bold', icon: BoldIcon, pressed: () => isActive('bold'), disabled: () => isDisabled.value, activate: toggleBold },
  { label: 'Italic', icon: ItalicIcon, pressed: () => isActive('italic'), disabled: () => isDisabled.value, activate: toggleItalic },
  { label: 'Inline code', icon: CodeIcon, pressed: () => isActive('code'), disabled: () => isDisabled.value, activate: toggleCode },
]

const listControls: ToolbarControl[] = [
  { label: 'Bullet list', icon: ListIcon, pressed: () => isActive('bulletList'), disabled: () => isDisabled.value, activate: toggleBulletList },
  { label: 'Ordered list', icon: ListOrderedIcon, pressed: () => isActive('orderedList'), disabled: () => isDisabled.value, activate: toggleOrderedList },
  { label: 'Blockquote', icon: TextQuoteIcon, pressed: () => isActive('blockquote'), disabled: () => isDisabled.value, activate: toggleBlockquote },
]

const insertControls: ToolbarControl[] = [
  { label: 'Code block', icon: SquareCodeIcon, pressed: () => isActive('codeBlock'), disabled: () => isDisabled.value, activate: toggleCodeBlock },
]
</script>

<template>
  <div
    role="toolbar"
    aria-label="Formatting"
    data-slot="markdown-editor-toolbar"
    class="markdown-editor-toolbar flex flex-wrap items-center gap-1 px-1 py-1"
  >
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            type="button"
            aria-label="Undo"
            :disabled="isDisabled || !canRun('undo')"
            @click="undo"
          >
            <Undo2Icon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Undo</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            type="button"
            aria-label="Redo"
            :disabled="isDisabled || !canRun('redo')"
            @click="redo"
          >
            <Redo2Icon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Redo</TooltipContent>
      </Tooltip>

      <span aria-hidden="true" class="mx-1 h-5 w-px bg-border" />

      <Select :model-value="headingValue" :disabled="isDisabled" @update:model-value="setHeading">
        <SelectTrigger
          size="sm"
          aria-label="Paragraph or heading"
          class="min-w-32"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="paragraph">
            Paragraph
          </SelectItem>
          <SelectItem v-for="level in HEADING_LEVELS" :key="level" :value="String(level)">
            H{{ level }}
          </SelectItem>
        </SelectContent>
      </Select>

      <span aria-hidden="true" class="mx-1 h-5 w-px bg-border" />

      <Tooltip v-for="control in markControls" :key="control.label">
        <TooltipTrigger as-child>
          <Toggle
            :model-value="control.pressed?.() ?? false"
            :aria-label="control.label"
            :disabled="control.disabled()"
            size="sm"
            @update:model-value="control.activate()"
          >
            <component :is="control.icon" />
          </Toggle>
        </TooltipTrigger>
        <TooltipContent>{{ control.label }}</TooltipContent>
      </Tooltip>

      <span aria-hidden="true" class="mx-1 h-5 w-px bg-border" />

      <Tooltip v-for="control in listControls" :key="control.label">
        <TooltipTrigger as-child>
          <Toggle
            :model-value="control.pressed?.() ?? false"
            :aria-label="control.label"
            :disabled="control.disabled()"
            size="sm"
            @update:model-value="control.activate()"
          >
            <component :is="control.icon" />
          </Toggle>
        </TooltipTrigger>
        <TooltipContent>{{ control.label }}</TooltipContent>
      </Tooltip>

      <span aria-hidden="true" class="mx-1 h-5 w-px bg-border" />

      <Tooltip>
        <TooltipTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            type="button"
            aria-label="Link"
            :disabled="isDisabled"
            @click="emit('requestLink')"
          >
            <LinkIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Link (Ctrl/⌘ K)</TooltipContent>
      </Tooltip>

      <Tooltip v-for="control in insertControls" :key="control.label">
        <TooltipTrigger as-child>
          <Toggle
            :model-value="control.pressed?.() ?? false"
            :aria-label="control.label"
            :disabled="control.disabled()"
            size="sm"
            @update:model-value="control.activate()"
          >
            <component :is="control.icon" />
          </Toggle>
        </TooltipTrigger>
        <TooltipContent>{{ control.label }}</TooltipContent>
      </Tooltip>

      <Select
        v-if="isActive('codeBlock')"
        :model-value="languageValue"
        :disabled="isDisabled"
        @update:model-value="setLanguage"
      >
        <SelectTrigger size="sm" aria-label="Code language" class="ml-1 min-w-28">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem
            v-for="option in languageOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </SelectItem>
        </SelectContent>
      </Select>
    </TooltipProvider>
  </div>
</template>
