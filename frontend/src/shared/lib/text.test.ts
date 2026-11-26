import { describe, expect, it } from 'vitest'
import {
  DESCRIPTION_MAX_CHARACTERS,
  SITE_INTRO_MAX_CHARACTERS,
  countCharacters,
  validateDescriptionLength,
} from './text'

describe('countCharacters', () => {
  it('counts code points like Go runes', () => {
    expect(countCharacters('plain')).toBe(5)
    expect(countCharacters('🎯')).toBe(1)
    expect(countCharacters('日本語')).toBe(3)
    expect(countCharacters('a🎯日')).toBe(3)
  })

  it('treats an empty string as zero characters', () => {
    expect(countCharacters('')).toBe(0)
  })
})

describe('validateDescriptionLength', () => {
  it('accepts the limit and rejects one character over', () => {
    expect(validateDescriptionLength('x'.repeat(50_000), DESCRIPTION_MAX_CHARACTERS)).toBeUndefined()
    expect(validateDescriptionLength('x'.repeat(50_001), DESCRIPTION_MAX_CHARACTERS))
      .toBe('Description must be at most 50,000 characters.')
    expect(validateDescriptionLength('🎯'.repeat(131_072), SITE_INTRO_MAX_CHARACTERS)).toBeUndefined()
  })

  it('counts code points rather than UTF-16 units for the site intro', () => {
    expect(
      validateDescriptionLength('🎯'.repeat(131_073), SITE_INTRO_MAX_CHARACTERS),
    ).toBe('Description must be at most 131,072 characters.')
  })
})
