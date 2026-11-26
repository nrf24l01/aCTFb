<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { isApiError } from '@/shared/api/errors'
import { changePassword, requestEmailVerification, updateMe } from '@/features/auth/api/auth'
import { useSession } from '@/features/auth/session'
import { validatePassword, validateUsername } from '@/features/auth/validation'

const router = useRouter()
const { state, refreshUser, forgetSession } = useSession()

const username = ref(state.user?.username ?? '')
const usernameError = ref<string | undefined>()
const usernamePending = ref(false)
const usernameSaved = ref(false)
const usernameServerError = ref<unknown>(null)

const password = reactive({ current: '', next: '', confirm: '' })
const passwordProblems = ref<Record<string, string | undefined>>({})
const passwordPending = ref(false)
const passwordError = ref<unknown>(null)

const verifyPending = ref(false)
const verifyMessage = ref<string | null>(null)
const verifyError = ref<unknown>(null)

const user = computed(() => state.user)

async function saveUsername() {
  usernameServerError.value = null
  usernameSaved.value = false
  usernameError.value = validateUsername(username.value.trim())
  if (usernameError.value || !user.value) return

  usernamePending.value = true
  try {
    await updateMe({ username: username.value.trim() })
    await refreshUser()
    usernameSaved.value = true
  } catch (caught) {
    if (isApiError(caught)) usernameError.value = caught.fieldIssue('username')
    usernameServerError.value = caught
  } finally {
    usernamePending.value = false
  }
}

async function savePassword() {
  passwordError.value = null
  passwordProblems.value = {
    current: password.current ? undefined : 'Enter your current password.',
    next: validatePassword(password.next),
    confirm: password.next === password.confirm ? undefined : 'The passwords do not match.',
  }
  if (Object.values(passwordProblems.value).some(Boolean)) return

  passwordPending.value = true
  try {
    await changePassword({ currentPassword: password.current, newPassword: password.next })
    forgetSession()
    await router.replace({ name: 'sign-in', query: { notice: 'password-changed' } })
  } catch (caught) {
    passwordError.value = caught
  } finally {
    passwordPending.value = false
  }
}

async function resendVerification() {
  verifyError.value = null
  verifyMessage.value = null
  verifyPending.value = true
  try {
    await requestEmailVerification()
    verifyMessage.value = 'Verification email requested. Check your inbox.'
  } catch (caught) {
    verifyError.value = caught
  } finally {
    verifyPending.value = false
  }
}
</script>

<template>
  <PageContainer bounded>
    <PageHeader title="Profile" description="Account details, email verification and password." />

    <EmptyState v-if="!user" title="Not signed in" description="Sign in to view your profile." />

    <div v-else class="space-y-8">
      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Account</h2>
        </div>
        <dl class="grid gap-3 px-4 py-4 text-sm sm:grid-cols-2">
          <div>
            <dt class="text-xs text-muted-foreground">Email</dt>
            <dd>{{ user.email }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">Email status</dt>
            <dd>{{ user.emailVerified ? 'Verified' : 'Not verified' }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">Global roles</dt>
            <dd>{{ user.roles.join(', ') }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">Created</dt>
            <dd>{{ user.createdAt }}</dd>
          </div>
        </dl>
        <div v-if="!user.emailVerified" class="space-y-3 border-t border-border px-4 py-4">
          <p class="text-sm text-muted-foreground">Your email is not verified. Request another verification email.</p>
          <ErrorAlert v-if="verifyError" :error="verifyError" title="Could not request verification" />
          <Alert v-if="verifyMessage" class="rounded-none border-pine/60 bg-black">
            <AlertDescription>{{ verifyMessage }}</AlertDescription>
          </Alert>
          <Button variant="outline" :disabled="verifyPending" @click="resendVerification">
            {{ verifyPending ? 'Requesting…' : 'Resend verification email' }}
          </Button>
        </div>
      </section>

      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Username</h2>
        </div>
        <form class="space-y-4 px-4 py-4" novalidate @submit.prevent="saveUsername">
          <ErrorAlert v-if="usernameServerError" :error="usernameServerError" title="Could not update username" />
          <Alert v-if="usernameSaved" class="rounded-none border-pine/60 bg-black">
            <AlertDescription>Username updated.</AlertDescription>
          </Alert>
          <FormField label="Username" for-id="profile-username" :error="usernameError">
            <Input id="profile-username" v-model="username" autocomplete="username" autocapitalize="none" spellcheck="false" />
          </FormField>
          <Button type="submit" :disabled="usernamePending">{{ usernamePending ? 'Saving…' : 'Save username' }}</Button>
        </form>
      </section>

      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Change password</h2>
        </div>
        <form class="space-y-4 px-4 py-4" novalidate @submit.prevent="savePassword">
          <p class="text-sm text-muted-foreground">
            Changing your password signs out every session. You will sign in again.
          </p>
          <ErrorAlert v-if="passwordError" :error="passwordError" title="Could not change password" />
          <FormField label="Current password" for-id="profile-current-password" :error="passwordProblems.current">
            <Input
              id="profile-current-password"
              v-model="password.current"
              type="password"
              autocomplete="current-password"
            />
          </FormField>
          <FormField label="New password" for-id="profile-new-password" hint="8–128 characters." :error="passwordProblems.next">
            <Input
              id="profile-new-password"
              v-model="password.next"
              type="password"
              autocomplete="new-password"
            />
          </FormField>
          <FormField label="Confirm new password" for-id="profile-confirm-password" :error="passwordProblems.confirm">
            <Input
              id="profile-confirm-password"
              v-model="password.confirm"
              type="password"
              autocomplete="new-password"
            />
          </FormField>
          <Button type="submit" variant="outline" :disabled="passwordPending">
            {{ passwordPending ? 'Changing…' : 'Change password' }}
          </Button>
        </form>
      </section>
    </div>
  </PageContainer>
</template>