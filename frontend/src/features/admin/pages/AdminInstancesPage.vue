<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { instancesApi } from '@/features/instances/api'
import { useSession } from '@/features/auth/session'
import { formatDateTime } from '@/shared/lib/format'
import type { Instance } from '@/shared/types/api'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)
const items = ref<Instance[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
const boardUuid = ref('')
const moduleUuid = ref('')
const status = ref('')
const selected = ref<Instance | null>(null)
const actionError = ref<unknown>(null)
const busy = ref(false)
const destroyOpen = ref(false)

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    items.value = (await instancesApi.adminList({
      limit: 100,
      boardUuid: boardUuid.value.trim() || undefined,
      moduleUuid: moduleUuid.value.trim() || undefined,
      status: status.value.trim() || undefined,
    })).items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function open(instance: Instance) {
  selected.value = instance
  actionError.value = null
  try {
    selected.value = await instancesApi.adminGet(instance.uuid)
  } catch (caught) {
    actionError.value = caught
  }
}

async function action(next: 'RESET' | 'DESTROY' | 'EXTEND') {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    selected.value = await instancesApi.adminAction(selected.value.uuid, next)
    destroyOpen.value = false
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Instances" description="Read-only runtime inventory plus backend-allowed actions. No fake provider health.">
      <template #actions>
        <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState v-if="!isAdmin" title="Administrator access required" description="Instance inventory is global-admin visible." />
    <template v-else>
      <div class="mb-4 flex flex-wrap gap-2">
        <Input v-model="boardUuid" placeholder="boardUuid" class="max-w-xs" />
        <Input v-model="moduleUuid" placeholder="moduleUuid" class="max-w-xs" />
        <Input v-model="status" placeholder="status" class="max-w-40" />
        <Button variant="outline" @click="load">Filter</Button>
      </div>
      <LoadingState v-if="loading && items.length === 0" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not list instances" />
      <EmptyState v-else-if="items.length === 0" title="No instances" description="No backend instance inventory matches the filter." />
      <div class="space-y-2">
        <div v-for="instance in items" :key="instance.uuid" class="border border-border p-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p class="break-all text-sm">{{ instance.uuid }}</p>
              <p class="text-xs text-muted-foreground">{{ instance.scope }} · {{ instance.boardUuid }} / {{ instance.moduleUuid }} · {{ instance.owner.type }} {{ instance.owner.name }}</p>
            </div>
            <div class="flex items-center gap-2">
              <StatusBadge :status="instance.status" />
              <Button variant="outline" size="sm" @click="open(instance)">Details</Button>
            </div>
          </div>
        </div>
      </div>
      <div v-if="selected" class="mt-6 border border-border p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-sm font-semibold">Instance {{ selected.uuid }}</h2>
          <Button variant="ghost" size="sm" @click="selected = null">Close</Button>
        </div>
        <ErrorAlert v-if="actionError" :error="actionError" title="Instance action failed" class="mt-3" />
        <div class="mt-3 grid gap-2 text-sm sm:grid-cols-3">
          <p>Status: {{ selected.status }}</p>
          <p>Expires: {{ formatDateTime(selected.expiresAt) }}</p>
          <p>Remaining: {{ selected.remainingSeconds ?? '—' }} s</p>
        </div>
        <div class="mt-3 space-y-2">
          <div v-for="node in selected.nodes" :key="node.uuid" class="border border-border p-2 text-xs">
            <p>{{ node.key }} · {{ node.name }} · {{ node.kind }} · {{ node.status }}</p>
            <p v-for="endpoint in node.endpoints" :key="endpoint.key" class="break-all text-muted-foreground">
              {{ endpoint.key }} · {{ endpoint.host }}:{{ endpoint.port }} · {{ endpoint.exposure }}<span v-if="endpoint.url"> · {{ endpoint.url }}</span>
            </p>
          </div>
        </div>
        <div class="mt-3 flex flex-wrap gap-2">
          <Button variant="outline" size="sm" :disabled="busy" @click="action('RESET')">Reset</Button>
          <Button variant="outline" size="sm" :disabled="busy" @click="action('EXTEND')">Extend</Button>
          <Button variant="destructive" size="sm" :disabled="busy" @click="destroyOpen = true">Destroy</Button>
        </div>
      </div>
    </template>
    <ConfirmDialog
      v-model:open="destroyOpen"
      title="Destroy instance"
      description="The provider must confirm destroy before ports/leases are released."
      confirm-label="Destroy instance"
      destructive
      :pending="busy"
      @confirm="action('DESTROY')"
    />
  </PageContainer>
</template>