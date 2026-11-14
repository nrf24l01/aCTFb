<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { errorReference, errorMessage } from '@/shared/api/errors'

const props = defineProps<{
  error: unknown
  title?: string
}>()
</script>

<template>
  <Alert variant="destructive" class="rounded-none border-love/60 bg-black">
    <AlertTitle>{{ props.title || 'Request failed' }}</AlertTitle>
    <AlertDescription>
      <p>{{ errorMessage(props.error) }}</p>
      <p v-if="errorReference(props.error)" class="mt-1 text-xs text-muted-foreground">
        Reference: {{ errorReference(props.error) }}
      </p>
      <slot />
    </AlertDescription>
  </Alert>
</template>