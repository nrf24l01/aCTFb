<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import RoleGrantDialog from '@/features/boards/components/RoleGrantDialog.vue'
import RoleExpiryDialog from '@/features/boards/components/RoleExpiryDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { expiryState, grantableRoles } from '@/features/boards/permissions'
import { formatDateTime } from '@/shared/lib/format'
import type { BoardPermissionsResponse, BoardRoleBinding, ModuleListItem } from '@/shared/types/api'

const props = defineProps<{
  boardUuid: string
  permissions: BoardPermissionsResponse | null
  modules: ModuleListItem[]
  globalAdmin: boolean
}>()

const emit = defineEmits<{ (event: 'changed'): void }>()

const items = ref<BoardRoleBinding[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<unknown>(null)
const grantOpen = ref(false)
const editBinding = ref<BoardRoleBinding | null>(null)
const revokeBinding = ref<BoardRoleBinding | null>(null)
const revoking = ref(false)
const revokeError = ref<unknown>(null)

const roles = computed(() => grantableRoles(props.permissions, props.globalAdmin))
const moduleName = (uuid: string | null | undefined) =>
  uuid ? props.modules.find((module) => module.uuid === uuid)?.name ?? uuid : 'Board-wide'

async function load() {
  loading.value = true
  error.value = null
  try {
    const page = await boardsApi.listRoleBindings(props.boardUuid, { limit: 100 })
    items.value = page.items
    total.value = page.total
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function onSaved() {
  void load()
  emit('changed')
}

async function revoke() {
  if (!revokeBinding.value) return
  revoking.value = true
  revokeError.value = null
  try {
    await boardsApi.revokeRoleBinding(props.boardUuid, revokeBinding.value.uuid)
    revokeBinding.value = null
    onSaved()
  } catch (caught) {
    revokeError.value = caught
  } finally {
    revoking.value = false
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="roles-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="roles-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Scoped roles
        </h2>
        <p class="mt-1 text-xs text-muted-foreground">Grants are live. Revocation and expiry take effect on the next request.</p>
      </div>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
        <Button size="sm" :disabled="roles.length === 0" @click="grantOpen = true">Grant role</Button>
      </div>
    </div>

    <div class="px-4 py-4">
      <p class="mb-3 text-xs text-muted-foreground">
        <template v-if="globalAdmin">As a global admin you can grant board_owner, board_editor or reviewer.</template>
        <template v-else>Owners can grant board_editor or reviewer only. Only a global admin can grant board_owner.</template>
      </p>

      <LoadingState v-if="loading" :rows="3" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load role bindings" />
      <EmptyState v-else-if="items.length === 0" title="No role bindings" description="No scoped staff grants exist for this board." />
      <div v-else class="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>User</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Expiry</TableHead>
              <TableHead>State</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="binding in items" :key="binding.uuid">
              <TableCell>
                <span class="font-medium">{{ binding.user.username }}</span>
                <span class="block text-xs text-muted-foreground">{{ binding.user.uuid }}</span>
              </TableCell>
              <TableCell><Badge variant="outline" class="rounded-none">{{ binding.role }}</Badge></TableCell>
              <TableCell class="text-xs">{{ moduleName(binding.moduleUuid) }}</TableCell>
              <TableCell class="whitespace-nowrap text-xs">{{ formatDateTime(binding.expiresAt) }}</TableCell>
              <TableCell>
                <Badge
                  variant="outline"
                  class="rounded-none"
                  :class="expiryState(binding) === 'active' ? 'border-pine/60 text-pine' : 'border-border text-muted-foreground'"
                >
                  {{ expiryState(binding) }}
                </Badge>
              </TableCell>
              <TableCell class="text-right">
                <div v-if="expiryState(binding) === 'active'" class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="editBinding = binding">Edit expiry</Button>
                  <Button variant="destructive" size="sm" @click="revokeBinding = binding; revokeError = null">Revoke</Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p class="mt-2 text-xs text-muted-foreground">{{ total }} binding(s)</p>
      </div>
    </div>

    <RoleGrantDialog
      v-model:open="grantOpen"
      :board-uuid="boardUuid"
      :permissions="permissions"
      :modules="modules"
      :global-admin="globalAdmin"
      @saved="onSaved"
    />
    <RoleExpiryDialog
      :open="Boolean(editBinding)"
      :board-uuid="boardUuid"
      :binding="editBinding"
      @update:open="(open) => { if (!open) editBinding = null }"
      @saved="onSaved"
    />
    <ConfirmDialog
      :open="Boolean(revokeBinding)"
      title="Revoke role"
      :description="`${revokeBinding?.user.username ?? 'This user'} loses ${revokeBinding?.role ?? 'the role'} immediately. Revoked history is kept.`"
      confirm-label="Revoke role"
      destructive
      :pending="revoking"
      @update:open="(open) => { if (!open) revokeBinding = null }"
      @confirm="revoke"
    />
    <ErrorAlert v-if="revokeError" :error="revokeError" title="Could not revoke role" class="m-4" />
  </section>
</template>