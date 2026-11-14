<script setup lang="ts">
import { ref } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { requestPasswordReset } from '@/features/auth/api/auth'
import { validateEmail } from '@/features/auth/validation'

const email = ref('')
const problem = ref<string | undefined>()
const pending = ref(false)
const error = ref<unknown>(null)
const sent = ref(false)

async function submit() {
  problem.value = validateEmail(email.value.trim())
  if (problem.value) return

  pending.value = true
  error.value = null
  try {
    await requestPasswordReset({ email: email.value.trim() })
    sent.value = true
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-semibold">Reset password</h1>
      <p class="mt-1 text-sm text-muted-foreground">Request a single-use reset link by email.</p>
    </div>

    <Alert v-if="sent" class="rounded-none border-pine/60 bg-black">
      <AlertTitle>Request accepted</AlertTitle>
      <AlertDescription>
        If an account exists for that address, a reset email is on its way. The response is the same for every address.
      </AlertDescription>
    </Alert>

    <ErrorAlert v-if="error" :error="error" title="Could not request a reset" />

    <form v-if="!sent" class="space-y-4" novalidate @submit.prevent="submit">
      <FormField label="Email" for-id="reset-email" :error="problem">
        <Input
          id="reset-email"
          v-model="email"
          type="email"
          autocomplete="email"
          autocapitalize="none"
          spellcheck="false"
          :aria-invalid="Boolean(problem)"
          required
        />
      </FormField>
      <Button type="submit" class="w-full" :disabled="pending">
        {{ pending ? 'Sending…' : 'Send reset link' }}
      </Button>
    </form>

    <p class="text-sm">
      <RouterLink to="/sign-in" class="underline decoration-border underline-offset-4">Back to sign in</RouterLink>
    </p>
  </div>
</template>