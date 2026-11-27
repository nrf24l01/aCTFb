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
    class="flex flex-wrap items-center justify-between gap-3 bg-black px-4 py-3 hover:bg-muted focus-visible:outline-offset-4"
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