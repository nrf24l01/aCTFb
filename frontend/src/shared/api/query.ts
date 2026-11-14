export type QueryValue = string | number | boolean | null | undefined

export function withQuery(path: string, query: Record<string, QueryValue>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null && value !== '') params.set(key, String(value))
  }
  const suffix = params.toString()
  return suffix ? `${path}?${suffix}` : path
}

export function pageQuery(limit = 100, offset = 0, query?: string): Record<string, QueryValue> {
  return { limit, offset, query }
}