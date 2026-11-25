<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { isApiError } from '@/shared/api/errors'
import type { BoardJoinResponse } from '@/shared/types/api'

/**
 * One code field, nothing else. The board is resolved server-side from the code,
 * so the dialog never asks for a UUID, a link or a URL parameter. The entered
 * code lives only in this component's memory: it is never written to Web
 * Storage, the URL, logs or analytics, and it is cleared on close and success.
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    /** Optional UX hint from a board page. It never authorizes anything. */
    boardUuid?: string
  }>(),
  { boardUuid: undefined },
)

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'joined', result: BoardJoinResponse): void
}>()

const code = ref('')
const pending = ref(false)
const error = ref<unknown>(null)

const trimmed = computed(() => code.value.trim())
const canSubmit = computed(() => !pending.value && trimmed.value.length >= 8 && trimmed.value.length <= 64)

const errorMessage = computed(() => {
  if (!error.value) return null
  if (isApiError(error.value) && error.value.code === 'JOIN_CODE_INVALID') {
    return 'That board code is not valid. Check the code with the organizer.'
  }
  return null
})

watch(
  () => props.open,
  (open) => {
    if (!open) reset()
  },
)

function reset() {
  code.value = ''
  error.value = null
  pending.value = false
}

function close() {
  reset()
  emit('update:open', false)
}

async function submit() {
  if (!canSubmit.value) return
  pending.value = true
  error.value = null
  try {
    const result = await boardsApi.joinBoardByCode(trimmed.value, props.boardUuid)
    reset()
    emit('joined', result)
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(value: boolean) => (value ? emit('update:open', true) : close())">
    <DialogContent class="rounded-none border-border bg-black">
      <DialogHeader>
        <DialogTitle>Join a board</DialogTitle>
        <DialogDescription>
          Enter the board code from the organizer. Public boards are browsable, but joining always needs a code.
        </DialogDescription>
      </DialogHeader>
      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not join board">
          <p v-if="errorMessage" class="mt-1 text-xs">{{ errorMessage }}</p>
        </ErrorAlert>
        <FormField
          label="Board code"
          for-id="join-board-code"
          hint="8–64 characters. Spaces around the code are ignored."
        >
          <Input
            id="join-board-code"
            v-model="code"
            autocomplete="off"
            autocapitalize="characters"
            spellcheck="false"
            :disabled="pending"
          />
        </FormField>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="close">Cancel</Button>
          <Button type="submit" :disabled="!canSubmit">{{ pending ? 'Joining…' : 'Join' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
