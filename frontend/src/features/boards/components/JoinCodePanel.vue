<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTime } from '@/shared/lib/format'

/**
 * Board enrollment code. The plaintext is shown exactly once, immediately after
 * generation or replacement, and lives only in this component's memory. It is
 * never written to Web Storage, the URL, logs or analytics, and it is cleared on
 * hide and unmount. A status read never returns code material.
 */
const props = defineProps<{ boardUuid: string }>()

type Status = 'unknown' | 'loading' | 'ready' | 'failed'
/** The action the organizer actually asked for, frozen when they clicked. */
type PendingAction = { kind: 'random' } | { kind: 'custom'; value: string }

const status = ref<Status>('unknown')
const configured = ref(false)
const revision = ref(0)
const updatedAt = ref<string | null>(null)
const revealed = ref<string | null>(null)
const custom = ref('')
const pending = ref(false)
const error = ref<unknown>(null)
const pendingAction = ref<PendingAction | null>(null)

const statusReady = computed(() => status.value === 'ready')
const mutating = computed(() => !statusReady.value || pending.value)
const replaceConfirmationOpen = computed(() => pendingAction.value !== null && configured.value)

async function loadStatus() {
  status.value = 'loading'
  error.value = null
  try {
    const response = await boardsApi.getBoardJoinCodeStatus(props.boardUuid)
    configured.value = response.configured
    revision.value = response.revision
    updatedAt.value = response.updatedAt ?? null
    status.value = 'ready'
  } catch (caught) {
    status.value = 'failed'
    error.value = caught
  }
}

/** First click on a mutating control freezes the intention. */
function requestGenerate() {
  if (mutating.value) return
  error.value = null
  pendingAction.value = { kind: 'random' }
  if (!configured.value) void commitPending()
}

function requestCustom() {
  if (mutating.value) return
  const value = custom.value.trim()
  if (!value) return
  error.value = null
  pendingAction.value = { kind: 'custom', value }
  if (!configured.value) void commitPending()
}

/** The confirmation (or the direct first-create path) commits the frozen action. */
async function commitPending() {
  const action = pendingAction.value
  if (!action || pending.value) return
  pending.value = true
  error.value = null
  try {
    const response =
      action.kind === 'random'
        ? await boardsApi.setBoardJoinCode(props.boardUuid, undefined, configured.value ? revision.value : undefined)
        : await boardsApi.setBoardJoinCode(props.boardUuid, action.value, configured.value ? revision.value : undefined)
    revealed.value = response.code
    configured.value = response.configured
    revision.value = response.revision
    updatedAt.value = response.updatedAt ?? null
    // The revealed code is retained until the organizer hides it; the custom
    // field is cleared because it has been consumed.
    custom.value = ''
    pendingAction.value = null
    status.value = 'ready'
  } catch (caught) {
    // Keep the typed value on a network/conflict failure so it can be retried.
    error.value = caught
    // A conflict means our revision is stale: refresh the status so the next
    // attempt uses the committed revision.
    pendingAction.value = null
    status.value = 'failed'
    void loadStatus()
  } finally {
    pending.value = false
  }
}

function cancelPending() {
  pendingAction.value = null
}

async function copy() {
  if (!revealed.value) return
  try {
    await navigator.clipboard.writeText(revealed.value)
  } catch {
    /* Clipboard can be unavailable; the visible value is still selectable. */
  }
}

/** Clears every secret and draft value this component holds. */
function hide() {
  revealed.value = null
  custom.value = ''
  pendingAction.value = null
}

onUnmounted(hide)

void loadStatus()
</script>

<template>
  <section class="border border-border" aria-labelledby="join-code-heading">
    <div class="border-b border-border px-4 py-3">
      <h2 id="join-code-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Join code
      </h2>
      <p class="mt-1 text-xs text-muted-foreground">
        Participants need this code to join. It is shown once and stored only as a hash, so it can never be
        revealed again — replace it if it is lost.
      </p>
    </div>
    <div class="space-y-3 px-4 py-4">
      <ErrorAlert v-if="error" :error="error" title="Could not update the join code" />

      <template v-if="revealed">
        <p class="text-xs font-semibold uppercase tracking-wider text-pine">Copy this code now</p>
        <Input :model-value="revealed" readonly class="font-mono" aria-label="New board join code" />
        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="copy">Copy</Button>
          <Button variant="outline" size="sm" @click="hide">Hide</Button>
        </div>
      </template>

      <template v-else>
        <p v-if="status === 'loading' || status === 'unknown'" class="text-sm text-muted-foreground">
          Loading join code status…
        </p>
        <template v-else-if="status === 'failed'">
          <p class="text-sm text-muted-foreground">
            The join code status could not be read, so generating or replacing is disabled until it succeeds.
          </p>
          <Button variant="outline" @click="loadStatus">Retry status</Button>
        </template>
        <template v-else>
          <p class="text-sm">
            Status:
            <span :class="configured ? 'text-pine' : 'text-muted-foreground'">
              {{ configured ? 'configured' : 'not configured' }}
            </span>
            <span v-if="configured && updatedAt" class="ml-2 text-xs text-muted-foreground">
              updated {{ formatDateTime(updatedAt) }}
            </span>
          </p>
          <p v-if="!configured" class="text-xs text-muted-foreground">
            No code is configured, so nobody can self-enroll yet. Generate one and share it with participants.
          </p>
          <div class="flex flex-wrap gap-2">
            <Button :disabled="mutating" @click="requestGenerate">
              {{ pending ? 'Working…' : configured ? 'Replace with random code' : 'Generate join code' }}
            </Button>
          </div>
          <div class="border-t border-border pt-3">
            <FormField
              label="Set a custom code"
              for-id="join-code-custom"
              hint="8–64 characters: A–Z, 0–9 and hyphen. Case is normalized to uppercase."
            >
              <Input
                id="join-code-custom"
                v-model="custom"
                autocomplete="off"
                spellcheck="false"
                :disabled="mutating"
              />
            </FormField>
            <Button
              class="mt-2"
              variant="outline"
              :disabled="mutating || !custom.trim()"
              @click="requestCustom"
            >
              {{ configured ? 'Replace with custom code' : 'Set custom code' }}
            </Button>
          </div>
          <EmptyState
            v-if="replaceConfirmationOpen"
            title="Replace the current join code?"
            description="The current code stops working immediately. Everyone who has not joined yet must receive the new code."
          >
            <div class="flex flex-wrap gap-2">
              <Button variant="destructive" :disabled="pending" @click="commitPending">
                {{ pending ? 'Replacing…' : 'Replace code' }}
              </Button>
              <Button variant="ghost" :disabled="pending" @click="cancelPending">Cancel</Button>
            </div>
          </EmptyState>
        </template>
      </template>
    </div>
  </section>
</template>
