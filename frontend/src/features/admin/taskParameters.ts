/**
 * Normalises the operator-supplied template `parametersPolicy` into typed form
 * fields. The backend stores this policy as raw JSON, so the UI accepts the two
 * documented shapes:
 *
 * 1. an array of `{key,label,type,options,default,required,secret}` objects
 *    (the deployment manifest `parameters` shape), or
 * 2. an object map `{key: {…}}` / `{key: "default"}`.
 *
 * Anything unrecognised yields no fields. The UI never falls back to a raw JSON
 * textarea and never invents a template UUID field.
 */
export interface TemplateParameterField {
  key: string
  label: string
  type: 'string' | 'number' | 'boolean' | 'select' | 'textarea'
  options: string[]
  defaultValue: string
  required: boolean
  secret: boolean
}

const KNOWN_TYPES = new Set(['string', 'number', 'boolean', 'select', 'textarea'])

function normaliseField(key: string, raw: unknown): TemplateParameterField | null {
  if (typeof raw === 'string' || typeof raw === 'number' || typeof raw === 'boolean') {
    return {
      key,
      label: key,
      type: 'string',
      options: [],
      defaultValue: String(raw),
      required: false,
      secret: false,
    }
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null
  const record = raw as Record<string, unknown>
  const declaredType = typeof record.type === 'string' ? record.type : 'string'
  const options = Array.isArray(record.options) ? record.options.map(String) : []
  const type = KNOWN_TYPES.has(declaredType)
    ? (declaredType as TemplateParameterField['type'])
    : options.length > 0
      ? 'select'
      : 'string'
  return {
    key: typeof record.key === 'string' && record.key ? record.key : key,
    label: typeof record.label === 'string' && record.label ? record.label : key,
    type: options.length > 0 && type !== 'select' ? 'select' : type,
    options,
    defaultValue: record.default === undefined || record.default === null ? '' : String(record.default),
    required: record.required === true,
    secret: record.secret === true,
  }
}

export function parseParameterPolicy(policy: unknown): TemplateParameterField[] {
  if (Array.isArray(policy)) {
    const fields: TemplateParameterField[] = []
    for (const entry of policy) {
      if (!entry || typeof entry !== 'object') continue
      const record = entry as Record<string, unknown>
      const key = typeof record.key === 'string' ? record.key : ''
      if (!key) continue
      const field = normaliseField(key, entry)
      if (field) fields.push(field)
    }
    return fields
  }
  if (policy && typeof policy === 'object') {
    const fields: TemplateParameterField[] = []
    for (const [key, value] of Object.entries(policy as Record<string, unknown>)) {
      const field = normaliseField(key, value)
      if (field) fields.push(field)
    }
    return fields
  }
  return []
}

export function initialParameterValues(fields: TemplateParameterField[]): Record<string, string> {
  const values: Record<string, string> = {}
  for (const field of fields) {
    if (field.defaultValue !== '') values[field.key] = field.defaultValue
  }
  return values
}
