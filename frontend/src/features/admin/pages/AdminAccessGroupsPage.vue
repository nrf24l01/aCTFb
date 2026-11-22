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
import UserPicker from '@/features/access/components/UserPicker.vue'
import { accessApi } from '@/features/access/api'
import { useSession } from '@/features/auth/session'
import type { AccessGroup, UserDirectoryEntry } from '@/shared/types/api'

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

const membersOpen = ref(false)
const membersGroup = ref<AccessGroup | null>(null)
const members = ref<UserDirectoryEntry[]>([])
const membersLoading = ref(false)
const membersError = ref<unknown>(null)
const memberActionError = ref<unknown>(null)
const memberPending = ref(false)

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

async function openMembers(group: AccessGroup) {
  membersGroup.value = group
  membersOpen.value = true
  members.value = []
  memberActionError.value = null
  await loadMembers()
}

async function loadMembers() {
  if (!membersGroup.value) return
  membersLoading.value = true
  membersError.value = null
  try {
    members.value = (await accessApi.listGroupMembers(membersGroup.value.uuid, 100)).items
  } catch (caught) {
    membersError.value = caught
  } finally {
    membersLoading.value = false
  }
}

async function searchMembers(query: string): Promise<UserDirectoryEntry[]> {
  const page = await accessApi.searchUsers(query, 20, 0)
  return page.items
}

async function addMember(user: UserDirectoryEntry) {
  if (!membersGroup.value) return
  memberPending.value = true
  memberActionError.value = null
  try {
    await accessApi.addGroupMember(membersGroup.value.uuid, user.uuid)
    await loadMembers()
    await load()
  } catch (caught) {
    memberActionError.value = caught
  } finally {
    memberPending.value = false
  }
}

async function removeMember(user: UserDirectoryEntry) {
  if (!membersGroup.value) return
  memberPending.value = true
  memberActionError.value = null
  try {
    await accessApi.removeGroupMember(membersGroup.value.uuid, user.uuid)
    await loadMembers()
    await load()
  } catch (caught) {
    memberActionError.value = caught
  } finally {
    memberPending.value = false
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
                  <Button variant="outline" size="sm" @click="openMembers(group)">Members</Button>
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

    <Dialog v-model:open="membersOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Members · {{ membersGroup?.name }}</DialogTitle>
          <DialogDescription>Add or remove users. Membership takes effect immediately for boards using this group.</DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <UserPicker
            label="Add a user"
            :search="searchMembers"
            :exclude="members.map((member) => member.uuid)"
            :placeholder="memberPending ? 'Working…' : 'Search by username, email or UUID'"
            @select="addMember"
          />
          <ErrorAlert v-if="memberActionError" :error="memberActionError" title="Could not update membership" />
          <LoadingState v-if="membersLoading" :rows="3" />
          <ErrorAlert v-else-if="membersError" :error="membersError" title="Could not load members">
            <Button variant="ghost" class="mt-2" @click="loadMembers">Retry</Button>
          </ErrorAlert>
          <div v-else class="border border-border">
            <p v-if="members.length === 0" class="px-3 py-3 text-sm text-muted-foreground">No members yet.</p>
            <ul v-else class="divide-y divide-border">
              <li v-for="member in members" :key="member.uuid" class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
                <span>
                  <span class="font-medium">{{ member.username }}</span>
                  <span class="ml-2 text-xs text-muted-foreground">{{ member.email }}</span>
                </span>
                <Button variant="ghost" size="sm" :disabled="memberPending" @click="removeMember(member)">Remove</Button>
              </li>
            </ul>
          </div>
        </div>
        <DialogFooter>
          <Button type="button" variant="ghost" @click="membersOpen = false">Done</Button>
        </DialogFooter>
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