<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { filesApi } from '@/features/files/api'
import { hashAndUploadFile } from '@/features/files/draft'
import type { AdminTask, FileRef, PendingFile } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  moduleUuid: string
  task: AdminTask
}>()

const emit = defineEmits<{ (event: 'changed'): void }>()

const files = ref<FileRef[]>([...props.task.attachments])
const uploading = ref(false)
const error = ref<unknown>(null)
const status = ref<string | null>(null)
const inputKey = ref(0)
const deleteTarget = ref<FileRef | null>(null)
const deleting = ref(false)

async function refresh() {
  const fresh = await boardsApi.getAdminTask(props.boardUuid, props.moduleUuid, props.task.uuid)
  files.value = fresh.attachments
  emit('changed')
}

async function onFiles(event: Event) {
  const target = event.target as HTMLInputElement
  const selected = Array.from(target.files ?? [])
  target.value = ''
  inputKey.value += 1
  if (selected.length === 0) return

  uploading.value = true
  error.value = null
  status.value = null
  try {
    for (const file of selected) {
      if (file.size < 1 || file.size > 1_073_741_824) throw new Error('Task attachment size must be 1..1073741824 bytes.')
      const pending: PendingFile = await hashAndUploadFile(
        file,
        (request) =>
          filesApi.adminPresignTaskAttachment(props.boardUuid, props.moduleUuid, props.task.uuid, request),
        (fileUuid) =>
          filesApi.adminCompleteTaskAttachment(
            props.boardUuid,
            props.moduleUuid,
            props.task.uuid,
            fileUuid,
          ),
      )
      status.value = `Uploaded ${pending.filename}; status ${pending.status}.`
      if (pending.status === 'REJECTED') {
        error.value = new Error(`Rejected: ${pending.rejectionCode ?? 'server rejected the file'}`)
        break
      }
      // The task attachment endpoint has no per-file status GET. Poll the
      // task resource until the immutable attachment metadata appears.
      for (let attempt = 0; attempt < 20; attempt += 1) {
        await new Promise((resolve) => setTimeout(resolve, 1500))
        await refresh()
        if (files.value.some((item) => item.uuid === pending.uuid)) {
          status.value = `Attachment ${pending.filename} is ready.`
          break
        }
      }
    }
  } catch (caught) {
    error.value = caught
  } finally {
    uploading.value = false
  }
}

async function download(file: FileRef) {
  error.value = null
  try {
    const response = await filesApi.adminDownloadTaskAttachment(
      props.boardUuid,
      props.moduleUuid,
      props.task.uuid,
      file.uuid,
    )
    window.open(response.url, '_blank', 'noopener,noreferrer')
  } catch (caught) {
    error.value = caught
  }
}

async function remove() {
  if (!deleteTarget.value) return
  deleting.value = true
  error.value = null
  try {
    await filesApi.adminDeleteTaskAttachment(
      props.boardUuid,
      props.moduleUuid,
      props.task.uuid,
      deleteTarget.value.uuid,
    )
    deleteTarget.value = null
    await refresh()
  } catch (caught) {
    error.value = caught
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section class="space-y-3 border border-border p-4" aria-labelledby="task-attachments-heading">
    <div>
      <h2 id="task-attachments-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Task attachments
      </h2>
      <p class="mt-1 text-xs text-muted-foreground">
        Task-scoped presigned pipeline only; there is no generic task upload. Files are attached only after scanning.
      </p>
    </div>
    <ErrorAlert v-if="error" :error="error" title="Attachment upload failed" />
    <p v-if="status" class="text-xs text-muted-foreground">{{ status }}</p>
    <LoadingState v-if="uploading" :rows="1" />
    <EmptyState v-if="files.length === 0" title="No attachments" description="Upload task-provided files for participants." />
    <div v-else class="space-y-2">
      <div v-for="file in files" :key="file.uuid" class="flex flex-wrap items-center justify-between gap-2 border border-border px-3 py-2 text-sm">
        <span>{{ file.filename }} · {{ file.size }} bytes · {{ file.contentType }}</span>
        <div class="flex gap-1">
          <Button variant="outline" size="sm" @click="download(file)">Download</Button>
          <Button variant="destructive" size="sm" @click="deleteTarget = file">Delete</Button>
        </div>
      </div>
    </div>
    <Input
      :key="inputKey"
      type="file"
      multiple
      :disabled="uploading"
      aria-label="Choose task attachments"
      @change="onFiles"
    />
    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete task attachment"
      description="The backend detaches the file and schedules deletion when safe. This cannot be undone."
      confirm-label="Delete attachment"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="remove"
    />
  </section>
</template>