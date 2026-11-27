<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import ModuleCard from '@/features/boards/components/ModuleCard.vue'
import TaskProgress from '@/features/boards/components/TaskProgress.vue'
import JoinBoardDialog from '@/features/boards/components/JoinBoardDialog.vue'
import TeamPanel from '@/features/teams/components/TeamPanel.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { useSession } from '@/features/auth/session'
import {
  canManageParticipants,
  canManageStaff,
  canReadAudit,
  hasBoardPermission,
  isBoardJoinOffered,
} from '@/features/boards/permissions'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime, plural } from '@/shared/lib/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import { accessModeLabel, participationLabel } from '@/features/boards/accessMode'
import type { Board, BoardJoinResponse, BoardMe, BoardPermissionsResponse, ModuleListItem } from '@/shared/types/api'

const route = useRoute()
const { state } = useSession()
const boardUuid = computed(() => String(route.params.boardUuid))

const board = ref<Board | null>(null)
const me = ref<BoardMe | null>(null)
const permissions = ref<BoardPermissionsResponse | null>(null)
const modules = ref<ModuleListItem[]>([])
const modulesTotal = ref(0)
const modulesOffset = ref(0)
const modulesLoading = ref(false)
const modulesError = ref<unknown>(null)
const loading = ref(true)
const error = ref<unknown>(null)
const permissionsError = ref<unknown>(null)
const accessDenied = ref(false)
const joinOpen = ref(false)
const joinNotice = ref<string | null>(null)
const leaveOpen = ref(false)
const leaving = ref(false)
const leaveError = ref<unknown>(null)
let loadId = 0

const isGlobalAdmin = computed(() => state.user?.roles.includes('admin') ?? false)
const isAuthenticated = computed(() => state.status === 'authenticated')
const isStaff = computed(
  () =>
    Boolean(permissions.value?.roles.length) ||
    Boolean(permissions.value?.modules.length) ||
    isGlobalAdmin.value,
)
const canManageBoard = computed(() => {
  if (isGlobalAdmin.value) return true
  if (hasBoardPermission(permissions.value, 'board.update')) return true
  const management = ['module.create', 'module.update', 'module.delete', 'module.tasks.manage'] as const
  if (management.some((code) => hasBoardPermission(permissions.value, code))) return true
  return Boolean(
    permissions.value?.modules.some((entry) =>
      management.some((code) => entry.permissions.includes(code)),
    ),
  )
})
const canOpenStaff = computed(
  () =>
    canManageStaff(permissions.value) ||
    canManageParticipants(permissions.value) ||
    canReadAudit(permissions.value),
)
const canJoinFromPage = computed(() => isBoardJoinOffered(board.value, me.value))
const schedule = computed(() => {
  if (!board.value) return ''
  if (!board.value.startsAt && !board.value.endsAt) return 'No schedule set'
  return `${formatDateTime(board.value.startsAt)} → ${formatDateTime(board.value.endsAt)}`
})
const boardTaskProgress = computed(() =>
  me.value?.joined && me.value.preview !== true ? (me.value.taskProgress ?? null) : null,
)
const hasMoreModules = computed(() => modulesOffset.value < modulesTotal.value)

function mergeModules(existing: ModuleListItem[], incoming: ModuleListItem[]): ModuleListItem[] {
  const seen = new Set(existing.map((module) => module.uuid))
  return [...existing, ...incoming.filter((module) => !seen.has(module.uuid))]
}

async function load() {
  const id = ++loadId
  loading.value = true
  error.value = null
  accessDenied.value = false
  permissionsError.value = null
  modulesError.value = null
  modulesLoading.value = false
  modules.value = []
  modulesTotal.value = 0
  modulesOffset.value = 0
  me.value = null
  permissions.value = null
  board.value = null
  try {
    const loadedBoard = await boardsApi.getBoard(boardUuid.value)
    if (id !== loadId) return
    board.value = loadedBoard
    if (!isAuthenticated.value) {
      return
    }
    const loadedMe = await boardsApi.getBoardMe(boardUuid.value)
    if (id !== loadId) return
    me.value = loadedMe
    try {
      const page = await boardsApi.listModules(boardUuid.value, { limit: 100 })
      if (id !== loadId) return
      modules.value = page.items
      modulesTotal.value = page.total
      modulesOffset.value = page.offset + page.items.length
    } catch (caught) {
      if (id !== loadId) return
      // A TEAM board has no score subject before the participant joins.
      // Modules are not needed to render the join affordance; the page reloads
      // them after the real join succeeds.
      if (isApiError(caught) && caught.code === 'WRONG_PARTICIPATION_MODE') {
        modules.value = []
        modulesTotal.value = 0
        modulesOffset.value = 0
      } else {
        throw caught
      }
    }
    try {
      const loadedPermissions = await boardsApi.getMyPermissions(boardUuid.value)
      if (id !== loadId) return
      permissions.value = loadedPermissions
    } catch (caught) {
      if (id !== loadId) return
      permissions.value = null
      permissionsError.value = caught
    }
  } catch (caught) {
    if (id !== loadId) return
    if (isApiError(caught) && (caught.code === 'BOARD_ACCESS_DENIED' || caught.code === 'BOARD_NOT_FOUND' || caught.code === 'FORBIDDEN' || caught.status === 404 || caught.status === 403)) {
      accessDenied.value = true
    } else {
      error.value = caught
    }
  } finally {
    if (id === loadId) loading.value = false
  }
}

async function loadMoreModules() {
  if (modulesLoading.value || !hasMoreModules.value) return
  const id = loadId
  modulesLoading.value = true
  modulesError.value = null
  try {
    const page = await boardsApi.listModules(boardUuid.value, {
      limit: 100,
      offset: modulesOffset.value,
    })
    if (id !== loadId) return
    modules.value = mergeModules(modules.value, page.items)
    modulesTotal.value = page.total
    modulesOffset.value = page.offset + page.items.length
  } catch (caught) {
    if (id !== loadId) return
    modulesError.value = caught
  } finally {
    if (id === loadId) modulesLoading.value = false
  }
}

function openJoin() {
  joinNotice.value = null
  joinOpen.value = true
}

async function onJoined(result: BoardJoinResponse) {
  joinOpen.value = false
  joinNotice.value = result.alreadyParticipating ? 'Already participating' : 'Joined board'
  await load()
}

async function leave() {
  leaving.value = true
  leaveError.value = null
  try {
    await boardsApi.leaveBoard(boardUuid.value)
    leaveOpen.value = false
    await load()
  } catch (caught) {
    leaveError.value = caught
  } finally {
    leaving.value = false
  }
}

watch(boardUuid, () => void load(), { immediate: true })
</script>

<template>
  <PageContainer>
    <LoadingState v-if="loading" :rows="6" />

    <EmptyState
      v-else-if="accessDenied"
      title="This board requires access"
      description="Sign in and enter the board code from the organizer to continue."
    >
      <div class="space-y-3">
        <template v-if="isAuthenticated">
          <Button @click="openJoin">Enter board code</Button>
          <p class="text-xs text-muted-foreground">
            The board is resolved from the code, so you do not need a link. Browsing this page never joins you.
          </p>
        </template>
        <Button v-else as-child>
          <RouterLink :to="{ name: 'sign-in', query: { redirect: route.fullPath } }">Sign in to join</RouterLink>
        </Button>
      </div>
    </EmptyState>

    <ErrorAlert v-else-if="error" :error="error" title="Could not load board">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="board">
      <PageHeader :title="board.name">
        <template #description>
          <!-- Sanitised by renderMarkdown; description-only, same subtitle slot. -->
          <div
            v-if="board.description"
            class="markdown-body markdown-body-inline"
            v-html="renderMarkdown(board.description)"
          />
          <span v-else>{{ board.slug }}</span>
        </template>
        <template #actions>
          <Button v-if="canManageBoard" as-child variant="outline">
            <RouterLink :to="`/admin/boards/${board.uuid}`">Manage board</RouterLink>
          </Button>
          <Button v-if="canOpenStaff" as-child variant="outline">
            <RouterLink :to="`/boards/${board.uuid}/staff`">Staff &amp; access</RouterLink>
          </Button>
          <Button v-if="board.rating.visibility === 'PUBLIC'" as-child variant="outline">
            <RouterLink :to="`/boards/${board.uuid}/scoreboard`">Scoreboard</RouterLink>
          </Button>
          <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
        </template>
      </PageHeader>

      <div class="mb-6 flex flex-wrap items-center gap-2">
        <StatusPill :status="board.status" />
        <span class="text-xs text-muted-foreground">{{ schedule }}</span>
      </div>

      <Alert v-if="board.rating.visibility === 'HIDDEN'" class="mb-6 rounded-none border-gold/60 bg-black">
        <AlertTitle>Rating is hidden</AlertTitle>
        <AlertDescription>The organizer hides the scoreboard and places. Scores are still recorded.</AlertDescription>
      </Alert>

      <Alert v-if="permissionsError" class="mb-6 rounded-none border-border bg-black">
        <AlertTitle>Staff controls unavailable</AlertTitle>
        <AlertDescription>
          The scoped permission endpoint did not answer, so owner/editor controls are hidden. Board content below is real.
        </AlertDescription>
      </Alert>

      <div class="workspace-layout" data-slot="workspace">
        <section class="workspace-main" data-slot="workspace-main" aria-labelledby="modules-heading">
          <h2 id="modules-heading" class="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Modules
          </h2>
          <div v-if="boardTaskProgress" class="mb-3 max-w-96" data-slot="board-task-progress">
            <p class="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground" aria-hidden="true">
              Task progress
            </p>
            <TaskProgress
              :solved-tasks="boardTaskProgress.solvedTasks"
              :total-tasks="boardTaskProgress.totalTasks"
              label="Task progress"
              compact
            />
          </div>
          <div v-if="!isAuthenticated" class="border border-dashed border-border px-4 py-6 text-sm text-muted-foreground">
            Sign in to see this board's modules and submit answers.
          </div>
          <div v-else-if="modules.length === 0" class="border border-dashed border-border px-4 py-6 text-sm text-muted-foreground">
            No modules are available yet. The organizer may still be preparing this board.
          </div>
          <div v-else class="workspace-cards workspace-cards--board" data-slot="board-modules">
            <div class="workspace-card-grid gap-2">
              <ModuleCard v-for="module in modules" :key="module.uuid" :module="module" />
            </div>
            <Button
              v-if="hasMoreModules"
              variant="outline"
              class="mt-3"
              :disabled="modulesLoading"
              @click="loadMoreModules"
            >
              {{ modulesLoading ? 'Loading…' : 'Load more' }}
            </Button>
            <ErrorAlert v-if="modulesError" :error="modulesError" title="Could not load more modules" class="mt-3">
              <Button variant="ghost" class="mt-2" @click="loadMoreModules">Retry</Button>
            </ErrorAlert>
          </div>
          <div v-if="board.participationMode === 'TEAM' && me?.joined" class="mt-6">
            <TeamPanel :board-uuid="board.uuid" />
          </div>
        </section>

        <aside class="workspace-aside space-y-4" data-slot="workspace-aside">
          <section v-if="!isAuthenticated" class="space-y-3 text-sm">
            <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Participate</h2>
            <p class="text-muted-foreground">Sign in to join this board, see its modules and submit answers.</p>
            <Button as-child class="w-full">
              <RouterLink :to="{ name: 'sign-in', query: { redirect: route.fullPath } }">Sign in</RouterLink>
            </Button>
          </section>
          <section v-else class="space-y-3">
            <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Your standing</h2>
            <div class="space-y-2 text-sm">
              <p>Joined: {{ me?.joined ? 'yes' : 'no' }}</p>
              <p v-if="me?.joinedAt" class="text-xs text-muted-foreground">Since {{ formatDateTime(me.joinedAt) }}</p>
              <p>Score: {{ me?.score ?? '—' }}</p>
              <p v-if="board.rating.visibility === 'PUBLIC'">Place: {{ me?.place ?? '—' }}</p>
              <p v-else class="text-xs text-muted-foreground">Place hidden while rating is hidden.</p>
              <p class="text-xs text-muted-foreground">
                {{ plural(modulesTotal, 'module') }} · {{ participationLabel(board.participationMode) }} · {{ accessModeLabel(board.accessMode) }}
              </p>
              <Button
                v-if="me?.joined"
                variant="outline"
                class="w-full"
                @click="leaveOpen = true"
              >
                Leave board
              </Button>
              <div v-else-if="canJoinFromPage" class="space-y-2 border-t border-border pt-3">
                <p v-if="joinNotice" class="text-xs text-pine">{{ joinNotice }}</p>
                <p class="text-xs text-muted-foreground">
                  Joining needs the board code from the organizer.
                </p>
                <Button class="w-full" @click="openJoin">Enter board code</Button>
              </div>
              <ErrorAlert v-if="leaveError" :error="leaveError" title="Could not leave board" />
            </div>
          </section>

          <section v-if="isStaff" class="border border-border px-4 py-3 text-xs text-muted-foreground">
            <p class="font-semibold uppercase tracking-wider">Staff view</p>
            <p class="mt-1">Manage tasks and review submissions from the board actions above.</p>
          </section>
        </aside>
      </div>

      <ConfirmDialog
        v-model:open="leaveOpen"
        title="Leave board"
        description="Leaving removes your participant row. The server refuses if you already submitted anything, so score history is never silently destroyed."
        confirm-label="Leave board"
        destructive
        :pending="leaving"
        @confirm="leave"
      />
    </template>

    <JoinBoardDialog v-model:open="joinOpen" :board-uuid="boardUuid" @joined="onJoined" />
  </PageContainer>
</template>