import { describe, expect, it } from 'vitest'
import { normalizeStatus, statusLabel, statusMeta, statusTone } from '@/shared/lib/status'

describe('normalizeStatus', () => {
  it('uppercases and normalizes separators', () => {
    expect(normalizeStatus('pending-review')).toBe('PENDING_REVIEW')
    expect(normalizeStatus('  pending review  ')).toBe('PENDING_REVIEW')
    expect(normalizeStatus('enabled')).toBe('ENABLED')
  })
})

describe('statusTone', () => {
  it('maps successful outcomes to success', () => {
    for (const status of ['ACCEPTED', 'SOLVED', 'PASSED', 'PATCHED', 'READY', 'ATTACHED', 'CONNECTED', 'ENABLED', 'DELIVERED']) {
      expect(statusTone(status), status).toBe('success')
    }
  })

  it('maps active and processing states to active', () => {
    for (const status of ['ACTIVE', 'RUNNING', 'CHECKING', 'VALIDATING', 'PROVISIONING', 'RESETTING', 'DESTROYING', 'AVAILABLE']) {
      expect(statusTone(status), status).toBe('active')
    }
  })

  it('maps pending review and attention states to attention', () => {
    for (const status of ['PENDING', 'PENDING_REVIEW', 'SCHEDULED', 'UPCOMING', 'QUEUED', 'AWAITING_UPLOAD', 'FOUND']) {
      expect(statusTone(status), status).toBe('attention')
    }
  })

  it('maps failures to error', () => {
    for (const status of ['REJECTED', 'ERROR', 'FAILED', 'UNHEALTHY', 'DEAD']) {
      expect(statusTone(status), status).toBe('error')
    }
  })

  it('maps access restrictions to restricted', () => {
    for (const status of ['LOCKED', 'RESTRICTED', 'BLOCKED', 'INCOMPATIBLE']) {
      expect(statusTone(status), status).toBe('restricted')
    }
  })

  it('maps draft and unknown states to neutral', () => {
    for (const status of ['DRAFT', 'UNKNOWN', 'DISCOVERED', 'NOT_CREATED', 'INSTALLED']) {
      expect(statusTone(status), status).toBe('neutral')
    }
  })

  it('maps finished, expired, and canceled states to muted', () => {
    for (const status of ['FINISHED', 'ARCHIVED', 'EXPIRED', 'CANCELED', 'CANCELLED', 'CONSUMED', 'DISABLED', 'STOPPED', 'DESTROYED']) {
      expect(statusTone(status), status).toBe('muted')
    }
  })

  it('never maps expired or canceled to active', () => {
    expect(statusTone('EXPIRED')).not.toBe('active')
    expect(statusTone('CANCELED')).not.toBe('active')
    expect(statusTone('CANCELLED')).not.toBe('active')
    expect(statusTone('expired')).not.toBe('active')
    expect(statusTone('canceled')).not.toBe('active')
  })

  it('normalizes lowercase API statuses', () => {
    expect(statusTone('enabled')).toBe('success')
    expect(statusTone('unhealthy')).toBe('error')
    expect(statusTone('failed')).toBe('error')
    expect(statusTone('pending')).toBe('attention')
  })

  it('returns neutral for unknown statuses', () => {
    expect(statusTone('SOMETHING_NEW')).toBe('neutral')
    expect(statusTone('')).toBe('neutral')
  })
})

describe('statusLabel', () => {
  it('uses explicit friendly labels', () => {
    expect(statusLabel('ACCEPTED')).toBe('Accepted')
    expect(statusLabel('PENDING_REVIEW')).toBe('Needs review')
    expect(statusLabel('EXPIRED')).toBe('Expired')
    expect(statusLabel('enabled')).toBe('Enabled')
  })

  it('humanizes unknown statuses without hiding the raw value', () => {
    expect(statusLabel('SOMETHING_NEW')).toBe('Something new')
    expect(statusLabel('provider-timeout')).toBe('Provider timeout')
  })
})

describe('statusMeta', () => {
  it('combines label and tone', () => {
    expect(statusMeta('PENDING_REVIEW')).toEqual({ label: 'Needs review', tone: 'attention' })
    expect(statusMeta('WHATEVER')).toEqual({ label: 'Whatever', tone: 'neutral' })
  })
})
