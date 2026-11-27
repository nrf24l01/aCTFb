<script setup lang="ts">
import { computed, ref } from 'vue'
import { CircleCheckIcon } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { createIdempotencyKey } from '@/shared/api/idempotency'
import { isApiError, isNetworkError } from '@/shared/api/errors'
import { formatPoints } from '@/shared/lib/format'
import { statusLabel } from '@/shared/lib/status'
import type { Submission, Task } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  moduleUuid: string
  task: Task
}>()

const emit = defineEmits<{
  (event: 'submitted', submission: Submission): void
}>()

const answer = ref('')
const pending = ref(false)
const fieldError = ref<string | undefined>()
const serverError = ref<unknown>(null)
const networkFailure = ref(false)
const verdict = ref<Submission | null>(null)

/**
 * Holds exactly the previous in-flight attempt. The flag itself is never
 * persisted, logged, shown again, or put in a URL. It exists only so a
 * network retry can reuse the same Idempotency-Key and payload.
 */
let inFlight: { key: string; text: string } | null = null

const supportsFlag = computed(
  () =>
    props.task.type === 'FLAG' &&
    props.task.submission.mode === 'TEXT' &&
    props.task.submission.checker === 'AUTOMATIC',
)
/**
 * The backend's default FLAG text policy is 1..1_000_000 and custom bounds are
 * not persisted in the first slice. Never impose a smaller client-only cap
 * that could reject a flag the backend accepts. Task responses normally carry
 * the policy; the fallback matches the server default exactly.
 */
const SERVER_DEFAULT_FLAG_MAX_LENGTH = 1_000_000
const maxLength = computed(
  () => props.task.submission.text?.maxLength ?? SERVER_DEFAULT_FLAG_MAX_LENGTH,
)
const minLength = computed(() => props.task.submission.text?.minLength ?? 1)
const canSubmit = computed(
  () => supportsFlag.value && (props.task.status === 'AVAILABLE' || props.task.status === 'SOLVED'),
)

/** One compact outcome line. Points come only from the response, never the task maximum. */
const outcome = computed(() => {
  const current = verdict.value
  if (!current) return null
  if (current.status === 'ACCEPTED') {
    const points = current.scoreAwarded
    if (points === null || points === undefined) return 'Accepted'
    return `Accepted · +${formatPoints(points)} points`
  }
  if (current.status === 'REJECTED') return 'Incorrect flag. Try again.'
  return statusLabel(current.status)
})

const outcomeTone = computed(() => {
  if (verdict.value?.status === 'ACCEPTED') return 'text-pine'
  if (verdict.value?.status === 'REJECTED') return 'text-love'
  return 'text-muted-foreground'
})

function clearFeedback() {
  fieldError.value = undefined
  serverError.value = null
  networkFailure.value = false
}

async function send(text: string, key: string) {
  pending.value = true
  try {
    const submission = await boardsApi.submitTask(
      props.boardUuid,
      props.moduleUuid,
      props.task.uuid,
      key,
      text,
    )
    inFlight = null
    verdict.value = submission
    emit('submitted', submission)
  } catch (caught) {
    if (isNetworkError(caught)) {
      networkFailure.value = true
      return
    }
    inFlight = null
    if (isApiError(caught)) {
      fieldError.value = caught.fieldIssue('text')
    }
    serverError.value = caught
  } finally {
    pending.value = false
  }
}

async function submit() {
  clearFeedback()
  const text = answer.value
  if (!text.trim()) {
    fieldError.value = 'Enter a flag before submitting.'
    return
  }
  if (text.length < minLength.value) {
    fieldError.value = `Flag must be at least ${minLength.value} characters.`
    return
  }
  if (text.length > maxLength.value) {
    fieldError.value = `Flag must be at most ${maxLength.value} characters.`
    return
  }

  if (!inFlight || inFlight.text !== text) {
    inFlight = { key: createIdempotencyKey(), text }
  }
  const attempt = inFlight

  // Clear immediately: the submitted answer is never kept in visible state.
  answer.value = ''
  await send(attempt.text, attempt.key)
}

async function retry() {
  if (!inFlight || pending.value) return
  clearFeedback()
  await send(inFlight.text, inFlight.key)
}
</script>

<template>
  <section class="space-y-4" aria-labelledby="submit-heading">
    <h2 id="submit-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
      Submit flag
    </h2>

    <div class="space-y-4">
      <Alert v-if="task.status === 'LOCKED'" class="border-0 bg-transparent px-0 py-0">
        <AlertTitle>Task is not available</AlertTitle>
      </Alert>
      <Alert v-else-if="outcome" class="border-0 bg-transparent px-0 py-0" :class="outcomeTone">
        <AlertTitle>{{ outcome }}</AlertTitle>
      </Alert>
      <Alert v-else-if="task.solved" class="border-0 bg-transparent px-0 py-0 text-pine">
        <CircleCheckIcon aria-hidden="true" class="size-4" />
        <AlertTitle>Solved</AlertTitle>
      </Alert>

      <ErrorAlert v-if="serverError" :error="serverError" title="Submission failed" />

      <Alert v-if="networkFailure" class="border-0 bg-transparent px-0 py-0">
        <AlertTitle>Network error</AlertTitle>
        <AlertDescription>
          The request may not have reached the server. Your answer is kept for a retry.
          <Button variant="outline" class="mt-3" :disabled="pending" @click="retry">
            {{ pending ? 'Retrying…' : 'Retry same submission' }}
          </Button>
        </AlertDescription>
      </Alert>

      <form v-if="!task.solved && canSubmit" class="space-y-3" novalidate @submit.prevent="submit">
        <FormField
          label="Flag"
          for-id="task-flag"
          :error="fieldError"
          :hint="`${minLength}–${maxLength} characters.`"
        >
          <Input
            id="task-flag"
            v-model="answer"
            name="flag"
            autocomplete="off"
            autocapitalize="none"
            spellcheck="false"
            :aria-invalid="Boolean(fieldError)"
            :maxlength="maxLength"
            :disabled="pending"
          />
        </FormField>
        <Button type="submit" :disabled="pending || !answer.trim()">
          {{ pending ? 'Checking…' : 'Submit flag' }}
        </Button>
      </form>

      <p v-else-if="!task.solved && !supportsFlag" class="text-sm text-muted-foreground">
        This task does not accept automatic flag submissions.
      </p>
    </div>
  </section>
</template>