<script setup lang="ts">
import { computed } from 'vue'
import StatusBadge from './StatusBadge.vue'
import type { BoardStatus, ModuleStatus, SubmissionStatus, TaskStatus } from '@/shared/types/api'

const props = defineProps<{
  status: BoardStatus | ModuleStatus | SubmissionStatus | TaskStatus | string
}>()

const tone = computed(() => {
  switch (props.status) {
    case 'ACTIVE':
    case 'ACCEPTED':
    case 'SOLVED':
    case 'AVAILABLE':
      return 'ok' as const
    case 'SCHEDULED':
    case 'UPCOMING':
    case 'PENDING':
    case 'CHECKING':
    case 'PENDING_REVIEW':
      return 'unknown' as const
    case 'DRAFT':
    case 'DISABLED':
    case 'LOCKED':
      return 'off' as const
    case 'FINISHED':
      return 'gone' as const
    case 'ARCHIVED':
      return 'gone' as const
    case 'REJECTED':
    case 'ERROR':
      return 'broken' as const
    default:
      return 'unknown' as const
  }
})

const words: Record<string, string> = {
  DRAFT: 'Draft',
  SCHEDULED: 'Scheduled',
  ACTIVE: 'Active',
  FINISHED: 'Finished',
  ARCHIVED: 'Archived',
  DISABLED: 'Disabled',
  UPCOMING: 'Upcoming',
  LOCKED: 'Locked',
  AVAILABLE: 'Available',
  SOLVED: 'Solved',
  PENDING: 'Pending',
  CHECKING: 'Checking',
  PENDING_REVIEW: 'Needs review',
  ACCEPTED: 'Accepted',
  REJECTED: 'Rejected',
  ERROR: 'Error',
}

const label = computed(() => words[props.status] ?? props.status)
</script>

<template>
  <StatusBadge :label="label" :tone="tone" />
</template>