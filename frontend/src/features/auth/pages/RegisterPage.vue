<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { isApiError } from '@/shared/api/errors'
import { useSession } from '@/features/auth/session'
import { hasProblems, validateRegistration } from '@/features/auth/validation'

const router = useRouter()
const { signUp } = useSession()

const form = reactive({ email: '', username: '', password: '', confirmPassword: '' })
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)

async function submit() {
  error.value = null
  problems.value = validateRegistration(
    form.email.trim(),
    form.username.trim(),
    form.password,
    form.confirmPassword,
  )
  if (hasProblems(problems.value)) return

  pending.value = true
  try {
    await signUp(form.email.trim(), form.username.trim(), form.password)
    await router.replace({ path: '/boards', query: { notice: 'registered' } })
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        email: caught.fieldIssue('email'),
        username: caught.fieldIssue('username'),
        password: caught.fieldIssue('password'),
      }
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-semibold">Create account</h1>
      <p class="mt-1 text-sm text-muted-foreground">Register with an email address and username.</p>
    </div>

    <ErrorAlert v-if="error" :error="error" title="Could not register" />

    <Alert class="rounded-none border-border bg-black">
      <AlertTitle>Email verification</AlertTitle>
      <AlertDescription>
        If the deployment requires verification, a confirmation link is sent to your inbox. You can request it again
        from your profile.
      </AlertDescription>
    </Alert>

    <form class="space-y-4" novalidate @submit.prevent="submit">
      <FormField label="Email" for-id="register-email" :error="problems.email">
        <Input
          id="register-email"
          v-model="form.email"
          name="email"
          type="email"
          autocomplete="email"
          autocapitalize="none"
          spellcheck="false"
          :aria-invalid="Boolean(problems.email)"
          required
        />
      </FormField>

      <FormField label="Username" for-id="register-username" hint="3–32 letters, digits, dot, underscore or hyphen." :error="problems.username">
        <Input
          id="register-username"
          v-model="form.username"
          name="username"
          autocomplete="username"
          autocapitalize="none"
          spellcheck="false"
          :aria-invalid="Boolean(problems.username)"
          required
        />
      </FormField>

      <FormField label="Password" for-id="register-password" hint="8–128 characters." :error="problems.password">
        <Input
          id="register-password"
          v-model="form.password"
          name="password"
          type="password"
          autocomplete="new-password"
          :aria-invalid="Boolean(problems.password)"
          required
        />
      </FormField>

      <FormField label="Confirm password" for-id="register-confirm" :error="problems.confirmPassword">
        <Input
          id="register-confirm"
          v-model="form.confirmPassword"
          name="confirmPassword"
          type="password"
          autocomplete="new-password"
          :aria-invalid="Boolean(problems.confirmPassword)"
          required
        />
      </FormField>

      <Button type="submit" class="w-full" :disabled="pending">
        {{ pending ? 'Creating account…' : 'Create account' }}
      </Button>
    </form>

    <p class="text-sm">
      Already registered?
      <RouterLink to="/sign-in" class="underline decoration-border underline-offset-4">Sign in</RouterLink>
    </p>
  </div>
</template>