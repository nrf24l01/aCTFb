<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{
  class?: HTMLAttributes['class']
}>()
</script>

<template>
  <tr
    data-slot="table-row"
    :class="cn('hover:bg-muted data-[state=selected]:bg-muted transition-colors has-aria-expanded:bg-muted', props.class)"
  >
    <slot />
  </tr>
</template>
