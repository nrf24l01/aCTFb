import type { ApiErrorBody, FieldError } from '@/shared/types/api'

/** The request never reached the backend, or the connection dropped. */
export class NetworkError extends Error {
  constructor(message = 'Network request failed') {
    super(message)
    this.name = 'NetworkError'
  }
}

/** The backend answered with a non-2xx ApiError/ValidationError envelope. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly traceId: string
  readonly timestamp: string
  readonly path: string
  readonly fieldErrors: FieldError[]
  readonly details?: Record<string, unknown>

  constructor(status: number, body: ApiErrorBody) {
    super(body.message || 'Request failed')
    this.name = 'ApiError'
    this.status = status
    this.code = body.code || 'INTERNAL_ERROR'
    this.traceId = body.traceId || ''
    this.timestamp = body.timestamp || ''
    this.path = body.path || ''
    this.fieldErrors = Array.isArray(body.fieldErrors) ? body.fieldErrors : []
    this.details = body.details
  }

  fieldIssue(field: string): string | undefined {
    return this.fieldErrors.find((item) => item.field === field || item.field.endsWith(`.${field}`))?.issue
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError
}

export function isNetworkError(error: unknown): error is NetworkError {
  return error instanceof NetworkError
}

export function errorMessage(error: unknown): string {
  if (isApiError(error)) return error.message
  if (isNetworkError(error)) return 'Cannot reach the server. Check your connection and retry.'
  if (error instanceof Error) return error.message
  return 'Unexpected error'
}

/** Human support reference; never includes request bodies or tokens. */
export function errorReference(error: unknown): string | null {
  if (isApiError(error) && error.traceId) return error.traceId
  return null
}