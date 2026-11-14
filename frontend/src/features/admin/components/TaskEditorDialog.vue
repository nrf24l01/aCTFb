<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
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
import { isApiError } from '@/shared/api/errors'
import { isUuid } from '@/shared/lib/validate'
import type { AdminTask, SubmissionMode, TaskInstanceConfig, TaskType } from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  boardUuid: string
  moduleUuid: string
  task?: AdminTask | null
  allowedTaskTypes?: TaskType[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const nativeTaskTypes: TaskType[] = ['FLAG', 'MANUAL', 'INSTANCE']
const form = reactive({
  title: '',
  category: '',
  description: '',
  points: 100,
  visible: true,
  type: 'FLAG' as TaskType,
  mode: 'TEXT' as SubmissionMode,
  textRequired: true,
  textMinLength: 1,
  textMaxLength: 1_000_000,
  filesRequired: true,
  filesMinCount: 1,
  filesMaxCount: 5,
  filesMaxSizeBytes: 25 * 1024 * 1024,
  filesAllowedContentTypes: 'application/pdf,text/plain',
  acceptedAnswers: '',
  deploymentTemplateUuid: '',
  templateVersion: 1,
  parametersJson: '',
  secretParametersJson: '',
})
const availableTypes = computed(() => {
  const allowed = props.allowedTaskTypes?.length ? props.allowedTaskTypes : nativeTaskTypes
  return nativeTaskTypes.filter((type) => allowed.includes(type))
})
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.task))
const approvedTemplates = ref<ApprovedInstanceTemplate[]>([])

watch(
  () => [props.open, props.task] as const,
  ([open, task]) => {
    if (!open) return
    form.title = task?.title ?? ''
    form.category = task?.category ?? ''
    form.description = task?.description ?? ''
    form.points = task?.points ?? 100
    form.visible = task?.visible ?? true
    form.type = task?.type ?? availableTypes.value[0] ?? 'FLAG'
    form.mode = task?.submission.mode ?? 'TEXT'
    form.textRequired = task?.submission.text?.required ?? true
    form.textMinLength = task?.submission.text?.minLength ?? 1
    form.textMaxLength = task?.submission.text?.maxLength ?? 1_000_000
    form.filesRequired = task?.submission.files?.required ?? true
    form.filesMinCount = task?.submission.files?.minCount ?? 1
    form.filesMaxCount = task?.submission.files?.maxCount ?? 5
    form.filesMaxSizeBytes = task?.submission.files?.maxSizeBytes ?? 25 * 1024 * 1024
    form.filesAllowedContentTypes = task?.submission.files?.allowedContentTypes.join(', ') ?? 'application/pdf,text/plain'
    form.acceptedAnswers = ''
    form.deploymentTemplateUuid = task?.instanceConfig?.deploymentTemplateUuid ?? ''
    form.templateVersion = task?.instanceConfig?.templateVersion ?? 1
    form.parametersJson = task?.instanceConfig?.parameters ? JSON.stringify(task.instanceConfig.parameters) : ''
    form.secretParametersJson = ''
    problems.value = {}
    if (form.type === 'INSTANCE') void loadApprovedTemplates()
    error.value = null
  },
  { immediate: true },
)

function validatePolicy(): string | undefined {
  if (form.type === 'FLAG') return undefined
  if (form.mode === 'TEXT' || form.mode === 'TEXT_AND_FILES' || form.mode === 'TEXT_OR_FILES') {
    if (form.textMinLength < 0 || form.textMaxLength < 1 || form.textMaxLength > 1_000_000) {
      return 'Text bounds must be within 0..1000000 and maxLength must be at least 1.'
    }
    if (form.textMaxLength < form.textMinLength) return 'Text maxLength must be >= minLength.'
  }
  if (form.mode === 'FILES' || form.mode === 'TEXT_AND_FILES' || form.mode === 'TEXT_OR_FILES') {
    if (form.filesMinCount < 0 || form.filesMaxCount < 1 || form.filesMaxCount < form.filesMinCount) {
      return 'File count bounds must be valid (maxCount >= minCount >= 0).'
    }
    if (form.filesMaxSizeBytes < 1 || form.filesMaxSizeBytes > 1_073_741_824) {
      return 'File size cap must be 1..1073741824 bytes.'
    }
    if (parseContentTypes().length === 0) return 'Add at least one allowed content type.'
  }
  return undefined
}

function parseContentTypes(): string[] {
  return form.filesAllowedContentTypes.split(',').map((value) => value.trim()).filter(Boolean)
}

async function loadApprovedTemplates() {
  if (isEdit.value) return
  try {
    approvedTemplates.value = (await instancesApi.listApprovedTemplates(props.boardUuid, props.moduleUuid)).items
  } catch {
    approvedTemplates.value = []
  }
}

watch(
  () => form.type,
  (type) => {
    if (props.open && type === 'INSTANCE') void loadApprovedTemplates()
  },
)

function selectApprovedTemplate(value: unknown) {
  if (value === null || value === undefined) return
  const selected = String(value)
  const template = approvedTemplates.value.find(
    (item) => `${item.templateUuid}:${item.version}` === selected,
  )
  if (!template) return
  form.deploymentTemplateUuid = template.templateUuid
  form.templateVersion = template.version
  form.parametersJson = template.parametersPolicy
    ? JSON.stringify(template.parametersPolicy)
    : ''
}

function parseStringMap(raw: string): { value?: Record<string, string>; problem?: string } {
  if (!raw.trim()) return {}
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return { problem: 'Must be a JSON object of string values.' }
    }
    const result: Record<string, string> = {}
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value !== 'string') return { problem: 'Every parameter value must be a string.' }
      result[key] = value
    }
    return { value: result }
  } catch {
    return { problem: 'Invalid JSON.' }
  }
}

async function submit() {
  error.value = null
  const parameters = parseStringMap(form.parametersJson)
  const secretParameters = parseStringMap(form.secretParametersJson)
  let instanceProblem: string | undefined
  if (form.type === 'INSTANCE') {
    if (isEdit.value) {
      instanceProblem = undefined
    } else if (!isUuid(form.deploymentTemplateUuid)) {
      instanceProblem = 'Enter an approved deployment template UUID.'
    } else if (!Number.isInteger(form.templateVersion) || form.templateVersion < 1) {
      instanceProblem = 'Template version must be a positive integer.'
    }
  }
  if (!instanceProblem) instanceProblem = parameters.problem || secretParameters.problem
  const answers = form.type === 'FLAG'
    ? (form.acceptedAnswers.trim()
        ? parseAcceptedAnswers(form.acceptedAnswers)
        : { answers: [], problem: isEdit.value ? undefined : 'Add at least one accepted answer for a FLAG task.' })
    : { answers: [], problem: undefined }

  problems.value = {
    title: validateTaskTitle(form.title.trim()),
    category: validateCategory(form.category.trim()),
    points: validatePoints(form.points),
    answers: answers.problem,
    policy: validatePolicy(),
    instance: instanceProblem,
  }
  if (Object.values(problems.value).some(Boolean)) return

  pending.value = true
  try {
    const policy = buildSubmissionPolicy({
      type: form.type,
      mode: form.type === 'FLAG' ? 'TEXT' : form.mode,
      textRequired: form.textRequired,
      textMinLength: form.textMinLength,
      textMaxLength: form.textMaxLength,
      filesRequired: form.filesRequired,
      filesMinCount: form.filesMinCount,
      filesMaxCount: form.filesMaxCount,
      filesMaxSizeBytes: form.filesMaxSizeBytes,
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
    const instance: TaskInstanceConfig = form.type === 'INSTANCE'
      ? (props.task
          ? { enabled: true }
          : {
              enabled: true,
              deploymentTemplateUuid: form.deploymentTemplateUuid.trim(),
              templateVersion: form.templateVersion,
              ...(parameters.value ? { parameters: parameters.value } : {}),
              ...(secretParameters.value ? { secretParameters: secretParameters.value } : {}),
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
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit task' : 'Create task' }}</DialogTitle>
        <DialogDescription>
          FLAG uses TEXT/AUTOMATIC with write-only accepted answers. MANUAL tasks persist their text/file policy and
          award points only after scoped review. Instance tasks need an approved deployment binding; no fake instance is created here.
        </DialogDescription>
      </DialogHeader>

      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not save task" />
        <FormField label="Title" for-id="task-title" :error="problems.title">
          <Input id="task-title" v-model="form.title" />
        </FormField>
        <FormField label="Category" for-id="task-category" :error="problems.category">
          <Input id="task-category" v-model="form.category" />
        </FormField>
        <FormField label="Description" for-id="task-description">
          <Textarea id="task-description" v-model="form.description" rows="4" />
        </FormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField label="Points" for-id="task-points" :error="problems.points">
            <Input id="task-points" v-model.number="form.points" type="number" min="0" step="1" />
          </FormField>
          <FormField label="Task type" for-id="task-type" hint="Immutable after creation.">
            <Select v-model="form.type" :disabled="isEdit">
              <SelectTrigger id="task-type" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="type in availableTypes" :key="type" :value="type">{{ type }}</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField label="Submission mode" for-id="task-mode">
            <Select v-model="form.mode" :disabled="form.type === 'FLAG'">
              <SelectTrigger id="task-mode" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="TEXT">TEXT</SelectItem>
                <SelectItem value="FILES">FILES</SelectItem>
                <SelectItem value="TEXT_AND_FILES">TEXT_AND_FILES</SelectItem>
                <SelectItem value="TEXT_OR_FILES">TEXT_OR_FILES</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
          <FormField label="Checker" for-id="task-checker">
            <Input id="task-checker" :model-value="form.type === 'FLAG' ? 'AUTOMATIC' : 'MANUAL'" disabled />
          </FormField>
        </div>
        <div v-if="form.type !== 'FLAG'" class="space-y-3 border border-border p-3">
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Text policy</p>
          <label v-if="form.mode !== 'FILES'" class="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox v-model="form.textRequired" />
            Text required
          </label>
          <div v-if="form.mode !== 'FILES'" class="grid gap-3 sm:grid-cols-2">
            <FormField label="Text min length" for-id="task-text-min">
              <Input id="task-text-min" v-model.number="form.textMinLength" type="number" min="0" max="1000000" />
            </FormField>
            <FormField label="Text max length" for-id="task-text-max">
              <Input id="task-text-max" v-model.number="form.textMaxLength" type="number" min="1" max="1000000" />
            </FormField>
          </div>
          <p v-if="form.mode !== 'TEXT'" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">File policy</p>
          <template v-if="form.mode !== 'TEXT'">
            <label class="flex min-h-11 items-center gap-2 text-sm">
              <Checkbox v-model="form.filesRequired" />
              Files required
            </label>
            <div class="grid gap-3 sm:grid-cols-2">
              <FormField label="Min files" for-id="task-files-min">
                <Input id="task-files-min" v-model.number="form.filesMinCount" type="number" min="0" max="100" />
              </FormField>
              <FormField label="Max files" for-id="task-files-max">
                <Input id="task-files-max" v-model.number="form.filesMaxCount" type="number" min="1" max="100" />
              </FormField>
            </div>
            <FormField label="Max size bytes" for-id="task-files-size">
              <Input id="task-files-size" v-model.number="form.filesMaxSizeBytes" type="number" min="1" max="1073741824" />
            </FormField>
            <FormField label="Allowed content types" for-id="task-files-types" hint="Comma-separated MIME types.">
              <Input id="task-files-types" v-model="form.filesAllowedContentTypes" />
            </FormField>
          </template>
          <p v-if="problems.policy" class="text-xs text-love">{{ problems.policy }}</p>
        </div>
        <label class="flex min-h-11 items-center gap-2 text-sm">
          <Checkbox v-model="form.visible" />
          Visible to participants
        </label>
        <FormField
          v-if="form.type === 'FLAG'"
          label="Accepted answers"
          for-id="task-answers"
          :error="problems.answers"
          :hint="isEdit
            ? `Write-only. Existing: ${task?.answerConfiguration.configured ? task.answerConfiguration.acceptedAnswersCount : 0}. Leave blank to keep them; entering answers replaces them.`
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
        <div v-if="form.type === 'INSTANCE'" class="space-y-3 border border-border p-3">
          <p class="text-xs text-gold">
            INSTANCE uses an approved immutable deployment-template version and allowlist. The backend rejects
            unapproved versions; no raw provider/image/driver option is accepted. The binding is immutable after create.
          </p>
          <FormField v-if="approvedTemplates.length > 0 && !isEdit" label="Approved version" for-id="task-instance-approved">
            <Select
              :model-value="`${form.deploymentTemplateUuid}:${form.templateVersion}`"
              @update:model-value="selectApprovedTemplate"
            >
              <SelectTrigger id="task-instance-approved" class="w-full"><SelectValue placeholder="Select an allowlisted version" /></SelectTrigger>
              <SelectContent>
                <SelectItem
                  v-for="template in approvedTemplates"
                  :key="`${template.templateUuid}:${template.version}`"
                  :value="`${template.templateUuid}:${template.version}`"
                >
                  {{ template.name }} v{{ template.version }} ({{ template.slug }})
                </SelectItem>
              </SelectContent>
            </Select>
          </FormField>
          <FormField label="Deployment template UUID" for-id="task-instance-template" :error="problems.instance">
            <Input id="task-instance-template" v-model="form.deploymentTemplateUuid" :disabled="isEdit" placeholder="00000000-0000-4000-8000-000000000000" />
          </FormField>
          <FormField label="Template version" for-id="task-instance-version">
            <Input id="task-instance-version" v-model.number="form.templateVersion" type="number" min="1" :disabled="isEdit" />
          </FormField>
          <FormField label="Parameters JSON" for-id="task-instance-parameters" hint="Non-secret values only.">
            <Textarea id="task-instance-parameters" v-model="form.parametersJson" rows="3" :disabled="isEdit" placeholder='{"region":"local"}' />
          </FormField>
          <FormField label="Secret parameters JSON" for-id="task-instance-secrets" hint="Write-only; sealed by the trusted instances binder.">
            <Textarea id="task-instance-secrets" v-model="form.secretParametersJson" rows="3" :disabled="isEdit" placeholder='{"apiToken":"..."}' autocomplete="off" spellcheck="false" />
          </FormField>
          <p v-if="isEdit" class="text-xs text-muted-foreground">The existing instance binding is immutable. Create a new task to change it.</p>
        </div>
        <p v-else class="text-xs text-muted-foreground">
          MANUAL submissions enter PENDING_REVIEW with no score; a permitted reviewer accepts or rejects. Scores stay server-authoritative.
        </p>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : isEdit ? 'Save task' : 'Create task' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>