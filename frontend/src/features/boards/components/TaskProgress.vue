<script setup lang="ts">
import { computed } from 'vue'
import { CircleCheckIcon } from '@lucide/vue'
import { Progress } from '@/components/ui/progress'

const props = withDefaults(
  defineProps<{
    solvedTasks: number
    totalTasks: number
    label: string
    compact?: boolean
  }>(),
  {
    compact: false,
  },
)

const solved = computed(() => Math.max(0, Math.trunc(props.solvedTasks)))
const total = computed(() => Math.max(0, Math.trunc(props.totalTasks)))
const complete = computed(() => total.value > 0 && solved.value >= total.value)
// 0/0 is an empty track, never a complete/100% bar: a max of 1 keeps the
// progressbar numeric attributes valid while the visible fraction stays 0/0.
const max = computed(() => (total.value > 0 ? total.value : 1))
const value = computed(() => (total.value > 0 ? Math.min(solved.value, total.value) : 0))
const fraction = computed(() => `${solved.value}/${total.value} solved`)
const valueText = computed(() => `${solved.value} of ${total.value} tasks solved`)
</script>

<template>
  <div class="w-full" :class="{ 'max-w-96': compact }" data-slot="task-progress">
    <Progress
      :model-value="value"
      :max="max"
      :get-value-label="() => label"
      :get-value-text="() => valueText"
    />
    <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span aria-hidden="true">{{ fraction }}</span>
      <span v-if="complete" class="inline-flex items-center gap-1 text-status-success-ink">
        <CircleCheckIcon aria-hidden="true" class="size-3.5 shrink-0" />
        Complete
      </span>
    </div>
  </div>
</template>
