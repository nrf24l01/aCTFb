<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'

defineProps<{
  status: string
}>()
</script>

<template>
  <StatusBadge :status="status" />
</template>
