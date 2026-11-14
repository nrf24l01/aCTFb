import { describe, expect, it, vi } from 'vitest'
import {
  DraftCancelledError,
  hashAndUploadFile,
  pollDraftUntilSettled,
  validateFileAgainstPolicy,
} from '@/features/files/draft'
import type { PendingFile, SubmissionDraft, SubmissionFilesPolicy } from '@/shared/types/api'

const policy: SubmissionFilesPolicy = {
  required: true,
  minCount: 1,
  maxCount: 2,
  maxSizeBytes: 1024,
  allowedContentTypes: ['text/plain'],
}

function pending(status: PendingFile['status']): PendingFile {
  return {
    uuid: '11111111-1111-4111-8111-111111111111',
    filename: 'a.txt',
    contentType: 'text/plain',
    size: 5,
    sha256: 'a'.repeat(64),
    status,
  }
}

function draft(status: SubmissionDraft['status'], fileStatus: PendingFile['status']): SubmissionDraft {
  return {
    uuid: '22222222-2222-4222-8222-222222222222',
    resourceType: 'TASK',
    moduleUuid: '33333333-3333-4333-8333-333333333333',
    boardUuid: '44444444-4444-4444-8444-444444444444',
    status,
    createdAt: '2026-01-01T00:00:00Z',
    expiresAt: '2026-01-01T00:15:00Z',
    files: [pending(fileStatus)],
  }
}

describe('file policy', () => {
  it('rejects oversize, wrong type and count overflow', () => {
    expect(validateFileAgainstPolicy(new File(['hello'], 'a.txt', { type: 'text/plain' }), policy, 0)).toBeNull()
    expect(validateFileAgainstPolicy(new File(['hello'], 'a.txt', { type: 'text/plain' }), policy, 2)).toContain('At most')
    expect(validateFileAgainstPolicy(new File(['hello'], 'a.exe', { type: 'application/x-msdownload' }), policy, 0)).toContain('not allowed')
    expect(validateFileAgainstPolicy(new File([new Uint8Array(2048)], 'a.txt', { type: 'text/plain' }), policy, 0)).toContain('between')
  })
})

describe('direct upload pipeline', () => {
  it('hashes, presigns, PUTs signed headers without Authorization or manual Content-Length', async () => {
    const file = new File(['hello'], 'a.txt', { type: 'text/plain' })
    const presign = vi.fn().mockResolvedValue({
      fileUuid: pending('AWAITING_UPLOAD').uuid,
      method: 'PUT',
      uploadUrl: 'https://s3.example.test/quarantine/key?sig=1',
      requiredHeaders: { 'Content-Type': 'text/plain', 'x-amz-checksum-sha256': 'signed', 'Content-Length': '5' },
      expiresAt: '2026-01-01T00:05:00Z',
    })
    const complete = vi.fn().mockResolvedValue(pending('VALIDATING'))
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await hashAndUploadFile(file, presign, complete)

    expect(result.status).toBe('VALIDATING')
    expect(presign).toHaveBeenCalledWith({
      filename: 'a.txt',
      contentType: 'text/plain',
      size: 5,
      sha256: '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824',
    })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('https://s3.example.test/quarantine/key?sig=1')
    expect(init.method).toBe('PUT')
    expect(init.credentials).toBe('omit')
    expect(init.headers.Authorization).toBeUndefined()
    expect(init.headers.Cookie).toBeUndefined()
    expect(init.headers['Content-Length']).toBeUndefined()
    expect(init.headers['x-amz-checksum-sha256']).toBe('signed')
    expect(init.headers['Content-Type']).toBe('text/plain')
  })
})

describe('draft polling', () => {
  it('polls until READY and returns the terminal draft', async () => {
    const responses = [draft('OPEN', 'VALIDATING'), draft('OPEN', 'VALIDATING'), draft('OPEN', 'READY')]
    const getDraft = vi.fn(async () => responses.shift()!)
    const sleep = vi.fn(async () => undefined)

    const result = await pollDraftUntilSettled(getDraft, { sleep, attempts: 5 })

    expect(result.files[0].status).toBe('READY')
    expect(getDraft).toHaveBeenCalledTimes(3)
    expect(sleep).toHaveBeenCalledTimes(2)
  })

  it('cancels polling instead of waiting indefinitely', async () => {
    let cancelled = false
    const getDraft = vi.fn(async () => {
      cancelled = true
      return draft('OPEN', 'VALIDATING')
    })
    await expect(
      pollDraftUntilSettled(getDraft, { isCancelled: () => cancelled, sleep: async () => undefined }),
    ).rejects.toBeInstanceOf(DraftCancelledError)
  })

  it('returns the last observed state on bounded timeout (replay-safe read)', async () => {
    const getDraft = vi.fn(async () => draft('OPEN', 'VALIDATING'))
    const result = await pollDraftUntilSettled(getDraft, { attempts: 2, sleep: async () => undefined })
    expect(result.files[0].status).toBe('VALIDATING')
  })

  it('propagates a real draft read error instead of reporting fake readiness', async () => {
    const getDraft = vi.fn(async () => {
      throw new Error('network')
    })
    await expect(pollDraftUntilSettled(getDraft, { attempts: 1 })).rejects.toThrow('network')
  })
})