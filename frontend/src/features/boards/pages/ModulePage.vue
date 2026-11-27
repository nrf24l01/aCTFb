<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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
import TaskProgress from '@/features/boards/components/TaskProgress.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import type { Board, Module, TaskCompletion, TaskListItem } from '@/shared/types/api'

type CompletionSelection = 'ALL' | TaskCompletion

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))

const board = ref<Board | null>(null)
const module = ref<Module | null>(null)
const tasks = ref<TaskListItem[]>([])
const tasksTotal = ref(0)
const tasksOffset = ref(0)
const tasksLoading = ref(false)
const tasksError = ref<unknown>(null)
const completion = ref<CompletionSelection>('ALL')
const loading = ref(true)
const error = ref<unknown>(null)
const tasksUnavailable = ref(false)

let loadId = 0
let taskRequestId = 0
let taskController: AbortController | null = null

const isPentest = computed(() => module.value?.type === 'PENTEST')
// The backend marks a staff management preview explicitly; it is not inferred
// from board.joined, because a participant row can outlive a revoked group.
const isPreview = computed(() => module.value?.preview === true)
const isTaskModule = computed(
  () => module.value?.type === 'TASKS' || module.value?.type === 'EXTENSION',
)
// Progress is an unfiltered server aggregate; it never follows the filtered
// task page total and is hidden in preview/older-server responses.
const moduleProgress = computed(() =>
  module.value?.taskProgress && !isPreview.value ? module.value.taskProgress : null,
)
const showTaskControls = computed(
  () =>
    isTaskModule.value &&
    !isPreview.value &&
    board.value?.joined === true &&
    module.value?.canParticipate !== false &&
    Boolean(module.value?.taskProgress) &&
    !tasksUnavailable.value,
)
const hasMoreTasks = computed(() => tasksOffset.value < tasksTotal.value)
const emptyTitle = computed(() => {
  if (completion.value === 'SOLVED') return 'No solved tasks'
  if (completion.value === 'UNSOLVED') return 'No unsolved tasks'
  return 'No tasks available'
})
const emptyDescription = computed(() =>
  completion.value === 'ALL'
    ? 'Tasks appear when the organizer publishes them and the module is active.'
    : undefined,
)

const filterOptions: Array<{ value: CompletionSelection; label: string }> = [
  { value: 'ALL', label: 'All' },
  { value: 'UNSOLVED', label: 'Unsolved' },
  { value: 'SOLVED', label: 'Solved' },
]

function mergeTasks(existing: TaskListItem[], incoming: TaskListItem[]): TaskListItem[] {
  const seen = new Set(existing.map((task) => task.uuid))
  return [...existing, ...incoming.filter((task) => !seen.has(task.uuid))]
}

async function loadTasks(reset: boolean, propagate: boolean) {
  if (!isTaskModule.value) return
  const requestId = ++taskRequestId
  taskController?.abort()
  const controller = new AbortController()
  taskController = controller

  if (reset) {
    tasks.value = []
    tasksTotal.value = 0
    tasksOffset.value = 0
  }
  tasksError.value = null
  tasksLoading.value = true
  const offset = reset ? 0 : tasksOffset.value
  const query = completion.value === 'ALL'
    ? { limit: 100, offset }
    : { limit: 100, offset, completion: completion.value }

  try {
    const page = await boardsApi.listTasks(
      boardUuid.value,
      moduleUuid.value,
      query,
      controller.signal,
    )
    if (requestId !== taskRequestId) return
    tasks.value = reset ? page.items : mergeTasks(tasks.value, page.items)
    tasksTotal.value = page.total
    tasksOffset.value = page.offset + page.items.length
    tasksError.value = null
  } catch (caught) {
    if (requestId !== taskRequestId) return
    // A denied task list is a participation state, not a page failure: either
    // the reader is staff and the module is a preview, or the reader has not
    // joined. Anything else stays a real error.
    if (isApiError(caught) && (module.value?.preview === true || board.value?.joined === false)) {
      tasks.value = []
      tasksTotal.value = 0
      tasksOffset.value = 0
      tasksError.value = null
      tasksUnavailable.value = true
      return
    }
    if (propagate) throw caught
    tasksError.value = caught
  } finally {
    if (requestId === taskRequestId) tasksLoading.value = false
  }
}

async function load() {
  const id = ++loadId
  taskRequestId += 1
  taskController?.abort()
  taskController = null

  loading.value = true
  error.value = null
  tasksUnavailable.value = false
  tasksError.value = null
  tasksLoading.value = false
  completion.value = 'ALL'
  tasks.value = []
  tasksTotal.value = 0
  tasksOffset.value = 0
  board.value = null
  module.value = null

  try {
    const [loadedBoard, loadedModule] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
    ])
    if (id !== loadId) return
    board.value = loadedBoard
    module.value = loadedModule
    if (!isTaskModule.value) return
    await loadTasks(true, true)
  } catch (caught) {
    if (id !== loadId) return
    error.value = caught
  } finally {
    if (id === loadId) loading.value = false
  }
}

function selectCompletion(next: CompletionSelection) {
  if (completion.value === next) return
  completion.value = next
  void loadTasks(true, false)
}

function retryTasks() {
  void loadTasks(tasks.value.length === 0, false)
}

function loadMoreTasks() {
  if (tasksLoading.value || !hasMoreTasks.value) return
  void loadTasks(false, false)
}

watch([boardUuid, moduleUuid], () => void load(), { immediate: true })
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
        <template v-else>
          <div v-if="moduleProgress" class="mb-3 max-w-96">
            <TaskProgress
              :solved-tasks="moduleProgress.solvedTasks"
              :total-tasks="moduleProgress.totalTasks"
              label="Task progress"
              compact
            />
          </div>
          <div
            v-if="showTaskControls"
            class="mb-3 flex flex-wrap gap-2"
            role="group"
            aria-label="Task completion filter"
          >
            <button
              v-for="option in filterOptions"
              :key="option.value"
              type="button"
              class="min-h-11 min-w-11 border border-transparent px-3 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              :class="
                completion === option.value
                  ? 'bg-secondary text-foreground shadow-[inset_0_-2px_0_0_var(--subtle)]'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground'
              "
              :aria-pressed="completion === option.value"
              @click="selectCompletion(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
          <LoadingState v-if="tasksLoading && tasks.length === 0" :rows="3" />
          <ErrorAlert v-if="tasksError" :error="tasksError" title="Could not load tasks">
            <Button variant="ghost" class="mt-2" @click="retryTasks">Retry</Button>
          </ErrorAlert>
          <EmptyState
            v-if="!tasksLoading && !tasksError && tasks.length === 0"
            :title="emptyTitle"
            :description="emptyDescription"
          />
          <div v-if="tasks.length > 0" class="workspace-cards workspace-cards--tasks" data-slot="module-tasks">
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
          <Button
            v-if="hasMoreTasks"
            variant="outline"
            class="mt-3"
            :disabled="tasksLoading"
            @click="loadMoreTasks"
          >
            {{ tasksLoading ? 'Loading…' : 'Load more' }}
          </Button>
        </template>
      </section>
    </template>
  </PageContainer>
</template>
