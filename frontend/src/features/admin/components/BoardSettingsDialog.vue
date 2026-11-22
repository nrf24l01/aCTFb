<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { accessApi } from '@/features/access/api'
import { parseDateWindow, validateBoardName, validateSlug } from '@/features/admin/validation'
import { formatDateTimeLocal } from '@/shared/lib/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import { isApiError } from '@/shared/api/errors'
import { useDiscardGuard } from '@/features/admin/useDiscardGuard'
import type {
  AccessGroup,
  AdminBoard,
  AdminBoardCreateRequest,
  AdminBoardPatchRequest,
  BoardAccessMode,
  ParticipationMode,
  RatingVisibility,
} from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  board?: AdminBoard | null
  canManageAccessGroups?: boolean
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved', board: AdminBoard): void
}>()

const modes: Array<{ value: Exclude<BoardAccessMode, 'LEGACY'>; label: string; description: string }> = [
  { value: 'PUBLIC', label: 'Public', description: 'Anyone can browse the published board. Sign-in is still required to join, submit and score.' },
  { value: 'AUDIENCE', label: 'Selected audiences', description: 'Only members of the selected access groups can join.' },
  { value: 'INVITE', label: 'Invite code', description: 'Joining requires the board invite code. Rotating it invalidates the old code.' },
]

const form = reactive({
  slug: '',
  name: '',
  description: '',
  participationMode: 'INDIVIDUAL' as ParticipationMode,
  accessMode: 'PUBLIC' as BoardAccessMode,
  rating: 'PUBLIC' as RatingVisibility,
  startsAt: '',
  endsAt: '',
  teamMinMembers: 1,
  teamMaxMembers: 4,
  teamAllowSelfCreate: true,
  teamAllowLeaveAfterStart: true,
  accessGroupUuids: [] as string[],
})
const accessGroups = ref<AccessGroup[]>([])
const accessGroupQuery = ref('')
const accessGroupsLoading = ref(false)
const accessGroupsError = ref<unknown>(null)
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.board))
const slugTouched = ref(false)
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone

const inviteCode = ref('')
const inviteLoading = ref(false)
const inviteError = ref<unknown>(null)
const inviteCopied = ref(false)
const rotateConfirmOpen = ref(false)
const rotating = ref(false)
const rotateError = ref<unknown>(null)

const {
  discardOpen: discardConfirmOpen,
  reset: resetGuard,
  attemptClose,
  keepEditing,
  discardAndClose,
} = useDiscardGuard({
  snapshot: () => ({ ...form, inviteCode: inviteCode.value }),
  close: () => emit('update:open', false),
})

const initialAccessMode = ref<BoardAccessMode>('PUBLIC')
const gateChangeConfirmOpen = ref(false)
const pendingWindow = ref<{ startsAt: string | null; endsAt: string | null } | null>(null)
const legacyMode = computed(() => form.accessMode === 'LEGACY')
const accessModeChanged = computed(() => form.accessMode !== initialAccessMode.value)
// A legacy board has no canonical mode to send. Saving its name/dates must
// never send one, or the server would normalize (and replace) its gates.
const sendsCanonicalMode = computed(() => form.accessMode !== 'LEGACY')
// Replacing or clearing a live gate set is the one access change that needs an
// explicit acknowledgement, so a mode click cannot silently drop a group gate.
const gateChangeNeedsConfirm = computed(() => {
  if (!props.board || !accessModeChanged.value) return false
  if (initialAccessMode.value === 'LEGACY') return false
  return props.board.accessGroupUuids.length > 0 && form.accessMode !== 'AUDIENCE'
})
const showAudience = computed(() => form.accessMode === 'AUDIENCE')
const showInvite = computed(() => form.accessMode === 'INVITE')
const preview = computed(() => renderMarkdown(form.description))

watch(
  () => [props.open, props.board] as const,
  ([open, board]) => {
    if (!open) return
    form.name = board?.name ?? ''
    form.slug = board?.slug ?? ''
    form.description = board?.description ?? ''
    form.participationMode = board?.participationMode ?? 'INDIVIDUAL'
    form.accessMode = board?.accessMode ?? 'PUBLIC'
    initialAccessMode.value = board?.accessMode ?? 'PUBLIC'
    form.rating = board?.rating.visibility ?? 'PUBLIC'
    form.startsAt = board?.startsAt ? formatDateTimeLocal(new Date(board.startsAt)) : ''
    form.endsAt = board?.endsAt ? formatDateTimeLocal(new Date(board.endsAt)) : ''
    form.teamMinMembers = board?.teamSettings?.minMembers ?? 1
    form.teamMaxMembers = board?.teamSettings?.maxMembers ?? 4
    form.teamAllowSelfCreate = board?.teamSettings?.allowSelfCreate ?? true
    form.teamAllowLeaveAfterStart = board?.teamSettings?.allowLeaveAfterStart ?? true
    form.accessGroupUuids = [...(board?.accessGroupUuids ?? [])]
    slugTouched.value = Boolean(board)
    problems.value = {}
    error.value = null
    inviteCode.value = ''
    inviteError.value = null
    inviteCopied.value = false
    accessGroupQuery.value = ''
    if (props.canManageAccessGroups) void loadAccessGroups()
    if (board && board.accessMode === 'INVITE') void loadInvite()
    gateChangeConfirmOpen.value = false
    resetGuard()
  },
  { immediate: true },
)

watch(
  () => form.name,
  (name) => {
    if (slugTouched.value) return
    form.slug = name
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 64)
  },
)

async function loadAccessGroups() {
  accessGroupsLoading.value = true
  accessGroupsError.value = null
  try {
    accessGroups.value = (await accessApi.listGroups(100, 0, accessGroupQuery.value.trim())).items
  } catch (caught) {
    accessGroupsError.value = caught
  } finally {
    accessGroupsLoading.value = false
  }
}

async function loadInvite() {
  if (!props.board) return
  inviteLoading.value = true
  inviteError.value = null
  try {
    inviteCode.value = (await boardsApi.getBoardInviteCode(props.board.uuid)).inviteCode
  } catch (caught) {
    inviteError.value = caught
  } finally {
    inviteLoading.value = false
  }
}

function toggleAccessGroup(uuid: string) {
  form.accessGroupUuids = form.accessGroupUuids.includes(uuid)
    ? form.accessGroupUuids.filter((id) => id !== uuid)
    : [...form.accessGroupUuids, uuid]
}

function validateSettings(): string | undefined {
  if (showAudience.value && form.accessGroupUuids.length === 0) {
    return 'Choose at least one access group.'
  }
  if (form.participationMode === 'TEAM') {
    if (form.teamMinMembers < 1 || form.teamMaxMembers > 100 || form.teamMaxMembers < form.teamMinMembers) {
      return 'Team minimum must be 1–100 and maximum must be between minimum and 100.'
    }
  }
  return undefined
}

function focusFirstInvalid() {
  void nextTick(() => {
    const id = problems.value.name ? 'board-name' : problems.value.slug ? 'board-slug' : undefined
    if (id) document.getElementById(id)?.focus()
  })
}

async function submit() {
  error.value = null
  const window = parseDateWindow(form.startsAt, form.endsAt)
  problems.value = {
    slug: validateSlug(form.slug.trim()),
    name: validateBoardName(form.name.trim()),
    window: window.problem ?? validateSettings(),
  }
  if (Object.values(problems.value).some(Boolean)) {
    focusFirstInvalid()
    return
  }

  if (gateChangeNeedsConfirm.value) {
    pendingWindow.value = window
    gateChangeConfirmOpen.value = true
    return
  }
  await save(window)
}

async function save(window: { startsAt: string | null; endsAt: string | null }) {
  gateChangeConfirmOpen.value = false
  pendingWindow.value = null
  pending.value = true
  try {
    const teamSettings = form.participationMode === 'TEAM'
      ? {
          minMembers: form.teamMinMembers,
          maxMembers: form.teamMaxMembers,
          allowSelfCreate: form.teamAllowSelfCreate,
          allowLeaveAfterStart: form.teamAllowLeaveAfterStart,
        }
      : null
    // Common fields. Access fields are added only for an explicit mode, so a
    // legacy board can still have its name/dates edited without touching gates.
    const body: AdminBoardPatchRequest = {
      slug: form.slug.trim(),
      name: form.name.trim(),
      description: form.description,
      participationMode: form.participationMode,
      rating: { visibility: form.rating },
      startsAt: window.startsAt,
      endsAt: window.endsAt,
      teamSettings,
    }
    if (sendsCanonicalMode.value) {
      const access = {
        PUBLIC: { visibility: 'PUBLIC' as const, registrationMode: 'OPEN' as const, accessGroupUuids: [] as string[] },
        AUDIENCE: { visibility: 'PRIVATE' as const, registrationMode: 'OPEN' as const, accessGroupUuids: form.accessGroupUuids },
        INVITE: { visibility: 'PRIVATE' as const, registrationMode: 'INVITE_ONLY' as const, accessGroupUuids: [] as string[] },
      }[form.accessMode as 'PUBLIC' | 'AUDIENCE' | 'INVITE']
      body.registrationMode = access.registrationMode
      body.visibility = access.visibility
      body.accessMode = form.accessMode
      body.accessGroupUuids = access.accessGroupUuids
    }
    const saved = props.board
      ? await boardsApi.updateBoard(props.board.uuid, body)
      : await boardsApi.createBoard(body as AdminBoardCreateRequest)
    emit('saved', saved)
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        slug: caught.fieldIssue('slug'),
        name: caught.fieldIssue('name'),
        window: caught.fieldIssue('accessGroupUuids') ?? caught.fieldIssue('accessMode'),
      }
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}

async function copyInvite() {
  try {
    await navigator.clipboard.writeText(inviteCode.value)
    inviteCopied.value = true
  } catch {
    inviteCopied.value = false
  }
}

async function rotateInvite() {
  if (!props.board) return
  rotating.value = true
  rotateError.value = null
  try {
    inviteCode.value = (await boardsApi.rotateBoardInviteCode(props.board.uuid)).inviteCode
    rotateConfirmOpen.value = false
  } catch (caught) {
    rotateError.value = caught
  } finally {
    rotating.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(value) => { if (!value) attemptClose() }">
    <DialogContent class="flex max-h-[90dvh] flex-col rounded-none border-border bg-black max-md:h-dvh max-md:max-h-dvh max-md:max-w-none sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit board' : 'Create board' }}</DialogTitle>
        <DialogDescription>
          Choose how participants reach the board. The board is saved as a draft; publishing is a separate action.
        </DialogDescription>
      </DialogHeader>

      <form class="flex min-h-0 flex-1 flex-col" novalidate @submit.prevent="submit">
        <div class="min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
          <ErrorAlert v-if="error" :error="error" title="Could not save board" />

          <FormField label="Name" for-id="board-name" :error="problems.name">
            <Input id="board-name" v-model="form.name" autocomplete="off" />
          </FormField>

          <div class="space-y-2">
            <p class="text-sm font-medium">Description</p>
            <Tabs default-value="write">
              <TabsList variant="line">
                <TabsTrigger value="write">Write</TabsTrigger>
                <TabsTrigger value="preview">Preview</TabsTrigger>
              </TabsList>
              <TabsContent value="write">
                <Textarea id="board-description" v-model="form.description" rows="4" />
              </TabsContent>
              <TabsContent value="preview">
                <div class="min-h-24 border border-border px-3 py-2">
                  <!-- Sanitised by renderMarkdown. -->
                  <article class="space-y-2 text-sm" v-html="preview" />
                  <p v-if="!form.description.trim()" class="text-sm text-muted-foreground">Nothing to preview yet.</p>
                </div>
              </TabsContent>
            </Tabs>
          </div>

          <fieldset class="space-y-2 border border-border p-3">
            <legend class="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Access</legend>
            <p v-if="legacyMode" class="border border-gold/60 px-3 py-2 text-xs text-gold">
              This board uses a legacy access combination, so none of the three modes is selected. Its current read and
              join behaviour is preserved when you save the name, dates or other details. Choosing a mode below is an
              explicit change; it replaces the old access rules.
            </p>
            <label
              v-for="mode in modes"
              :key="mode.value"
              class="flex cursor-pointer items-start gap-3 border border-border p-3 text-sm"
              :class="form.accessMode === mode.value ? 'border-primary bg-muted' : ''"
            >
              <input
                v-model="form.accessMode"
                type="radio"
                name="board-access-mode"
                :value="mode.value"
                class="mt-1"
              />
              <span>
                <span class="block font-medium">{{ mode.label }}</span>
                <span class="mt-0.5 block text-xs text-muted-foreground">{{ mode.description }}</span>
              </span>
            </label>

            <div v-if="showAudience" class="space-y-2 border-t border-border pt-3">
              <p class="text-sm font-medium">Choose access groups</p>
              <ErrorAlert v-if="accessGroupsError" :error="accessGroupsError" title="Could not load access groups">
                <Button type="button" variant="ghost" class="mt-2" @click="loadAccessGroups">Retry</Button>
              </ErrorAlert>
              <p v-else-if="accessGroupsLoading" class="text-xs text-muted-foreground">Loading access groups…</p>
              <template v-else-if="canManageAccessGroups">
                <Input
                  v-model="accessGroupQuery"
                  placeholder="Search groups"
                  aria-label="Search access groups"
                  @update:model-value="loadAccessGroups"
                />
                <label v-for="group in accessGroups" :key="group.uuid" class="flex min-h-10 items-center gap-2 text-sm">
                  <Checkbox
                    :model-value="form.accessGroupUuids.includes(group.uuid)"
                    @update:model-value="toggleAccessGroup(group.uuid)"
                  />
                  {{ group.name }} <span class="text-xs text-muted-foreground">({{ group.usersCount }} members)</span>
                </label>
                <p v-if="accessGroups.length === 0" class="text-xs text-muted-foreground">No access groups exist yet.</p>
              </template>
              <p v-else class="text-xs text-muted-foreground">
                {{ form.accessGroupUuids.length }} group(s) currently gate this board. Only a global admin can change them.
              </p>
            </div>

            <div v-if="showInvite && isEdit" class="space-y-2 border-t border-border pt-3">
              <p class="text-sm font-medium">Invite code</p>
              <ErrorAlert v-if="inviteError" :error="inviteError" title="Could not load the invite code">
                <Button type="button" variant="ghost" class="mt-2" @click="loadInvite">Retry</Button>
              </ErrorAlert>
              <p v-else-if="inviteLoading" class="text-xs text-muted-foreground">Loading…</p>
              <template v-else-if="inviteCode">
                <div class="flex flex-wrap items-center gap-2">
                  <code class="border border-border px-2 py-1 font-mono text-xs">{{ inviteCode }}</code>
                  <Button type="button" variant="outline" size="sm" @click="copyInvite">
                    {{ inviteCopied ? 'Copied' : 'Copy' }}
                  </Button>
                  <Button type="button" variant="outline" size="sm" @click="rotateConfirmOpen = true">Rotate</Button>
                </div>
                <p class="text-xs text-muted-foreground">Rotating invalidates the old code immediately.</p>
              </template>
            </div>
            <p v-else-if="showInvite" class="border-t border-border pt-3 text-xs text-muted-foreground">
              Save the board first to generate and copy its invite code.
            </p>
          </fieldset>

          <div class="grid gap-3 sm:grid-cols-2">
            <FormField label="Participation" for-id="board-participation">
              <select id="board-participation" v-model="form.participationMode" class="h-9 w-full border border-border bg-black px-2 text-sm">
                <option value="INDIVIDUAL">Individual</option>
                <option value="TEAM">Teams</option>
              </select>
            </FormField>
            <FormField label="Starts at (optional)" for-id="board-starts" :hint="`Your timezone: ${timezone}`">
              <Input id="board-starts" v-model="form.startsAt" type="datetime-local" />
            </FormField>
          </div>
          <FormField label="Ends at (optional)" for-id="board-ends" :error="problems.window" :hint="`Your timezone: ${timezone}`">
            <Input id="board-ends" v-model="form.endsAt" type="datetime-local" />
          </FormField>

          <details class="border border-border p-3">
            <summary class="cursor-pointer text-sm font-medium">Advanced</summary>
            <div class="mt-3 space-y-3">
              <FormField label="URL slug" for-id="board-slug" hint="Lowercase letters, digits and hyphens." :error="problems.slug">
                <Input id="board-slug" v-model="form.slug" autocomplete="off" spellcheck="false" @input="slugTouched = true" />
              </FormField>
              <FormField label="Rating" for-id="board-rating">
                <select id="board-rating" v-model="form.rating" class="h-9 w-full border border-border bg-black px-2 text-sm">
                  <option value="PUBLIC">Public scoreboard</option>
                  <option value="HIDDEN">Hidden scores</option>
                </select>
              </FormField>
              <div v-if="form.participationMode === 'TEAM'" class="space-y-3 border-t border-border pt-3">
                <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Team settings</p>
                <div class="grid gap-3 sm:grid-cols-2">
                  <FormField label="Minimum members" for-id="team-min">
                    <Input id="team-min" v-model.number="form.teamMinMembers" type="number" min="1" max="100" />
                  </FormField>
                  <FormField label="Maximum members" for-id="team-max">
                    <Input id="team-max" v-model.number="form.teamMaxMembers" type="number" min="1" max="100" />
                  </FormField>
                </div>
                <label class="flex min-h-11 items-center gap-2 text-sm">
                  <Checkbox v-model="form.teamAllowSelfCreate" />
                  Allow participants to create teams
                </label>
                <label class="flex min-h-11 items-center gap-2 text-sm">
                  <Checkbox v-model="form.teamAllowLeaveAfterStart" />
                  Allow leaving a team after the board starts
                </label>
              </div>
            </div>
          </details>
        </div>

        <DialogFooter class="mt-4 border-t border-border pt-4">
          <Button type="button" variant="ghost" :disabled="pending" @click="attemptClose()">Cancel</Button>
          <Button type="submit" :disabled="pending">
            {{ pending ? 'Saving…' : isEdit ? 'Save changes' : 'Create board' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>

  <ConfirmDialog
    :open="gateChangeConfirmOpen"
    title="Change board access?"
    description="This board currently uses access groups. Switching away from Selected audiences replaces that group gate: non-members with an invite code, or anyone for Public, would be able to join. This cannot be undone by cancelling later."
    confirm-label="Change access"
    destructive
    :pending="pending"
    @update:open="(value) => { if (!value) gateChangeConfirmOpen = false }"
    @confirm="pendingWindow ? save(pendingWindow) : undefined"
  />
  <ConfirmDialog
    :open="discardConfirmOpen"
    title="Discard board changes?"
    description="Your unsaved edits will be lost. This cannot be undone."
    confirm-label="Discard changes"
    destructive
    @update:open="(value) => { if (!value) keepEditing() }"
    @confirm="discardAndClose()"
  />
  <ConfirmDialog
    :open="rotateConfirmOpen"
    title="Rotate invite code"
    description="The current invite code stops working immediately. Participants who already joined keep access."
    confirm-label="Rotate code"
    destructive
    :pending="rotating"
    @update:open="(open) => { if (!open) rotateConfirmOpen = false }"
    @confirm="rotateInvite"
  />
  <ErrorAlert v-if="rotateError" :error="rotateError" title="Could not rotate the invite code" />
</template>
