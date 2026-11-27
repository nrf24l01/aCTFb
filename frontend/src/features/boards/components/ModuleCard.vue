<script setup lang="ts">
import StatusPill from '@/components/layout/StatusPill.vue'
import { moduleFormatLabel } from '@/features/boards/format'
import { Badge } from '@/components/ui/badge'
import type { ModuleListItem } from '@/shared/types/api'

defineProps<{ module: ModuleListItem }>()
</script>

<template>
  <RouterLink
    :to="`/boards/${module.boardUuid}/modules/${module.uuid}`"
    class="block bg-black px-4 py-3 transition-colors hover:bg-muted focus-visible:outline-offset-4"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="font-semibold">{{ module.name }}</h3>
      <StatusPill :status="module.status" />
    </div>
    <div class="mt-2 flex flex-wrap gap-2">
      <Badge variant="outline" class="rounded-none">{{ moduleFormatLabel(module.type, module.allowedTaskTypes) }}</Badge>
    </div>
  </RouterLink>
</template>