import { describe, expect, it } from 'vitest'
import {
  buildCreateFlagTaskRequest,
  buildCreateTaskRequest,
  buildSubmissionPolicy,
  buildUpdateFlagTaskRequest,
  buildUpdateTaskRequest,
  flagSubmissionPolicy,
} from '@/features/admin/taskPayload'

const form = {
  title: 'Warm up',
  category: 'web',
  description: 'Find the flag',
  points: 100,
  visible: true,
}

describe('FLAG task payloads', () => {
  it('sends only the TEXT/AUTOMATIC policy the first-slice backend accepts', () => {
    expect(flagSubmissionPolicy()).toEqual({ mode: 'TEXT', checker: 'AUTOMATIC' })
    expect(Object.keys(flagSubmissionPolicy()).sort()).toEqual(['checker', 'mode'])
  })

  it('creates with no custom text/files policy or instance details', () => {
    const body = buildCreateFlagTaskRequest(form, ['flag{one}'])

    expect(body.submission).toEqual({ mode: 'TEXT', checker: 'AUTOMATIC' })
    expect(body.submission).not.toHaveProperty('text')
    expect(body.submission).not.toHaveProperty('files')
    expect(body.instance).toEqual({ enabled: false })
    expect(body.instance).not.toHaveProperty('providerIntegrationUuid')
    expect(body.instance).not.toHaveProperty('nodes')
    expect(body.acceptedAnswers).toEqual(['flag{one}'])
    expect(JSON.stringify(body)).not.toContain('1024')
  })

  it('edits with the same policy and omits unchanged accepted answers', () => {
    const body = buildUpdateFlagTaskRequest(form)

    expect(body.submission).toEqual({ mode: 'TEXT', checker: 'AUTOMATIC' })
    expect(body.submission).not.toHaveProperty('text')
    expect(body.submission).not.toHaveProperty('files')
    expect(body).not.toHaveProperty('acceptedAnswers')
    expect(body.instance).toEqual({ enabled: false })
    expect(JSON.stringify(body)).not.toContain('1024')
  })

  it('edits with replacement accepted answers only when provided', () => {
    const body = buildUpdateFlagTaskRequest(form, ['flag{replacement}'])
    expect(body.acceptedAnswers).toEqual(['flag{replacement}'])
  })
})
describe('MANUAL task policies', () => {
  const manualBase = {
    type: 'MANUAL' as const,
    textRequired: true,
    textMinLength: 10,
    textMaxLength: 2000,
    filesRequired: true,
    filesMinCount: 1,
    filesMaxCount: 2,
    filesMaxSizeBytes: 4096,
    filesAllowedContentTypes: ['application/pdf', 'text/plain'],
  }

  it('persists TEXT_AND_FILES policies with MANUAL checker', () => {
    const policy = buildSubmissionPolicy({ ...manualBase, mode: 'TEXT_AND_FILES' })
    expect(policy.checker).toBe('MANUAL')
    expect(policy.text).toEqual({ required: true, minLength: 10, maxLength: 2000 })
    expect(policy.files).toEqual({
      required: true,
      minCount: 1,
      maxCount: 2,
      maxSizeBytes: 4096,
      allowedContentTypes: ['application/pdf', 'text/plain'],
    })
    expect(policy).not.toHaveProperty('checkerKey')
  })

  it('omits text for FILES and omits files for TEXT', () => {
    expect(buildSubmissionPolicy({ ...manualBase, mode: 'FILES' })).not.toHaveProperty('text')
    expect(buildSubmissionPolicy({ ...manualBase, mode: 'TEXT' })).not.toHaveProperty('files')
  })

  it('creates a MANUAL task without acceptedAnswers', () => {
    const body = buildCreateTaskRequest(
      {
        title: 'Manual report',
        category: 'misc',
        description: 'Review me',
        points: 100,
        visible: true,
        type: 'MANUAL',
        submission: buildSubmissionPolicy({ ...manualBase, mode: 'TEXT' }),
      },
    )
    expect(body.type).toBe('MANUAL')
    expect(body).not.toHaveProperty('acceptedAnswers')
    expect(body.submission.checker).toBe('MANUAL')
  })
})

describe('INSTANCE edit payloads', () => {
  it('keeps the immutable binding and never sends blank secrets', () => {
    const body = buildUpdateTaskRequest(
      { ...form, submission: { mode: 'TEXT', checker: 'MANUAL' } },
      [],
      { enabled: true },
    )
    expect(body.instance).toEqual({ enabled: true })
    expect(JSON.stringify(body)).not.toContain('secretParameters')
    expect(JSON.stringify(body)).not.toContain('parameters')
  })
})
