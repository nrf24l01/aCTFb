import { apiV2BaseUrl } from '@/shared/config/env'
import type { ApiErrorBody, TokenResponse } from '@/shared/types/api'
import { ApiError, NetworkError } from './errors'

/**
 * Access JWT lives only in this module variable. It is never written to
 * localStorage, sessionStorage, IndexedDB, URLs, or logs. A page reload
 * recovers the session by replaying the HttpOnly refresh cookie.
 */
let accessToken: string | null = null
let refreshInFlight: Promise<TokenResponse | null> | null = null
let authLostHandler: ((error?: unknown) => void) | null = null

export function getAccessToken(): string | null {
  return accessToken
}

export function setAccessToken(token: string | null): void {
  accessToken = token
}

export function clearAccessToken(): void {
  accessToken = null
}

export function onAuthLost(handler: (error?: unknown) => void): void {
  authLostHandler = handler
}

function notifyAuthLost(error?: unknown): void {
  clearAccessToken()
  authLostHandler?.(error)
}

async function readError(response: Response): Promise<ApiError> {
  let body: ApiErrorBody
  try {
    body = (await response.json()) as ApiErrorBody
  } catch {
    const unavailable = response.status === 502 || response.status === 503 || response.status === 504
    body = {
      code: unavailable ? 'DEPENDENCY_UNAVAILABLE' : 'INTERNAL_ERROR',
      message: unavailable
        ? 'The server is unavailable. Try again shortly.'
        : response.status >= 500
          ? 'The server failed to respond. Try again shortly.'
          : response.statusText || 'Request failed',
      traceId: '',
      timestamp: '',
      path: response.url,
    }
  }
  return new ApiError(response.status, body)
}

/**
 * Single-flight refresh. Concurrent 401s share one refresh request, and the
 * refresh cookie is the only credential involved.
 */
export function refreshAccessToken(): Promise<TokenResponse | null> {
  if (refreshInFlight) return refreshInFlight

  refreshInFlight = (async () => {
    let response: Response
    try {
      response = await fetch(`${apiV2BaseUrl}/auth/refresh`, {
        method: 'POST',
        credentials: 'include',
        headers: { Accept: 'application/json' },
      })
    } catch {
      throw new NetworkError()
    }

    if (response.status === 401 || response.status === 403) {
      // Surface the real refresh failure (missing/invalid/expired token,
      // disabled account, origin policy) instead of a generic request error.
      throw await readError(response)
    }
    if (!response.ok) {
      throw await readError(response)
    }
    const token = (await response.json()) as TokenResponse
    accessToken = token.accessToken
    return token
  })().finally(() => {
    refreshInFlight = null
  })

  return refreshInFlight
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  headers?: Record<string, string>
  auth?: boolean
  signal?: AbortSignal
  /** Internal: a 401 may be retried exactly once after a successful refresh. */
  retryOn401?: boolean
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const {
    method = 'GET',
    body,
    headers = {},
    auth = true,
    signal,
    retryOn401 = true,
  } = options

  const requestHeaders: Record<string, string> = {
    Accept: 'application/json',
    ...headers,
  }
  if (body !== undefined) requestHeaders['Content-Type'] = 'application/json'
  if (auth && accessToken) requestHeaders.Authorization = `Bearer ${accessToken}`

  let response: Response
  try {
    response = await fetch(`${apiV2BaseUrl}${path}`, {
      method,
      credentials: 'include',
      headers: requestHeaders,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new NetworkError()
  }

  if (response.status === 401 && auth) {
    if (retryOn401) {
      try {
        const refreshed = await refreshAccessToken()
        if (refreshed) {
          return apiRequest<T>(path, { ...options, retryOn401: false })
        }
      } catch (error) {
        notifyAuthLost(error)
        throw error
      }
    }
    // Either refresh was impossible or the retried request still got 401.
    const error = await readError(response)
    notifyAuthLost(error)
    throw error
  }

  if (!response.ok) {
    throw await readError(response)
  }

  if (response.status === 204) return undefined as T
  // A successful response is allowed to have no body (for example 201 Created
  // without a representation). Parsing that as JSON would surface a fake
  // "Unexpected end of JSON input" error for a request that actually succeeded.
  const raw = await response.text()
  if (raw.trim() === '') return undefined as T
  return JSON.parse(raw) as T
}

export function apiGet<T>(path: string, signal?: AbortSignal): Promise<T> {
  return apiRequest<T>(path, { signal })
}

export function apiPost<T>(path: string, body?: unknown, extra?: Partial<RequestOptions>): Promise<T> {
  return apiRequest<T>(path, { ...extra, method: 'POST', body })
}

export function apiPatch<T>(path: string, body: unknown): Promise<T> {
  return apiRequest<T>(path, { method: 'PATCH', body })
}

export function apiDelete(path: string): Promise<void> {
  return apiRequest<void>(path, { method: 'DELETE' })
}