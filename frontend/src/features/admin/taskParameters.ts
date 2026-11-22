/**
 * The authoritative source for the typed parameter form is the approved
 * immutable template's published-manifest `parameters` projection. It carries
 * only parameter definitions, and a secret parameter never carries a default.
 *
 * `parametersPolicy` (the operator allowlist restriction) is NOT a field schema
 * and is not interpreted as one: a non-empty policy is still rejected by the
 * server, so the UI surfaces that as an unsupported configuration instead of
 * pretending the form works.
 */
export function fromTemplateParameters(parameters: ApprovedTemplateParameter[]): TemplateParameterField[] {
  const fields: TemplateParameterField[] = []
  for (const parameter of parameters) {
    if (!parameter.key) continue
    const options = parameter.options ?? []
    fields.push({
      key: parameter.key,
      label: parameter.label || parameter.key,
      type: normalizeDeclaredType(parameter.type, options.length > 0),
      options,
      // A secret default is never sent; even if a server bug echoed one, it is
      // dropped here so the input always starts blank.
      defaultValue: parameter.secret ? '' : (parameter.default ?? ''),
      required: parameter.required === true,
      secret: parameter.secret === true,
    })
  }
  return fields
}

function normalizeDeclaredType(declared: string, hasOptions: boolean): TemplateParameterField['type'] {
  const value = (declared || '').toLowerCase()
  switch (value) {
    case 'int':
    case 'integer':
    case 'number':
      return 'number'
    case 'bool':
    case 'boolean':
      return 'boolean'
    case 'choice':
    case 'select':
      return 'select'
    case 'textarea':
    case 'text':
      return 'textarea'
    case 'string':
      return hasOptions ? 'select' : 'string'
    default:
      return hasOptions ? 'select' : 'string'
  }
}

/** True when the allowlist carries a restriction the server does not support. */
export function hasUnsupportedPolicy(policy: Record<string, unknown> | null | undefined): boolean {
  if (!policy) return false
  return Object.keys(policy).length > 0
}

/**
 * Legacy fallback: normalises an operator-supplied `parametersPolicy` into typed
 * form fields. Only used when a template carries no manifest projection. The UI
 * accepts the two documented shapes:
 *
 * 1. an array of `{key,label,type,options,default,required,secret}` objects
 *    (the deployment manifest `parameters` shape), or
 * 2. an object map `{key: {…}}` / `{key: "default"}`.
 *
 * Anything unrecognised yields no fields. The UI never falls back to a raw JSON
 * textarea and never invents a template UUID field.
 */
import type { ApprovedTemplateParameter } from '@/features/instances/api'

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
