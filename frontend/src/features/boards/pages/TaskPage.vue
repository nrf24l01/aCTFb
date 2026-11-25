<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import FlagSubmitForm from '@/features/boards/components/FlagSubmitForm.vue'
import ManualSubmissionForm from '@/features/boards/components/ManualSubmissionForm.vue'
import InstancePanel from '@/features/instances/components/InstancePanel.vue'
import SubmissionHistory from '@/features/boards/components/SubmissionHistory.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { filesApi } from '@/features/files/api'
import { canManageModuleTasks } from '@/features/boards/permissions'
import { taskTypeLabel } from '@/features/boards/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import { formatBytes } from '@/shared/lib/format'
import { useSession } from '@/features/auth/session'
import type { Board, BoardPermissionsResponse, FileRef, Module, Submission, Task } from '@/shared/types/api'

const route = useRoute()
const { state } = useSession()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))
const taskUuid = computed(() => String(route.params.taskUuid))

const task = ref<Task | null>(null)
const board = ref<Board | null>(null)
const module = ref<Module | null>(null)
const permissions = ref<BoardPermissionsResponse | null>(null)
const submissions = ref<Submission[]>([])
const loading = ref(true)
const historyLoading = ref(false)
const error = ref<unknown>(null)
const historyError = ref<unknown>(null)

const isAuthenticated = computed(() => state.status === 'authenticated')
// Staff read an authorized module/task as an organizer preview without joining.
// Own-submission history and every participation-gated action stay denied for
// them, so the page must not call those endpoints or show fake attempts.
const isParticipant = computed(() => board.value?.joined === true)
const isPreview = computed(() => board.value !== null && board.value.joined === false)
const canEdit = computed(
  () =>
    (state.user?.roles.includes('admin') ?? false) ||
    canManageModuleTasks(permissions.value, moduleUuid.value),
)
const description = computed(() => renderMarkdown(task.value?.description ?? ''))
const isManualReview = computed(() => task.value?.type === 'MANUAL' || task.value?.type === 'INSTANCE')

async function loadHistory() {
  historyLoading.value = true
  historyError.value = null
  try {
    const page = await boardsApi.listMySubmissions(boardUuid.value, moduleUuid.value, taskUuid.value, {
      limit: 100,
    })
    submissions.value = page.items
  } catch (caught) {
    historyError.value = caught
  } finally {
    historyLoading.value = false
  }
}

async function load() {
  loading.value = true
  error.value = null
  historyError.value = null
  try {
    const [loadedBoard, loadedModule, loadedTask] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
      boardsApi.getTask(boardUuid.value, moduleUuid.value, taskUuid.value),
    ])
    board.value = loadedBoard
    module.value = loadedModule
    task.value = loadedTask
    try {
      permissions.value = await boardsApi.getMyPermissions(boardUuid.value)
    } catch {
      permissions.value = null
    }
    if (loadedBoard.joined) {
      await loadHistory()
    } else {
      submissions.value = []
      historyLoading.value = false
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function downloadAttachment(file: FileRef) {
  if (!task.value) return
  try {
    const response = await filesApi.downloadTaskAttachment(
      boardUuid.value,
      moduleUuid.value,
      task.value.uuid,
      file.uuid,
    )
    window.open(response.url, '_blank', 'noopener,noreferrer')
  } catch (caught) {
    historyError.value = caught
  }
}

async function downloadOwnFile(submissionUuid: string, file: FileRef) {
  if (!task.value) return
  try {
    const response = await filesApi.downloadOwnTaskSubmissionFile(
      boardUuid.value,
      moduleUuid.value,
      task.value.uuid,
      submissionUuid,
      file.uuid,
    )
    window.open(response.url, '_blank', 'noopener,noreferrer')
  } catch (caught) {
    historyError.value = caught
  }
}

async function onSubmitted() {
  await Promise.all([
    boardsApi
      .getTask(boardUuid.value, moduleUuid.value, taskUuid.value)
      .then((fresh) => {
        task.value = fresh
      })
      .catch(() => undefined),
    isParticipant.value ? loadHistory() : Promise.resolve(),
  ])
}

void load()
</script>

<template>
  <PageContainer>
    <nav class="mb-4 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm" aria-label="Breadcrumb">
      <RouterLink :to="`/boards/${boardUuid}`" class="text-muted-foreground underline decoration-border underline-offset-4">
        Board
      </RouterLink>
      <span class="text-muted-foreground">/</span>
      <RouterLink
        :to="`/boards/${boardUuid}/modules/${moduleUuid}`"
        class="text-muted-foreground underline decoration-border underline-offset-4"
      >
        {{ module?.name ?? 'Module' }}
      </RouterLink>
      <span class="text-muted-foreground">/</span>
      <span>{{ task?.title ?? 'Task' }}</span>
    </nav>

    <LoadingState v-if="loading" :rows="6" />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load task">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="task">
      <header class="mb-4 border-b border-border pb-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 class="text-2xl font-semibold tracking-tight">{{ task.title }}</h1>
            <p class="mt-1 text-sm text-muted-foreground">
              {{ task.category }} · {{ task.points }} points
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <StatusPill :status="task.status" />
            <Badge variant="outline" class="rounded-none">{{ taskTypeLabel(task.type) }}</Badge>
            <Badge v-if="task.solved" variant="secondary" class="rounded-none">Solved</Badge>
            <Button v-if="canEdit" as-child variant="outline" size="sm">
              <RouterLink :to="`/admin/boards/${boardUuid}`">Edit task</RouterLink>
            </Button>
            <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
          </div>
        </div>
      </header>

      <Alert v-if="isPreview" class="mb-4 rounded-none border-gold/60 bg-black">
        <AlertTitle>Organizer preview</AlertTitle>
        <AlertDescription>
          You are viewing this task as staff without joining the board. Submission and instance actions require
          participation.
        </AlertDescription>
      </Alert>
      <Alert v-else-if="!isAuthenticated" class="mb-4 rounded-none border-gold/60 bg-black">
        <AlertTitle>Sign in to submit</AlertTitle>
        <AlertDescription>
          <RouterLink :to="{ name: 'sign-in', query: { redirect: route.fullPath } }" class="underline underline-offset-4">Sign in</RouterLink>
          to submit an answer or start an instance.
        </AlertDescription>
      </Alert>
      <Alert v-else-if="task.status === 'LOCKED'" class="mb-4 rounded-none border-gold/60 bg-black">
        <AlertTitle>Not available yet</AlertTitle>
        <AlertDescription>The board, module or schedule does not allow submissions right now.</AlertDescription>
      </Alert>

      <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
        <div class="space-y-6">
          <section class="border border-border" aria-labelledby="description-heading">
            <div class="border-b border-border px-4 py-3">
              <h2 id="description-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Description
              </h2>
            </div>
            <!-- Sanitised by renderMarkdown. -->
            <article class="space-y-3 px-4 py-4 text-sm leading-relaxed" v-html="description" />
            <p v-if="!task.description" class="px-4 pb-4 text-sm text-muted-foreground">No description.</p>
          </section>

          <section v-if="task.attachments.length > 0" class="border border-border" aria-labelledby="attachments-heading">
            <div class="border-b border-border px-4 py-3">
              <h2 id="attachments-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Task files
              </h2>
            </div>
            <ul class="divide-y divide-border">
              <li v-for="file in task.attachments" :key="file.uuid" class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
                <span>
                  <span class="font-medium">{{ file.filename }}</span>
                  <span class="ml-2 text-xs text-muted-foreground">{{ formatBytes(file.size) }}</span>
                </span>
                <Button variant="outline" size="sm" @click="downloadAttachment(file)">Download</Button>
              </li>
            </ul>
          </section>
        </div>

        <div class="space-y-4 self-start">
          <template v-if="isParticipant">
            <FlagSubmitForm
              v-if="task.type === 'FLAG'"
              :board-uuid="boardUuid"
              :module-uuid="moduleUuid"
              :task="task"
              @submitted="onSubmitted"
            />
            <ManualSubmissionForm
              v-else-if="isManualReview"
              :board-uuid="boardUuid"
              :module-uuid="moduleUuid"
              :task="task"
              @submitted="onSubmitted"
            />
            <p v-else class="border border-border px-4 py-3 text-sm text-muted-foreground">
              Use the module-specific workflow for this task.
            </p>
            <InstancePanel
              v-if="task.type === 'INSTANCE' || task.instance.required"
              :board-uuid="boardUuid"
              :module-uuid="moduleUuid"
              :task="task"
              @changed="load"
            />
          </template>
          <p v-else class="border border-border px-4 py-3 text-sm text-muted-foreground">
            Submission and instance actions are available after joining the board.
          </p>
        </div>
      </div>

      <section v-if="isParticipant" class="mt-8" aria-labelledby="history-heading">
        <h2 id="history-heading" class="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Your submissions
        </h2>
        <SubmissionHistory :submissions="submissions" :loading="historyLoading" @download="downloadOwnFile" />
        <ErrorAlert v-if="historyError" :error="historyError" title="Could not load submission history" class="mt-3" />
      </section>
    </template>
  </PageContainer>
</template>
