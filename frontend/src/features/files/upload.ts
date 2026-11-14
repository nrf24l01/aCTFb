import type { ScopedFilePresignResponse } from '@/shared/types/api'

export class UploadError extends Error {
  readonly status: number
  constructor(status: number) {
    super(`Direct upload failed (${status})`)
    this.name = 'UploadError'
    this.status = status
  }
}

/**
 * Direct quarantine upload. This deliberately does not go through the API
 * client: no platform Authorization/cookie header is sent to object storage.
 * The signed URL carries its own authorization. `Content-Length` is never set
 * manually; the browser computes it and S3/edge policy validates the signed
 * headers returned by presign.
 */
export async function uploadToPresignedUrl(
  presign: ScopedFilePresignResponse,
  file: File,
  signal?: AbortSignal,
): Promise<void> {
  const target = new URL(presign.uploadUrl)
  if (target.protocol !== 'http:' && target.protocol !== 'https:') {
    throw new UploadError(0)
  }

  const headers: Record<string, string> = {}
  for (const [name, value] of Object.entries(presign.requiredHeaders)) {
    if (name.toLowerCase() === 'content-length') continue
    if (name.toLowerCase() === 'authorization' || name.toLowerCase() === 'cookie') continue
    headers[name] = value
  }

  const response = await fetch(target.toString(), {
    method: presign.method,
    headers,
    body: file,
    credentials: 'omit',
    cache: 'no-store',
    redirect: 'error',
    signal,
  })
  if (!response.ok) throw new UploadError(response.status)
}