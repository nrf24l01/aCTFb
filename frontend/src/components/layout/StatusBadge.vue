<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'
import {
  CircleAlertIcon,
  CircleCheckIcon,
  CircleDotIcon,
  CircleIcon,
  CircleSlashIcon,
  ClockIcon,
  LockIcon,
} from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { statusLabel, statusTone } from '@/shared/lib/status'
import type { StatusTone } from '@/shared/lib/status'

// Older callers still pass these names; map them to the semantic tones instead
// of keeping a second visual language.
type LegacyTone = 'ok' | 'look' | 'blocked' | 'broken' | 'unknown' | 'off' | 'running' | 'gone'
type Tone = StatusTone | LegacyTone

const LEGACY_TONES: Record<string, StatusTone> = {
  ok: 'success',
  look: 'attention',
  blocked: 'restricted',
  broken: 'error',
  unknown: 'neutral',
  off: 'neutral',
  running: 'active',
  gone: 'muted',
}

const TONE_ICONS: Record<StatusTone, Component> = {
  success: CircleCheckIcon,
  active: CircleDotIcon,
  attention: ClockIcon,
  error: CircleAlertIcon,
  restricted: LockIcon,
  neutral: CircleIcon,
  muted: CircleSlashIcon,
}

const props = defineProps<{
  label?: string
  tone?: Tone
  status?: string
}>()

const resolvedTone = computed<StatusTone>(() => {
  if (props.tone) {
    const legacy = LEGACY_TONES[props.tone]
    if (legacy) return legacy
    // An unexpected runtime value must not produce an empty icon or `var(undefined)`.
    return props.tone in TONE_ICONS ? (props.tone as StatusTone) : 'neutral'
  }
  return props.status ? statusTone(props.status) : 'neutral'
})

const text = computed(() => props.label ?? (props.status ? statusLabel(props.status) : ''))

const icon = computed(() => TONE_ICONS[resolvedTone.value])

const palette = computed(() => ({
  color: `var(--status-${resolvedTone.value}-ink)`,
  backgroundColor: `var(--status-${resolvedTone.value}-surface)`,
  borderColor: `var(--status-${resolvedTone.value}-border)`,
}))
</script>

<template>
  <Badge variant="outline" class="gap-1.5 rounded-none" :style="palette">
    <component :is="icon" aria-hidden="true" class="size-3 shrink-0" />
    <span class="min-w-0 truncate">{{ text }}</span>
  </Badge>
</template>
