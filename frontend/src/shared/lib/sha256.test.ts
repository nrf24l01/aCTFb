import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import { Sha256, sha256File } from '@/shared/lib/sha256'

function nodeHash(value: Uint8Array): string {
  return createHash('sha256').update(value).digest('hex')
}

describe('SHA-256', () => {
  it('matches known vectors', () => {
    expect(new Sha256().update(new TextEncoder().encode('')).digestHex()).toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    )
    expect(new Sha256().update(new TextEncoder().encode('abc')).digestHex()).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    )
    const long = new TextEncoder().encode('a'.repeat(1_000_000))
    expect(new Sha256().update(long).digestHex()).toBe(nodeHash(long))
  })

  it('streams a File in chunks and matches the whole-buffer digest', async () => {
    const bytes = new TextEncoder().encode('chunked file hashing '.repeat(1000))
    const file = new File([bytes], 'sample.txt', { type: 'text/plain' })
    expect(await sha256File(file, 7)).toBe(nodeHash(bytes))
  })
})