<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/shared/api/errors'
import type { UserDirectoryEntry } from '@/shared/types/api'

const props = defineProps<{
  search: (query: string, signal: AbortSignal) => Promise<UserDirectoryEntry[]>
  exclude?: string[]
  placeholder?: string
  label?: string
}>()

const emit = defineEmits<{
  (event: 'select', user: UserDirectoryEntry): void
}>()

const listId = useId()
const input = ref<HTMLInputElement | null>(null)
const query = ref('')
const open = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
const results = ref<UserDirectoryEntry[]>([])
const activeIndex = ref(-1)

let timer: ReturnType<typeof setTimeout> | null = null
let controller: AbortController | null = null

const excluded = computed(() => new Set(props.exclude ?? []))
const hasQuery = computed(() => query.value.trim().length > 0)

function cancelPending() {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  controller?.abort()
  controller = null
}

async function run(value: string) {
  cancelPending()
  const trimmed = value.trim()
  if (!trimmed) {
    results.value = []
    open.value = false
    loading.value = false
    error.value = null
    return
  }
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = null
  open.value = true
  try {
    const items = await props.search(trimmed, current.signal)
    if (current.signal.aborted) return
    results.value = items
    activeIndex.value = items.length > 0 ? 0 : -1
  } catch (caught) {
    if (current.signal.aborted) return
    error.value = errorMessage(caught)
    results.value = []
    activeIndex.value = -1
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}

watch(query, (value) => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    void run(value)
  }, 250)
})

function onFocus() {
  if (hasQuery.value) open.value = true
}

function focusInput() {
  const component = input.value as unknown as { $el?: HTMLInputElement; focus?: () => void } | null
  if (component?.$el && typeof component.$el.focus === 'function') {
    component.$el.focus()
    return
  }
  component?.focus?.()
}

function choose(user: UserDirectoryEntry) {
  if (excluded.value.has(user.uuid)) return
  emit('select', user)
  query.value = ''
  results.value = []
  open.value = false
  activeIndex.value = -1
  void nextTick(focusInput)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    open.value = false
    focusInput()
    return
  }
  if (!open.value || results.value.length === 0) {
    if (event.key === 'ArrowDown' && hasQuery.value) {
      open.value = true
      event.preventDefault()
    }
    return
  }
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value + 1) % results.value.length
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value - 1 + results.value.length) % results.value.length
  } else if (event.key === 'Enter') {
    const user = results.value[activeIndex.value]
    if (user) {
      event.preventDefault()
      choose(user)
    }
  }
}

function retry() {
  void run(query.value)
}

onBeforeUnmount(cancelPending)
</script>

<template>
  <div class="relative">
    <label v-if="label" class="mb-1 block text-sm font-medium" :for="`${listId}-input`">{{ label }}</label>
    <Input
      :id="`${listId}-input`"
      ref="input"
      v-model="query"
      role="combobox"
      autocomplete="off"
      spellcheck="false"
      :placeholder="placeholder ?? 'Search by username, email or UUID'"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-activedescendant="activeIndex >= 0 ? `${listId}-option-${activeIndex}` : undefined"
      aria-autocomplete="list"
      @focus="onFocus"
      @keydown="onKeydown"
    />

    <div
      v-if="open && hasQuery"
      class="absolute z-40 mt-1 w-full border border-border bg-black shadow-lg"
    >
      <p v-if="loading" class="px-3 py-2 text-xs text-muted-foreground" role="status">Searching…</p>
      <div v-else-if="error" class="space-y-2 px-3 py-2">
        <p class="text-xs text-love" role="alert">{{ error }}</p>
        <Button type="button" variant="outline" size="sm" @click="retry">Retry</Button>
      </div>
      <p v-else-if="results.length === 0" class="px-3 py-2 text-xs text-muted-foreground" role="status">No matching users.</p>
      <ul v-else :id="listId" role="listbox" class="max-h-64 overflow-y-auto py-1">
        <li
          v-for="(user, index) in results"
          :id="`${listId}-option-${index}`"
          :key="user.uuid"
          role="option"
          :aria-selected="index === activeIndex"
          :aria-disabled="excluded.has(user.uuid)"
          class="cursor-pointer px-3 py-2 text-sm"
          :class="[
            index === activeIndex ? 'bg-muted text-foreground' : 'text-muted-foreground',
            excluded.has(user.uuid) ? 'cursor-not-allowed opacity-50' : 'hover:bg-muted hover:text-foreground',
          ]"
          @mousedown.prevent
          @click="choose(user)"
          @mouseenter="activeIndex = index"
        >
          <span class="font-medium text-foreground">{{ user.username }}</span>
          <span class="ml-2 text-xs">{{ user.email }}</span>
          <span class="ml-2 font-mono text-[10px] uppercase">{{ user.uuid.slice(0, 8) }}</span>
          <span v-if="excluded.has(user.uuid)" class="ml-2 text-[10px] uppercase tracking-wide">Already added</span>
        </li>
      </ul>
    </div>
  </div>
</template>
