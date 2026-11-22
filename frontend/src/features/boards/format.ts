export function moduleFormatLabel(type: string, allowedTaskTypes: string[]): string {
  if (type === 'PENTEST') return 'Pentest'
  if (type === 'ATTACK_DEFENSE') return 'Attack & Defense'
  if (type === 'EXTENSION') return 'Extension'
  const single = allowedTaskTypes.length === 1 ? allowedTaskTypes[0] : ''
  switch (single) {
    case 'FLAG':
      return 'Flag challenges'
    case 'MANUAL':
      return 'Manual review'
    case 'INSTANCE':
      return 'Instance tasks'
    default:
      return 'Mixed tasks (legacy)'
  }
}

export function taskTypeLabel(type: string): string {
  switch (type) {
    case 'FLAG':
      return 'Flag'
    case 'MANUAL':
      return 'Manual review'
    case 'INSTANCE':
      return 'Instance'
    case 'EXPLOIT':
      return 'Exploit'
    case 'PATCH':
      return 'Patch'
    case 'PENTEST_REPORT':
      return 'Pentest report'
    case 'EXTENSION':
      return 'Extension'
    default:
      return type
  }
}
