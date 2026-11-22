<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import { moduleFormatLabel } from '@/features/boards/format'
import TaskCard from '@/features/boards/components/TaskCard.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTime } from '@/shared/lib/format'
import type { Board, Module, TaskListItem } from '@/shared/types/api'

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))

const board = ref<Board | null>(null)
const module = ref<Module | null>(null)
const tasks = ref<TaskListItem[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const isPentest = computed(() => module.value?.type === 'PENTEST')

async function load() {
  loading.value = true
  error.value = null
  try {
    const [loadedBoard, loadedModule] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
    ])
    board.value = loadedBoard
    module.value = loadedModule
    if (loadedModule.type === 'TASKS') {
      tasks.value = (await boardsApi.listTasks(boardUuid.value, moduleUuid.value, { limit: 100 })).items
    } else {
      tasks.value = []
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 text-sm">
      <RouterLink :to="`/boards/${boardUuid}`" class="text-muted-foreground underline decoration-border underline-offset-4">
        ‹ {{ board?.name ?? 'Board' }}
      </RouterLink>
    </div>

    <LoadingState v-if="loading" :rows="5" />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load module">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="module">
      <PageHeader :title="module.name" :description="module.description || 'Tasks module'">
        <template #actions>
          <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
        </template>
      </PageHeader>

      <div class="mb-6 flex flex-wrap items-center gap-2">
        <StatusPill :status="module.status" />
        <Badge variant="outline" class="rounded-none">{{ moduleFormatLabel(module.type, module.allowedTaskTypes) }}</Badge>
        <span class="text-xs text-muted-foreground">
          {{ formatDateTime(module.startsAt) }} → {{ formatDateTime(module.endsAt) }}
        </span>
      </div>

      <section v-if="isPentest" class="mb-6 border border-border px-4 py-4" aria-labelledby="pentest-heading">
        <h2 id="pentest-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Pentest lab
        </h2>
        <p class="mt-1 text-sm text-muted-foreground">
          Start the approved lab environment and submit your report. The environment expires with the module/board.
        </p>
        <Button as-child class="mt-3">
          <RouterLink :to="`/boards/${boardUuid}/modules/${moduleUuid}/pentest`">Open Pentest lab</RouterLink>
        </Button>
      </section>

      <section v-if="!isPentest" aria-labelledby="tasks-heading">
        <h2 id="tasks-heading" class="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Tasks
        </h2>
        <EmptyState
          v-if="tasks.length === 0"
          title="No tasks available"
          description="Tasks appear when the organizer publishes them and the module is active."
        />
        <div v-else class="space-y-2">
          <TaskCard
            v-for="task in tasks"
            :key="task.uuid"
            :task="task"
            :board-uuid="boardUuid"
            :module-uuid="moduleUuid"
          />
        </div>
      </section>
    </template>
  </PageContainer>
</template>