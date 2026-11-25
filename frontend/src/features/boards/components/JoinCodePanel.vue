<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
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

const configured = ref(false)
const updatedAt = ref<string | null>(null)
const revealed = ref<string | null>(null)
const custom = ref('')
const statusLoading = ref(false)
const pending = ref(false)
const error = ref<unknown>(null)
const replaceOpen = ref(false)
const statusLoaded = ref(false)

async function loadStatus() {
  statusLoading.value = true
  error.value = null
  try {
    const status = await boardsApi.getBoardJoinCodeStatus(props.boardUuid)
    configured.value = status.configured
    updatedAt.value = status.updatedAt ?? null
    statusLoaded.value = true
  } catch (caught) {
    error.value = caught
  } finally {
    statusLoading.value = false
  }
}

async function generate() {
  if (configured.value && !replaceOpen.value) {
    replaceOpen.value = true
    return
  }
  await setCode(undefined)
}

async function setCustom() {
  const value = custom.value.trim()
  if (!value) return
  if (configured.value && !replaceOpen.value) {
    replaceOpen.value = true
    return
  }
  await setCode(value)
}

async function setCode(value: string | undefined) {
  pending.value = true
  error.value = null
  try {
    const response = await boardsApi.setBoardJoinCode(props.boardUuid, value)
    revealed.value = response.code
    configured.value = response.configured
    updatedAt.value = response.updatedAt ?? null
    custom.value = ''
    replaceOpen.value = false
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}

async function copy() {
  if (!revealed.value) return
  try {
    await navigator.clipboard.writeText(revealed.value)
  } catch {
    /* Clipboard can be unavailable; the visible value is still selectable. */
  }
}

function hide() {
  revealed.value = null
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
        <p v-if="statusLoading" class="text-sm text-muted-foreground">Loading join code status…</p>
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
          <p v-if="!configured && statusLoaded" class="text-xs text-muted-foreground">
            No code is configured, so nobody can self-enroll yet. Generate one and share it with participants.
          </p>
          <div class="flex flex-wrap gap-2">
            <Button :disabled="pending" @click="generate">
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
                :disabled="pending"
              />
            </FormField>
            <Button class="mt-2" variant="outline" :disabled="pending || !custom.trim()" @click="setCustom">
              {{ configured ? 'Replace with custom code' : 'Set custom code' }}
            </Button>
          </div>
          <EmptyState
            v-if="replaceOpen"
            title="Replace the current join code?"
            description="The current code stops working immediately. Everyone who has not joined yet must receive the new code."
          >
            <div class="flex flex-wrap gap-2">
              <Button variant="destructive" :disabled="pending" @click="setCode(custom.trim() || undefined)">
                {{ pending ? 'Replacing…' : 'Replace code' }}
              </Button>
              <Button variant="ghost" :disabled="pending" @click="replaceOpen = false">Cancel</Button>
            </div>
          </EmptyState>
        </template>
      </template>
    </div>
  </section>
</template>
