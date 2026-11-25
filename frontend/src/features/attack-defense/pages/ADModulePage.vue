<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import EmptyState from '@/components/layout/EmptyState.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { adApi } from '@/features/attack-defense/api'
import { formatDateTime } from '@/shared/lib/format'
import type { ADProgress, ADRepositoriesResponse, ADRun, ADRunSummary, Board, Module } from '@/shared/types/api'

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))
const moduleUuid = computed(() => String(route.params.moduleUuid))

const board = ref<Board | null>(null)
const module = ref<Module | null>(null)
const repositories = ref<ADRepositoriesResponse | null>(null)
const progress = ref<ADProgress | null>(null)
const runs = ref<ADRunSummary[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const actionError = ref<unknown>(null)
const runKind = ref<'EXPLOIT' | 'PATCH'>('EXPLOIT')
const commitSha = ref('')
const submitting = ref(false)
const selectedRun = ref<ADRun | null>(null)
const logs = ref<string[]>([])
const logsTruncated = ref(false)
const logsLoading = ref(false)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let alive = true
let pollCount = 0

async function load() {
  loading.value = true
  error.value = null
  try {
    const [loadedBoard, loadedModule, repos, prog, runPage] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getModule(boardUuid.value, moduleUuid.value),
      adApi.getRepositories(boardUuid.value, moduleUuid.value),
      adApi.getProgress(boardUuid.value, moduleUuid.value),
      adApi.listRuns(boardUuid.value, moduleUuid.value, 100, 0),
    ])
    board.value = loadedBoard
    module.value = loadedModule
    repositories.value = repos
    progress.value = prog
    runs.value = runPage.items
    const active = runs.value.find((run) => run.status === 'QUEUED' || run.status === 'RUNNING')
    if (active) schedulePoll()
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function schedulePoll() {
  if (pollTimer) clearTimeout(pollTimer)
  if (!alive || pollCount >= 20) return
  pollCount += 1
  pollTimer = setTimeout(async () => {
    if (!alive) return
    try {
      const page = await adApi.listRuns(boardUuid.value, moduleUuid.value, 100, 0)
      runs.value = page.items
      if (selectedRun.value) {
        selectedRun.value = await adApi.getRun(boardUuid.value, moduleUuid.value, selectedRun.value.uuid)
      }
      if (runs.value.some((run) => run.status === 'QUEUED' || run.status === 'RUNNING')) schedulePoll()
    } catch {
      /* bounded polling; errors remain visible via explicit actions */
    }
  }, 5000)
}

async function createRun() {
  submitting.value = true
  actionError.value = null
  try {
    await adApi.createRun(boardUuid.value, moduleUuid.value, {
      kind: runKind.value,
      ...(commitSha.value.trim() ? { commitSha: commitSha.value.trim() } : {}),
    })
    commitSha.value = ''
    pollCount = 0
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    submitting.value = false
  }
}

async function openRun(run: ADRunSummary) {
  logs.value = []
  logsTruncated.value = false
  try {
    selectedRun.value = await adApi.getRun(boardUuid.value, moduleUuid.value, run.uuid)
  } catch (caught) {
    actionError.value = caught
  }
}

async function loadLogs() {
  if (!selectedRun.value) return
  logsLoading.value = true
  actionError.value = null
  try {
    const response = await adApi.getRunLogs(boardUuid.value, moduleUuid.value, selectedRun.value.uuid)
    logs.value = response.lines
    logsTruncated.value = response.truncated ?? false
  } catch (caught) {
    actionError.value = caught
  } finally {
    logsLoading.value = false
  }
}

onUnmounted(() => {
  alive = false
  if (pollTimer) clearTimeout(pollTimer)
})

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 text-sm">
      <RouterLink :to="`/boards/${boardUuid}`" class="text-muted-foreground underline decoration-border underline-offset-4">‹ {{ board?.name ?? 'Board' }}</RouterLink>
    </div>
    <PageHeader :title="module?.name ?? 'Attack & Defense'" description="Exploit and patch are separate verdicts. Logs are bounded and sanitized." />
    <LoadingState v-if="loading" :rows="6" />
    <ErrorAlert v-else-if="error" :error="error" title="Could not load A&D module" />
    <div v-else class="space-y-6">
      <ErrorAlert v-if="actionError" :error="actionError" title="A&D action failed" />

      <section v-if="repositories" class="grid gap-4 border border-border p-4 md:grid-cols-2">
        <div>
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Source repository</p>
          <a :href="repositories.source.webUrl" target="_blank" rel="noopener noreferrer" class="break-all text-sm underline decoration-border underline-offset-4">{{ repositories.source.repositoryUrl }}</a>
          <p class="text-xs text-muted-foreground">Branch {{ repositories.source.branch }} · {{ repositories.source.lastCommit?.sha ?? 'no commit' }}</p>
        </div>
        <div>
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Exploit repository</p>
          <a :href="repositories.exploit.webUrl" target="_blank" rel="noopener noreferrer" class="break-all text-sm underline decoration-border underline-offset-4">{{ repositories.exploit.repositoryUrl }}</a>
          <p class="text-xs text-muted-foreground">Branch {{ repositories.exploit.branch }} · {{ repositories.exploit.lastCommit?.sha ?? 'no commit' }}</p>
        </div>
      </section>

      <section v-if="progress" class="border border-border p-4">
        <div class="grid gap-3 text-sm sm:grid-cols-4">
          <div><p class="text-xs text-muted-foreground">Total vulnerabilities</p><p class="text-lg font-semibold">{{ progress.totalVulnerabilities }}</p></div>
          <div><p class="text-xs text-muted-foreground">Found</p><p class="text-lg font-semibold">{{ progress.foundCount }}</p></div>
          <div><p class="text-xs text-muted-foreground">Patched</p><p class="text-lg font-semibold">{{ progress.patchedCount }}</p></div>
          <div><p class="text-xs text-muted-foreground">Score</p><p class="text-lg font-semibold">{{ progress.score }}</p></div>
        </div>
        <div class="mt-3 space-y-1">
          <EmptyState v-if="progress.vulnerabilities.length === 0" title="No discovered vulnerabilities" description="Hidden vulnerabilities are represented only by the total count." />
          <div v-for="vulnerability in progress.vulnerabilities" :key="vulnerability.uuid" class="flex flex-wrap items-center gap-2 border-b border-border py-1 text-sm last:border-0">
            <StatusBadge :status="vulnerability.status" />
            <span>{{ vulnerability.title }}</span>
            <span v-if="vulnerability.currentPatchValid !== null && vulnerability.currentPatchValid !== undefined" class="text-xs text-muted-foreground">patch valid: {{ vulnerability.currentPatchValid }}</span>
          </div>
        </div>
      </section>

      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Runs</h2>
        </div>
        <div class="space-y-3 px-4 py-4">
          <form class="flex flex-wrap items-end gap-2" @submit.prevent="createRun">
            <div class="min-w-40">
              <FormField label="Kind" for-id="ad-run-kind">
                <Select v-model="runKind">
                  <SelectTrigger id="ad-run-kind"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="EXPLOIT">EXPLOIT</SelectItem>
                    <SelectItem value="PATCH">PATCH</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </div>
            <div class="min-w-64 flex-1">
              <FormField label="Commit SHA (optional)" for-id="ad-commit" hint="Absent means the configured HEAD.">
                <Input id="ad-commit" v-model="commitSha" autocomplete="off" spellcheck="false" />
              </FormField>
            </div>
            <Button type="submit" :disabled="submitting">{{ submitting ? 'Queuing…' : 'Run' }}</Button>
          </form>

          <EmptyState v-if="runs.length === 0" title="No runs" description="Push to a configured repository or start a run." />
          <div v-else class="space-y-1">
            <button
              v-for="run in runs"
              :key="run.uuid"
              class="flex w-full flex-wrap items-center justify-between gap-2 border-b border-border py-2 text-left text-sm last:border-0"
              @click="openRun(run)"
            >
              <span>
                <StatusBadge :status="run.status" />
                <span class="ml-2">{{ run.kind }}</span>
              </span>
              <span class="text-xs text-muted-foreground">{{ run.commitSha.slice(0, 12) }} · {{ formatDateTime(run.createdAt) }}</span>
            </button>
          </div>

          <div v-if="selectedRun" class="space-y-2 border border-border p-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <p class="text-sm font-semibold">{{ selectedRun.kind }} · {{ selectedRun.status }}</p>
              <Button variant="outline" size="sm" :disabled="logsLoading" @click="loadLogs">{{ logsLoading ? 'Loading…' : 'Load logs' }}</Button>
            </div>
            <p v-if="selectedRun.summary" class="text-xs text-muted-foreground">{{ JSON.stringify(selectedRun.summary) }}</p>
            <p class="text-xs text-muted-foreground">
              Hidden test criteria are not rendered. Only the backend-sanitized summary, status and logs are shown.
            </p>
            <pre v-if="logs.length > 0" class="max-h-72 overflow-auto border border-border bg-base p-3 text-xs">{{ logs.join('\n') }}</pre>
            <p v-if="logsTruncated" class="text-xs text-gold">Logs are truncated by the backend.</p>
          </div>
        </div>
      </section>
    </div>
  </PageContainer>
</template>