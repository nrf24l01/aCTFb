<script setup lang="ts">
defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <div class="mb-6 flex flex-col gap-4 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
    <div class="min-w-0">
      <h1 class="text-[22px] font-semibold leading-7 tracking-tight">{{ title }}</h1>
      <p v-if="description && !$slots.description" class="mt-1 max-w-3xl text-[13px] leading-5 text-muted-foreground">
        {{ description }}
      </p>
      <div
        v-else-if="$slots.description"
        class="mt-1 min-w-0 max-w-3xl text-[13px] leading-5 text-muted-foreground"
      >
        <slot name="description" />
      </div>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
