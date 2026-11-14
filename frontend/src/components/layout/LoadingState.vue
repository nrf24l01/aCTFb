<script setup lang="ts">
import { Skeleton } from '@/components/ui/skeleton'

withDefaults(defineProps<{ rows?: number }>(), { rows: 4 })
</script>

<template>
  <div class="space-y-2" role="status" aria-label="Loading">
    <Skeleton v-for="row in rows" :key="row" class="h-9 w-full rounded-none bg-muted" />
  </div>
</template>