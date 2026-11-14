<script setup lang="ts">
import type { FieldError } from '@/shared/types/api'

defineProps<{
  label: string
  forId?: string
  hint?: string
  error?: string | FieldError | null
}>()

function issueOf(error: string | FieldError | null | undefined): string | undefined {
  if (!error) return undefined
  return typeof error === 'string' ? error : error.issue
}
</script>

<template>
  <div class="space-y-1.5">
    <label :for="forId" class="block text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
      {{ label }}
    </label>
    <slot />
    <p v-if="hint && !issueOf(error)" class="text-xs text-muted-foreground">{{ hint }}</p>
    <p v-if="issueOf(error)" class="text-xs text-love">{{ issueOf(error) }}</p>
  </div>
</template>