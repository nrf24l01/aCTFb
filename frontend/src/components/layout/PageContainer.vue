<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <div :class="cn('mx-auto w-full max-w-6xl px-4 py-6 sm:px-6', props.class)">
    <slot />
  </div>
</template>