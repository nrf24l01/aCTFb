<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTime } from '@/shared/lib/format'

/**
 * Board invite codes live only in this component's memory while revealed.
 * They are never written to Web Storage, URLs, logs or analytics. Hiding or
 * unmounting clears the value.
 */
const code = ref<string | null>(null)
const expiresAt = ref<string | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
const rotateOpen = ref(false)
const rotating = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    const response = await boardsApi.getBoardInviteCode(props.boardUuid)
    code.value = response.inviteCode
    expiresAt.value = response.expiresAt ?? null
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

const props = defineProps<{ boardUuid: string }>()

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
}

onUnmounted(hide)
</script>

<template>
  <section class="border border-border" aria-labelledby="invite-code-heading">
    <div class="border-b border-border px-4 py-3">
      <h2 id="invite-code-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        Board invite code
      </h2>
      <p class="mt-1 text-xs text-muted-foreground">
        Needed to join this board. Shown only to staff who may manage it and never stored in the browser.
      </p>
    </div>
    <div class="space-y-3 px-4 py-4">
      <ErrorAlert v-if="error" :error="error" title="Could not read invite code" />
      <template v-if="code">
        <Input :model-value="code" readonly class="font-mono" aria-label="Current board invite code" />
        <p class="text-xs text-muted-foreground">Expires: {{ formatDateTime(expiresAt) }}</p>
        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="copy">Copy</Button>
          <Button variant="outline" size="sm" @click="hide">Hide</Button>
          <Button variant="destructive" size="sm" @click="rotateOpen = true; error = null">Rotate code</Button>
        </div>
      </template>
      <Button v-else :disabled="loading" @click="load">
        {{ loading ? 'Loading…' : 'Reveal current code' }}
      </Button>
      <p class="text-xs text-muted-foreground">
        Rotating invalidates the old code immediately.
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