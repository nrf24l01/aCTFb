<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { accessApi } from '@/features/access/api'
import { useSession } from '@/features/auth/session'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import type { AccessGroup, AdminUser, GlobalRole } from '@/shared/types/api'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const items = ref<AdminUser[]>([])
const total = ref(0)
const query = ref('')
const loading = ref(false)
const error = ref<unknown>(null)
const groups = ref<AccessGroup[]>([])

const dialogOpen = ref(false)
const editTarget = ref<AdminUser | null>(null)
const pending = ref(false)
const formError = ref<unknown>(null)
const problems = ref<Record<string, string | undefined>>({})
const form = reactive({
  email: '',
  username: '',
  password: '',
  emailVerified: false,
  disabled: false,
  isAdmin: false,
  accessGroupUuids: [] as string[],
})

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    const page = await accessApi.listUsers(100, 0, query.value.trim())
    items.value = page.items
    total.value = page.total
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function loadGroups() {
  try {
    groups.value = (await accessApi.listGroups(100, 0)).items
  } catch {
    groups.value = []
  }
}

function openCreate() {
  editTarget.value = null
  Object.assign(form, {
    email: '',
    username: '',
    password: '',
    emailVerified: false,
    disabled: false,
    isAdmin: false,
    accessGroupUuids: [],
  })
  problems.value = {}
  formError.value = null
  dialogOpen.value = true
}

function openEdit(user: AdminUser) {
  editTarget.value = user
  Object.assign(form, {
    email: user.email,
    username: user.username,
    password: '',
    emailVerified: user.emailVerified,
    disabled: user.disabled,
    isAdmin: user.roles.includes('admin'),
    accessGroupUuids: user.accessGroups.map((group) => group.uuid),
  })
  problems.value = {}
  formError.value = null
  dialogOpen.value = true
}

function toggleGroup(uuid: string) {
  form.accessGroupUuids = form.accessGroupUuids.includes(uuid)
    ? form.accessGroupUuids.filter((id) => id !== uuid)
    : [...form.accessGroupUuids, uuid]
}

async function save() {
  formError.value = null
  problems.value = {
    email: form.email.trim() ? undefined : 'Enter an email.',
    username: form.username.trim() ? undefined : 'Enter a username.',
    password: editTarget.value || form.password ? undefined : 'Enter an initial password.',
  }
  if (Object.values(problems.value).some(Boolean)) return

  const roles: GlobalRole[] = form.isAdmin ? ['user', 'admin'] : ['user']
  pending.value = true
  try {
    if (editTarget.value) {
      await accessApi.updateUser(editTarget.value.uuid, {
        email: form.email.trim(),
        username: form.username.trim(),
        emailVerified: form.emailVerified,
        disabled: form.disabled,
        roles,
        accessGroupUuids: form.accessGroupUuids,
      })
    } else {
      await accessApi.createUser({
        email: form.email.trim(),
        username: form.username.trim(),
        password: form.password,
        emailVerified: form.emailVerified,
        disabled: form.disabled,
        roles,
        accessGroupUuids: form.accessGroupUuids,
      })
    }
    dialogOpen.value = false
    await load()
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        email: caught.fieldIssue('email'),
        username: caught.fieldIssue('username'),
        password: caught.fieldIssue('password'),
      }
    }
    formError.value = caught
  } finally {
    pending.value = false
  }
}

void load()
void loadGroups()
</script>

<template>
  <PageContainer>
    <PageHeader title="Users" description="Global-admin user administration, roles and access-group membership.">
      <template #actions>
        <Button v-if="isAdmin" @click="openCreate">Create user</Button>
        <Button v-if="isAdmin" variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState
      v-if="!isAdmin"
      title="Administrator access required"
      description="Only a global admin can list users, change roles or assign access groups."
    />
    <template v-else>
      <form class="mb-4 flex flex-wrap gap-2" @submit.prevent="load">
        <Input v-model="query" placeholder="Search users" class="max-w-sm" />
        <Button type="submit" variant="outline">Search</Button>
      </form>
      <LoadingState v-if="loading && items.length === 0" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load users" />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>User</TableHead>
              <TableHead>Roles</TableHead>
              <TableHead>Access groups</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="user in items" :key="user.uuid">
              <TableCell>
                <span class="font-medium">{{ user.username }}</span>
                <span class="block text-xs text-muted-foreground">{{ user.email }}</span>
              </TableCell>
              <TableCell class="text-xs">{{ user.roles.join(', ') }}</TableCell>
              <TableCell class="text-xs">{{ user.accessGroups.map((g) => g.name).join(', ') || '—' }}</TableCell>
              <TableCell class="text-xs">
                {{ user.disabled ? 'disabled' : 'active' }} · {{ user.emailVerified ? 'verified' : 'unverified' }}
              </TableCell>
              <TableCell class="whitespace-nowrap text-xs text-muted-foreground">{{ formatDateTime(user.createdAt) }}</TableCell>
              <TableCell class="text-right">
                <Button variant="outline" size="sm" @click="openEdit(user)">Edit</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p class="px-4 py-2 text-xs text-muted-foreground">{{ total }} user(s)</p>
      </div>
    </template>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{{ editTarget ? 'Edit user' : 'Create user' }}</DialogTitle>
          <DialogDescription>Role changes are global-admin operations. Access groups do not grant board RBAC by themselves.</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="save">
          <ErrorAlert v-if="formError" :error="formError" title="Could not save user" />
          <FormField label="Email" for-id="admin-user-email" :error="problems.email">
            <Input id="admin-user-email" v-model="form.email" type="email" autocomplete="off" />
          </FormField>
          <FormField label="Username" for-id="admin-user-username" :error="problems.username">
            <Input id="admin-user-username" v-model="form.username" autocomplete="off" />
          </FormField>
          <FormField v-if="!editTarget" label="Initial password" for-id="admin-user-password" :error="problems.password">
            <Input id="admin-user-password" v-model="form.password" type="password" autocomplete="new-password" />
          </FormField>
          <label class="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox v-model="form.isAdmin" />
            Global admin
          </label>
          <label class="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox v-model="form.emailVerified" />
            Email verified
          </label>
          <label class="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox v-model="form.disabled" />
            Account disabled
          </label>
          <div class="space-y-2 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Access groups</p>
            <label v-for="group in groups" :key="group.uuid" class="flex min-h-10 items-center gap-2 text-sm">
              <Checkbox
                :model-value="form.accessGroupUuids.includes(group.uuid)"
                @update:model-value="toggleGroup(group.uuid)"
              />
              {{ group.name }}
            </label>
            <p v-if="groups.length === 0" class="text-xs text-muted-foreground">No groups exist yet.</p>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="pending" @click="dialogOpen = false">Cancel</Button>
            <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save user' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </PageContainer>
</template>