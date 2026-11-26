<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { instancesApi, type ApprovedInstanceTemplate } from '@/features/instances/api'
import {
  parseAcceptedAnswers,
  validateCategory,
  validatePoints,
  validateTaskTitle,
} from '@/features/admin/validation'
import { buildCreateTaskRequest, buildSubmissionPolicy, buildUpdateTaskRequest } from '@/features/admin/taskPayload'
import {
  fromTemplateParameters,
  hasUnsupportedPolicy,
  initialParameterValues,
  parseParameterPolicy,
  type TemplateParameterField,
} from '@/features/admin/taskParameters'
import { isApiError } from '@/shared/api/errors'
import { useDiscardGuard } from '@/features/admin/useDiscardGuard'
import { DESCRIPTION_MAX_CHARACTERS, validateDescriptionLength } from '@/shared/lib/text'
import { MarkdownEditorLazy } from '@/shared/components/MarkdownEditorLazy'
import { taskTypeLabel } from '@/features/boards/format'
import type {
  AdminTask,
  SubmissionMode,
  TaskInstanceConfig,
  TaskInstanceModuleSettings,
  TaskType,
} from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  boardUuid: string
  moduleUuid: string
  task?: AdminTask | null
  allowedTaskTypes?: TaskType[]
  /** Persisted module policy, used to show the real inherited values. */
  instanceSettings?: TaskInstanceModuleSettings | null
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const nativeTaskTypes: TaskType[] = ['FLAG', 'MANUAL', 'INSTANCE']
const availableTypes = computed(() => {
  const allowed = props.allowedTaskTypes?.length ? props.allowedTaskTypes : nativeTaskTypes
  return nativeTaskTypes.filter((type) => allowed.includes(type))
})
const isLegacyChoice = computed(() => availableTypes.value.length > 1)

type SubmissionChoice = 'TEXT' | 'FILES' | 'BOTH' | 'EITHER'
const choiceToMode: Record<SubmissionChoice, SubmissionMode> = {
  TEXT: 'TEXT',
  FILES: 'FILES',
  BOTH: 'TEXT_AND_FILES',
  EITHER: 'TEXT_OR_FILES',
}
const modeToChoice: Partial<Record<SubmissionMode, SubmissionChoice>> = {
  TEXT: 'TEXT',
  FILES: 'FILES',
  TEXT_AND_FILES: 'BOTH',
  TEXT_OR_FILES: 'EITHER',
}

const form = reactive({
  title: '',
  category: '',
  description: '',
  points: 100,
  visible: false,
  type: 'FLAG' as TaskType,
  choice: 'TEXT' as SubmissionChoice,
  textRequired: true,
  textMinLength: 1,
  textMaxLength: 1_000_000,
  filesRequired: true,
  filesMinCount: 1,
  filesMaxCount: 5,
  filesMaxSizeMiB: 25,
  filesAllowedContentTypes: 'application/pdf,text/plain',
  acceptedAnswers: '',
  deploymentTemplateUuid: '',
  templateVersion: 1,
  parameters: {} as Record<string, string>,
  secretParameters: {} as Record<string, string>,
  lifetimeMode: 'INHERIT' as 'INHERIT' | 'CUSTOM',
  lifetimeUnit: 'minutes' as 'minutes' | 'hours',
  lifetimeInitial: 30,
  lifetimeMaxTotal: 120,
  extendEnabled: true,
  extendStep: 15,
  extendMaxCount: 6,
})
const {
  discardOpen: discardConfirmOpen,
  reset: resetGuard,
  attemptClose,
  keepEditing,
  discardAndClose,
} = useDiscardGuard({
  // Secrets are part of the dirty check so a typed secret is never lost
  // silently. The snapshot is in memory only and never persisted.
  snapshot: () => ({ ...form, parameterFields: parameterFields.value }),
  close: () => emit('update:open', false),
})
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.task))
// One live message, owned by the form and rendered outside the editor, so an
// over-limit save is never a silent no-op in either editor mode.
const descriptionError = computed(() =>
  validateDescriptionLength(form.description, DESCRIPTION_MAX_CHARACTERS),
)

const approvedTemplates = ref<ApprovedInstanceTemplate[]>([])
const templatesLoading = ref(false)
const templatesError = ref<unknown>(null)
const parameterFields = ref<TemplateParameterField[]>([])
// Set when the approved template's allowlist carries a restriction the server
// does not support yet, so the dialog shows an actionable operator error rather
// than an empty (and misleading) "no parameters" state.
const unsupportedPolicy = ref(false)
const templateHasNoParameters = ref(false)

// The inherited values come from the persisted module policy, never from a
// guessed global, so the helper text cannot promise a lifetime the server would
// reject.
const modulePolicy = computed(() => props.instanceSettings ?? null)
const moduleDefaultSeconds = computed(() => modulePolicy.value?.defaultTtlSeconds ?? 3600)
const moduleMaxSeconds = computed(() => modulePolicy.value?.maxTtlSeconds ?? 604800)
const moduleExtension = computed(
  () => modulePolicy.value?.extension ?? { enabled: true, seconds: 1800, maxCount: 3 },
)

/** Converts the visible values when the unit changes so the duration is stable. */
function setLifetimeUnit(next: 'minutes' | 'hours') {
  if (next === form.lifetimeUnit) return
  const factor = next === 'hours' ? 1 / 60 : 60
  form.lifetimeInitial = Math.round(form.lifetimeInitial * factor * 100) / 100
  form.lifetimeMaxTotal = Math.round(form.lifetimeMaxTotal * factor * 100) / 100
  form.extendStep = Math.round(form.extendStep * factor * 100) / 100
  form.lifetimeUnit = next
}

function unitSeconds(): number {
  return form.lifetimeUnit === 'hours' ? 3600 : 60
}

function secondsToInput(seconds: number): number {
  return Math.round((seconds / unitSeconds()) * 100) / 100
}

function inputToSeconds(value: number): number {
  return Math.round(value * unitSeconds())
}

/** Human duration for helper text, e.g. "30 min", "1 h 30 min". */
function formatDuration(seconds: number): string {
  const safe = Math.max(0, Math.round(seconds))
  const hours = Math.floor(safe / 3600)
  const minutes = Math.round((safe % 3600) / 60)
  if (hours > 0 && minutes > 0) return `${hours} h ${minutes} min`
  if (hours > 0) return `${hours} h`
  return `${minutes} min`
}

const inheritedSummary = computed(
  () =>
    `Module defaults: ${formatDuration(moduleDefaultSeconds.value)} initial, ` +
    `${formatDuration(moduleMaxSeconds.value)} maximum total, ` +
    (moduleExtension.value.enabled
      ? `+${formatDuration(moduleExtension.value.seconds)} × ${moduleExtension.value.maxCount} extensions.`
      : 'extensions disabled.'),
)

watch(
  () => [props.open, props.task] as const,
  ([open, task]) => {
    if (!open) return
    form.title = task?.title ?? ''
    form.category = task?.category ?? ''
    form.description = task?.description ?? ''
    form.points = task?.points ?? 100
    form.visible = task?.visible ?? false
    form.type = task?.type ?? availableTypes.value[0] ?? 'FLAG'
    form.choice = modeToChoice[task?.submission.mode ?? 'TEXT'] ?? 'TEXT'
    form.textRequired = task?.submission.text?.required ?? true
    form.textMinLength = task?.submission.text?.minLength ?? 1
    form.textMaxLength = task?.submission.text?.maxLength ?? 1_000_000
    form.filesRequired = task?.submission.files?.required ?? true
    form.filesMinCount = task?.submission.files?.minCount ?? 1
    form.filesMaxCount = task?.submission.files?.maxCount ?? 5
    form.filesMaxSizeMiB = Math.max(1, Math.round((task?.submission.files?.maxSizeBytes ?? 25 * 1024 * 1024) / (1024 * 1024)))
    form.filesAllowedContentTypes = task?.submission.files?.allowedContentTypes.join(', ') ?? 'application/pdf,text/plain'
    form.acceptedAnswers = ''
    form.deploymentTemplateUuid = task?.instanceConfig?.deploymentTemplateUuid ?? ''
    form.templateVersion = task?.instanceConfig?.templateVersion ?? 1
    form.parameters = {}
    form.secretParameters = {}
    const config = task?.instanceConfig
    const hasOverride =
      config?.ttlSeconds != null || config?.maxTtlSeconds != null || config?.extension != null
    form.lifetimeMode = hasOverride ? 'CUSTOM' : 'INHERIT'
    form.lifetimeUnit = 'minutes'
    form.lifetimeInitial = secondsToInput(config?.ttlSeconds ?? moduleDefaultSeconds.value)
    form.lifetimeMaxTotal = secondsToInput(config?.maxTtlSeconds ?? moduleMaxSeconds.value)
    const extension = config?.extension ?? moduleExtension.value
    form.extendEnabled = extension.enabled
    form.extendStep = secondsToInput(extension.seconds)
    form.extendMaxCount = extension.maxCount
    parameterFields.value = []
    unsupportedPolicy.value = false
    templateHasNoParameters.value = false
    problems.value = {}
    error.value = null
    templatesError.value = null
    if (form.type === 'INSTANCE' && !isEdit.value) void loadApprovedTemplates()
    resetGuard()
  },
  { immediate: true },
)

watch(
  () => form.type,
  (type) => {
    if (props.open && type === 'INSTANCE' && !isEdit.value && approvedTemplates.value.length === 0) {
      void loadApprovedTemplates()
    }
  },
)

function validatePolicy(): string | undefined {
  if (form.type === 'FLAG') return undefined
  if (form.choice === 'TEXT' || form.choice === 'BOTH' || form.choice === 'EITHER') {
    if (form.textMinLength < 0 || form.textMaxLength < 1 || form.textMaxLength > 1_000_000) {
      return 'Text bounds must be within 0..1000000 and max must be at least 1.'
    }
    if (form.textMaxLength < form.textMinLength) return 'Text max length must be at least the minimum.'
  }
  if (form.choice === 'FILES' || form.choice === 'BOTH' || form.choice === 'EITHER') {
    if (form.filesMinCount < 0 || form.filesMaxCount < 1 || form.filesMaxCount < form.filesMinCount) {
      return 'File count bounds must be valid (max >= min >= 0).'
    }
    if (form.filesMaxSizeMiB < 1 || form.filesMaxSizeMiB > 1024) {
      return 'File size limit must be between 1 and 1024 MiB.'
    }
    if (parseContentTypes().length === 0) return 'Add at least one allowed content type.'
  }
  return undefined
}

function parseContentTypes(): string[] {
  return form.filesAllowedContentTypes.split(',').map((value) => value.trim()).filter(Boolean)
}

async function loadApprovedTemplates() {
  templatesLoading.value = true
  templatesError.value = null
  try {
    approvedTemplates.value = (await instancesApi.listApprovedTemplates(props.boardUuid, props.moduleUuid)).items
  } catch (caught) {
    approvedTemplates.value = []
    templatesError.value = caught
  } finally {
    templatesLoading.value = false
  }
}

function selectApprovedTemplate(value: unknown) {
  if (value === null || value === undefined || value === '') return
  const selected = String(value)
  const template = approvedTemplates.value.find(
    (item) => `${item.templateUuid}:${item.version}` === selected,
  )
  if (!template) return
  form.deploymentTemplateUuid = template.templateUuid
  form.templateVersion = template.version
  unsupportedPolicy.value = hasUnsupportedPolicy(template.parametersPolicy)
  // The published manifest projection is the source of truth. The operator
  // allowlist policy is a restriction, never a field schema.
  const declared = template.parameters ?? []
  parameterFields.value = declared.length > 0
    ? fromTemplateParameters(declared)
    : parseParameterPolicy(template.parametersPolicy)
  templateHasNoParameters.value = parameterFields.value.length === 0
  form.parameters = initialParameterValues(parameterFields.value.filter((field) => !field.secret))
  // Secret inputs always start blank: a stored secret is write-only.
  form.secretParameters = {}
}

/**
 * The wire contract types every template parameter value as a string. A
 * `type="number"` input is auto-cast to a number by Vue, and a checkbox can
 * yield a boolean, so coerce here instead of sending a value the server rejects
 * as a type error.
 */
function toStringMap(values: Record<string, string>): Record<string, string> {
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(values)) {
    result[key] = String(value ?? '')
  }
  return result
}

function instanceProblem(): string | undefined {
  if (form.type !== 'INSTANCE') return undefined
  if (!isEdit.value) {
    if (!form.deploymentTemplateUuid) return 'Choose an approved template version.'
    if (!Number.isInteger(form.templateVersion) || form.templateVersion < 1) return 'Choose a valid template version.'
    for (const field of parameterFields.value) {
      const value = field.secret ? form.secretParameters[field.key] : form.parameters[field.key]
      if (field.required && !String(value ?? '').trim()) {
        return `${field.label} is required.`
      }
    }
  }
  if (form.lifetimeMode !== 'CUSTOM') return undefined
  const initial = inputToSeconds(form.lifetimeInitial)
  const maxTotal = inputToSeconds(form.lifetimeMaxTotal)
  const step = inputToSeconds(form.extendStep)
  if (!Number.isFinite(initial) || initial < 60) return 'Initial lifetime must be at least 1 minute.'
  if (!Number.isFinite(maxTotal) || maxTotal < 60) return 'Maximum total lifetime must be at least 1 minute.'
  if (maxTotal < initial) return 'Maximum total lifetime must be at least the initial lifetime.'
  if (maxTotal > moduleMaxSeconds.value) {
    return `Maximum total lifetime must not exceed the module maximum of ${formatDuration(moduleMaxSeconds.value)}.`
  }
  if (!form.extendEnabled) return undefined
  if (!moduleExtension.value.enabled) {
    return 'This module disables student extensions; remove the extension override.'
  }
  if (!Number.isFinite(step) || step < 60 || step > 86400) {
    return 'One extension step must be between 1 minute and 24 hours.'
  }
  if (!Number.isInteger(form.extendMaxCount) || form.extendMaxCount < 0 || form.extendMaxCount > 100) {
    return 'Max extensions must be a whole number between 0 and 100.'
  }
  if (form.extendMaxCount > moduleExtension.value.maxCount) {
    return `Max extensions must not exceed the module maximum of ${moduleExtension.value.maxCount}.`
  }
  return undefined
}

/**
 * Lifetime overrides for the wire. Inherit sends explicit nulls so a PATCH
 * really clears the override instead of leaving a stale value behind.
 */
function lifetimePayload(): Pick<TaskInstanceConfig, 'ttlSeconds' | 'maxTtlSeconds' | 'extension'> {
  if (form.lifetimeMode !== 'CUSTOM') {
    return { ttlSeconds: null, maxTtlSeconds: null, extension: null }
  }
  return {
    ttlSeconds: inputToSeconds(form.lifetimeInitial),
    maxTtlSeconds: inputToSeconds(form.lifetimeMaxTotal),
    extension: {
      enabled: form.extendEnabled,
      seconds: inputToSeconds(form.extendStep),
      maxCount: form.extendMaxCount,
    },
  }
}

async function submit() {
  error.value = null
  const answers = form.type === 'FLAG'
    ? (form.acceptedAnswers.trim()
        ? parseAcceptedAnswers(form.acceptedAnswers)
        : { answers: [], problem: isEdit.value ? undefined : 'Add at least one accepted answer for a flag task.' })
    : { answers: [], problem: undefined }

  problems.value = {
    title: validateTaskTitle(form.title.trim()),
    category: validateCategory(form.category.trim()),
    description: validateDescriptionLength(form.description, DESCRIPTION_MAX_CHARACTERS),
    points: validatePoints(form.points),
    answers: answers.problem,
    policy: validatePolicy(),
    instance: instanceProblem(),
  }
  if (Object.values(problems.value).some(Boolean)) return

  pending.value = true
  try {
    const policy = buildSubmissionPolicy({
      type: form.type,
      mode: form.type === 'FLAG' ? 'TEXT' : choiceToMode[form.choice],
      textRequired: form.textRequired,
      textMinLength: form.textMinLength,
      textMaxLength: form.textMaxLength,
      filesRequired: form.filesRequired,
      filesMinCount: form.filesMinCount,
      filesMaxCount: form.filesMaxCount,
      filesMaxSizeBytes: form.filesMaxSizeMiB * 1024 * 1024,
      filesAllowedContentTypes: parseContentTypes(),
    })
    const values = {
      title: form.title.trim(),
      category: form.category.trim(),
      description: form.description,
      points: form.points,
      visible: form.visible,
      type: form.type,
      submission: policy,
    }
    // The template binding is immutable after creation, so an edit sends only
    // the lifetime policy and never touches the binding or its parameters.
    const instance: TaskInstanceConfig = form.type === 'INSTANCE'
      ? (props.task
          ? { enabled: true, ...lifetimePayload() }
          : {
              enabled: true,
              deploymentTemplateUuid: form.deploymentTemplateUuid,
              templateVersion: form.templateVersion,
              ...(Object.keys(form.parameters).length > 0 ? { parameters: toStringMap(form.parameters) } : {}),
              ...(Object.keys(form.secretParameters).length > 0 ? { secretParameters: toStringMap(form.secretParameters) } : {}),
              ...lifetimePayload(),
            })
      : { enabled: false }
    if (props.task) {
      await boardsApi.updateTask(
        props.boardUuid,
        props.moduleUuid,
        props.task.uuid,
        buildUpdateTaskRequest(values, answers.answers, instance),
      )
    } else {
      await boardsApi.createTask(
        props.boardUuid,
        props.moduleUuid,
        buildCreateTaskRequest(values, answers.answers, instance),
      )
    }
    emit('saved')
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        title: caught.fieldIssue('title'),
        category: caught.fieldIssue('category'),
        points: caught.fieldIssue('points'),
        answers: caught.fieldIssue('acceptedAnswers'),
        instance: caught.fieldIssue('instance'),
      }
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(value) => { if (!value) attemptClose() }">
    <DialogContent class="authoring-dialog authoring-dialog--wide flex max-h-[92dvh] flex-col rounded-none border-border bg-black max-md:h-dvh max-md:max-h-dvh">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit task' : 'Create task' }}</DialogTitle>
        <DialogDescription>
          The task format comes from the module. {{ taskTypeLabel(form.type) }} tasks appear as a draft until you publish them.
        </DialogDescription>
      </DialogHeader>

      <form class="flex min-h-0 flex-1 flex-col" novalidate @submit.prevent="submit">
        <div class="authoring-body min-h-0 flex-1 overflow-y-auto md:pr-1" data-slot="authoring-scroll">
          <ErrorAlert v-if="error" :error="error" title="Could not save task" class="mb-3" />

          <div class="authoring-layout" data-slot="authoring-layout">
            <div class="authoring-main space-y-4" data-slot="authoring-main">
              <FormField label="Title" for-id="task-title" :error="problems.title">
                <Input id="task-title" v-model="form.title" />
              </FormField>

              <div class="space-y-2">
                <p class="text-sm font-medium">Content</p>
                <p
                  v-if="descriptionError"
                  id="task-description-error"
                  role="alert"
                  class="text-xs text-status-error-ink"
                >
                  {{ descriptionError }}
                </p>
                <MarkdownEditorLazy
                  id="task-description"
                  v-model="form.description"
                  label="Task description"
                  :max-length="DESCRIPTION_MAX_CHARACTERS"
                  :show-length-error="false"
                  :invalid="Boolean(descriptionError)"
                  :described-by="descriptionError ? 'task-description-error' : undefined"
                />
                <p class="text-xs text-muted-foreground">Attachments are added from the task editor after the draft is saved.</p>
              </div>

              <div v-if="form.type === 'INSTANCE'" class="space-y-3 border border-border p-3">
                <template v-if="isEdit">
                  <p class="text-xs text-muted-foreground">
                    The instance template binding is immutable after creation. Secret values are never shown; leaving them blank keeps them.
                  </p>
                </template>
                <template v-else>
                  <ErrorAlert v-if="templatesError" :error="templatesError" title="Could not load approved templates">
                    <Button type="button" variant="ghost" class="mt-2" @click="loadApprovedTemplates">Retry</Button>
                  </ErrorAlert>
                  <p v-else-if="templatesLoading" class="text-xs text-muted-foreground">Loading approved templates…</p>
                  <div v-else-if="approvedTemplates.length === 0" class="space-y-2 text-sm">
                    <p class="text-muted-foreground">No approved template version is available for this module.</p>
                    <Button as-child variant="outline" size="sm">
                      <RouterLink to="/admin/templates">Open deployment templates</RouterLink>
                    </Button>
                  </div>
                  <template v-else>
                    <FormField label="Approved template" for-id="task-instance-approved">
                      <select
                        id="task-instance-approved"
                        :value="`${form.deploymentTemplateUuid}:${form.templateVersion}`"
                        class="h-9 w-full border border-border bg-black px-2 text-sm"
                        @change="selectApprovedTemplate(($event.target as HTMLSelectElement).value)"
                      >
                        <option value="">Choose an approved version</option>
                        <option
                          v-for="template in approvedTemplates"
                          :key="`${template.templateUuid}:${template.version}`"
                          :value="`${template.templateUuid}:${template.version}`"
                        >
                          {{ template.name }} v{{ template.version }} ({{ template.slug }})
                        </option>
                      </select>
                    </FormField>

                    <div v-if="unsupportedPolicy" class="space-y-2 border border-gold/60 p-3">
                      <p class="text-xs text-gold">
                        This template's allowlist declares a parameter restriction that is not supported yet, so the task
                        cannot bind. Ask an operator to clear <code>parametersPolicy</code> on the allowlist: template
                        parameters are validated from the published manifest instead.
                      </p>
                    </div>
                    <div v-else-if="parameterFields.length > 0" class="space-y-3 border-t border-border pt-3">
                      <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Template parameters</p>
                      <FormField
                        v-for="field in parameterFields.filter((entry) => !entry.secret)"
                        :key="field.key"
                        :label="field.label + (field.required ? ' *' : '')"
                        :for-id="`param-${field.key}`"
                      >
                        <select
                          v-if="field.type === 'select'"
                          :id="`param-${field.key}`"
                          v-model="form.parameters[field.key]"
                          class="h-9 w-full border border-border bg-black px-2 text-sm"
                        >
                          <option value="">Choose…</option>
                          <option v-for="option in field.options" :key="option" :value="option">{{ option }}</option>
                        </select>
                        <label v-else-if="field.type === 'boolean'" class="flex min-h-9 items-center gap-2 text-sm">
                          <input v-model="form.parameters[field.key]" type="checkbox" true-value="true" false-value="false" />
                          {{ field.defaultValue === 'true' ? 'Enabled' : 'Toggle' }}
                        </label>
                        <Textarea
                          v-else-if="field.type === 'textarea'"
                          :id="`param-${field.key}`"
                          v-model="form.parameters[field.key]"
                          rows="3"
                        />
                        <Input
                          v-else
                          :id="`param-${field.key}`"
                          v-model="form.parameters[field.key]"
                          :type="field.type === 'number' ? 'number' : 'text'"
                        />
                      </FormField>

                      <template v-if="parameterFields.some((entry) => entry.secret)">
                        <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Secret parameters</p>
                        <FormField
                          v-for="field in parameterFields.filter((entry) => entry.secret)"
                          :key="field.key"
                          :label="field.label + (field.required ? ' *' : '')"
                          :for-id="`secret-${field.key}`"
                          hint="Write-only. Never displayed again."
                        >
                          <Input
                            :id="`secret-${field.key}`"
                            v-model="form.secretParameters[field.key]"
                            type="password"
                            autocomplete="new-password"
                            spellcheck="false"
                          />
                        </FormField>
                      </template>
                    </div>
                    <p v-else-if="templateHasNoParameters" class="text-xs text-muted-foreground">
                      This template declares no parameters, so the task binds with its defaults.
                    </p>
                  </template>
                </template>
              </div>
            </div>

            <div class="authoring-settings space-y-4" data-slot="authoring-settings">
              <div class="flex flex-wrap items-center gap-2 text-sm">
                <span class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Format</span>
                <span class="border border-border px-2 py-1">{{ taskTypeLabel(form.type) }}</span>
              </div>

              <div v-if="isLegacyChoice && !isEdit" class="space-y-2 border border-gold/60 p-3">
                <p class="text-xs text-gold">This module mixes task types. Choose the task type for this new task.</p>
                <select v-model="form.type" class="h-9 w-full border border-border bg-black px-2 text-sm">
                  <option v-for="type in availableTypes" :key="type" :value="type">{{ taskTypeLabel(type) }}</option>
                </select>
              </div>

              <FormField label="Points" for-id="task-points" :error="problems.points">
                <Input id="task-points" v-model.number="form.points" type="number" min="0" step="1" />
              </FormField>
              <FormField label="Category" for-id="task-category" :error="problems.category">
                <Input id="task-category" v-model="form.category" />
              </FormField>

              <FormField
                v-if="form.type === 'FLAG'"
                label="Accepted answers"
                for-id="task-answers"
                :error="problems.answers"
                :hint="isEdit
                  ? `Write-only. ${task?.answerConfiguration.configured ? task.answerConfiguration.acceptedAnswersCount : 0} configured. Leave blank to keep them.`
                  : 'One answer per line. Never returned by the API.'"
              >
                <Textarea
                  id="task-answers"
                  v-model="form.acceptedAnswers"
                  rows="4"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="flag{...}"
                />
              </FormField>

              <fieldset v-if="form.type !== 'FLAG'" class="space-y-3 border border-border p-3">
                <legend class="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Submission</legend>
                <FormField label="What participants submit" for-id="task-choice">
                  <select id="task-choice" v-model="form.choice" class="h-9 w-full border border-border bg-black px-2 text-sm">
                    <option value="TEXT">Text answer</option>
                    <option value="FILES">Files</option>
                    <option value="BOTH">Text and files</option>
                    <option value="EITHER">Text or files</option>
                  </select>
                </FormField>

                <template v-if="form.choice !== 'FILES'">
                  <label class="flex min-h-11 items-center gap-2 text-sm">
                    <Checkbox v-model="form.textRequired" />
                    Text is required
                  </label>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <FormField label="Minimum characters" for-id="task-text-min">
                      <Input id="task-text-min" v-model.number="form.textMinLength" type="number" min="0" max="1000000" />
                    </FormField>
                    <FormField label="Maximum characters" for-id="task-text-max">
                      <Input id="task-text-max" v-model.number="form.textMaxLength" type="number" min="1" max="1000000" />
                    </FormField>
                  </div>
                </template>

                <template v-if="form.choice !== 'TEXT'">
                  <label class="flex min-h-11 items-center gap-2 text-sm">
                    <Checkbox v-model="form.filesRequired" />
                    Files are required
                  </label>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <FormField label="Minimum files" for-id="task-files-min">
                      <Input id="task-files-min" v-model.number="form.filesMinCount" type="number" min="0" max="100" />
                    </FormField>
                    <FormField label="Maximum files" for-id="task-files-max">
                      <Input id="task-files-max" v-model.number="form.filesMaxCount" type="number" min="1" max="100" />
                    </FormField>
                    <FormField label="Max size (MiB)" for-id="task-files-size">
                      <Input id="task-files-size" v-model.number="form.filesMaxSizeMiB" type="number" min="1" max="1024" />
                    </FormField>
                  </div>
                  <FormField label="Allowed content types" for-id="task-files-types" hint="Comma-separated MIME types.">
                    <Input id="task-files-types" v-model="form.filesAllowedContentTypes" />
                  </FormField>
                </template>
                <p v-if="problems.policy" class="text-xs text-love">{{ problems.policy }}</p>
              </fieldset>

              <label class="flex min-h-11 items-center gap-2 text-sm">
                <Checkbox v-model="form.visible" />
                Visible to participants
              </label>

              <div v-if="form.type === 'INSTANCE'" class="space-y-3 border border-border p-3">
                <p v-if="problems.instance" class="text-xs text-love">{{ problems.instance }}</p>
                <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Lifetime</p>
                <div class="flex flex-wrap gap-2" role="group" aria-label="Lifetime policy">
                  <Button
                    type="button"
                    size="sm"
                    :variant="form.lifetimeMode === 'INHERIT' ? 'default' : 'outline'"
                    @click="form.lifetimeMode = 'INHERIT'"
                  >
                    Inherit module defaults
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    :variant="form.lifetimeMode === 'CUSTOM' ? 'default' : 'outline'"
                    @click="form.lifetimeMode = 'CUSTOM'"
                  >
                    Custom
                  </Button>
                </div>
                <p class="text-xs text-muted-foreground">{{ inheritedSummary }}</p>
                <p class="text-xs text-muted-foreground">
                  Lifetime begins when the environment is requested; startup time is included.
                </p>

                <template v-if="form.lifetimeMode === 'CUSTOM'">
                  <FormField label="Initial lifetime" for-id="task-lifetime-initial">
                    <div class="flex gap-2">
                      <Input
                        id="task-lifetime-initial"
                        v-model.number="form.lifetimeInitial"
                        type="number"
                        min="1"
                        step="1"
                      />
                      <select
                        :value="form.lifetimeUnit"
                        class="h-9 border border-border bg-black px-2 text-sm"
                        aria-label="Lifetime unit"
                        @change="setLifetimeUnit(($event.target as HTMLSelectElement).value as 'minutes' | 'hours')"
                      >
                        <option value="minutes">minutes</option>
                        <option value="hours">hours</option>
                      </select>
                    </div>
                  </FormField>

                  <details class="border border-border">
                    <summary class="cursor-pointer px-3 py-2 text-sm font-medium">Advanced</summary>
                    <div class="space-y-3 border-t border-border p-3">
                      <FormField label="Maximum total lifetime" for-id="task-lifetime-max">
                        <Input
                          id="task-lifetime-max"
                          v-model.number="form.lifetimeMaxTotal"
                          type="number"
                          min="1"
                          step="1"
                        />
                      </FormField>
                      <label class="flex min-h-11 items-center gap-2 text-sm">
                        <Checkbox v-model="form.extendEnabled" />
                        Allow student extensions
                      </label>
                      <div class="grid gap-3 sm:grid-cols-2">
                        <FormField label="One extension step" for-id="task-extension-step">
                          <Input
                            id="task-extension-step"
                            v-model.number="form.extendStep"
                            type="number"
                            min="1"
                            step="1"
                            :disabled="!form.extendEnabled"
                          />
                        </FormField>
                        <FormField label="Max extensions" for-id="task-extension-count">
                          <Input
                            id="task-extension-count"
                            v-model.number="form.extendMaxCount"
                            type="number"
                            min="0"
                            max="100"
                            step="1"
                            :disabled="!form.extendEnabled"
                          />
                        </FormField>
                      </div>
                      <p class="text-xs text-muted-foreground">
                        Participants extend by the step you set; they never choose a duration. The maximum total
                        lifetime is a hard cap measured from the request time.
                      </p>
                    </div>
                  </details>
                </template>
              </div>
            </div>
          </div>
        </div>

        <DialogFooter class="mt-4 border-t border-border pt-4">
          <Button type="button" variant="ghost" :disabled="pending" @click="attemptClose()">Cancel</Button>
          <Button type="submit" :disabled="pending">
            {{ pending ? 'Saving…' : isEdit ? 'Save changes' : 'Save draft' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>

  <ConfirmDialog
    :open="discardConfirmOpen"
    title="Discard task changes?"
    description="Your unsaved edits, including any typed answers or secrets, will be lost. This cannot be undone."
    confirm-label="Discard changes"
    destructive
    @update:open="(value) => { if (!value) keepEditing() }"
    @confirm="discardAndClose()"
  />
</template>
