<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { filesApi } from '@/features/files/api'
import { createIdempotencyKey } from '@/shared/api/idempotency'
import { formatDateTime } from '@/shared/lib/format'
import type {
  AdminSubmission,
  AdminSubmissionListResponse,
  BoardPermissionsResponse,
  FileRef,
  ModuleListItem,
  SubmissionStatus,
} from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  modules: ModuleListItem[]
  permissions: BoardPermissionsResponse | null
  canReview: boolean
}>()

const items = ref<AdminSubmission[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const moduleFilter = ref('all')
const resourceFilter = ref<'all' | 'TASK' | 'PENTEST'>('all')
const statusFilter = ref<SubmissionStatus | 'all'>('all')
const selected = ref<AdminSubmission | null>(null)
const decision = ref<'ACCEPT' | 'REJECT'>('ACCEPT')
const message = ref('')
const pending = ref(false)
const reviewError = ref<unknown>(null)
const downloadError = ref<unknown>(null)

const visibleModules = computed(() =>
  props.modules.filter((module) =>
    props.permissions?.modules.some((entry) => entry.moduleUuid === module.uuid) ||
    props.permissions?.permissions.includes('module.submissions.read'),
  ),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    const query = {
      limit: 100,
      moduleUuid: moduleFilter.value === 'all' ? undefined : moduleFilter.value,
      status: statusFilter.value === 'all' ? undefined : statusFilter.value,
    }
    if (resourceFilter.value === 'all') {
      // TASK and PENTEST are canonical but stored separately; the review UI
      // must not hide PENTEST reports behind an omitted resourceType.
      const results = await Promise.allSettled([
        boardsApi.listAdminSubmissions(props.boardUuid, { ...query, resourceType: 'TASK' }),
        boardsApi.listAdminSubmissions(props.boardUuid, { ...query, resourceType: 'PENTEST' }),
      ])
      const fulfilled = results.filter(
        (result): result is PromiseFulfilledResult<AdminSubmissionListResponse> => result.status === 'fulfilled',
      )
      if (fulfilled.length === 0) {
        const rejected = results.find((result): result is PromiseRejectedResult => result.status === 'rejected')
        throw rejected?.reason ?? new Error('No submission source answered.')
      }
      items.value = fulfilled
        .flatMap((result) => result.value.items)
        .sort((left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime())
    } else {
      const page = await boardsApi.listAdminSubmissions(props.boardUuid, {
        ...query,
        resourceType: resourceFilter.value,
      })
      items.value = page.items
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function openReview(submission: AdminSubmission, next: 'ACCEPT' | 'REJECT') {
  selected.value = submission
  decision.value = next
  message.value = ''
  reviewError.value = null
}

async function review() {
  if (!selected.value) return
  pending.value = true
  reviewError.value = null
  try {
    await boardsApi.reviewSubmission(props.boardUuid, selected.value.uuid, createIdempotencyKey(), {
      decision: decision.value,
      message: message.value.trim() || null,
    })
    selected.value = null
    await load()
  } catch (caught) {
    reviewError.value = caught
  } finally {
    pending.value = false
  }
}

async function download(submissionUuid: string, file: FileRef) {
  downloadError.value = null
  try {
    const response = await filesApi.adminDownloadSubmissionFile(props.boardUuid, submissionUuid, file.uuid)
    window.open(response.url, '_blank', 'noopener,noreferrer')
  } catch (caught) {
    downloadError.value = caught
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="review-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="review-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Submissions review</h2>
        <p class="mt-1 text-xs text-muted-foreground">
          Scoped by module.submissions.read/review. ACCEPT/REJECT only; no arbitrary score is sent.
        </p>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
    </div>
    <div class="space-y-3 px-4 py-4">
      <div class="flex flex-wrap gap-2">
        <div class="min-w-44">
          <Select v-model="moduleFilter" @update:model-value="load">
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All permitted modules</SelectItem>
              <SelectItem v-for="module in visibleModules" :key="module.uuid" :value="module.uuid">{{ module.name }}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-40">
          <Select v-model="resourceFilter" @update:model-value="load">
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Task + Pentest</SelectItem>
              <SelectItem value="TASK">Task only</SelectItem>
              <SelectItem value="PENTEST">Pentest only</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-40">
          <Select v-model="statusFilter" @update:model-value="load">
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All statuses</SelectItem>
              <SelectItem value="PENDING_REVIEW">Waiting review</SelectItem>
              <SelectItem value="ACCEPTED">Accepted</SelectItem>
              <SelectItem value="REJECTED">Rejected</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <ErrorAlert v-if="downloadError" :error="downloadError" title="Download failed" />
      <LoadingState v-if="loading" :rows="2" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load submissions" />
      <EmptyState v-else-if="items.length === 0" title="No submissions in scope" description="The list is scoped to modules you can read." />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Subject</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
              <TableHead>Files</TableHead>
              <TableHead class="text-right">Review</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="submission in items" :key="submission.uuid">
              <TableCell>
                <span class="text-sm">{{ submission.subject.name }}</span>
                <span class="block text-xs text-muted-foreground">{{ submission.subject.type }} · {{ submission.moduleUuid }}</span>
              </TableCell>
              <TableCell><StatusPill :status="submission.status" /></TableCell>
              <TableCell class="whitespace-nowrap text-xs text-muted-foreground">{{ formatDateTime(submission.createdAt) }}</TableCell>
              <TableCell class="text-xs">
                <button
                  v-for="file in submission.files"
                  :key="file.uuid"
                  class="block text-left underline decoration-border underline-offset-4"
                  @click="download(submission.uuid, file)"
                >
                  {{ file.filename }}
                </button>
                <span v-if="submission.files.length === 0" class="text-muted-foreground">—</span>
              </TableCell>
              <TableCell class="text-right">
                <div v-if="canReview && submission.status === 'PENDING_REVIEW'" class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="openReview(submission, 'ACCEPT')">Accept</Button>
                  <Button variant="destructive" size="sm" @click="openReview(submission, 'REJECT')">Reject</Button>
                </div>
                <span v-else class="text-xs text-muted-foreground">—</span>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>

    <Dialog :open="Boolean(selected)" @update:open="(open) => { if (!open) selected = null }">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>{{ decision === 'ACCEPT' ? 'Accept submission' : 'Reject submission' }}</DialogTitle>
          <DialogDescription>
            This changes acceptance only. ACCEPT awards the task points exactly once; no score override is sent.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="review">
          <ErrorAlert v-if="reviewError" :error="reviewError" title="Review failed" />
          <FormField label="Review message (optional)" for-id="review-message" hint="At most 2000 characters.">
            <Textarea id="review-message" v-model="message" rows="3" maxlength="2000" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="pending" @click="selected = null">Cancel</Button>
            <Button
              type="submit"
              :variant="decision === 'REJECT' ? 'destructive' : 'default'"
              :disabled="pending"
            >
              {{ pending ? 'Saving…' : decision === 'ACCEPT' ? 'Accept submission' : 'Reject submission' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </section>
</template>