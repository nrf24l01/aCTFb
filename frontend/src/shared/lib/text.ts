/**
 * Character counting for organizer-authored prose.
 *
 * The server validates `MaxLength` in runes (Go `utf8.RuneCountInString`), which
 * equals the number of Unicode code points for valid UTF-8. `String#length`
 * counts UTF-16 code units, so an emoji or a CJK ideograph would drift by one
 * and the save would fail with a 422 after the editor accepted the text.
 * `Array.from(value)` iterates code points, matching the server.
 */

/** `Board`, `Module` and `Task` descriptions: 50 000 runes server-side. */
export const DESCRIPTION_MAX_CHARACTERS = 50_000

/** `Site.introMarkdown`: 131 072 runes server-side. */
export const SITE_INTRO_MAX_CHARACTERS = 131_072

export function countCharacters(value: string): number {
  if (typeof value !== 'string' || value.length === 0) return 0
  return Array.from(value).length
}

/**
 * Returns a blocking field message when the value exceeds `max`, otherwise
 * `undefined`. The limit itself is allowed; the message is stable so tests and
 * dialogs can assert on it.
 */
export function validateDescriptionLength(value: string, max: number): string | undefined {
  const count = countCharacters(value)
  if (count <= max) return undefined
  return `Description must be at most ${max.toLocaleString('en-US')} characters.`
}
