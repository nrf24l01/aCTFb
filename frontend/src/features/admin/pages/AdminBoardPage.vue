<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import InviteCodePanel from '@/features/boards/components/InviteCodePanel.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import AdminModuleSection from '@/features/admin/components/AdminModuleSection.vue'
import BoardSettingsDialog from '@/features/admin/components/BoardSettingsDialog.vue'
import ModuleEditorDialog from '@/features/admin/components/ModuleEditorDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { audiencesApi } from '@/features/audiences/api'
import {
  canCreateModule,
  canDeleteModule,
  canManageModuleTasks,
  canManageModule,
  canManageParticipants,
  canManageStaff,
  canReadAudit,
  hasBoardPermission,
} from '@/features/boards/permissions'
import { accessModeLabel, participationLabel, requiresInviteCode } from '@/features/boards/accessMode'
import { useSession } from '@/features/auth/session'
import type { AdminBoard, AdminModule, Audience, BoardPermissionsResponse } from '@/shared/types/api'

const route = useRoute()
const { state } = useSession()
const boardUuid = computed(() => String(route.params.boardUuid))
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const board = ref<AdminBoard | null>(null)
const permissions = ref<BoardPermissionsResponse | null>(null)
const permissionsError = ref<unknown>(null)
const modules = ref<AdminModule[]>([])
const audiences = ref<Audience[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const actionError = ref<unknown>(null)
const actionPending = ref(false)

const editBoardOpen = ref(false)
const createModuleOpen = ref(false)
const editModule = ref<AdminModule | null>(null)
const deleteModule = ref<AdminModule | null>(null)
const deleteModulePending = ref(false)
const deleteModuleError = ref<unknown>(null)
const archiveOpen = ref(false)
const deleteBoardOpen = ref(false)

const canEditBoard = computed(() => isAdmin.value || hasBoardPermission(permissions.value, 'board.update'))
const canPublish = computed(() => isAdmin.value || hasBoardPermission(permissions.value, 'board.publish'))
const canCreate = computed(() => isAdmin.value || canCreateModule(permissions.value))
const canOpenStaff = computed(
  () =>
    isAdmin.value ||
    canManageStaff(permissions.value) ||
    canManageParticipants(permissions.value) ||
    canReadAudit(permissions.value),
)
const hasAnyControl = computed(
  () => isAdmin.value || Boolean(permissions.value && (permissions.value.permissions.length > 0 || permissions.value.modules.length > 0)),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    const [loadedBoard, modulePage] = await Promise.all([
      boardsApi.getAdminBoard(boardUuid.value),
      boardsApi.listAdminModules(boardUuid.value, { limit: 100 }),
    ])
    board.value = loadedBoard
    modules.value = modulePage.items
    try {
      audiences.value = (await audiencesApi.adminList(boardUuid.value, 100, 0)).items
    } catch {
      audiences.value = []
    }
    permissionsError.value = null
    try {
      permissions.value = await boardsApi.getMyPermissions(boardUuid.value)
    } catch (caught) {
      permissions.value = null
      permissionsError.value = caught
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function runAction(action: 'PUBLISH' | 'UNPUBLISH' | 'ARCHIVE') {
  actionPending.value = true
  actionError.value = null
  try {
    await boardsApi.boardAction(boardUuid.value, action)
    archiveOpen.value = false
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    actionPending.value = false
  }
}

async function removeModule() {
  if (!deleteModule.value) return
  deleteModulePending.value = true
  deleteModuleError.value = null
  try {
    await boardsApi.deleteModule(boardUuid.value, deleteModule.value.uuid)
    deleteModule.value = null
    await load()
  } catch (caught) {
    deleteModuleError.value = caught
  } finally {
    deleteModulePending.value = false
  }
}

async function removeBoard() {
  actionPending.value = true
  actionError.value = null
  try {
    await boardsApi.deleteBoard(boardUuid.value)
    deleteBoardOpen.value = false
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    actionPending.value = false
  }
}

function moduleCanEdit(module: AdminModule) {
  return isAdmin.value || canManageModule(permissions.value, module.uuid)
}
function moduleCanDelete(module: AdminModule) {
  return isAdmin.value || canDeleteModule(permissions.value, module.uuid)
}
function moduleCanManageTasks(module: AdminModule) {
  return isAdmin.value || canManageModuleTasks(permissions.value, module.uuid)
}

function onBoardSaved() {
  void load()
}

function onModuleSaved() {
  editModule.value = null
  void load()
}

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 flex flex-wrap gap-x-4 gap-y-1 text-sm">
      <RouterLink to="/admin/boards" class="text-muted-foreground underline decoration-border underline-offset-4">
        ‹ Admin boards
      </RouterLink>
      <RouterLink :to="`/boards/${boardUuid}`" class="text-muted-foreground underline decoration-border underline-offset-4">
        Participant view
      </RouterLink>
    </div>

    <LoadingState v-if="loading" :rows="6" />
    <ErrorAlert v-else-if="error" :error="error" title="Could not load board">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>
    <ErrorAlert v-else-if="!hasAnyControl && permissionsError" :error="permissionsError" title="Could not load scoped permissions">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>
    <EmptyState
      v-else-if="!hasAnyControl"
      title="No board management access"
      description="Your effective scoped permissions do not allow managing this board."
    />

    <template v-else-if="board">
      <PageHeader :title="board.name" :description="`${board.slug} · ${accessModeLabel(board.accessMode)} · ${participationLabel(board.participationMode)}`">
        <template #actions>
          <Button v-if="canEditBoard" variant="outline" :disabled="loading" @click="editBoardOpen = true">Edit board</Button>
          <Button v-if="canOpenStaff" as-child variant="outline">
            <RouterLink :to="`/boards/${boardUuid}/staff`">Staff &amp; access</RouterLink>
          </Button>
          <Button v-if="canCreate" :disabled="loading" @click="createModuleOpen = true">Create module</Button>
        </template>
      </PageHeader>

      <div class="mb-4 flex flex-wrap items-center gap-2">
        <StatusPill :status="board.status" />
        <span class="text-xs text-muted-foreground">{{ accessModeLabel(board.accessMode) }} · rating {{ board.rating.visibility === 'HIDDEN' ? 'hidden' : 'public' }}</span>
      </div>

      <Alert v-if="board.accessMode === 'LEGACY'" class="mb-4 rounded-none border-gold/60 bg-black">
        <AlertTitle>Legacy access</AlertTitle>
        <AlertDescription>
          This board uses an old access combination. Its current behaviour is unchanged. Open <strong>Edit board</strong>
          and choose Public, Selected audiences or Invite code to update it.
        </AlertDescription>
      </Alert>

      <ErrorAlert v-if="actionError" :error="actionError" title="Board action failed" class="mb-4" />

      <section v-if="canPublish || isAdmin" class="mb-6 border border-border" aria-labelledby="lifecycle-heading">
        <div class="border-b border-border px-4 py-3">
          <h2 id="lifecycle-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Lifecycle
          </h2>
        </div>
        <div class="flex flex-wrap items-center gap-2 px-4 py-4">
          <Button
            v-if="canPublish && board.status !== 'ARCHIVED' && board.status !== 'ACTIVE'"
            :disabled="actionPending || modules.length === 0"
            @click="runAction('PUBLISH')"
          >
            {{ actionPending ? 'Working…' : 'Publish' }}
          </Button>
          <Button
            v-if="canPublish && board.status === 'SCHEDULED'"
            variant="outline"
            :disabled="actionPending"
            @click="runAction('UNPUBLISH')"
          >
            Unpublish
          </Button>
          <Button
            v-if="isAdmin && board.status !== 'ARCHIVED'"
            variant="destructive"
            :disabled="actionPending"
            @click="archiveOpen = true"
          >
            Archive board
          </Button>
          <Button
            v-if="isAdmin"
            variant="destructive"
            :disabled="actionPending"
            @click="deleteBoardOpen = true"
          >
            Delete board
          </Button>
          <p v-if="modules.length === 0" class="text-xs text-muted-foreground">
            Publishing requires at least one enabled TASKS module.
          </p>
          <p class="text-xs text-muted-foreground">
            Only a global admin can archive or delete a board. Owners cannot create boards or grant owner.
          </p>
        </div>
      </section>

      <InviteCodePanel
        v-if="requiresInviteCode(board)"
        :board-uuid="boardUuid"
        class="mb-6"
      />

      <Alert v-if="board.rating.visibility === 'HIDDEN'" class="mb-6 rounded-none border-gold/60 bg-black">
        <AlertTitle>Rating hidden</AlertTitle>
        <AlertDescription>Participant scoreboard and place return 403 while this setting is on.</AlertDescription>
      </Alert>

      <section aria-labelledby="admin-modules-heading">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 id="admin-modules-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Modules and tasks
          </h2>
          <Button v-if="canCreate" size="sm" variant="outline" @click="createModuleOpen = true">Create module</Button>
        </div>
        <EmptyState
          v-if="modules.length === 0"
          title="No modules"
          description="Create a TASKS module that allows FLAG tasks, then add a task."
        >
          <Button v-if="canCreate" @click="createModuleOpen = true">Create module</Button>
        </EmptyState>
        <div v-else class="space-y-4">
          <AdminModuleSection
            v-for="module in modules"
            :key="module.uuid"
            :board-uuid="boardUuid"
            :module="module"
            :can-edit-module="moduleCanEdit(module)"
            :can-delete-module="moduleCanDelete(module)"
            :can-manage-tasks="moduleCanManageTasks(module)"
            @edit-module="editModule = module"
            @delete-module="deleteModule = module; deleteModuleError = null"
            @changed="load"
          />
        </div>
      </section>

      <BoardSettingsDialog v-model:open="editBoardOpen" :board="board" :can-manage-access-groups="isAdmin" @saved="onBoardSaved" />
      <ModuleEditorDialog
        v-model:open="createModuleOpen"
        :board-uuid="boardUuid"
        :audiences="audiences"
        @saved="onModuleSaved"
      />
      <ModuleEditorDialog
        :open="Boolean(editModule)"
        :board-uuid="boardUuid"
        :module="editModule"
        :audiences="audiences"
        @update:open="(open) => { if (!open) editModule = null }"
        @saved="onModuleSaved"
      />

      <ConfirmDialog
        :open="Boolean(deleteModule)"
        title="Delete module"
        :description="`Delete ${deleteModule?.name ?? 'this module'} and its tasks? The backend refuses while submissions or resources require it.`"
        confirm-label="Delete module"
        destructive
        :pending="deleteModulePending"
        @update:open="(open) => { if (!open) deleteModule = null }"
        @confirm="removeModule"
      />
      <ErrorAlert v-if="deleteModuleError" :error="deleteModuleError" title="Could not delete module" class="mt-4" />

      <ConfirmDialog
        v-model:open="archiveOpen"
        title="Archive board"
        description="Archiving is a global-admin action and hides the board from participants. It is not the same as deleting data."
        confirm-label="Archive board"
        destructive
        :pending="actionPending"
        @confirm="runAction('ARCHIVE')"
      />
      <ConfirmDialog
        v-model:open="deleteBoardOpen"
        title="Delete board"
        description="Deleting a board is irreversible and global-admin only. The backend may refuse if retained data exists."
        confirm-label="Delete board"
        destructive
        :pending="actionPending"
        :typed-name="board.slug"
        @confirm="removeBoard"
      />
    </template>
  </PageContainer>
</template>