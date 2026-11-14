<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { isApiError } from '@/shared/api/errors'
import { useSession } from '@/features/auth/session'
import { hasProblems, validateSignIn } from '@/features/auth/validation'

const route = useRoute()
const router = useRouter()
const { state, signIn } = useSession()

const form = reactive({ username: '', password: '' })
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)

const notice = computed(() => {
  if (route.query.notice === 'password-changed') return 'Password changed. Sign in with your new password.'
  if (route.query.notice === 'registered') return 'Account created. Check your email if verification is required.'
  return null
})

async function submit() {
  error.value = null
  problems.value = validateSignIn(form.username.trim(), form.password)
  if (hasProblems(problems.value)) return

  pending.value = true
  try {
    await signIn(form.username.trim(), form.password)
    const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/')
      ? route.query.redirect
      : '/boards'
    await router.replace(redirect)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
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
      <h1 class="text-2xl font-semibold">Sign in</h1>
      <p class="mt-1 text-sm text-muted-foreground">Use your aCTFb account.</p>
    </div>

    <Alert v-if="state.sessionLost" class="rounded-none border-gold/60 bg-black">
      <AlertTitle>Session ended</AlertTitle>
      <AlertDescription>
        Sign in again to continue where you left off.
        <span v-if="state.error" class="block">{{ state.error }}</span>
      </AlertDescription>
    </Alert>

    <Alert v-if="notice" class="rounded-none border-pine/60 bg-black">
      <AlertTitle>Done</AlertTitle>
      <AlertDescription>{{ notice }}</AlertDescription>
    </Alert>

    <ErrorAlert v-if="error" :error="error" title="Could not sign in">
      <p v-if="isApiError(error) && error.code === 'RATE_LIMITED'" class="mt-1 text-xs text-muted-foreground">
        Too many attempts. Wait a minute, then retry.
      </p>
    </ErrorAlert>

    <form class="space-y-4" novalidate @submit.prevent="submit">
      <FormField label="Username" for-id="sign-in-username" :error="problems.username">
        <Input
          id="sign-in-username"
          v-model="form.username"
          name="username"
          autocomplete="username"
          autocapitalize="none"
          spellcheck="false"
          :aria-invalid="Boolean(problems.username)"
          required
        />
      </FormField>

      <FormField label="Password" for-id="sign-in-password" :error="problems.password">
        <Input
          id="sign-in-password"
          v-model="form.password"
          name="password"
          type="password"
          autocomplete="current-password"
          :aria-invalid="Boolean(problems.password)"
          required
        />
      </FormField>

      <Button type="submit" class="w-full" :disabled="pending">
        {{ pending ? 'Signing in…' : 'Sign in' }}
      </Button>
    </form>

    <div class="flex flex-wrap justify-between gap-3 text-sm">
      <RouterLink to="/register" class="underline decoration-border underline-offset-4">Create an account</RouterLink>
      <RouterLink to="/reset-password" class="text-muted-foreground underline decoration-border underline-offset-4">
        Forgot password?
      </RouterLink>
    </div>

    <p v-if="state.status === 'unavailable'" class="text-xs text-muted-foreground">
      {{ state.error || 'Cannot reach the server.' }}
      <button type="button" class="underline decoration-border underline-offset-4" @click="$router.go(0)">Retry</button>
    </p>
  </div>
</template>