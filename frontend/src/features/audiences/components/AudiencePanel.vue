<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import UserPicker from '@/features/access/components/UserPicker.vue'
import TeamPicker from '@/features/access/components/TeamPicker.vue'
import { accessApi } from '@/features/access/api'
import { audiencesApi } from '@/features/audiences/api'
import type {
  Audience,
  AudienceMember,
  ParticipationMode,
  SubjectType,
  TeamDirectoryEntry,
  UserDirectoryEntry,
} from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  participationMode: ParticipationMode
}>()

const items = ref<Audience[]>([])
const members = ref<AudienceMember[]>([])
const selected = ref<Audience | null>(null)
const loading = ref(true)
const membersLoading = ref(false)
const error = ref<unknown>(null)
const membersError = ref<unknown>(null)
const memberActionError = ref<unknown>(null)

const dialogOpen = ref(false)
const editTarget = ref<Audience | null>(null)
const name = ref('')
const description = ref('')
const formProblem = ref<string | undefined>()
const pending = ref(false)
const deleteTarget = ref<Audience | null>(null)
const deleting = ref(false)
const deleteError = ref<unknown>(null)
const addingMember = ref(false)
const removeTarget = ref<AudienceMember | null>(null)
const removingMember = ref(false)

// Team boards may gate modules by TEAM audiences; individual boards by USER.
const subjectType = computed<SubjectType>(() => (props.participationMode === 'TEAM' ? 'TEAM' : 'USER'))
const existingSubjectIds = computed(() => members.value.map((member) => member.subject.uuid))

async function load() {
  loading.value = true
  error.value = null
  try {
    const page = await audiencesApi.adminList(props.boardUuid, 100, 0)
    items.value = page.items
    if (!selected.value && items.value.length > 0) await select(items.value[0])
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function select(audience: Audience) {
  selected.value = audience
  membersLoading.value = true
  membersError.value = null
  try {
    members.value = (await audiencesApi.listMembers(props.boardUuid, audience.uuid, 100, 0)).items
  } catch (caught) {
    membersError.value = caught
  } finally {
    membersLoading.value = false
  }
}

function openCreate() {
  editTarget.value = null
  name.value = ''
  description.value = ''
  formProblem.value = undefined
  dialogOpen.value = true
}

function openEdit(audience: Audience) {
  editTarget.value = audience
  name.value = audience.name
  description.value = audience.description
  formProblem.value = undefined
  dialogOpen.value = true
}

async function save() {
  formProblem.value = name.value.trim() ? undefined : 'Enter an audience name.'
  if (formProblem.value) return
  pending.value = true
  try {
    if (editTarget.value) {
      await audiencesApi.adminUpdate(props.boardUuid, editTarget.value.uuid, {
        name: name.value.trim(),
        description: description.value,
      })
    } else {
      await audiencesApi.adminCreate(props.boardUuid, { name: name.value.trim(), description: description.value })
    }
    dialogOpen.value = false
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}

async function removeAudience() {
  if (!deleteTarget.value) return
  deleting.value = true
  deleteError.value = null
  try {
    await audiencesApi.adminDelete(props.boardUuid, deleteTarget.value.uuid)
    if (selected.value?.uuid === deleteTarget.value.uuid) {
      selected.value = null
      members.value = []
    }
    deleteTarget.value = null
    await load()
  } catch (caught) {
    deleteError.value = caught
  } finally {
    deleting.value = false
  }
}

async function searchUsers(query: string, signal: AbortSignal): Promise<UserDirectoryEntry[]> {
  if (signal.aborted) return []
  return (await accessApi.searchBoardUsers(props.boardUuid, query, 20, 0)).items
}

async function searchTeams(query: string, signal: AbortSignal): Promise<TeamDirectoryEntry[]> {
  if (signal.aborted) return []
  return (await accessApi.searchBoardTeams(props.boardUuid, query, 20, 0)).items
}

async function addUser(user: UserDirectoryEntry) {
  await addSubject('USER', user.uuid)
}

async function addTeam(team: TeamDirectoryEntry) {
  await addSubject('TEAM', team.uuid)
}

async function addSubject(type: SubjectType, subjectUuid: string) {
  if (!selected.value) return
  addingMember.value = true
  memberActionError.value = null
  try {
    await audiencesApi.addMember(props.boardUuid, selected.value.uuid, type, subjectUuid)
    await select(selected.value)
  } catch (caught) {
    memberActionError.value = caught
  } finally {
    addingMember.value = false
  }
}

async function removeMember() {
  if (!selected.value || !removeTarget.value) return
  removingMember.value = true
  memberActionError.value = null
  try {
    await audiencesApi.removeMember(
      props.boardUuid,
      selected.value.uuid,
      removeTarget.value.subject.type,
      removeTarget.value.subject.uuid,
    )
    removeTarget.value = null
    await select(selected.value)
  } catch (caught) {
    memberActionError.value = caught
  } finally {
    removingMember.value = false
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="audiences-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="audiences-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Audiences</h2>
        <p class="mt-1 text-xs text-muted-foreground">
          Board-scoped audiences gate modules. They are different from global access groups, which gate board access.
        </p>
      </div>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
        <Button size="sm" @click="openCreate">Create audience</Button>
      </div>
    </div>

    <div class="space-y-4 px-4 py-4">
      <LoadingState v-if="loading" :rows="2" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load audiences" />
      <template v-else>
        <EmptyState v-if="items.length === 0" title="No audiences" description="Create an audience, add participants or teams, then assign it to modules." />
        <div v-else class="overflow-x-auto border border-border">
          <Table>
            <TableHeader>
              <TableRow class="border-border hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead>Members</TableHead>
                <TableHead>Description</TableHead>
                <TableHead class="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="audience in items" :key="audience.uuid" :class="selected?.uuid === audience.uuid ? 'bg-muted' : ''">
                <TableCell><button class="underline decoration-border underline-offset-4" @click="select(audience)">{{ audience.name }}</button></TableCell>
                <TableCell>{{ audience.membersCount }}</TableCell>
                <TableCell class="max-w-sm whitespace-normal text-xs text-muted-foreground">{{ audience.description || '—' }}</TableCell>
                <TableCell class="text-right">
                  <div class="flex justify-end gap-1">
                    <Button variant="outline" size="sm" @click="openEdit(audience)">Edit</Button>
                    <Button variant="destructive" size="sm" @click="deleteTarget = audience; deleteError = null">Delete</Button>
                  </div>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>

        <div v-if="selected" class="space-y-3 border border-border p-3">
          <p class="text-sm font-semibold">{{ selected.name }} members</p>
          <LoadingState v-if="membersLoading" :rows="2" />
          <ErrorAlert v-else-if="membersError" :error="membersError" title="Could not load audience members" />
          <EmptyState v-else-if="members.length === 0" title="No members" description="Search this board's participants to add one." />
          <div v-else class="space-y-1">
            <div v-for="member in members" :key="`${member.subject.type}:${member.subject.uuid}`" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-1 text-sm last:border-0">
              <span>{{ member.subject.name }} <span class="text-xs text-muted-foreground">{{ member.subject.type === 'TEAM' ? 'Team' : 'Participant' }}</span></span>
              <Button variant="destructive" size="sm" @click="removeTarget = member">Remove</Button>
            </div>
          </div>
          <div class="space-y-2 border-t border-border pt-3">
            <ErrorAlert v-if="memberActionError" :error="memberActionError" title="Could not update audience membership" />
            <UserPicker
              v-if="subjectType === 'USER'"
              label="Add a participant"
              :search="searchUsers"
              :exclude="existingSubjectIds"
              :placeholder="addingMember ? 'Adding…' : 'Search by username, email or UUID'"
              @select="addUser"
            />
            <TeamPicker
              v-else
              label="Add a team"
              :search="searchTeams"
              :exclude="existingSubjectIds"
              :placeholder="addingMember ? 'Adding…' : 'Search teams by name or UUID'"
              @select="addTeam"
            />
          </div>
        </div>
      </template>
    </div>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>{{ editTarget ? 'Edit audience' : 'Create audience' }}</DialogTitle>
          <DialogDescription>Audiences belong to this board only.</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="save">
          <FormField label="Name" for-id="audience-name" :error="formProblem">
            <Input id="audience-name" v-model="name" />
          </FormField>
          <FormField label="Description" for-id="audience-description">
            <Textarea id="audience-description" v-model="description" rows="3" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="pending" @click="dialogOpen = false">Cancel</Button>
            <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save audience' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete audience"
      description="The backend refuses while a module references this audience."
      confirm-label="Delete audience"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="removeAudience"
    />
    <ConfirmDialog
      :open="Boolean(removeTarget)"
      title="Remove audience member"
      description="The subject loses this audience. Module access may change immediately."
      confirm-label="Remove member"
      destructive
      :pending="removingMember"
      @update:open="(open) => { if (!open) removeTarget = null }"
      @confirm="removeMember"
    />
    <ErrorAlert v-if="deleteError" :error="deleteError" title="Could not delete audience" class="m-4" />
  </section>
</template>
