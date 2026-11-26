<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{
  class?: HTMLAttributes['class']
  /** Keep a simple purpose-specific form on the previous bounded width. */
  bounded?: boolean
}>()
</script>

<template>
  <div
    data-slot="page-container"
    :class="cn(
      'page-workspace mx-auto w-full px-4 py-6 sm:px-6',
      props.bounded ? 'max-w-6xl' : 'max-w-[1600px]',
      props.class,
    )"
  >
    <slot />
  </div>
</template>
