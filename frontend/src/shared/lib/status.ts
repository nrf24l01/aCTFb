/**
 * One place that turns an API status string into a label and a visual tone.
 *
 * Tones are semantic, never colour-only: every badge also renders its label and
 * a decorative icon. Unknown statuses stay neutral instead of guessing.
 */
export type StatusTone = 'success' | 'active' | 'attention' | 'error' | 'restricted' | 'neutral' | 'muted'

export interface StatusMeta {
  label: string
  tone: StatusTone
}

const STATUS_META: Record<string, StatusMeta> = {
  // Board / module lifecycle
  DRAFT: { label: 'Draft', tone: 'neutral' },
  SCHEDULED: { label: 'Scheduled', tone: 'attention' },
  ACTIVE: { label: 'Active', tone: 'active' },
  FINISHED: { label: 'Finished', tone: 'muted' },
  ARCHIVED: { label: 'Archived', tone: 'muted' },
  DISABLED: { label: 'Disabled', tone: 'muted' },
  UPCOMING: { label: 'Upcoming', tone: 'attention' },
  LOCKED: { label: 'Locked', tone: 'restricted' },
  AVAILABLE: { label: 'Available', tone: 'active' },
  SOLVED: { label: 'Solved', tone: 'success' },

  // Submission review
  PENDING: { label: 'Pending', tone: 'attention' },
  CHECKING: { label: 'Checking', tone: 'active' },
  PENDING_REVIEW: { label: 'Needs review', tone: 'attention' },
  ACCEPTED: { label: 'Accepted', tone: 'success' },
  REJECTED: { label: 'Rejected', tone: 'error' },
  ERROR: { label: 'Error', tone: 'error' },

  // Join codes
  OPEN: { label: 'Open', tone: 'active' },
  CONSUMED: { label: 'Consumed', tone: 'muted' },
  EXPIRED: { label: 'Expired', tone: 'muted' },

  // Submission files
  AWAITING_UPLOAD: { label: 'Awaiting upload', tone: 'attention' },
  VALIDATING: { label: 'Validating', tone: 'active' },
  READY: { label: 'Ready', tone: 'success' },
  ATTACHED: { label: 'Attached', tone: 'success' },

  // Runtime instances
  NOT_CREATED: { label: 'Not started', tone: 'neutral' },
  PROVISIONING: { label: 'Provisioning', tone: 'active' },
  RUNNING: { label: 'Running', tone: 'active' },
  RESETTING: { label: 'Resetting', tone: 'active' },
  DESTROYING: { label: 'Destroying', tone: 'active' },
  STOPPED: { label: 'Stopped', tone: 'muted' },
  DESTROYED: { label: 'Destroyed', tone: 'muted' },

  // Integrations
  CONNECTED: { label: 'Connected', tone: 'success' },
  UNKNOWN: { label: 'Unknown', tone: 'neutral' },

  // Attack/defense
  FOUND: { label: 'Found', tone: 'attention' },
  PATCHED: { label: 'Patched', tone: 'success' },
  QUEUED: { label: 'Queued', tone: 'attention' },
  PASSED: { label: 'Passed', tone: 'success' },
  FAILED: { label: 'Failed', tone: 'error' },
  CANCELED: { label: 'Canceled', tone: 'muted' },
  CANCELLED: { label: 'Canceled', tone: 'muted' },

  // Plugin lifecycle
  DISCOVERED: { label: 'Discovered', tone: 'neutral' },
  INSTALLED: { label: 'Installed', tone: 'neutral' },
  ENABLED: { label: 'Enabled', tone: 'success' },
  INCOMPATIBLE: { label: 'Incompatible', tone: 'restricted' },
  HEALTHY: { label: 'Healthy', tone: 'success' },
  UNHEALTHY: { label: 'Unhealthy', tone: 'error' },

  // Outbox delivery
  DELIVERED: { label: 'Delivered', tone: 'success' },
  DEAD: { label: 'Dead', tone: 'error' },

  // Explicit restrictions
  RESTRICTED: { label: 'Restricted', tone: 'restricted' },
  BLOCKED: { label: 'Blocked', tone: 'restricted' },
}

export function normalizeStatus(status: string): string {
  return status.trim().replace(/[\s-]+/g, '_').toUpperCase()
}

export function statusTone(status: string): StatusTone {
  return STATUS_META[normalizeStatus(status)]?.tone ?? 'neutral'
}

export function statusLabel(status: string): string {
  const key = normalizeStatus(status)
  const known = STATUS_META[key]
  if (known) return known.label

  const words = key.toLowerCase().replace(/_/g, ' ').trim()
  return words.length > 0 ? words.charAt(0).toUpperCase() + words.slice(1) : status
}

export function statusMeta(status: string): StatusMeta {
  return { label: statusLabel(status), tone: statusTone(status) }
}
