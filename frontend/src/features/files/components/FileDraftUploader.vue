<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import { filesApi } from '@/features/files/api'
import {
  DraftCancelledError,
  hashAndUploadFile,
  pollDraftUntilSettled,
  validateFileAgainstPolicy,
} from '@/features/files/draft'
import { formatDateTime } from '@/shared/lib/format'
import type { PendingFile, SubmissionDraft, SubmissionFilesPolicy } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  moduleUuid: string
  taskUuid?: string | null
  resourceType: 'TASK' | 'PENTEST'
  policy: SubmissionFilesPolicy
  modelValue?: string | null
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: string | null): void
  (event: 'ready', value: boolean): void
}>()

const draft = ref<SubmissionDraft | null>(null)
const uploading = ref(false)
const polling = ref(false)
const error = ref<unknown>(null)
const inputKey = ref(0)
let abort: AbortController | null = null

const files = computed(() => draft.value?.files ?? [])
const readyFiles = computed(() => files.value.filter((file) => file.status === 'READY').length)
const rejected = computed(() => files.value.filter((file) => file.status === 'REJECTED' || file.status === 'EXPIRED'))
const allReady = computed(
  () =>
    Boolean(draft.value) &&
    readyFiles.value >= props.policy.minCount &&
    readyFiles.value === files.value.length &&
    rejected.value.length === 0,
)

function publishState() {
  emit('update:modelValue', draft.value?.uuid ?? null)
  emit('ready', allReady.value)
}

async function ensureDraft(): Promise<SubmissionDraft> {
  if (draft.value) return draft.value
  draft.value = props.resourceType === 'TASK'
    ? await filesApi.createTaskDraft(props.boardUuid, props.moduleUuid, props.taskUuid ?? '')
    : await filesApi.createPentestDraft(props.boardUuid, props.moduleUuid)
  return draft.value
}

async function poll() {
  if (!draft.value) return
  polling.value = true
  try {
    draft.value = await pollDraftUntilSettled(
      () =>
        props.resourceType === 'TASK'
          ? filesApi.getTaskDraft(props.boardUuid, props.moduleUuid, props.taskUuid ?? '', draft.value!.uuid)
          : filesApi.getPentestDraft(props.boardUuid, props.moduleUuid, draft.value!.uuid),
      { isCancelled: () => abort?.signal.aborted ?? false },
    )
    publishState()
  } catch (caught) {
    if (!(caught instanceof DraftCancelledError)) error.value = caught
  } finally {
    polling.value = false
  }
}

async function onFiles(event: Event) {
  const target = event.target as HTMLInputElement
  const selected = Array.from(target.files ?? [])
  target.value = ''
  inputKey.value += 1
  if (selected.length === 0) return

  error.value = null
  abort = new AbortController()
  uploading.value = true
  try {
    draft.value = await ensureDraft()
    for (const file of selected) {
      const problem = validateFileAgainstPolicy(file, props.policy, files.value.length)
      if (problem) {
        error.value = new Error(problem)
        break
      }
      const pending = await hashAndUploadFile(
        file,
        (request) =>
          props.resourceType === 'TASK'
            ? filesApi.presignTaskFile(
                props.boardUuid,
                props.moduleUuid,
                props.taskUuid ?? '',
                draft.value!.uuid,
                request,
              )
            : filesApi.presignPentestFile(
                props.boardUuid,
                props.moduleUuid,
                draft.value!.uuid,
                request,
              ),
        (fileUuid) =>
          props.resourceType === 'TASK'
            ? filesApi.completeTaskFile(
                props.boardUuid,
                props.moduleUuid,
                props.taskUuid ?? '',
                draft.value!.uuid,
                fileUuid,
              )
            : filesApi.completePentestFile(
                props.boardUuid,
                props.moduleUuid,
                draft.value!.uuid,
                fileUuid,
              ),
        abort.signal,
      )
      draft.value.files = [...draft.value.files.filter((item) => item.uuid !== pending.uuid), pending]
      publishState()
    }
    await poll()
  } catch (caught) {
    if (!(caught instanceof DraftCancelledError)) error.value = caught
  } finally {
    uploading.value = false
  }
}

async function retryScan() {
  error.value = null
  await poll()
}

async function cancel() {
  abort?.abort()
  const current = draft.value
  draft.value = null
  emit('update:modelValue', null)
  emit('ready', false)
  if (current) {
    try {
      if (props.resourceType === 'TASK') {
        await filesApi.discardTaskDraft(props.boardUuid, props.moduleUuid, props.taskUuid ?? '', current.uuid)
      } else {
        await filesApi.discardPentestDraft(props.boardUuid, props.moduleUuid, current.uuid)
      }
    } catch {
      /* Draft may already be consumed/expired; no fake success is shown. */
    }
  }
}

function statusBadge(file: PendingFile) {
  switch (file.status) {
    case 'READY':
    case 'ATTACHED':
      return 'ok' as const
    case 'REJECTED':
    case 'EXPIRED':
      return 'broken' as const
    case 'VALIDATING':
      return 'running' as const
    default:
      return 'unknown' as const
  }
}

onUnmounted(() => abort?.abort())
</script>

<template>
  <section class="space-y-3 border border-border p-3" aria-labelledby="uploader-heading">
    <div>
      <p id="uploader-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Submission files
      </p>
      <p class="mt-1 text-xs text-muted-foreground">
        {{ policy.minCount }}–{{ policy.maxCount }} file(s), up to {{ policy.maxSizeBytes }} bytes each. Files go
        directly to quarantine storage with a client SHA-256; scanning is server-authoritative.
      </p>
    </div>

    <ErrorAlert v-if="error" :error="error" title="File upload failed" />

    <EmptyState
      v-if="files.length === 0"
      title="No files uploaded"
      description="Choose files to create the caller-bound draft. A rejected/unscanned file can never be finalized."
    />

    <div v-else class="space-y-2">
      <div v-for="file in files" :key="file.uuid" class="border border-border px-3 py-2 text-sm">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="break-all">{{ file.filename }}</span>
          <StatusBadge :label="file.status" :tone="statusBadge(file)" />
        </div>
        <p class="text-xs text-muted-foreground">{{ file.contentType }} · {{ file.size }} bytes · SHA-256 {{ file.sha256 }}</p>
        <p v-if="file.rejectionCode" class="text-xs text-love">Rejected: {{ file.rejectionCode }}</p>
      </div>
    </div>

    <p v-if="draft" class="text-xs text-muted-foreground">
      Draft expires {{ formatDateTime(draft.expiresAt) }}. Files: {{ readyFiles }}/{{ files.length }} ready.
    </p>

    <div class="flex flex-wrap gap-2">
      <Input
        :key="inputKey"
        type="file"
        multiple
        :disabled="uploading || polling || files.length >= policy.maxCount"
        :accept="policy.allowedContentTypes.join(',')"
        aria-label="Choose submission files"
        @change="onFiles"
      />
      <Button v-if="polling" variant="outline" size="sm" disabled>Scanning…</Button>
      <Button v-else-if="rejected.length > 0" variant="outline" size="sm" @click="retryScan">Refresh scan status</Button>
      <Button v-if="draft" variant="destructive" size="sm" :disabled="uploading" @click="cancel">Cancel draft</Button>
    </div>
    <p v-if="policy.required && !allReady" class="text-xs text-gold">
      Finalization requires {{ policy.minCount }} READY file(s); this draft is not ready yet.
    </p>
  </section>
</template>