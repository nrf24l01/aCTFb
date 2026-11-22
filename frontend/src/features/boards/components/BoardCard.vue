<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import StatusPill from '@/components/layout/StatusPill.vue'
import { accessModeLabel, participationLabel } from '@/features/boards/accessMode'
import { formatDateTime, plural } from '@/shared/lib/format'
import type { BoardListItem } from '@/shared/types/api'

const props = defineProps<{ board: BoardListItem }>()

const schedule = computed(() => {
  if (!props.board.startsAt && !props.board.endsAt) return 'No schedule'
  return `${formatDateTime(props.board.startsAt)} → ${formatDateTime(props.board.endsAt)}`
})
</script>

<template>
  <RouterLink :to="`/boards/${board.uuid}`" class="block focus-visible:outline-offset-4">
    <Card class="rounded-none border-border bg-black shadow-none transition-colors hover:border-muted-foreground">
      <CardHeader class="border-b border-border">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <CardTitle class="text-base">{{ board.name }}</CardTitle>
          <StatusPill :status="board.status" />
        </div>
        <p class="text-xs text-muted-foreground">{{ board.slug }}</p>
      </CardHeader>
      <CardContent class="space-y-3 pt-4">
        <div class="flex flex-wrap gap-2">
          <Badge variant="outline" class="rounded-none">
            {{ participationLabel(board.participationMode) }}
          </Badge>
          <Badge variant="outline" class="rounded-none">
            {{ accessModeLabel(board.accessMode) }}
          </Badge>
          <Badge variant="outline" class="rounded-none">
            {{ board.rating.visibility === 'HIDDEN' ? 'Rating hidden' : 'Rating public' }}
          </Badge>
          <Badge v-if="board.joined" variant="secondary" class="rounded-none">Joined</Badge>
        </div>
        <p class="text-xs text-muted-foreground">
          {{ plural(board.modulesCount, 'module') }} · {{ board.moduleTypes.join(', ') || 'No modules yet' }}
        </p>
        <p class="text-xs text-muted-foreground">{{ schedule }}</p>
      </CardContent>
    </Card>
  </RouterLink>
</template>