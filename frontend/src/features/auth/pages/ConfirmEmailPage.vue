<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import { confirmEmailVerification } from '@/features/auth/api/auth'
import { useSession } from '@/features/auth/session'

const route = useRoute()
const { refreshUser } = useSession()
const token = ref(typeof route.query.token === 'string' ? route.query.token : '')
const pending = ref(false)
const error = ref<unknown>(null)
const done = ref(false)

async function submit() {
  if (!token.value.trim()) return
  pending.value = true
  error.value = null
  try {
    await confirmEmailVerification({ token: token.value.trim() })
    done.value = true
    token.value = ''
    await refreshUser().catch(() => undefined)
  } catch (caught) {
    error.value = caught
  } finally {
    pending.value = false
  }
}

onMounted(() => {
  if (token.value) void submit()
})
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-semibold">Confirm email</h1>
      <p class="mt-1 text-sm text-muted-foreground">Open the link from your verification email, or paste the token.</p>
    </div>

    <Alert v-if="done" class="rounded-none border-pine/60 bg-black">
      <AlertTitle>Email verified</AlertTitle>
      <AlertDescription>Your address is confirmed.</AlertDescription>
    </Alert>

    <ErrorAlert v-if="error" :error="error" title="Could not verify email" />

    <form v-if="!done" class="space-y-4" novalidate @submit.prevent="submit">
      <FormField label="Verification token" for-id="verification-token">
        <Input id="verification-token" v-model="token" autocomplete="off" spellcheck="false" required />
      </FormField>
      <Button type="submit" class="w-full" :disabled="pending || !token.trim()">
        {{ pending ? 'Confirming…' : 'Confirm email' }}
      </Button>
    </form>

    <p class="text-sm">
      <RouterLink to="/boards" class="underline decoration-border underline-offset-4">Continue to boards</RouterLink>
    </p>
  </div>
</template>