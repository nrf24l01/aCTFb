<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { accessApi } from '@/features/access/api'
import { parseDateWindow, validateBoardName, validateSlug } from '@/features/admin/validation'
import { formatDateTimeLocal } from '@/shared/lib/format'
import { isApiError } from '@/shared/api/errors'
import type {
  AccessGroup,
  AdminBoard,
  BoardVisibility,
  ParticipationMode,
  RatingVisibility,
  RegistrationMode,
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

const form = reactive({
  slug: '',
  name: '',
  description: '',
  participationMode: 'INDIVIDUAL' as ParticipationMode,
  registrationMode: 'OPEN' as RegistrationMode,
  visibility: 'PRIVATE' as BoardVisibility,
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
const accessGroupsLoading = ref(false)
const accessGroupsError = ref<unknown>(null)
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.board))

watch(
  () => [props.open, props.board] as const,
  ([open, board]) => {
    if (!open) return
    form.slug = board?.slug ?? ''
    form.name = board?.name ?? ''
    form.description = board?.description ?? ''
    form.participationMode = board?.participationMode ?? 'INDIVIDUAL'
    form.registrationMode = board?.registrationMode ?? 'OPEN'
    form.visibility = board?.visibility ?? 'PRIVATE'
    form.rating = board?.rating.visibility ?? 'PUBLIC'
    form.startsAt = board?.startsAt ? formatDateTimeLocal(new Date(board.startsAt)) : ''
    form.endsAt = board?.endsAt ? formatDateTimeLocal(new Date(board.endsAt)) : ''
    form.teamMinMembers = board?.teamSettings?.minMembers ?? 1
    form.teamMaxMembers = board?.teamSettings?.maxMembers ?? 4
    form.teamAllowSelfCreate = board?.teamSettings?.allowSelfCreate ?? true
    form.teamAllowLeaveAfterStart = board?.teamSettings?.allowLeaveAfterStart ?? true
    form.accessGroupUuids = [...(board?.accessGroupUuids ?? [])]
    problems.value = {}
    error.value = null
    if (props.canManageAccessGroups) void loadAccessGroups()
  },
  { immediate: true },
)

async function loadAccessGroups() {
  accessGroupsLoading.value = true
  accessGroupsError.value = null
  try {
    const page = await accessApi.listGroups(100, 0)
    accessGroups.value = page.items
  } catch (caught) {
    accessGroupsError.value = caught
  } finally {
    accessGroupsLoading.value = false
  }
}

function toggleAccessGroup(uuid: string) {
  form.accessGroupUuids = form.accessGroupUuids.includes(uuid)
    ? form.accessGroupUuids.filter((id) => id !== uuid)
    : [...form.accessGroupUuids, uuid]
}

function validateSettings(): string | undefined {
  if (form.participationMode === 'TEAM') {
    if (form.teamMinMembers < 1 || form.teamMaxMembers > 100 || form.teamMaxMembers < form.teamMinMembers) {
      return 'Team minimum must be 1–100 and maximum must be between minimum and 100.'
    }
  }
  if (form.visibility === 'PUBLIC' && form.accessGroupUuids.length > 0) {
    return 'A PUBLIC board cannot use access groups.'
  }
  return undefined
}

async function submit() {
  error.value = null
  const window = parseDateWindow(form.startsAt, form.endsAt)
  problems.value = {
    slug: validateSlug(form.slug.trim()),
    name: validateBoardName(form.name.trim()),
    window: window.problem ?? validateSettings(),
  }
  if (Object.values(problems.value).some(Boolean)) return

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
    const body = {
      slug: form.slug.trim(),
      name: form.name.trim(),
      description: form.description,
      participationMode: form.participationMode,
      registrationMode: form.registrationMode,
      visibility: form.visibility,
      rating: { visibility: form.rating },
      startsAt: window.startsAt,
      endsAt: window.endsAt,
      accessGroupUuids: form.visibility === 'PRIVATE' ? form.accessGroupUuids : [],
      teamSettings,
    }
    const saved = props.board
      ? await boardsApi.updateBoard(props.board.uuid, body)
      : await boardsApi.createBoard(body)
    emit('saved', saved)
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        slug: caught.fieldIssue('slug'),
        name: caught.fieldIssue('name'),
      }
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit board' : 'Create board' }}</DialogTitle>
        <DialogDescription>
          P1R board policy: PUBLIC/PRIVATE visibility, INDIVIDUAL/TEAM participation and OPEN/INVITE_ONLY/CLOSED
          registration. Unsupported combinations are rejected by the backend.
        </DialogDescription>
      </DialogHeader>

      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not save board" />
        <FormField label="Slug" for-id="board-slug" hint="Lowercase letters, digits and hyphens." :error="problems.slug">
          <Input id="board-slug" v-model="form.slug" autocomplete="off" spellcheck="false" />
        </FormField>
        <FormField label="Name" for-id="board-name" :error="problems.name">
          <Input id="board-name" v-model="form.name" />
        </FormField>
        <FormField label="Description" for-id="board-description">
          <Textarea id="board-description" v-model="form.description" rows="3" />
        </FormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField label="Participation" for-id="board-participation">
            <Select v-model="form.participationMode">
              <SelectTrigger id="board-participation" class="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="INDIVIDUAL">INDIVIDUAL</SelectItem>
                <SelectItem value="TEAM">TEAM</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
          <FormField label="Visibility" for-id="board-visibility">
            <Select v-model="form.visibility">
              <SelectTrigger id="board-visibility" class="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="PRIVATE">PRIVATE</SelectItem>
                <SelectItem value="PUBLIC">PUBLIC</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
        </div>
        <FormField label="Registration" for-id="board-registration">
          <Select v-model="form.registrationMode">
            <SelectTrigger id="board-registration" class="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="OPEN">OPEN — self join with a board link</SelectItem>
              <SelectItem value="INVITE_ONLY">INVITE_ONLY — board invite code required</SelectItem>
              <SelectItem value="CLOSED">CLOSED — staff add participants</SelectItem>
            </SelectContent>
          </Select>
        </FormField>

        <div v-if="form.participationMode === 'TEAM'" class="space-y-3 border border-border p-3">
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

        <div v-if="form.visibility === 'PRIVATE'" class="space-y-2 border border-border p-3">
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Access groups</p>
          <p class="text-xs text-muted-foreground">
            Private boards with access groups require membership in at least one group to join. Groups are global-admin managed.
          </p>
          <ErrorAlert v-if="accessGroupsError" :error="accessGroupsError" title="Could not load access groups" />
          <p v-if="accessGroupsLoading" class="text-xs text-muted-foreground">Loading access groups…</p>
          <template v-else-if="canManageAccessGroups">
            <label v-for="group in accessGroups" :key="group.uuid" class="flex min-h-10 items-center gap-2 text-sm">
              <Checkbox
                :model-value="form.accessGroupUuids.includes(group.uuid)"
                @update:model-value="toggleAccessGroup(group.uuid)"
              />
              {{ group.name }} <span class="text-xs text-muted-foreground">({{ group.usersCount }} users)</span>
            </label>
            <p v-if="accessGroups.length === 0" class="text-xs text-muted-foreground">No access groups exist yet.</p>
          </template>
          <p v-else class="text-xs text-muted-foreground">
            {{ form.accessGroupUuids.length }} group(s) currently gate this board. Only a global admin can change them.
          </p>
        </div>
        <FormField label="Rating" for-id="board-rating">
          <Select v-model="form.rating">
            <SelectTrigger id="board-rating" class="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="PUBLIC">PUBLIC — scoreboard visible</SelectItem>
              <SelectItem value="HIDDEN">HIDDEN — scores recorded, board hidden</SelectItem>
            </SelectContent>
          </Select>
        </FormField>
        <FormField label="Starts at (optional)" for-id="board-starts">
          <Input id="board-starts" v-model="form.startsAt" type="datetime-local" />
        </FormField>
        <FormField label="Ends at (optional)" for-id="board-ends" :error="problems.window">
          <Input id="board-ends" v-model="form.endsAt" type="datetime-local" />
        </FormField>
        <p class="text-xs text-muted-foreground">
          Team/access-group changes are validated against existing participation and modules by the backend.
        </p>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : isEdit ? 'Save board' : 'Create board' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>