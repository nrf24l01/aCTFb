<script setup lang="ts">
import StatusPill from '@/components/layout/StatusPill.vue'
import { Badge } from '@/components/ui/badge'
import type { ModuleListItem } from '@/shared/types/api'

defineProps<{ module: ModuleListItem }>()
</script>

<template>
  <RouterLink
    :to="`/boards/${module.boardUuid}/modules/${module.uuid}`"
    class="block border border-border bg-black px-4 py-3 hover:border-muted-foreground focus-visible:outline-offset-4"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="font-semibold">{{ module.name }}</h3>
      <StatusPill :status="module.status" />
    </div>
    <div class="mt-2 flex flex-wrap gap-2">
      <Badge variant="outline" class="rounded-none">{{ module.type }}</Badge>
      <Badge v-for="taskType in module.allowedTaskTypes" :key="taskType" variant="outline" class="rounded-none">
        {{ taskType }}
      </Badge>
    </div>
  </RouterLink>
</template>