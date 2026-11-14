import type { PendingFile, SubmissionDraft, SubmissionFilesPolicy } from '@/shared/types/api'
import { sha256File } from '@/shared/lib/sha256'
import { uploadToPresignedUrl } from './upload'

export class FilePolicyError extends Error {}
export class DraftCancelledError extends Error {}
export class DraftPollTimeoutError extends Error {}

export function validateFileAgainstPolicy(
  file: File,
  policy: SubmissionFilesPolicy,
  currentCount: number,
): string | null {
  if (currentCount >= policy.maxCount) return `At most ${policy.maxCount} file(s) are allowed.`
  if (file.size < 1 || file.size > policy.maxSizeBytes) {
    return `File must be between 1 and ${policy.maxSizeBytes} bytes.`
  }
  const contentType = file.type || 'application/octet-stream'
  if (!policy.allowedContentTypes.includes(contentType)) {
    return `Content type ${contentType} is not allowed.`
  }
  return null
}

export interface UploadPresignRequest {
  filename: string
  contentType: string
  size: number
  sha256: string
}

export interface UploadPresignResult {
  fileUuid: string
  method: 'PUT'
  uploadUrl: string
  requiredHeaders: Record<string, string>
  expiresAt: string
}

export async function hashAndUploadFile(
  file: File,
  presign: (request: UploadPresignRequest) => Promise<UploadPresignResult>,
  complete: (fileUuid: string) => Promise<PendingFile>,
  signal?: AbortSignal,
): Promise<PendingFile> {
  const sha256 = await sha256File(file)
  if (signal?.aborted) throw new DraftCancelledError()
  const signed = await presign({
    filename: file.name,
    contentType: file.type || 'application/octet-stream',
    size: file.size,
    sha256,
  })
  if (signal?.aborted) throw new DraftCancelledError()
  await uploadToPresignedUrl(
    {
      fileUuid: signed.fileUuid,
      method: signed.method,
      uploadUrl: signed.uploadUrl,
      requiredHeaders: signed.requiredHeaders,
      expiresAt: signed.expiresAt,
    },
    file,
    signal,
  )
  if (signal?.aborted) throw new DraftCancelledError()
  return complete(signed.fileUuid)
}

const TERMINAL = new Set(['READY', 'REJECTED', 'EXPIRED', 'ATTACHED'])

export interface PollDraftOptions {
  attempts?: number
  delayMs?: number
  sleep?: (ms: number) => Promise<void>
  isCancelled?: () => boolean
}

export async function pollDraftUntilSettled(
  getDraft: () => Promise<SubmissionDraft>,
  options: PollDraftOptions = {},
): Promise<SubmissionDraft> {
  const attempts = options.attempts ?? 20
  const delayMs = options.delayMs ?? 1500
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)))
  let latest: SubmissionDraft | null = null
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (options.isCancelled?.()) throw new DraftCancelledError()
    latest = await getDraft()
    if (latest.status !== 'OPEN') return latest
    if (latest.files.length > 0 && latest.files.every((file) => TERMINAL.has(file.status))) return latest
    if (attempt < attempts - 1) await sleep(delayMs)
  }
  if (options.isCancelled?.()) throw new DraftCancelledError()
  if (latest) return latest
  throw new DraftPollTimeoutError()
}