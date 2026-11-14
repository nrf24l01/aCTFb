<script setup lang="ts">
import { ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTimeLocal, toIsoOrNull } from '@/shared/lib/format'
import type { BoardRoleBinding } from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  boardUuid: string
  binding: BoardRoleBinding | null
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const expiresAt = ref('')
const clearExpiry = ref(false)
const problem = ref<string | null | undefined>()
const error = ref<unknown>(null)
const pending = ref(false)

watch(
  () => [props.open, props.binding] as const,
  ([open, binding]) => {
    if (!open || !binding) return
    expiresAt.value = binding.expiresAt ? formatDateTimeLocal(new Date(binding.expiresAt)) : ''
    clearExpiry.value = false
    problem.value = undefined
    error.value = null
  },
  { immediate: true },
)

async function submit() {
  if (!props.binding) return
  error.value = null
  const value = clearExpiry.value ? null : toIsoOrNull(expiresAt.value)
  if (!clearExpiry.value && !value) {
    problem.value = 'Choose an expiry or clear it.'
    return
  }
  if (value && new Date(value).getTime() <= Date.now()) {
    problem.value = 'Expiry must be in the future.'
    return
  }
  problem.value = undefined
  pending.value = true
  try {
    await boardsApi.updateRoleBinding(props.boardUuid, props.binding.uuid, { expiresAt: value })
    emit('saved')
    emit('update:open', false)
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="rounded-none border-border bg-black">
      <DialogHeader>
        <DialogTitle>Change expiry</DialogTitle>
        <DialogDescription>Role and module cannot be changed; revoke and grant instead.</DialogDescription>
      </DialogHeader>
      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not update binding" />
        <FormField label="Expiry" for-id="edit-expiry" :error="problem">
          <Input id="edit-expiry" v-model="expiresAt" type="datetime-local" :disabled="clearExpiry" />
        </FormField>
        <label class="flex min-h-11 items-center gap-2 text-sm">
          <input v-model="clearExpiry" type="checkbox" class="size-4 accent-[var(--text)]" />
          Remove expiry (never expires)
        </label>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save expiry' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>