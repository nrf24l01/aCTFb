<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ChevronLeftIcon } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import type { Board, Module, TaskListItem } from '@/shared/types/api'

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))

const board = ref<Board | null>(null)
const module = ref<Module | null>(null)
const tasks = ref<TaskListItem[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const tasksUnavailable = ref(false)
const isPentest = computed(() => module.value?.type === 'PENTEST')
// The backend marks a staff management preview explicitly; it is not inferred
// from board.joined, because a participant row can outlive a revoked group.
const isPreview = computed(() => module.value?.preview === true)

async function load() {
  loading.value = true
  error.value = null
  tasksUnavailable.value = false
  try {
    const [loadedBoard, loadedModule] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
    ])
    board.value = loadedBoard
    module.value = loadedModule
    if (loadedModule.type !== 'TASKS') {
      tasks.value = []
      return
    }
    try {
      tasks.value = (await boardsApi.listTasks(boardUuid.value, moduleUuid.value, { limit: 100 })).items
    } catch (caught) {
      // A denied task list is a participation state, not a page failure: either
      // the reader is staff and the module is a preview, or the reader has not
      // joined. Anything else stays a real error.
      if (isApiError(caught) && (loadedModule.preview === true || loadedBoard.joined === false)) {
        tasks.value = []
        tasksUnavailable.value = true
        return
      }
      throw caught
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
      <RouterLink
        :to="`/boards/${boardUuid}`"
        class="inline-flex items-center gap-1 text-muted-foreground underline decoration-border underline-offset-4"
      >
        <ChevronLeftIcon aria-hidden="true" class="size-3.5 shrink-0" />
        {{ board?.name ?? 'Board' }}
      </RouterLink>
    </div>

    <LoadingState v-if="loading" :rows="5" />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load module">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="module">
      <PageHeader :title="module.name">
        <template #description>
          <!-- Sanitised by renderMarkdown; description-only, same subtitle slot. -->
          <div
            v-if="module.description"
            class="markdown-body markdown-body-inline"
            v-html="renderMarkdown(module.description)"
          />
          <span v-else>Tasks module</span>
        </template>
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

      <Alert v-if="isPreview" class="mb-6 rounded-none border-gold/60 bg-black">
        <AlertTitle>Organizer preview</AlertTitle>
        <AlertDescription>
          You are viewing this module as staff without joining the board. Submissions and instance actions need
          participation.
        </AlertDescription>
      </Alert>

      <section v-if="isPentest" class="mb-6 space-y-3" aria-labelledby="pentest-heading">
        <h2 id="pentest-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Pentest lab
        </h2>
        <p class="text-sm text-muted-foreground">
          Start the approved lab environment and submit your report. The environment expires with the module/board.
        </p>
        <Button as-child>
          <RouterLink :to="`/boards/${boardUuid}/modules/${moduleUuid}/pentest`">Open Pentest lab</RouterLink>
        </Button>
      </section>

      <section v-if="!isPentest" aria-labelledby="tasks-heading">
        <h2 id="tasks-heading" class="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Tasks
        </h2>
        <EmptyState
          v-if="tasksUnavailable"
          :title="isPreview ? 'Preview only' : 'Join the board to view tasks'"
          :description="
            isPreview
              ? 'You are viewing this module as staff. The task list needs live participant access, so it is not shown here.'
              : 'Task lists are available to participants. Open the board and join to take part.'
          "
        />
        <EmptyState
          v-else-if="tasks.length === 0"
          title="No tasks available"
          description="Tasks appear when the organizer publishes them and the module is active."
        />
        <div v-else class="workspace-cards workspace-cards--tasks" data-slot="module-tasks">
          <div class="workspace-card-grid gap-2">
            <TaskCard
              v-for="task in tasks"
              :key="task.uuid"
              :task="task"
              :board-uuid="boardUuid"
              :module-uuid="moduleUuid"
            />
          </div>
        </div>
      </section>
    </template>
  </PageContainer>
</template>