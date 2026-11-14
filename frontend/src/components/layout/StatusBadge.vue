<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'

type Tone = 'ok' | 'look' | 'blocked' | 'broken' | 'unknown' | 'off' | 'running' | 'gone'

const props = defineProps<{
  label: string
  tone?: Tone
}>()

const colour = computed(() => {
  switch (props.tone) {
    case 'ok':
      return 'var(--pine)'
    case 'look':
      return 'var(--gold)'
    case 'blocked':
      return 'var(--iris)'
    case 'broken':
      return 'var(--love)'
    case 'running':
      return 'var(--text)'
    case 'gone':
      return 'var(--muted-ink)'
    default:
      return 'var(--subtle)'
  }
})

const shape = computed(() => {
  switch (props.tone) {
    case 'ok':
      return 'rounded-full'
    case 'look':
      return '[clip-path:polygon(50%_0,100%_100%,0_100%)]'
    case 'blocked':
      return '[clip-path:polygon(50%_0,100%_50%,50%_100%,0_50%)]'
    case 'broken':
      return ''
    case 'running':
      return 'rounded-full animate-pulse'
    case 'gone':
      return 'h-0.5 w-2.5'
    default:
      return 'border'
  }
})
</script>

<template>
  <Badge variant="outline" class="rounded-none border-border text-foreground">
    <span
      aria-hidden="true"
      class="inline-block size-2 shrink-0"
      :class="shape"
      :style="{ background: tone === 'unknown' || tone === 'off' ? 'transparent' : colour, borderColor: colour }"
    />
    {{ label }}
  </Badge>
</template>