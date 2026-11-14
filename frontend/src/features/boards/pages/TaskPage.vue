<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import FlagSubmitForm from '@/features/boards/components/FlagSubmitForm.vue'
import ManualSubmissionForm from '@/features/boards/components/ManualSubmissionForm.vue'
import InstancePanel from '@/features/instances/components/InstancePanel.vue'
import SubmissionHistory from '@/features/boards/components/SubmissionHistory.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { filesApi } from '@/features/files/api'
import type { FileRef, Module, Submission, Task } from '@/shared/types/api'

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))
const taskUuid = computed(() => String(route.params.taskUuid))

const task = ref<Task | null>(null)
const module = ref<Module | null>(null)
const submissions = ref<Submission[]>([])
const loading = ref(true)
const historyLoading = ref(false)
const error = ref<unknown>(null)
const historyError = ref<unknown>(null)

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
  try {
    const [loadedModule, loadedTask] = await Promise.all([
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
      boardsApi.getTask(boardUuid.value, moduleUuid.value, taskUuid.value),
    ])
    module.value = loadedModule
    task.value = loadedTask
    await loadHistory()
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
    loadHistory(),
  ])
}

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 flex flex-wrap gap-x-4 gap-y-1 text-sm">
      <RouterLink :to="`/boards/${boardUuid}`" class="text-muted-foreground underline decoration-border underline-offset-4">
        ‹ Board
      </RouterLink>
      <RouterLink
        :to="`/boards/${boardUuid}/modules/${moduleUuid}`"
        class="text-muted-foreground underline decoration-border underline-offset-4"
      >
        {{ module?.name ?? 'Module' }}
      </RouterLink>
    </div>

    <LoadingState v-if="loading" :rows="6" />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load task">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="task">
      <PageHeader :title="task.title" :description="`${task.category} · ${task.points} points`">
        <template #actions>
          <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
        </template>
      </PageHeader>

      <div class="mb-6 flex flex-wrap items-center gap-2">
        <StatusPill :status="task.status" />
        <Badge variant="outline" class="rounded-none">{{ task.type }}</Badge>
        <Badge v-if="task.solved" variant="secondary" class="rounded-none">Solved</Badge>
      </div>

      <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div class="space-y-6">
          <section class="border border-border" aria-labelledby="description-heading">
            <div class="border-b border-border px-4 py-3">
              <h2 id="description-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Description
              </h2>
            </div>
            <p class="whitespace-pre-wrap px-4 py-4 text-sm">{{ task.description || 'No description.' }}</p>
          </section>

          <section v-if="task.attachments.length > 0" class="border border-border" aria-labelledby="attachments-heading">
            <div class="border-b border-border px-4 py-3">
              <h2 id="attachments-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Task files
              </h2>
            </div>
            <ul class="space-y-1 px-4 py-4 text-sm">
              <li v-for="file in task.attachments" :key="file.uuid" class="flex flex-wrap items-center gap-2">
                <span>{{ file.filename }} · {{ file.size }} bytes · {{ file.contentType }}</span>
                <Button variant="outline" size="sm" @click="downloadAttachment(file)">Download</Button>
              </li>
            </ul>
            <p class="px-4 pb-4 text-xs text-muted-foreground">
              Downloads use a short-lived authorized URL with forced attachment disposition. File bytes are never rendered by the app.
            </p>
          </section>

          <SubmissionHistory :submissions="submissions" :loading="historyLoading" @download="downloadOwnFile" />
          <ErrorAlert v-if="historyError" :error="historyError" title="Could not load submission history" />
        </div>

        <div class="space-y-4 self-start">
          <FlagSubmitForm
            v-if="task.type === 'FLAG'"
            :board-uuid="boardUuid"
            :module-uuid="moduleUuid"
            :task="task"
            @submitted="onSubmitted"
          />
          <ManualSubmissionForm
            v-else-if="task.type === 'MANUAL'"
            :board-uuid="boardUuid"
            :module-uuid="moduleUuid"
            :task="task"
            @submitted="onSubmitted"
          />
          <p v-else class="border border-border px-4 py-3 text-sm text-muted-foreground">
            This task mechanic is not supported by the generic task submit UI. Use the module-specific AD/Pentest or instance workflow.
          </p>
          <InstancePanel
            v-if="task.type === 'INSTANCE' || task.instance.required"
            :board-uuid="boardUuid"
            :module-uuid="moduleUuid"
            :task="task"
            @changed="load"
          />
        </div>
      </div>
    </template>
  </PageContainer>
</template>