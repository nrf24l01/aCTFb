<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { audiencesApi } from '@/features/audiences/api'
import { boardsApi } from '@/features/boards/api/boards'
import { teamsApi } from '@/features/teams/api'
import type { Audience, AudienceMember, ParticipationMode, SubjectType, Team, UserSummary } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  participationMode: ParticipationMode
}>()

const items = ref<Audience[]>([])
const members = ref<AudienceMember[]>([])
const selected = ref<Audience | null>(null)
const participants = ref<UserSummary[]>([])
const teams = ref<Team[]>([])
const loading = ref(true)
const membersLoading = ref(false)
const error = ref<unknown>(null)
const membersError = ref<unknown>(null)

const dialogOpen = ref(false)
const editTarget = ref<Audience | null>(null)
const name = ref('')
const description = ref('')
const formProblem = ref<string | undefined>()
const pending = ref(false)
const deleteTarget = ref<Audience | null>(null)
const deleting = ref(false)
const deleteError = ref<unknown>(null)
const memberType = ref<SubjectType>(props.participationMode === 'TEAM' ? 'TEAM' : 'USER')
const memberUuid = ref('')
const memberProblem = ref<string | undefined>()
const addingMember = ref(false)
const removeTarget = ref<AudienceMember | null>(null)
const removingMember = ref(false)

const subjectOptions = computed(() =>
  memberType.value === 'TEAM'
    ? teams.value.map((team) => ({ uuid: team.uuid, name: team.name }))
    : participants.value.map((user) => ({ uuid: user.uuid, name: user.username })),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    const page = await audiencesApi.adminList(props.boardUuid, 100, 0)
    items.value = page.items
    if (props.participationMode === 'INDIVIDUAL') {
      participants.value = (await boardsApi.listParticipants(props.boardUuid, { limit: 100 })).items
    } else {
      teams.value = (await teamsApi.adminListTeams(props.boardUuid)).items
    }
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

async function addMember() {
  memberProblem.value = undefined
  if (!selected.value) return
  if (!memberUuid.value) {
    memberProblem.value = 'Choose a participant/team.'
    return
  }
  addingMember.value = true
  try {
    await audiencesApi.addMember(props.boardUuid, selected.value.uuid, memberType.value, memberUuid.value)
    memberUuid.value = ''
    await select(selected.value)
  } catch (caught) {
    membersError.value = caught
  } finally {
    addingMember.value = false
  }
}

async function removeMember() {
  if (!selected.value || !removeTarget.value) return
  removingMember.value = true
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
    membersError.value = caught
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
        <p class="mt-1 text-xs text-muted-foreground">Audience membership gates modules with non-empty audience sets.</p>
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
          <EmptyState v-else-if="members.length === 0" title="No members" description="Add a participant or team from this board." />
          <div v-else class="space-y-1">
            <div v-for="member in members" :key="`${member.subject.type}:${member.subject.uuid}`" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-1 text-sm last:border-0">
              <span>{{ member.subject.name }} <span class="text-xs text-muted-foreground">{{ member.subject.type }}</span></span>
              <Button variant="destructive" size="sm" @click="removeTarget = member">Remove</Button>
            </div>
          </div>
          <form class="flex flex-wrap items-end gap-2 border-t border-border pt-3" @submit.prevent="addMember">
            <div class="min-w-40">
              <FormField label="Subject type" for-id="audience-subject-type">
                <Select v-model="memberType" :disabled="participationMode !== 'TEAM'">
                  <SelectTrigger id="audience-subject-type"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="USER">USER</SelectItem>
                    <SelectItem value="TEAM">TEAM</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </div>
            <div class="min-w-64 flex-1">
              <FormField label="Participant / team" for-id="audience-subject" :error="memberProblem">
                <Select v-model="memberUuid">
                  <SelectTrigger id="audience-subject"><SelectValue placeholder="Choose…" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in subjectOptions" :key="option.uuid" :value="option.uuid">{{ option.name }}</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </div>
            <Button type="submit" :disabled="addingMember">{{ addingMember ? 'Adding…' : 'Add member' }}</Button>
          </form>
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