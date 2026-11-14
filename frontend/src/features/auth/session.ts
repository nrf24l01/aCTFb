import { reactive, readonly } from 'vue'
import { clearAccessToken, onAuthLost, refreshAccessToken } from '@/shared/api/client'
import { errorMessage, isApiError } from '@/shared/api/errors'
import type { Me } from '@/shared/types/api'
import * as authApi from './api/auth'

export type SessionStatus = 'unknown' | 'loading' | 'authenticated' | 'anonymous' | 'unavailable'

const state = reactive<{
  status: SessionStatus
  user: Me | null
  error: string | null
  sessionLost: boolean
}>({
  status: 'unknown',
  user: null,
  error: null,
  sessionLost: false,
})

let initPromise: Promise<void> | null = null

onAuthLost((error) => {
  state.user = null
  state.status = 'anonymous'
  state.sessionLost = true
  state.error = error ? errorMessage(error) : null
})

async function loadMe(): Promise<void> {
  state.user = await authApi.getMe()
  state.status = 'authenticated'
  state.error = null
}

export function initSession(force = false): Promise<void> {
  if (initPromise && !force) return initPromise

  initPromise = (async () => {
    state.status = state.user ? 'authenticated' : 'loading'
    try {
      const token = await refreshAccessToken()
      if (!token) {
        state.user = null
        state.status = 'anonymous'
        return
      }
      await loadMe()
    } catch (error) {
      if (isApiError(error) && (error.status === 401 || error.status === 403)) {
        state.user = null
        state.status = 'anonymous'
      } else {
        state.user = null
        state.status = 'unavailable'
        state.error = errorMessage(error)
      }
    }
  })().finally(() => {
    initPromise = null
  })

  return initPromise
}

export async function signIn(username: string, password: string): Promise<void> {
  const response = await authApi.login({ username, password })
  state.user = response.user
  state.status = 'authenticated'
  state.error = null
  state.sessionLost = false
}

export async function signUp(email: string, username: string, password: string): Promise<void> {
  const response = await authApi.register({ email, username, password })
  state.user = response.user
  state.status = 'authenticated'
  state.error = null
  state.sessionLost = false
}

export async function signOut(): Promise<string | null> {
  let warning: string | null = null
  try {
    await authApi.logout()
  } catch {
    warning = 'Signed out on this device, but the server session could not be revoked. Retry sign-out when online.'
  } finally {
    clearAccessToken()
    state.user = null
    state.status = 'anonymous'
    state.sessionLost = false
    state.error = null
  }
  return warning
}

export async function refreshUser(): Promise<void> {
  if (state.status !== 'authenticated') return
  state.user = await authApi.getMe()
}

export function forgetSession(): void {
  clearAccessToken()
  state.user = null
  state.status = 'anonymous'
  state.error = null
  state.sessionLost = false
}

export function useSession() {
  return {
    state: readonly(state),
    initSession,
    signIn,
    signUp,
    signOut,
    refreshUser,
    forgetSession,
  }
}