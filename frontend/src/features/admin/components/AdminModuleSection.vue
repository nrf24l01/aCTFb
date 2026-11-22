<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import TaskEditorDialog from '@/features/admin/components/TaskEditorDialog.vue'
import { moduleFormatLabel, taskTypeLabel } from '@/features/boards/format'
import TaskAttachmentsPanel from '@/features/admin/components/TaskAttachmentsPanel.vue'
import ADAdminPanel from '@/features/admin/components/ADAdminPanel.vue'
import PentestAdminPanel from '@/features/admin/components/PentestAdminPanel.vue'
import { boardsApi } from '@/features/boards/api/boards'
import type { AdminModule, AdminTask } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  module: AdminModule
  canEditModule: boolean
  canDeleteModule: boolean
  canManageTasks: boolean
}>()

const emit = defineEmits<{
  (event: 'edit-module'): void
  (event: 'delete-module'): void
  (event: 'changed'): void
}>()

const tasks = ref<AdminTask[]>([])
const isTasks = computed(() => props.module.type === 'TASKS')
const isAttackDefense = computed(() => props.module.type === 'ATTACK_DEFENSE')
const isPentest = computed(() => props.module.type === 'PENTEST')
const canCreateNativeTask = computed(() =>
  isTasks.value && props.module.allowedTaskTypes.some((type) => ['FLAG', 'MANUAL', 'INSTANCE'].includes(type)),
)
const loading = ref(true)
const error = ref<unknown>(null)
const createOpen = ref(false)
const editTarget = ref<AdminTask | null>(null)
const filesTarget = ref<AdminTask | null>(null)
const deleteTarget = ref<AdminTask | null>(null)
const deleting = ref(false)
const deleteError = ref<unknown>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    if (!isTasks.value) {
      tasks.value = []
      return
    }
    const page = await boardsApi.listAdminTasks(props.boardUuid, props.module.uuid, { limit: 100 })
    tasks.value = page.items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function removeTask() {
  if (!deleteTarget.value) return
  deleting.value = true
  deleteError.value = null
  try {
    await boardsApi.deleteTask(props.boardUuid, props.module.uuid, deleteTarget.value.uuid)
    deleteTarget.value = null
    await load()
    emit('changed')
  } catch (caught) {
    deleteError.value = caught
  } finally {
    deleting.value = false
  }
}

function onSaved() {
  void load()
  emit('changed')
}

void load()
</script>

<template>
  <section class="border border-border" :aria-labelledby="`module-${module.uuid}-heading`">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 :id="`module-${module.uuid}-heading`" class="font-semibold">{{ module.name }}</h2>
        <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <Badge variant="outline" class="rounded-none">{{ moduleFormatLabel(module.type, module.allowedTaskTypes) }}</Badge>
          <Badge variant="outline" class="rounded-none">{{ module.enabled ? 'enabled' : 'disabled' }}</Badge>
          <span>order {{ module.order }}</span>
          <span>{{ module.description || 'No description' }}</span>
        </div>
      </div>
      <div class="flex flex-wrap gap-1">
        <Button v-if="canManageTasks && canCreateNativeTask" size="sm" @click="createOpen = true">Create task</Button>
        <Button v-if="canEditModule" variant="outline" size="sm" @click="emit('edit-module')">Edit module</Button>
        <Button v-if="canDeleteModule" variant="destructive" size="sm" @click="emit('delete-module')">Delete module</Button>
      </div>
    </div>

    <ADAdminPanel
      v-if="isAttackDefense"
      :board-uuid="boardUuid"
      :module-uuid="module.uuid"
    />
    <PentestAdminPanel
      v-if="isPentest"
      :board-uuid="boardUuid"
      :module-uuid="module.uuid"
    />

    <div v-if="isTasks" class="px-4 py-4">
      <LoadingState v-if="loading" :rows="2" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load tasks">
        <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
      </ErrorAlert>
      <EmptyState
        v-else-if="tasks.length === 0"
        title="No tasks in this module"
        description="Create a FLAG task. Tasks stay visible only when the task and module are available to participants."
      />
      <div v-else class="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Title</TableHead>
              <TableHead>Category</TableHead>
              <TableHead>Points</TableHead>
              <TableHead>Visible</TableHead>
              <TableHead>Answers</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="task in tasks" :key="task.uuid" class="border-border">
              <TableCell>
                <span class="font-medium">{{ task.title }}</span>
                <span class="block text-xs text-muted-foreground">{{ taskTypeLabel(task.type) }}</span>
              </TableCell>
              <TableCell>{{ task.category }}</TableCell>
              <TableCell>{{ task.points }}</TableCell>
              <TableCell>{{ task.visible ? 'Yes' : 'No' }}</TableCell>
              <TableCell class="text-xs text-muted-foreground">
                {{ task.answerConfiguration.configured ? `${task.answerConfiguration.acceptedAnswersCount} configured` : 'Not configured' }}
              </TableCell>
              <TableCell class="text-right">
                <div v-if="canManageTasks" class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="filesTarget = task">Files</Button>
                  <Button variant="outline" size="sm" @click="editTarget = task">Edit</Button>
                  <Button variant="destructive" size="sm" @click="deleteTarget = task; deleteError = null">Delete</Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>

    <TaskEditorDialog
      v-model:open="createOpen"
      :board-uuid="boardUuid"
      :module-uuid="module.uuid"
      :allowed-task-types="module.allowedTaskTypes"
      @saved="onSaved"
    />
    <TaskEditorDialog
      :open="Boolean(editTarget)"
      :board-uuid="boardUuid"
      :module-uuid="module.uuid"
      :task="editTarget"
      :allowed-task-types="module.allowedTaskTypes"
      @update:open="(open) => { if (!open) editTarget = null }"
      @saved="onSaved"
    />

    <Dialog :open="Boolean(filesTarget)" @update:open="(open) => { if (!open) filesTarget = null }">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{{ filesTarget?.title }} files</DialogTitle>
        </DialogHeader>
        <TaskAttachmentsPanel
          v-if="filesTarget"
          :board-uuid="boardUuid"
          :module-uuid="module.uuid"
          :task="filesTarget"
          @changed="onSaved"
        />
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete task"
      :description="`Delete ${deleteTarget?.title ?? 'this task'}? This cannot be undone. The backend may refuse if submissions or resources retain it.`"
      confirm-label="Delete task"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="removeTask"
    />
    <ErrorAlert v-if="deleteError" :error="deleteError" title="Could not delete task" class="m-4" />
  </section>
</template>