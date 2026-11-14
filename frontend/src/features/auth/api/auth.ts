import { apiPost, apiRequest, setAccessToken } from '@/shared/api/client'
import type {
  ChangePasswordRequest,
  EmailVerificationConfirmRequest,
  LoginRequest,
  LoginResponse,
  Me,
  MePatchRequest,
  PasswordResetConfirmRequest,
  PasswordResetRequest,
  RegisterRequest,
  RegisterResponse,
  StatusResponse,
} from '@/shared/types/api'

export async function login(body: LoginRequest): Promise<LoginResponse> {
  const response = await apiRequest<LoginResponse>('/auth/login', {
    method: 'POST',
    body,
    auth: false,
  })
  setAccessToken(response.accessToken)
  return response
}

export async function register(body: RegisterRequest): Promise<RegisterResponse> {
  const response = await apiRequest<RegisterResponse>('/auth/register', {
    method: 'POST',
    body,
    auth: false,
  })
  setAccessToken(response.accessToken)
  return response
}

export async function logout(): Promise<void> {
  await apiRequest<void>('/auth/logout', { method: 'POST', auth: false })
  setAccessToken(null)
}

export function getMe(): Promise<Me> {
  return apiRequest<Me>('/me')
}

export function updateMe(body: MePatchRequest): Promise<Me> {
  return apiRequest<Me>('/me', { method: 'PATCH', body })
}

export function changePassword(body: ChangePasswordRequest): Promise<void> {
  return apiRequest<void>('/me/password', { method: 'POST', body })
}

export function requestEmailVerification(): Promise<StatusResponse> {
  return apiPost<StatusResponse>('/auth/email-verification/request')
}

export function confirmEmailVerification(body: EmailVerificationConfirmRequest): Promise<StatusResponse> {
  return apiRequest<StatusResponse>('/auth/email-verification/confirm', {
    method: 'POST',
    body,
    auth: false,
  })
}

export function requestPasswordReset(body: PasswordResetRequest): Promise<StatusResponse> {
  return apiRequest<StatusResponse>('/auth/password-reset/request', {
    method: 'POST',
    body,
    auth: false,
  })
}

export function confirmPasswordReset(body: PasswordResetConfirmRequest): Promise<StatusResponse> {
  return apiRequest<StatusResponse>('/auth/password-reset/confirm', {
    method: 'POST',
    body,
    auth: false,
  })
}