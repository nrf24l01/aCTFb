/**
 * Relative package path only; no leading slash, backslash, NUL/control chars,
 * empty/./.. segments. Matches the Swagger path pattern closely and rejects
 * traversal before any file is sent to the server.
 */
export function validateRelativePath(path: string): string | null {
  if (!path || path.length > 255) return 'Package path must be 1–255 characters.'
  if (path.startsWith('/') || path.includes('\\') || /[\x00-\x1f]/.test(path)) {
    return 'Package path must be relative and contain no slash prefix, backslash or control characters.'
  }
  const segments = path.split('/')
  if (segments.some((segment) => segment === '' || segment === '.' || segment === '..')) {
    return 'Package path cannot contain empty, . or .. segments.'
  }
  if (!segments.every((segment) => /^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$/.test(segment))) {
    return 'Package path contains an unsupported character.'
  }
  return null
}