<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { isUuid } from '@/shared/lib/validate'
import type { Page, UserSummary } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()
const emit = defineEmits<{ (event: 'changed'): void }>()

const items = ref<UserSummary[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const addUuid = ref('')
const addProblem = ref<string | undefined>()
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

async function add() {
  addError.value = null
  addProblem.value = isUuid(addUuid.value) ? undefined : 'Enter the participant user UUID.'
  if (addProblem.value) return

  adding.value = true
  try {
    await boardsApi.addParticipant(props.boardUuid, { userUuid: addUuid.value.trim() })
    addUuid.value = ''
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
      <form class="space-y-3 border-b border-border pb-4" novalidate @submit.prevent="add">
        <ErrorAlert v-if="addError" :error="addError" title="Could not add participant" />
        <FormField label="Add participant by user UUID" for-id="add-participant" :error="addProblem">
          <Input id="add-participant" v-model="addUuid" autocomplete="off" spellcheck="false" />
        </FormField>
        <Button type="submit" variant="outline" :disabled="adding">
          {{ adding ? 'Adding…' : 'Add participant' }}
        </Button>
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