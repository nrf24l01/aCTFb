<script setup lang="ts">
import SearchPicker, { type SearchPickerOption } from '@/features/access/components/SearchPicker.vue'
import type { TeamDirectoryEntry } from '@/shared/types/api'

/** Team adapter for the shared search picker (team audience membership). */
const props = defineProps<{
  search: (query: string, signal: AbortSignal) => Promise<TeamDirectoryEntry[]>
  exclude?: string[]
  placeholder?: string
  label?: string
}>()

const emit = defineEmits<{
  (event: 'select', team: TeamDirectoryEntry): void
}>()

async function search(query: string, signal: AbortSignal): Promise<SearchPickerOption[]> {
  const teams = await props.search(query, signal)
  return teams.map((team) => ({
    uuid: team.uuid,
    primary: team.name,
    hint: team.uuid.slice(0, 8),
  }))
}

function onSelect(option: SearchPickerOption) {
  emit('select', { uuid: option.uuid, name: option.primary })
}
</script>

<template>
  <SearchPicker
    :search="search"
    :exclude="exclude"
    :placeholder="placeholder ?? 'Search teams by name or UUID'"
    :label="label"
    @select="onSelect"
  />
</template>
