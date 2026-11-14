<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import { teamsApi } from '@/features/teams/api'
import { isTeamCaptain } from '@/features/teams/permissions'
import { useSession } from '@/features/auth/session'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import { isUuid } from '@/shared/lib/validate'
import type { Team } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()
const { state } = useSession()

const team = ref<Team | null>(null)
const loading = ref(true)
const error = ref<unknown>(null)
const busy = ref(false)

const teamName = ref('')
const inviteInput = ref('')
const memberUuid = ref('')
const formProblem = ref<string | undefined>()

const invite = ref<{ inviteKey: string; expiresAt?: string | null } | null>(null)
const inviteError = ref<unknown>(null)
const rotateOpen = ref(false)
const leaveOpen = ref(false)
const removeOpen = ref(false)

const myUuid = computed(() => state.user?.uuid ?? '')
const isCaptain = computed(() => isTeamCaptain(team.value, myUuid.value))

async function load() {
  loading.value = true
  error.value = null
  try {
    team.value = await teamsApi.getMyTeam(props.boardUuid)
    teamName.value = team.value.name
  } catch (caught) {
    if (isApiError(caught) && (caught.code === 'TEAM_NOT_FOUND' || caught.status === 404)) {
      team.value = null
    } else {
      error.value = caught
    }
  } finally {
    loading.value = false
  }
}

async function createTeam() {
  formProblem.value = undefined
  if (!teamName.value.trim()) {
    formProblem.value = 'Enter a team name.'
    return
  }
  busy.value = true
  error.value = null
  try {
    team.value = await teamsApi.createTeam(props.boardUuid, teamName.value.trim())
    teamName.value = team.value.name
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function joinTeam() {
  formProblem.value = undefined
  if (inviteInput.value.length < 16) {
    formProblem.value = 'Invite keys are 16–128 characters.'
    return
  }
  busy.value = true
  error.value = null
  try {
    team.value = await teamsApi.joinTeam(props.boardUuid, inviteInput.value.trim())
    inviteInput.value = ''
    teamName.value = team.value.name
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function renameTeam() {
  if (!team.value || !isCaptain.value) return
  busy.value = true
  error.value = null
  try {
    team.value = await teamsApi.updateMyTeam(props.boardUuid, teamName.value.trim())
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function revealInvite() {
  inviteError.value = null
  try {
    invite.value = await teamsApi.getInviteKey(props.boardUuid)
  } catch (caught) {
    inviteError.value = caught
  }
}

async function rotateInvite() {
  inviteError.value = null
  try {
    invite.value = await teamsApi.rotateInviteKey(props.boardUuid)
    rotateOpen.value = false
  } catch (caught) {
    inviteError.value = caught
  }
}

async function removeMember() {
  formProblem.value = undefined
  if (!isUuid(memberUuid.value)) {
    formProblem.value = 'Enter the member user UUID.'
    return
  }
  busy.value = true
  error.value = null
  try {
    await teamsApi.removeMember(props.boardUuid, memberUuid.value.trim())
    memberUuid.value = ''
    removeOpen.value = false
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function transferCaptain() {
  formProblem.value = undefined
  if (!isUuid(memberUuid.value)) {
    formProblem.value = 'Enter the new captain user UUID.'
    return
  }
  busy.value = true
  error.value = null
  try {
    team.value = await teamsApi.transferCaptain(props.boardUuid, memberUuid.value.trim())
    memberUuid.value = ''
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function leaveTeam() {
  busy.value = true
  error.value = null
  try {
    await teamsApi.leaveTeam(props.boardUuid)
    leaveOpen.value = false
    invite.value = null
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

function hideInvite() {
  invite.value = null
}

onUnmounted(hideInvite)

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="team-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="team-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Team</h2>
        <p class="mt-1 text-xs text-muted-foreground">TEAM participation: invites, captain transfer and membership are backend-authoritative.</p>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
    </div>

    <div class="space-y-4 px-4 py-4">
      <LoadingState v-if="loading" :rows="2" />
      <ErrorAlert v-else-if="error" :error="error" title="Team request failed" />

      <template v-if="!loading">
        <div v-if="!team" class="space-y-4">
          <p class="text-sm text-muted-foreground">You are not in a team yet. Create one if allowed, or join with an invite key.</p>
          <form class="space-y-3" novalidate @submit.prevent="createTeam">
            <FormField label="Create team" for-id="team-create-name" :error="formProblem">
              <Input id="team-create-name" v-model="teamName" autocomplete="off" />
            </FormField>
            <Button type="submit" :disabled="busy">{{ busy ? 'Working…' : 'Create team' }}</Button>
          </form>
          <form class="space-y-3 border-t border-border pt-4" novalidate @submit.prevent="joinTeam">
            <FormField label="Join with invite key" for-id="team-join-key" hint="16–128 URL-safe characters.">
              <Input id="team-join-key" v-model="inviteInput" autocomplete="off" spellcheck="false" />
            </FormField>
            <Button type="submit" variant="outline" :disabled="busy">{{ busy ? 'Working…' : 'Join team' }}</Button>
          </form>
        </div>

        <div v-else class="space-y-4">
          <div class="flex flex-wrap items-center gap-2">
            <StatusBadge label="Team" tone="ok" />
            <span class="text-sm">{{ team.name }}</span>
            <span class="text-xs text-muted-foreground">{{ team.membersCount }}/{{ team.maxMembers }} members</span>
            <span v-if="isCaptain" class="text-xs text-pine">You are captain</span>
          </div>
          <p class="break-all text-xs text-muted-foreground">Captain UUID: {{ team.captainUuid }}</p>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <FormField label="Rename team" for-id="team-rename">
              <Input id="team-rename" v-model="teamName" />
            </FormField>
            <Button variant="outline" size="sm" :disabled="busy || !teamName.trim()" @click="renameTeam">Save name</Button>
          </div>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Invite key</p>
            <p class="text-xs text-muted-foreground">
              The key is held only in this component while revealed and is cleared on hide/unmount.
            </p>
            <ErrorAlert v-if="inviteError" :error="inviteError" title="Could not read invite key" />
            <template v-if="invite">
              <Input :model-value="invite.inviteKey" readonly class="font-mono" aria-label="Team invite key" />
              <p class="text-xs text-muted-foreground">Expires: {{ formatDateTime(invite.expiresAt) }}</p>
              <div class="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" @click="hideInvite">Hide</Button>
                <Button variant="destructive" size="sm" @click="rotateOpen = true; inviteError = null">Rotate key</Button>
              </div>
            </template>
            <Button v-else variant="outline" size="sm" @click="revealInvite">Reveal current key</Button>
          </div>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Member actions</p>
            <p class="text-xs text-muted-foreground">
              The API does not expose a participant team-member directory. Enter the member UUID from the organizer.
            </p>
            <FormField label="Member user UUID" for-id="team-member-uuid" :error="formProblem">
              <Input id="team-member-uuid" v-model="memberUuid" autocomplete="off" spellcheck="false" />
            </FormField>
            <div class="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" :disabled="busy" @click="transferCaptain">Transfer captain</Button>
              <Button variant="destructive" size="sm" :disabled="busy" @click="removeOpen = true; error = null">Remove member</Button>
            </div>
          </div>

          <div class="flex flex-wrap gap-2 border-t border-border pt-4">
            <Button v-if="!isCaptain" variant="destructive" size="sm" :disabled="busy" @click="leaveOpen = true">Leave team</Button>
            <p v-else class="text-xs text-muted-foreground">Transfer captaincy before leaving a team.</p>
          </div>
        </div>
      </template>
    </div>

    <ConfirmDialog
      v-model:open="rotateOpen"
      title="Rotate team invite key"
      description="The old invite key stops working immediately."
      confirm-label="Rotate key"
      destructive
      @confirm="rotateInvite"
    />
    <ConfirmDialog
      v-model:open="removeOpen"
      title="Remove team member"
      description="The selected user leaves the team. The backend refuses captain removal without a transfer."
      confirm-label="Remove member"
      destructive
      :pending="busy"
      @confirm="removeMember"
    />
    <ConfirmDialog
      v-model:open="leaveOpen"
      title="Leave team"
      description="Leaving is subject to the board team settings and the board start window."
      confirm-label="Leave team"
      destructive
      :pending="busy"
      @confirm="leaveTeam"
    />
  </section>
</template>