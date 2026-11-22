<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
import UserPicker from '@/features/access/components/UserPicker.vue'
import { accessApi } from '@/features/access/api'
import { boardsApi } from '@/features/boards/api/boards'
import { grantableRoles, validateGrant } from '@/features/boards/permissions'
import { toIsoOrNull } from '@/shared/lib/format'
import { isApiError } from '@/shared/api/errors'
import type {
  BoardPermissionsResponse,
  BoardRole,
  ModuleListItem,
  UserDirectoryEntry,
} from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  boardUuid: string
  permissions: BoardPermissionsResponse | null
  modules: ModuleListItem[]
  globalAdmin: boolean
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const form = reactive({ userUuid: '', role: 'board_editor' as BoardRole, moduleUuid: 'none', expiresAt: '' })
const selectedUser = ref<UserDirectoryEntry | null>(null)
const problem = ref<string | null | undefined>()
const error = ref<unknown>(null)
const pending = ref(false)

const roles = computed(() => grantableRoles(props.permissions, props.globalAdmin))

async function searchUsers(query: string): Promise<UserDirectoryEntry[]> {
  return (await accessApi.searchBoardUsers(props.boardUuid, query, 20, 0)).items
}

function selectUser(user: UserDirectoryEntry) {
  selectedUser.value = user
  form.userUuid = user.uuid
  if (problem.value === 'Choose a user.') problem.value = undefined
}

watch(
  () => [props.open, roles.value] as const,
  ([open, available]) => {
    if (!open) return
    form.userUuid = ''
    selectedUser.value = null
    form.role = available.includes('board_editor') ? 'board_editor' : available[0] ?? 'reviewer'
    form.moduleUuid = 'none'
    form.expiresAt = ''
    problem.value = undefined
    error.value = null
  },
  { immediate: true },
)

watch(
  () => form.role,
  (role) => {
    if (role === 'board_owner') form.moduleUuid = 'none'
  },
)

async function submit() {
  error.value = null
  const moduleUuid = form.moduleUuid === 'none' ? null : form.moduleUuid
  const expiresAt = toIsoOrNull(form.expiresAt)
  if (form.expiresAt && !expiresAt) {
    problem.value = 'Enter a valid expiry date and time.'
    return
  }
  problem.value = !form.userUuid
    ? 'Choose a user.'
    : validateGrant({
        role: form.role,
        moduleUuid,
        expiresAt,
        modules: props.modules,
        permissions: props.permissions,
        globalAdmin: props.globalAdmin,
      })
  if (problem.value) return

  pending.value = true
  try {
    await boardsApi.createRoleBinding(props.boardUuid, {
      userUuid: form.userUuid.trim(),
      role: form.role,
      moduleUuid,
      expiresAt,
    })
    emit('saved')
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught) && caught.code === 'ROLE_GRANT_NOT_ALLOWED') {
      problem.value = caught.message
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="rounded-none border-border bg-black sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>Grant scoped role</DialogTitle>
        <DialogDescription>
          Role and module are immutable after creation. To change them, revoke and grant again.
        </DialogDescription>
      </DialogHeader>
      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not grant role" />
        <div class="space-y-2">
          <UserPicker
            label="User"
            :search="searchUsers"
            placeholder="Search by username, email or UUID"
            @select="selectUser"
          />
          <p v-if="selectedUser" class="text-xs text-muted-foreground">
            Selected: {{ selectedUser.username }}<span v-if="selectedUser.email"> · {{ selectedUser.email }}</span>
          </p>
          <p v-if="problem" class="text-xs text-love">{{ problem }}</p>
        </div>
        <FormField label="Role" for-id="grant-role">
          <Select v-model="form.role">
            <SelectTrigger id="grant-role" class="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="role in roles" :key="role" :value="role">{{ role }}</SelectItem>
            </SelectContent>
          </Select>
        </FormField>
        <FormField
          label="Module scope"
          for-id="grant-module"
          hint="Leave board-wide for a board_owner or board-wide editor/reviewer."
        >
          <Select v-model="form.moduleUuid" :disabled="form.role === 'board_owner'">
            <SelectTrigger id="grant-module" class="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">Board-wide</SelectItem>
              <SelectItem v-for="module in modules" :key="module.uuid" :value="module.uuid">
                {{ module.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </FormField>
        <FormField label="Expiry (optional)" for-id="grant-expiry" hint="Must be in the future. Leave empty for no expiry.">
          <Input id="grant-expiry" v-model="form.expiresAt" type="datetime-local" />
        </FormField>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Granting…' : 'Grant role' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>