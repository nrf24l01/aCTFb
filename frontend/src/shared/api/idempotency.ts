/**
 * Idempotency keys are generated with the platform CSPRNG and reused only
 * for retries of the same logical request (for example after a network
 * error). A new user action always gets a new key.
 */
export function createIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }

  const bytes = new Uint8Array(16)
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    crypto.getRandomValues(bytes)
  } else {
    throw new Error('A secure random source is required for idempotency keys')
  }

  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

/** The backend accepts 16..128 chars of [A-Za-z0-9._:-]. */
export function isIdempotencyKey(value: string): boolean {
  return /^[A-Za-z0-9._:-]{16,128}$/.test(value)
}