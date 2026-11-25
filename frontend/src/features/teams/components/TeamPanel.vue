<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import SearchPicker, { type SearchPickerOption } from '@/features/access/components/SearchPicker.vue'
import { accessApi } from '@/features/access/api'
import { teamsApi } from '@/features/teams/api'
import { isTeamCaptain } from '@/features/teams/permissions'
import { useSession } from '@/features/auth/session'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import type { CaptainRosterEntry, Team, TeamMember } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()
const { state } = useSession()

const team = ref<Team | null>(null)
const members = ref<TeamMember[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const busy = ref(false)

const teamName = ref('')
const inviteInput = ref('')
const formProblem = ref<string | undefined>()
const rosterError = ref<unknown>(null)
// A captain cannot add a member directly: the product only exposes invite-key
// self-join. Selecting an eligible participant therefore reveals the invite key
// and names who to send it to, which is the single clear next action.
const inviteTargetName = ref('')
const selectedMember = ref<TeamMember | null>(null)

const invite = ref<{ inviteKey: string; expiresAt?: string | null } | null>(null)
const inviteError = ref<unknown>(null)
const inviteCopied = ref(false)
const rotateOpen = ref(false)
const leaveOpen = ref(false)
const removeOpen = ref(false)
const transferOpen = ref(false)

const myUuid = computed(() => state.user?.uuid ?? '')
const isCaptain = computed(() => isTeamCaptain(team.value, myUuid.value))
const otherMembers = computed(() => members.value.filter((member) => member.userUuid !== myUuid.value))
const memberIds = computed(() => members.value.map((member) => member.userUuid))

async function load() {
  loading.value = true
  error.value = null
  try {
    team.value = await teamsApi.getMyTeam(props.boardUuid)
    teamName.value = team.value.name
    members.value = (await teamsApi.listMyTeamMembers(props.boardUuid)).items
  } catch (caught) {
    if (isApiError(caught) && (caught.code === 'TEAM_NOT_FOUND' || caught.status === 404)) {
      team.value = null
      members.value = []
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
    members.value = (await teamsApi.listMyTeamMembers(props.boardUuid)).items
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
    members.value = (await teamsApi.listMyTeamMembers(props.boardUuid)).items
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
  inviteCopied.value = false
  try {
    invite.value = await teamsApi.getInviteKey(props.boardUuid)
  } catch (caught) {
    inviteError.value = caught
  }
}

async function copyInvite() {
  if (!invite.value) return
  try {
    await navigator.clipboard.writeText(invite.value.inviteKey)
    inviteCopied.value = true
  } catch {
    inviteCopied.value = false
  }
}

async function rotateInvite() {
  inviteError.value = null
  try {
    invite.value = await teamsApi.rotateInviteKey(props.boardUuid)
    inviteCopied.value = false
    rotateOpen.value = false
  } catch (caught) {
    inviteError.value = caught
  }
}

function onRosterSelect(option: SearchPickerOption) {
  inviteTargetName.value = option.primary
  void revealInvite()
}

/** Captain-only: find an eligible board participant to send the invite key to. */
async function searchRoster(query: string, signal: AbortSignal): Promise<SearchPickerOption[]> {
  rosterError.value = null
  try {
    if (signal.aborted) return []
    const page = await accessApi.searchCaptainRoster(props.boardUuid, query, 20, 0)
    return (page.items as CaptainRosterEntry[]).map((entry) => ({
      uuid: entry.uuid,
      primary: entry.username,
      hint: entry.uuid.slice(0, 8),
    }))
  } catch (caught) {
    rosterError.value = caught
    return []
  }
}

async function removeMember() {
  if (!selectedMember.value) return
  busy.value = true
  error.value = null
  try {
    await teamsApi.removeMember(props.boardUuid, selectedMember.value.userUuid)
    selectedMember.value = null
    removeOpen.value = false
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function transferCaptain() {
  if (!selectedMember.value) return
  busy.value = true
  error.value = null
  try {
    team.value = await teamsApi.transferCaptain(props.boardUuid, selectedMember.value.userUuid)
    selectedMember.value = null
    transferOpen.value = false
    await load()
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
  inviteCopied.value = false
  inviteTargetName.value = ''
}

onUnmounted(hideInvite)

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="team-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="team-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Team</h2>
        <p class="mt-1 text-xs text-muted-foreground">Create or join a team. The captain manages the roster.</p>
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
            <StatusBadge label="Team" tone="neutral" />
            <span class="text-sm">{{ team.name }}</span>
            <span class="text-xs text-muted-foreground">{{ team.membersCount }}/{{ team.maxMembers }} members</span>
            <span v-if="isCaptain" class="text-xs text-pine">You are captain</span>
          </div>

          <div class="space-y-1">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Members</p>
            <ul class="divide-y divide-border border border-border">
              <li v-for="member in members" :key="member.userUuid" class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
                <span>
                  <span class="font-medium">{{ member.username }}</span>
                  <span v-if="member.userUuid === team.captainUuid" class="ml-2 text-xs text-pine">Captain</span>
                </span>
                <Button
                  v-if="isCaptain && member.userUuid !== myUuid"
                  variant="destructive"
                  size="sm"
                  :disabled="busy"
                  @click="selectedMember = member; removeOpen = true; error = null"
                >
                  Remove
                </Button>
              </li>
            </ul>
            <p v-if="members.length === 0" class="text-xs text-muted-foreground">Member names appear after the roster loads.</p>
          </div>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <FormField label="Rename team" for-id="team-rename">
              <Input id="team-rename" v-model="teamName" />
            </FormField>
            <Button variant="outline" size="sm" :disabled="busy || !teamName.trim()" @click="renameTeam">Save name</Button>
          </div>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Invite key</p>
            <p class="text-xs text-muted-foreground">
              Share the key with a board participant so they can join this team. It is held only while revealed.
            </p>
            <ErrorAlert v-if="inviteError" :error="inviteError" title="Could not read invite key" />
            <template v-if="invite">
              <Input :model-value="invite.inviteKey" readonly class="font-mono" aria-label="Team invite key" />
              <p class="text-xs text-muted-foreground">Expires: {{ formatDateTime(invite.expiresAt) }}</p>
              <div class="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" @click="copyInvite">{{ inviteCopied ? 'Copied' : 'Copy key' }}</Button>
                <Button variant="outline" size="sm" @click="hideInvite">Hide</Button>
                <Button variant="destructive" size="sm" @click="rotateOpen = true; inviteError = null">Rotate key</Button>
              </div>
            </template>
            <Button v-else variant="outline" size="sm" @click="revealInvite">Reveal current key</Button>
          </div>

          <div v-if="isCaptain" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Find a participant</p>
            <p class="text-xs text-muted-foreground">
              Search this board's participants who are not yet on a team. Send them the invite key to join.
            </p>
            <ErrorAlert v-if="rosterError" :error="rosterError" title="Could not search participants" />
            <SearchPicker
              label="Eligible participant"
              :search="searchRoster"
              :exclude="memberIds"
              placeholder="Search by username, email or UUID"
              @select="onRosterSelect"
            />
            <p v-if="inviteTargetName" class="text-xs text-pine">
              Invite key ready. Send it to <strong>{{ inviteTargetName }}</strong> so they can join with
              “Join team”.
            </p>
            <p class="text-xs text-muted-foreground">Only participant names are shown here; email addresses are never disclosed.</p>
          </div>

          <div v-if="isCaptain && otherMembers.length > 0" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Transfer captaincy</p>
            <label class="block text-sm font-medium" for="team-transfer-member">New captain</label>
            <select
              id="team-transfer-member"
              :value="selectedMember?.userUuid ?? ''"
              class="h-9 w-full border border-border bg-black px-2 text-sm"
              @change="selectedMember = otherMembers.find((member) => member.userUuid === ($event.target as HTMLSelectElement).value) ?? null"
            >
              <option value="">Choose a current member</option>
              <option v-for="member in otherMembers" :key="member.userUuid" :value="member.userUuid">{{ member.username }}</option>
            </select>
            <Button
              variant="outline"
              size="sm"
              :disabled="busy || !selectedMember"
              @click="transferOpen = true; error = null"
            >
              Transfer captain
            </Button>
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
      :description="`Remove ${selectedMember?.username ?? 'this member'} from the team?`"
      confirm-label="Remove member"
      destructive
      :pending="busy"
      @confirm="removeMember"
    />
    <ConfirmDialog
      v-model:open="transferOpen"
      title="Transfer captaincy"
      :description="`Make ${selectedMember?.username ?? 'this member'} the captain? You stop being captain immediately.`"
      confirm-label="Transfer captain"
      destructive
      :pending="busy"
      @confirm="transferCaptain"
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
