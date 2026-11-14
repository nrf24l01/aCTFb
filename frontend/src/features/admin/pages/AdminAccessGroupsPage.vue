<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { accessApi } from '@/features/access/api'
import { useSession } from '@/features/auth/session'
import type { AccessGroup } from '@/shared/types/api'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const items = ref<AccessGroup[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
const editOpen = ref(false)
const editing = ref<AccessGroup | null>(null)
const name = ref('')
const formProblem = ref<string | undefined>()
const pending = ref(false)
const deleteTarget = ref<AccessGroup | null>(null)
const deleting = ref(false)
const deleteError = ref<unknown>(null)

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    const page = await accessApi.listGroups(100, 0)
    items.value = page.items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  name.value = ''
  formProblem.value = undefined
  editOpen.value = true
}

function openEdit(group: AccessGroup) {
  editing.value = group
  name.value = group.name
  formProblem.value = undefined
  editOpen.value = true
}

async function save() {
  formProblem.value = name.value.trim() ? undefined : 'Enter a group name.'
  if (formProblem.value) return
  pending.value = true
  try {
    if (editing.value) await accessApi.updateGroup(editing.value.uuid, name.value.trim())
    else await accessApi.createGroup(name.value.trim())
    editOpen.value = false
    await load()
  } catch (caught) {
    formProblem.value = undefined
    error.value = caught
  } finally {
    pending.value = false
  }
}

async function remove() {
  if (!deleteTarget.value) return
  deleting.value = true
  deleteError.value = null
  try {
    await accessApi.deleteGroup(deleteTarget.value.uuid)
    deleteTarget.value = null
    await load()
  } catch (caught) {
    deleteError.value = caught
  } finally {
    deleting.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Access groups" description="Global-admin-managed user sets used to gate PRIVATE boards.">
      <template #actions>
        <Button v-if="isAdmin" @click="openCreate">Create group</Button>
        <Button v-if="isAdmin" variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState
      v-if="!isAdmin"
      title="Administrator access required"
      description="Access groups are global-admin managed. Board owners can only use groups already assigned to their board."
    />
    <template v-else>
      <LoadingState v-if="loading && items.length === 0" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load access groups" />
      <EmptyState v-else-if="items.length === 0" title="No access groups" description="Create a group, then assign users from Admin → Users." />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Name</TableHead>
              <TableHead>Users</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="group in items" :key="group.uuid">
              <TableCell>{{ group.name }}</TableCell>
              <TableCell>{{ group.usersCount }}</TableCell>
              <TableCell class="text-right">
                <div class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="openEdit(group)">Rename</Button>
                  <Button variant="destructive" size="sm" @click="deleteTarget = group; deleteError = null">Delete</Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </template>

    <Dialog v-model:open="editOpen">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>{{ editing ? 'Rename access group' : 'Create access group' }}</DialogTitle>
          <DialogDescription>Group names are unique. Membership is assigned on the user record.</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="save">
          <FormField label="Name" for-id="group-name" :error="formProblem">
            <Input id="group-name" v-model="name" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="pending" @click="editOpen = false">Cancel</Button>
            <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete access group"
      description="The backend refuses while the group is in use by a board. User accounts are not deleted."
      confirm-label="Delete group"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="remove"
    />
    <ErrorAlert v-if="deleteError" :error="deleteError" title="Could not delete access group" class="mt-4" />
  </PageContainer>
</template>