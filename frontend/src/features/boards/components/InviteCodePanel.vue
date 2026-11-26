<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTime } from '@/shared/lib/format'
import { isApiError } from '@/shared/api/errors'

/**
 * Legacy board invite code: the deprecated encrypted credential that only
 * boards created before join codes still carry. The canonical join code in
 * JoinCodePanel is what participants actually use; this panel is a read-only
 * compatibility view and is never fetched until an organizer asks for it.
 *
 * The value lives only in this component's memory while revealed. It is never
 * written to Web Storage, URLs, logs or analytics. Hiding or unmounting clears
 * it.
 */
const code = ref<string | null>(null)
const expiresAt = ref<string | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
const rotateOpen = ref(false)
const rotating = ref(false)
/** The board answered that it has no legacy code (a normal state for new boards). */
const absent = ref(false)

const props = defineProps<{ boardUuid: string }>()

async function load() {
  loading.value = true
  error.value = null
  absent.value = false
  try {
    const response = await boardsApi.getBoardInviteCode(props.boardUuid)
    code.value = response.inviteCode
    expiresAt.value = response.expiresAt ?? null
  } catch (caught) {
    // The documented no-code answer for a board created after join codes
    // shipped. It is an empty state, not a failure: no request is made until
    // the organizer asks, and the canonical panel is the primary flow.
    if (isApiError(caught) && caught.code === 'INVALID_INVITE_CODE') {
      absent.value = true
      return
    }
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function rotate() {
  rotating.value = true
  error.value = null
  try {
    const response = await boardsApi.rotateBoardInviteCode(props.boardUuid)
    code.value = response.inviteCode
    expiresAt.value = response.expiresAt ?? null
    rotateOpen.value = false
  } catch (caught) {
    error.value = caught
  } finally {
    rotating.value = false
  }
}

async function copy() {
  if (!code.value) return
  try {
    await navigator.clipboard.writeText(code.value)
  } catch {
    /* Clipboard can be unavailable; the visible value is still selectable. */
  }
}

function hide() {
  code.value = null
  expiresAt.value = null
  absent.value = false
  error.value = null
}

onUnmounted(hide)
</script>

<template>
  <section class="border border-border" aria-labelledby="invite-code-heading">
    <div class="border-b border-border px-4 py-3">
      <h2 id="invite-code-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Legacy invite code
      </h2>
      <p class="mt-1 text-xs text-muted-foreground">
        Only boards created before join codes existed have one. Participants join with the code in the
        <strong class="font-medium">Join code</strong> panel; this legacy code is shown only to staff who may
        manage the board and is never stored in the browser.
      </p>
    </div>
    <div class="space-y-3 px-4 py-4">
      <ErrorAlert v-if="error" :error="error" title="Could not read the legacy invite code" />
      <p v-else-if="absent" class="text-sm text-muted-foreground">
        This board has no legacy invite code. Use the Join code panel to generate or set the code participants
        need.
      </p>
      <template v-if="code">
        <Input :model-value="code" readonly class="font-mono" aria-label="Current board invite code" />
        <p class="text-xs text-muted-foreground">Expires: {{ formatDateTime(expiresAt) }}</p>
        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="copy">Copy</Button>
          <Button variant="outline" size="sm" @click="hide">Hide</Button>
          <Button variant="destructive" size="sm" @click="rotateOpen = true; error = null">Rotate code</Button>
        </div>
      </template>
      <Button v-else-if="!absent" :disabled="loading" @click="load">
        {{ loading ? 'Loading…' : 'Reveal legacy code' }}
      </Button>
      <p v-if="code" class="text-xs text-muted-foreground">
        Rotating this legacy code invalidates it immediately and does not change the join code.
      </p>
    </div>
    <ConfirmDialog
      v-model:open="rotateOpen"
      title="Rotate invite code"
      description="The current code will stop working immediately. Anyone holding it must receive the new code."
      confirm-label="Rotate code"
      destructive
      :pending="rotating"
      @confirm="rotate"
    />
  </section>
</template>