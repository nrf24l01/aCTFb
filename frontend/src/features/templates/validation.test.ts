import { describe, expect, it } from 'vitest'
import { validateRelativePath } from '@/features/templates/validation'

describe('deployment template package paths', () => {
  it('accepts bounded relative paths', () => {
    expect(validateRelativePath('cloud-init/user-data.yaml')).toBeNull()
    expect(validateRelativePath('app/entrypoint.sh')).toBeNull()
  })

  it('rejects traversal, absolute, backslash and control paths', () => {
    expect(validateRelativePath('../etc/passwd')).toContain('segments')
    expect(validateRelativePath('/etc/passwd')).toContain('relative')
    expect(validateRelativePath('a/../../b')).toContain('segments')
    expect(validateRelativePath('a\\b')).toContain('backslash')
    expect(validateRelativePath('a/\u0000b')).toContain('control')
    expect(validateRelativePath('a//b')).toContain('segments')
  })
})
