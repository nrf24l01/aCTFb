<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import UserPicker from '@/features/access/components/UserPicker.vue'
import { accessApi } from '@/features/access/api'
import { boardsApi } from '@/features/boards/api/boards'
import type { Page, UserDirectoryEntry, UserSummary } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()
const emit = defineEmits<{ (event: 'changed'): void }>()

const items = ref<UserSummary[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const addError = ref<unknown>(null)
const adding = ref(false)
const removeTarget = ref<UserSummary | null>(null)
const removing = ref(false)
const removeError = ref<unknown>(null)
const page = ref<Page<UserSummary> | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    page.value = await boardsApi.listParticipants(props.boardUuid, { limit: 100 })
    items.value = page.value.items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function searchUsers(query: string): Promise<UserDirectoryEntry[]> {
  // Adding a participant needs the broader directory, which the server only
  // allows with board.participants.manage.
  const response = await accessApi.searchBoardUsers(props.boardUuid, query, 'DIRECTORY', 20, 0)
  return response.items
}

async function add(user: UserDirectoryEntry) {
  addError.value = null
  adding.value = true
  try {
    await boardsApi.addParticipant(props.boardUuid, { userUuid: user.uuid })
    await load()
    emit('changed')
  } catch (caught) {
    addError.value = caught
  } finally {
    adding.value = false
  }
}

async function remove() {
  if (!removeTarget.value) return
  removing.value = true
  removeError.value = null
  try {
    await boardsApi.removeParticipant(props.boardUuid, removeTarget.value.uuid)
    removeTarget.value = null
    await load()
    emit('changed')
  } catch (caught) {
    removeError.value = caught
  } finally {
    removing.value = false
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="participants-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="participants-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Participants
        </h2>
        <p class="mt-1 text-xs text-muted-foreground">Staff are not participants unless a participant row exists.</p>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
    </div>

    <div class="space-y-4 px-4 py-4">
      <form class="space-y-3 border-b border-border pb-4" novalidate @submit.prevent>
        <ErrorAlert v-if="addError" :error="addError" title="Could not add participant" />
        <UserPicker
          label="Add participant"
          :search="searchUsers"
          :exclude="items.map((user) => user.uuid)"
          :placeholder="adding ? 'Adding…' : 'Search by username, email or UUID'"
          @select="add"
        />
      </form>

      <LoadingState v-if="loading" :rows="3" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load participants" />
      <EmptyState v-else-if="items.length === 0" title="No participants" description="Nobody has joined this board yet." />
      <div v-else class="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>User</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="user in items" :key="user.uuid" class="border-border">
              <TableCell>
                <span class="font-medium">{{ user.username }}</span>
                <span class="block text-xs text-muted-foreground">{{ user.uuid }}</span>
              </TableCell>
              <TableCell class="text-right">
                <Button variant="destructive" size="sm" @click="removeTarget = user; removeError = null">
                  Remove
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p class="mt-2 text-xs text-muted-foreground">{{ page?.total ?? items.length }} participant(s)</p>
      </div>
    </div>

    <ConfirmDialog
      :open="Boolean(removeTarget)"
      title="Remove participant"
      :description="`Remove ${removeTarget?.username ?? 'this participant'} from the board? The backend decides whether removal is allowed; no score history is destroyed by a client assumption.`"
      confirm-label="Remove participant"
      destructive
      :pending="removing"
      @update:open="(open) => { if (!open) removeTarget = null }"
      @confirm="remove"
    />
    <ErrorAlert v-if="removeError" :error="removeError" title="Could not remove participant" class="m-4" />
  </section>
</template>