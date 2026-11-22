<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import AuditPanel from '@/features/boards/components/AuditPanel.vue'
import AudiencePanel from '@/features/audiences/components/AudiencePanel.vue'
import AdminTeamsPanel from '@/features/admin/components/AdminTeamsPanel.vue'
import ReviewSubmissionsPanel from '@/features/boards/components/ReviewSubmissionsPanel.vue'
import ParticipantsPanel from '@/features/boards/components/ParticipantsPanel.vue'
import RoleBindingsPanel from '@/features/boards/components/RoleBindingsPanel.vue'
import { boardsApi } from '@/features/boards/api/boards'
import {
  canManageParticipants,
  canManageStaff,
  canReadAudit,
  hasBoardPermission,
} from '@/features/boards/permissions'
import { useSession } from '@/features/auth/session'
import type { Board, BoardPermissionsResponse, ModuleListItem } from '@/shared/types/api'

const route = useRoute()
const { state } = useSession()
const boardUuid = computed(() => String(route.params.boardUuid))
const globalAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const board = ref<Board | null>(null)
const permissions = ref<BoardPermissionsResponse | null>(null)
const modules = ref<ModuleListItem[]>([])
const loading = ref(true)
const error = ref<unknown>(null)

const staffAllowed = computed(
  () =>
    canManageStaff(permissions.value) ||
    canManageParticipants(permissions.value) ||
    canReadAudit(permissions.value) ||
    hasBoardPermission(permissions.value, 'module.submissions.read') ||
    globalAdmin.value ||
    hasBoardPermission(permissions.value, 'board.audiences.manage'),
)

const canManageAudiences = computed(
  () => globalAdmin.value || hasBoardPermission(permissions.value, 'board.audiences.manage'),
)

const canReadAnySubmissions = computed(
  () =>
    hasBoardPermission(permissions.value, 'module.submissions.read') ||
    Boolean(permissions.value?.modules.some((entry) => entry.permissions.includes('module.submissions.read'))),
)

const canReviewAnySubmissions = computed(
  () =>
    hasBoardPermission(permissions.value, 'module.submissions.review') ||
    Boolean(permissions.value?.modules.some((entry) => entry.permissions.includes('module.submissions.review'))),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    const [loadedBoard, loadedPermissions, modulePage] = await Promise.all([
      boardsApi.getBoard(boardUuid.value),
      boardsApi.getMyPermissions(boardUuid.value),
      boardsApi.listModules(boardUuid.value, { limit: 100 }),
    ])
    board.value = loadedBoard
    permissions.value = loadedPermissions
    modules.value = modulePage.items
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

    <PageHeader title="Staff & access" description="Scoped roles, participants and audit for this board.">
      <template #actions>
        <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>

    <LoadingState v-if="loading" :rows="5" />
    <ErrorAlert v-else-if="error" :error="error" title="Could not load staff context">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>
    <EmptyState
      v-else-if="!staffAllowed"
      title="No staff access"
      description="Your effective scoped permissions do not include staff management for this board."
    />
    <div v-else class="space-y-6">
      <p class="text-sm text-muted-foreground">
        Effective roles: {{ permissions?.roles.join(', ') || 'module-scoped only' }}. These controls call the real
        board/admin routes and the backend re-checks every request.
      </p>

      <RoleBindingsPanel
        v-if="canManageStaff(permissions)"
        :board-uuid="boardUuid"
        :permissions="permissions"
        :modules="modules"
        :global-admin="globalAdmin"
        @changed="load"
      />
      <ParticipantsPanel v-if="canManageParticipants(permissions)" :board-uuid="boardUuid" @changed="load" />
      <AdminTeamsPanel
        v-if="canManageParticipants(permissions) && board?.participationMode === 'TEAM'"
        :board-uuid="boardUuid"
      />
      <ReviewSubmissionsPanel
        v-if="canReadAnySubmissions"
        :board-uuid="boardUuid"
        :modules="modules"
        :permissions="permissions"
        :can-review="canReviewAnySubmissions"
      />
      <AudiencePanel
        v-if="canManageAudiences && board"
        :board-uuid="boardUuid"
        :participation-mode="board.participationMode"
      />
      <AuditPanel v-if="canReadAudit(permissions)" :board-uuid="boardUuid" />
      <EmptyState
        v-if="!canManageStaff(permissions) && !canManageParticipants(permissions) && !canReadAudit(permissions) && !canManageAudiences"
        title="Scoped read access"
        description="You can read submissions only within your assigned scope. Ask a board owner for review or participant permissions if you need them."
      />
    </div>
  </PageContainer>
</template>