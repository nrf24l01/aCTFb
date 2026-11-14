<script setup lang="ts">
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import { formatDateTime } from '@/shared/lib/format'
import type { FileRef, Submission } from '@/shared/types/api'

defineProps<{
  submissions: Submission[]
  loading?: boolean
}>()

const emit = defineEmits<{
  (event: 'download', submissionUuid: string, file: FileRef): void
}>()
</script>

<template>
  <section class="border border-border" aria-labelledby="history-heading">
    <div class="border-b border-border px-4 py-3">
      <h2 id="history-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Your submission history
      </h2>
    </div>
    <div v-if="loading" class="px-4 py-4 text-sm text-muted-foreground">Loading history…</div>
    <EmptyState
      v-else-if="submissions.length === 0"
      title="No submissions yet"
      description="Verdicts appear here after you submit a flag."
    />
    <div v-else class="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow class="border-border hover:bg-transparent">
            <TableHead>When</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Score</TableHead>
            <TableHead>Message</TableHead>
            <TableHead>Files</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="submission in submissions" :key="submission.uuid" class="border-border">
            <TableCell class="whitespace-nowrap text-xs text-muted-foreground">
              {{ formatDateTime(submission.createdAt) }}
            </TableCell>
            <TableCell><StatusPill :status="submission.status" /></TableCell>
            <TableCell>{{ submission.scoreAwarded ?? '—' }}</TableCell>
            <TableCell class="max-w-md whitespace-normal text-xs text-muted-foreground">
              {{ submission.message || '—' }}
            </TableCell>
            <TableCell class="text-xs">
              <div v-if="submission.files.length === 0" class="text-muted-foreground">—</div>
              <div v-else class="flex flex-col gap-1">
                <button
                  v-for="file in submission.files"
                  :key="file.uuid"
                  class="text-left underline decoration-border underline-offset-4"
                  @click="emit('download', submission.uuid, file)"
                >
                  {{ file.filename }}
                </button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </section>
</template>