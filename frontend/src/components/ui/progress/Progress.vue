<script setup lang="ts">
import type { ProgressRootProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
import { ProgressIndicator, ProgressRoot } from 'reka-ui'

const props = withDefaults(
  defineProps<
    ProgressRootProps & {
      class?: HTMLAttributes['class']
    }
  >(),
  {
    modelValue: 0,
  },
)

const delegatedProps = reactiveOmit(props, 'class')

const percentage = computed(() => {
  const max = props.max ?? 100
  const value = props.modelValue ?? 0
  if (max <= 0) return 0
  return Math.min(100, Math.max(0, (value / max) * 100))
})
</script>

<template>
  <ProgressRoot
    data-slot="progress"
    v-bind="delegatedProps"
    :class="cn('relative h-1 w-full overflow-hidden bg-muted', props.class)"
  >
    <ProgressIndicator
      data-slot="progress-indicator"
      class="h-full w-full flex-1 bg-status-success-ink"
      :style="{ transform: `translateX(-${100 - percentage}%)` }"
    />
  </ProgressRoot>
</template>
