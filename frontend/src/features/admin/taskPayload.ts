import type {
  AdminTaskCreateRequest,
  AdminTaskPatchRequest,
  SubmissionFilesPolicy,
  SubmissionMode,
  SubmissionPolicy,
  SubmissionTextPolicy,
  TaskInstanceConfig,
  TaskType,
} from '@/shared/types/api'

/**
 * FLAG/TEXT uses the backend default bounds (1..1_000_000). Custom text
 * bounds are accepted only for MANUAL/INSTANCE policies persisted by P1R.
 */
export function flagSubmissionPolicy(): SubmissionPolicy {
  return { mode: 'TEXT', checker: 'AUTOMATIC' }
}

export interface TaskPolicyFormValues {
  type: TaskType
  mode: SubmissionMode
  textRequired: boolean
  textMinLength: number
  textMaxLength: number
  filesRequired: boolean
  filesMinCount: number
  filesMaxCount: number
  filesMaxSizeBytes: number
  filesAllowedContentTypes: string[]
}

export function textPolicy(form: TaskPolicyFormValues): SubmissionTextPolicy {
  return {
    required: form.textRequired,
    minLength: form.textMinLength,
    maxLength: form.textMaxLength,
  }
}

export function filesPolicy(form: TaskPolicyFormValues): SubmissionFilesPolicy {
  return {
    required: form.filesRequired,
    minCount: form.filesMinCount,
    maxCount: form.filesMaxCount,
    maxSizeBytes: form.filesMaxSizeBytes,
    allowedContentTypes: form.filesAllowedContentTypes,
  }
}

export function buildSubmissionPolicy(form: TaskPolicyFormValues): SubmissionPolicy {
  if (form.type === 'FLAG') return flagSubmissionPolicy()
  const needsText = form.mode === 'TEXT' || form.mode === 'TEXT_AND_FILES' || form.mode === 'TEXT_OR_FILES'
  const needsFiles = form.mode === 'FILES' || form.mode === 'TEXT_AND_FILES' || form.mode === 'TEXT_OR_FILES'
  return {
    mode: form.mode,
    checker: 'MANUAL',
    ...(needsText ? { text: textPolicy(form) } : {}),
    ...(needsFiles ? { files: filesPolicy(form) } : {}),
  }
}

export interface FlagTaskFormValues {
  title: string
  category: string
  description: string
  points: number
  visible: boolean
}

export interface TaskFormValues extends FlagTaskFormValues {
  type: TaskType
  policy: TaskPolicyFormValues
}

export function buildCreateTaskRequest(
  form: FlagTaskFormValues & { type: TaskType; submission: SubmissionPolicy },
  acceptedAnswers?: string[],
  instance: TaskInstanceConfig = { enabled: false },
): AdminTaskCreateRequest {
  return {
    title: form.title,
    type: form.type,
    category: form.category,
    description: form.description,
    points: form.points,
    visible: form.visible,
    submission: form.submission,
    ...(form.type === 'FLAG' && acceptedAnswers ? { acceptedAnswers } : {}),
    instance,
  }
}

export function buildUpdateTaskRequest(
  form: FlagTaskFormValues & { submission: SubmissionPolicy },
  acceptedAnswers?: string[],
  instance: TaskInstanceConfig = { enabled: false },
): AdminTaskPatchRequest {
  return {
    title: form.title,
    category: form.category,
    description: form.description,
    points: form.points,
    visible: form.visible,
    submission: form.submission,
    instance,
    ...(acceptedAnswers && acceptedAnswers.length > 0 ? { acceptedAnswers } : {}),
  }
}

export function buildCreateFlagTaskRequest(
  form: FlagTaskFormValues,
  acceptedAnswers: string[],
): AdminTaskCreateRequest {
  return buildCreateTaskRequest(
    { ...form, type: 'FLAG', submission: flagSubmissionPolicy() },
    acceptedAnswers,
  )
}

export function buildUpdateFlagTaskRequest(
  form: FlagTaskFormValues,
  acceptedAnswers?: string[],
): AdminTaskPatchRequest {
  return buildUpdateTaskRequest(
    { ...form, submission: flagSubmissionPolicy() },
    acceptedAnswers,
  )
}