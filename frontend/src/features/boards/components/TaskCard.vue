<script setup lang="ts">
import StatusPill from '@/components/layout/StatusPill.vue'
import { Badge } from '@/components/ui/badge'
import { taskTypeLabel } from '@/features/boards/format'
import type { TaskListItem } from '@/shared/types/api'

defineProps<{ task: TaskListItem; boardUuid: string; moduleUuid: string }>()
</script>

<template>
  <RouterLink
    :to="`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${task.uuid}`"
    class="flex flex-wrap items-center justify-between gap-3 px-4 py-3 focus-visible:outline-offset-4"
    :class="task.solved ? 'task-card--solved' : 'bg-black hover:bg-muted'"
  >
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <h3 class="truncate font-semibold">{{ task.title }}</h3>
        <StatusPill :status="task.status" />
      </div>
      <div class="mt-2 flex flex-wrap gap-2">
        <Badge variant="outline" class="rounded-none">{{ task.category }}</Badge>
        <Badge variant="outline" class="rounded-none">{{ taskTypeLabel(task.type) }}</Badge>
      </div>
    </div>
    <div class="text-right">
      <p class="font-semibold">{{ task.points }}</p>
      <p class="text-xs text-muted-foreground">points</p>
    </div>
  </RouterLink>
</template>

<style scoped>
/*
 * Only solved cards leave the neutral black surface. The existing success ink
 * is mixed into black so the card stays quiet; there is no new border, outline
 * or motion, and the Solved pill remains the only status icon.
 */
.task-card--solved {
  background-color: color-mix(in srgb, var(--status-success-ink) 8%, black);
}

.task-card--solved:hover {
  background-color: color-mix(in srgb, var(--status-success-ink) 12%, black);
}
</style>