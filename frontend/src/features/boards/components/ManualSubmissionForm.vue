<script setup lang="ts">
import { computed, ref } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import FileDraftUploader from '@/features/files/components/FileDraftUploader.vue'
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

const emit = defineEmits<{ (event: 'submitted', submission: Submission): void }>()

const text = ref('')
const pending = ref(false)
const fieldError = ref<string | undefined>()
const serverError = ref<unknown>(null)
const networkFailure = ref(false)
const verdict = ref<Submission | null>(null)
const draftUuid = ref<string | null>(null)
const filesReady = ref(false)
const uploaderKey = ref(0)

let inFlight: { key: string; body: { text?: string; draftUuid?: string } } | null = null

const mode = computed(() => props.task.submission.mode)
const textPolicy = computed(() => props.task.submission.text)
const filesPolicy = computed(() => props.task.submission.files)
const needsText = computed(() => mode.value === 'TEXT' || mode.value === 'TEXT_AND_FILES' || mode.value === 'TEXT_OR_FILES')
const needsFiles = computed(() => mode.value === 'FILES' || mode.value === 'TEXT_AND_FILES' || mode.value === 'TEXT_OR_FILES')
const textRequired = computed(() => textPolicy.value?.required ?? false)
const filesRequired = computed(() => filesPolicy.value?.required ?? false)
const canSubmit = computed(
  () => props.task.status === 'AVAILABLE' || props.task.status === 'SOLVED',
)

/** One compact outcome line. Review points come only from the response. */
const outcome = computed(() => {
  const current = verdict.value
  if (!current) return null
  if (current.status === 'PENDING_REVIEW') return 'Awaiting review'
  if (current.status === 'ACCEPTED') {
    const points = current.scoreAwarded
    if (points === null || points === undefined) return 'Accepted'
    return `Accepted · +${formatPoints(points)} points`
  }
  return statusLabel(current.status)
})

const outcomeTone = computed(() => {
  if (verdict.value?.status === 'ACCEPTED') return 'text-status-success-ink'
  if (verdict.value?.status === 'REJECTED') return 'text-love'
  return 'text-gold'
})

function validate(): string | undefined {
  if (needsText.value && textRequired.value && !text.value.trim()) return 'Text is required.'
  if (needsText.value && textPolicy.value && text.value.length < textPolicy.value.minLength) {
    return `Text must be at least ${textPolicy.value.minLength} characters.`
  }
  if (needsText.value && textPolicy.value && text.value.length > textPolicy.value.maxLength) {
    return `Text must be at most ${textPolicy.value.maxLength} characters.`
  }
  if (draftUuid.value && !filesReady.value) {
    return 'Wait for the uploaded files to reach READY, or cancel the draft.'
  }
  if (needsFiles.value && filesRequired.value && !filesReady.value) {
    return 'Upload and scan the required files before finalizing.'
  }
  if (needsFiles.value && mode.value === 'FILES' && !draftUuid.value) {
    return 'Upload at least one file.'
  }
  if (mode.value === 'TEXT_OR_FILES' && !text.value.trim() && !draftUuid.value) {
    return 'Provide text or at least one file.'
  }
  return undefined
}

async function send(body: { text?: string; draftUuid?: string }, key: string) {
  pending.value = true
  try {
    const submission = await boardsApi.submitTaskBody(
      props.boardUuid,
      props.moduleUuid,
      props.task.uuid,
      key,
      body,
    )
    inFlight = null
    verdict.value = submission
    text.value = ''
    draftUuid.value = null
    filesReady.value = false
    uploaderKey.value += 1
    emit('submitted', submission)
  } catch (caught) {
    if (isNetworkError(caught)) {
      networkFailure.value = true
      return
    }
    inFlight = null
    if (isApiError(caught)) fieldError.value = caught.fieldIssue('text') || caught.fieldIssue('draftUuid')
    serverError.value = caught
  } finally {
    pending.value = false
  }
}

async function submit() {
  serverError.value = null
  networkFailure.value = false
  fieldError.value = validate()
  if (fieldError.value) return

  const body: { text?: string; draftUuid?: string } = {}
  if (needsText.value && text.value.trim()) body.text = text.value
  if (needsFiles.value && draftUuid.value) body.draftUuid = draftUuid.value
  if (!inFlight || JSON.stringify(inFlight.body) !== JSON.stringify(body)) {
    inFlight = { key: createIdempotencyKey(), body }
  }
  const attempt = inFlight
  await send(attempt.body, attempt.key)
}

async function retry() {
  if (!inFlight || pending.value) return
  await send(inFlight.body, inFlight.key)
}
</script>

<template>
  <section class="space-y-4" aria-labelledby="manual-submit-heading">
    <h2 id="manual-submit-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
      Submit for review
    </h2>
    <div class="space-y-4">
      <Alert v-if="outcome" class="border-0 bg-transparent px-0 py-0" :class="outcomeTone">
        <AlertTitle>{{ outcome }}</AlertTitle>
      </Alert>
      <ErrorAlert v-if="serverError" :error="serverError" title="Submission failed" />
      <Alert v-if="networkFailure" class="border-0 bg-transparent px-0 py-0">
        <AlertTitle>Network error</AlertTitle>
        <AlertDescription>
          The request may not have reached the server. Your submission is kept for a retry.
          <Button variant="outline" class="mt-3" :disabled="pending" @click="retry">Retry same submission</Button>
        </AlertDescription>
      </Alert>

      <form v-if="canSubmit" class="space-y-4" novalidate @submit.prevent="submit">
        <FormField
          v-if="needsText"
          label="Submission text"
          for-id="manual-submission-text"
          :error="fieldError"
          :hint="textPolicy ? `${textPolicy.minLength}–${textPolicy.maxLength} characters.${textRequired ? ' Required.' : ' Optional.'}` : undefined"
        >
          <Textarea
            id="manual-submission-text"
            v-model="text"
            rows="6"
            autocomplete="off"
            :maxlength="textPolicy?.maxLength"
            :disabled="pending"
          />
        </FormField>

        <FileDraftUploader
          v-if="needsFiles && filesPolicy"
          :key="uploaderKey"
          v-model="draftUuid"
          :board-uuid="boardUuid"
          :module-uuid="moduleUuid"
          :task-uuid="task.uuid"
          resource-type="TASK"
          :policy="filesPolicy"
          @ready="filesReady = $event"
        />

        <Button type="submit" :disabled="pending">{{ pending ? 'Submitting…' : 'Submit for review' }}</Button>
      </form>
      <p v-else-if="!task.solved" class="text-sm text-muted-foreground">
        This task does not accept submissions right now.
      </p>
    </div>
  </section>
</template>