export interface FieldProblems {
  [field: string]: string | undefined
}

export function validateUsername(value: string): string | undefined {
  if (value.length < 3 || value.length > 32) return 'Username must be 3–32 characters.'
  if (!/^[a-zA-Z0-9_.-]+$/.test(value)) return 'Use letters, digits, dot, underscore or hyphen only.'
  return undefined
}

export function validatePassword(value: string): string | undefined {
  if (value.length < 8 || value.length > 128) return 'Password must be 8–128 characters.'
  return undefined
}

export function validateEmail(value: string): string | undefined {
  if (value.length > 254) return 'Email must be at most 254 characters.'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) return 'Enter a valid email address.'
  return undefined
}

export function validateSignIn(username: string, password: string): FieldProblems {
  return {
    username: validateUsername(username),
    password: password ? undefined : 'Enter your password.',
  }
}

export function validateRegistration(
  email: string,
  username: string,
  password: string,
  confirmPassword: string,
): FieldProblems {
  return {
    email: validateEmail(email),
    username: validateUsername(username),
    password: validatePassword(password),
    confirmPassword:
      password === confirmPassword ? undefined : 'The passwords do not match.',
  }
}

export function validatePasswordReset(
  password: string,
  confirmPassword: string,
): FieldProblems {
  return {
    password: validatePassword(password),
    confirmPassword:
      password === confirmPassword ? undefined : 'The passwords do not match.',
  }
}

export function hasProblems(problems: FieldProblems): boolean {
  return Object.values(problems).some(Boolean)
}