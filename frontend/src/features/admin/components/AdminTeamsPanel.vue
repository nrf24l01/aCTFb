<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { teamsApi } from '@/features/teams/api'
import { isUuid } from '@/shared/lib/validate'
import type { Team } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()

const items = ref<Team[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const name = ref('')
const createOpen = ref(false)
const editTarget = ref<Team | null>(null)
const pending = ref(false)
const deleteTarget = ref<Team | null>(null)
const deleting = ref(false)
const memberTeam = ref<Team | null>(null)
const memberUuid = ref('')
const memberProblem = ref<string | undefined>()
const memberPending = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    items.value = (await teamsApi.adminListTeams(props.boardUuid, '', 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editTarget.value = null
  name.value = ''
  createOpen.value = true
}

function openEdit(team: Team) {
  editTarget.value = team
  name.value = team.name
  createOpen.value = true
}

async function save() {
  pending.value = true
  error.value = null
  try {
    if (editTarget.value) await teamsApi.adminUpdateTeam(props.boardUuid, editTarget.value.uuid, name.value.trim())
    else await teamsApi.adminCreateTeam(props.boardUuid, name.value.trim())
    createOpen.value = false
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}

async function removeTeam() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await teamsApi.adminDeleteTeam(props.boardUuid, deleteTarget.value.uuid)
    deleteTarget.value = null
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    deleting.value = false
  }
}

function openMember(team: Team) {
  memberTeam.value = team
  memberUuid.value = ''
  memberProblem.value = undefined
}

async function addMember() {
  if (!memberTeam.value) return
  memberProblem.value = isUuid(memberUuid.value) ? undefined : 'Enter the participant user UUID.'
  if (memberProblem.value) return
  memberPending.value = true
  try {
    await teamsApi.adminAddMember(props.boardUuid, memberTeam.value.uuid, memberUuid.value.trim())
    memberTeam.value = null
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    memberPending.value = false
  }
}

async function removeMember() {
  if (!memberTeam.value) return
  memberProblem.value = isUuid(memberUuid.value) ? undefined : 'Enter the member user UUID.'
  if (memberProblem.value) return
  memberPending.value = true
  try {
    await teamsApi.adminRemoveMember(props.boardUuid, memberTeam.value.uuid, memberUuid.value.trim())
    memberTeam.value = null
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    memberPending.value = false
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="admin-teams-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="admin-teams-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Teams</h2>
        <p class="mt-1 text-xs text-muted-foreground">Staff team management. Captain identity is the returned captainUuid, never inferred from user permissions.</p>
      </div>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
        <Button size="sm" @click="openCreate">Create team</Button>
      </div>
    </div>
    <div class="space-y-3 px-4 py-4">
      <ErrorAlert v-if="error" :error="error" title="Team request failed" />
      <LoadingState v-if="loading" :rows="2" />
      <EmptyState v-else-if="items.length === 0" title="No teams" description="Create a team and add participant user UUIDs." />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Name</TableHead>
              <TableHead>Captain UUID</TableHead>
              <TableHead>Members</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="team in items" :key="team.uuid">
              <TableCell>{{ team.name }}</TableCell>
              <TableCell class="break-all text-xs text-muted-foreground">{{ team.captainUuid }}</TableCell>
              <TableCell>{{ team.membersCount }}/{{ team.maxMembers }}</TableCell>
              <TableCell class="text-right">
                <div class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="openMember(team)">Members</Button>
                  <Button variant="outline" size="sm" @click="openEdit(team)">Rename</Button>
                  <Button variant="destructive" size="sm" @click="deleteTarget = team">Delete</Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>

    <Dialog v-model:open="createOpen">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>{{ editTarget ? 'Rename team' : 'Create team' }}</DialogTitle>
          <DialogDescription>The captain must already be a board participant.</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="save">
          <FormField label="Team name" for-id="admin-team-name">
            <Input id="admin-team-name" v-model="name" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" @click="createOpen = false">Cancel</Button>
            <Button type="submit" :disabled="pending || !name.trim()">{{ pending ? 'Saving…' : 'Save' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="Boolean(memberTeam)" @update:open="(open) => { if (!open) memberTeam = null }">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>{{ memberTeam?.name }} members</DialogTitle>
          <DialogDescription>
            No participant team-member list endpoint exists in the current contract. Enter the participant user UUID.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="addMember">
          <FormField label="Participant user UUID" for-id="admin-team-member" :error="memberProblem">
            <Input id="admin-team-member" v-model="memberUuid" autocomplete="off" spellcheck="false" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" @click="memberTeam = null">Cancel</Button>
            <Button type="submit" variant="outline" :disabled="memberPending">Add</Button>
            <Button type="button" variant="destructive" :disabled="memberPending" @click="removeMember">Remove</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete team"
      description="The backend refuses when the team is not empty or still has scored participation."
      confirm-label="Delete team"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="removeTeam"
    />
  </section>
</template>