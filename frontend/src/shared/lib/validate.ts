export function isUuid(value: string): boolean {
  return /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(
    value.trim(),
  )
}

export function normalizeUuidInput(value: string): string {
  const withoutQuery = value.trim().split(/[?#]/)[0]
  const afterBoardPath = withoutQuery.includes('/boards/')
    ? withoutQuery.slice(withoutQuery.lastIndexOf('/boards/') + '/boards/'.length)
    : withoutQuery
  return afterBoardPath.replace(/\/+$/, '')
}