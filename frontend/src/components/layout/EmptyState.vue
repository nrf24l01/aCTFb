<script setup lang="ts">
defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <div class="border border-dashed border-border px-4 py-8">
    <p class="font-semibold">{{ title }}</p>
    <p v-if="description" class="mt-1 max-w-2xl text-sm text-muted-foreground">{{ description }}</p>
    <div v-if="$slots.default" class="mt-4">
      <slot />
    </div>
  </div>
</template>