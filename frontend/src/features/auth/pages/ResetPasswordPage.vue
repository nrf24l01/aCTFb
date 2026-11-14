<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { confirmPasswordReset } from '@/features/auth/api/auth'
import { hasProblems, validatePasswordReset } from '@/features/auth/validation'

const route = useRoute()
const router = useRouter()
const token = ref(typeof route.query.token === 'string' ? route.query.token : '')
const form = reactive({ password: '', confirmPassword: '' })
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)

async function submit() {
  problems.value = {
    ...validatePasswordReset(form.password, form.confirmPassword),
    token: token.value.trim() ? undefined : 'Paste the reset token from the email.',
  }
  if (hasProblems(problems.value)) return

  pending.value = true
  error.value = null
  try {
    await confirmPasswordReset({ token: token.value.trim(), newPassword: form.password })
    await router.replace({ name: 'sign-in', query: { notice: 'password-changed' } })
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
      <h1 class="text-2xl font-semibold">Choose a new password</h1>
      <p class="mt-1 text-sm text-muted-foreground">Reset links expire and can be used once.</p>
    </div>

    <ErrorAlert v-if="error" :error="error" title="Could not reset password" />

    <form class="space-y-4" novalidate @submit.prevent="submit">
      <FormField label="Reset token" for-id="reset-token" :error="problems.token">
        <Input id="reset-token" v-model="token" autocomplete="off" spellcheck="false" :aria-invalid="Boolean(problems.token)" required />
      </FormField>

      <FormField label="New password" for-id="reset-new-password" hint="8–128 characters." :error="problems.password">
        <Input
          id="reset-new-password"
          v-model="form.password"
          type="password"
          autocomplete="new-password"
          :aria-invalid="Boolean(problems.password)"
          required
        />
      </FormField>

      <FormField label="Confirm new password" for-id="reset-confirm-password" :error="problems.confirmPassword">
        <Input
          id="reset-confirm-password"
          v-model="form.confirmPassword"
          type="password"
          autocomplete="new-password"
          :aria-invalid="Boolean(problems.confirmPassword)"
          required
        />
      </FormField>

      <Button type="submit" class="w-full" :disabled="pending">
        {{ pending ? 'Saving…' : 'Set new password' }}
      </Button>
    </form>

    <p class="text-sm">
      <RouterLink to="/sign-in" class="underline decoration-border underline-offset-4">Back to sign in</RouterLink>
    </p>
  </div>
</template>