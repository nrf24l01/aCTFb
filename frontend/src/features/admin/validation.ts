export function validateSlug(value: string): string | undefined {
  if (value.length < 3 || value.length > 64) return 'Slug must be 3–64 characters.'
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value)) {
    return 'Use lowercase letters, digits and single hyphens.'
  }
  return undefined
}

export function validateBoardName(value: string): string | undefined {
  if (value.length < 1 || value.length > 128) return 'Name must be 1–128 characters.'
  return undefined
}

export function validateModuleName(value: string): string | undefined {
  return validateBoardName(value)
}

export function validateTaskTitle(value: string): string | undefined {
  if (value.length < 1 || value.length > 128) return 'Title must be 1–128 characters.'
  return undefined
}

export function validateCategory(value: string): string | undefined {
  if (value.length < 1 || value.length > 64) return 'Category must be 1–64 characters.'
  return undefined
}

export function validatePoints(value: number): string | undefined {
  if (!Number.isInteger(value) || value < 0 || value > 1_000_000_000) {
    return 'Points must be a whole number from 0 to 1,000,000,000.'
  }
  return undefined
}

/** AdminTaskCreateRequest requires acceptedAnswers for FLAG, 1..100 unique. */
export function parseAcceptedAnswers(value: string): { answers: string[]; problem?: string } {
  const answers = value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (answers.length === 0) return { answers, problem: 'Add at least one accepted answer for a FLAG task.' }
  if (answers.length > 100) return { answers, problem: 'At most 100 accepted answers.' }
  if (answers.some((answer) => answer.length > 1024)) {
    return { answers, problem: 'Each accepted answer must be at most 1024 characters.' }
  }
  if (new Set(answers).size !== answers.length) {
    return { answers, problem: 'Accepted answers must be unique.' }
  }
  return { answers }
}

export function parseDateWindow(
  startsAt: string,
  endsAt: string,
): { startsAt: string | null; endsAt: string | null; problem?: string } {
  const start = startsAt ? new Date(startsAt) : null
  const end = endsAt ? new Date(endsAt) : null
  if (start && Number.isNaN(start.getTime())) return { startsAt: null, endsAt: null, problem: 'Start is invalid.' }
  if (end && Number.isNaN(end.getTime())) return { startsAt: null, endsAt: null, problem: 'End is invalid.' }
  if (start && end && start.getTime() >= end.getTime()) {
    return { startsAt: null, endsAt: null, problem: 'End must be after start.' }
  }
  return {
    startsAt: start ? start.toISOString() : null,
    endsAt: end ? end.toISOString() : null,
  }
}