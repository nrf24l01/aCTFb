<script setup lang="ts">
import SearchPicker, { type SearchPickerOption } from '@/features/access/components/SearchPicker.vue'
import type { UserDirectoryEntry } from '@/shared/types/api'

/**
 * User adapter for the shared search picker. Every user-assignment flow uses
 * this shape so identity disclosure rules stay in one place: username is always
 * primary, email is secondary only when the server returned it.
 */
const props = defineProps<{
  search: (query: string, signal: AbortSignal) => Promise<UserDirectoryEntry[]>
  exclude?: string[]
  placeholder?: string
  label?: string
}>()

const emit = defineEmits<{
  (event: 'select', user: UserDirectoryEntry): void
}>()

async function search(query: string, signal: AbortSignal): Promise<SearchPickerOption[]> {
  const users = await props.search(query, signal)
  return users.map((user) => ({
    uuid: user.uuid,
    primary: user.username,
    secondary: user.email || undefined,
    hint: user.uuid.slice(0, 8),
  }))
}

function onSelect(option: SearchPickerOption) {
  emit('select', {
    uuid: option.uuid,
    username: option.primary,
    email: option.secondary ?? '',
  })
}
</script>

<template>
  <SearchPicker
    :search="search"
    :exclude="exclude"
    :placeholder="placeholder ?? 'Search by username, email or UUID'"
    :label="label"
    @select="onSelect"
  />
</template>
