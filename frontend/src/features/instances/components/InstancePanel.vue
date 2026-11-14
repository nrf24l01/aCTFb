<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { instancesApi } from '@/features/instances/api'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import type { Instance, Task } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string; moduleUuid: string; task: Task }>()
const emit = defineEmits<{ (event: 'changed'): void }>()

const instance = ref<Instance | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref<unknown>(null)
const destroyOpen = ref(false)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let alive = true

const transient = computed(() =>
  instance.value ? ['PROVISIONING', 'RESETTING', 'DESTROYING'].includes(instance.value.status) : false,
)
const canStart = computed(() => !instance.value && props.task.instance.canStart)
const canAct = computed(() => instance.value?.status === 'RUNNING')

function stopPoll() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = null
  try {
    instance.value = await instancesApi.getTaskInstance(props.boardUuid, props.moduleUuid, props.task.uuid)
    if (transient.value) schedulePoll()
  } catch (caught) {
    if (isApiError(caught) && (caught.status === 404 || caught.code === 'INSTANCE_NOT_FOUND')) {
      instance.value = null
    } else {
      error.value = caught
    }
  } finally {
    loading.value = false
  }
}

function schedulePoll() {
  stopPoll()
  if (!alive) return
  pollTimer = setTimeout(() => {
    if (alive) void load(true)
  }, 2500)
}

async function start() {
  busy.value = true
  error.value = null
  try {
    instance.value = await instancesApi.startTaskInstance(props.boardUuid, props.moduleUuid, props.task.uuid)
    schedulePoll()
    emit('changed')
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function action(next: 'RESET' | 'DESTROY' | 'EXTEND') {
  busy.value = true
  error.value = null
  try {
    instance.value = await instancesApi.taskAction(props.boardUuid, props.moduleUuid, props.task.uuid, next)
    destroyOpen.value = false
    schedulePoll()
    emit('changed')
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

function copy(value: string) {
  void navigator.clipboard?.writeText(value)
}

onUnmounted(() => {
  alive = false
  stopPoll()
})

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="instance-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <div>
        <h2 id="instance-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Instance</h2>
        <p class="mt-1 text-xs text-muted-foreground">Actions are backend-authorized and idempotent. No client-side lifecycle override.</p>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="load()">Refresh</Button>
    </div>
    <div class="space-y-3 px-4 py-4">
      <LoadingState v-if="loading" :rows="2" />
      <ErrorAlert v-else-if="error" :error="error" title="Instance request failed" />
      <template v-else>
        <EmptyState
          v-if="!instance"
          title="No instance"
          :description="task.instance.cannotStartReason || 'Start the lab when the backend permits it.'"
        >
          <Button v-if="canStart" :disabled="busy" @click="start">{{ busy ? 'Starting…' : 'Start instance' }}</Button>
          <p v-else class="text-xs text-muted-foreground">Starting is not currently allowed for this task/subject.</p>
        </EmptyState>
        <template v-else>
          <div class="flex flex-wrap items-center gap-2">
            <StatusBadge :label="instance.status" :tone="instance.status === 'RUNNING' ? 'ok' : instance.status === 'ERROR' ? 'broken' : 'running'" />
            <span class="text-xs text-muted-foreground">{{ instance.owner.type }} {{ instance.owner.name }}</span>
            <span class="text-xs text-muted-foreground">Created {{ formatDateTime(instance.createdAt) }}</span>
          </div>
          <div class="grid gap-2 text-sm sm:grid-cols-3">
            <p>Expires: {{ formatDateTime(instance.expiresAt) }}</p>
            <p>Remaining: {{ instance.remainingSeconds ?? '—' }} s</p>
            <p>Extensions: {{ instance.extensionCount ?? 0 }}</p>
          </div>
          <div class="space-y-2">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Endpoints</p>
            <EmptyState v-if="instance.nodes.length === 0" title="No nodes yet" description="The provider has not reported node state." />
            <div v-for="node in instance.nodes" :key="node.uuid" class="border border-border p-2">
              <p class="text-sm">{{ node.key }} · {{ node.name }} · {{ node.kind }} · {{ node.status }}</p>
              <div v-for="endpoint in node.endpoints" :key="endpoint.key" class="mt-1 flex flex-wrap items-center gap-2 text-xs">
                <span class="break-all">{{ endpoint.label || endpoint.key }} · {{ endpoint.type }} · {{ endpoint.host }}:{{ endpoint.port }} · {{ endpoint.exposure }}</span>
                <button v-if="endpoint.url" class="underline decoration-border underline-offset-4" @click="copy(endpoint.url)">Copy URL</button>
                <span v-if="endpoint.command" class="text-muted-foreground">display only: {{ endpoint.command }}</span>
              </div>
            </div>
          </div>
          <div class="flex flex-wrap gap-2 border-t border-border pt-3">
            <Button v-if="canAct" variant="outline" size="sm" :disabled="busy" @click="action('RESET')">Reset</Button>
            <Button v-if="canAct" variant="outline" size="sm" :disabled="busy" @click="action('EXTEND')">Extend</Button>
            <Button v-if="instance.status !== 'DESTROYING'" variant="destructive" size="sm" :disabled="busy" @click="destroyOpen = true">Destroy</Button>
          </div>
          <Alert class="rounded-none border-border bg-black">
            <AlertTitle>Instance logs</AlertTitle>
            <AlertDescription>No log endpoint exists in the current Swagger; the UI does not fabricate logs. Provider status/endpoints above are real.</AlertDescription>
          </Alert>
        </template>
      </template>
    </div>
    <ConfirmDialog
      v-model:open="destroyOpen"
      title="Destroy instance"
      description="The provider must confirm destroy before ports/leases are released. This cannot be undone."
      confirm-label="Destroy instance"
      destructive
      :pending="busy"
      @confirm="action('DESTROY')"
    />
  </section>
</template>