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
import FormField from '@/components/layout/FormField.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { parseDateWindow, validateModuleName } from '@/features/admin/validation'
import { formatDateTimeLocal } from '@/shared/lib/format'
import { isApiError } from '@/shared/api/errors'
import type { AdminModule, Audience, ModuleType, TaskType } from '@/shared/types/api'

const props = defineProps<{
  open: boolean
  boardUuid: string
  module?: AdminModule | null
  audiences?: Audience[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const form = reactive({
  name: '',
  description: '',
  moduleType: 'TASKS' as ModuleType,
  allowedTaskTypes: ['FLAG'] as TaskType[],
  audienceUuids: [] as string[],
  enabled: true,
  order: 0,
  startsAt: '',
  endsAt: '',
  instanceMaxConcurrent: 1,
  instanceDefaultTtlSeconds: 3600,
  instanceMaxTtlSeconds: 14400,
  instanceResetEnabled: true,
})

const taskTypeOptions: Record<string, TaskType[]> = {
  TASKS: ['FLAG', 'MANUAL', 'INSTANCE'],
  ATTACK_DEFENSE: ['EXPLOIT', 'PATCH'],
  PENTEST: ['PENTEST_REPORT'],
  EXTENSION: [],
}

watch(
  () => form.moduleType,
  (type) => {
    if (props.module) return
    form.allowedTaskTypes = type === 'TASKS'
      ? ['FLAG']
      : type === 'ATTACK_DEFENSE'
        ? ['EXPLOIT', 'PATCH']
        : type === 'PENTEST'
          ? ['PENTEST_REPORT']
          : []
  },
)
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.module))

watch(
  () => [props.open, props.module] as const,
  ([open, module]) => {
    if (!open) return
    form.name = module?.name ?? ''
    form.description = module?.description ?? ''
    form.moduleType = module?.type ?? 'TASKS'
    form.allowedTaskTypes = [...(module?.allowedTaskTypes ?? ['FLAG'])]
    form.audienceUuids = [...(module?.audienceUuids ?? [])]
    form.enabled = module?.enabled ?? true
    form.order = module?.order ?? 0
    form.instanceMaxConcurrent = module?.taskInstanceSettings?.maxConcurrentInstancesPerSubject ?? 1
    form.instanceDefaultTtlSeconds = module?.taskInstanceSettings?.defaultTtlSeconds ?? 3600
    form.instanceMaxTtlSeconds = module?.taskInstanceSettings?.maxTtlSeconds ?? 14400
    form.instanceResetEnabled = module?.taskInstanceSettings?.resetEnabled ?? true
    form.startsAt = module?.startsAt ? formatDateTimeLocal(new Date(module.startsAt)) : ''
    form.endsAt = module?.endsAt ? formatDateTimeLocal(new Date(module.endsAt)) : ''
    problems.value = {}
    error.value = null
  },
  { immediate: true },
)

function toggleTaskType(type: TaskType) {
  form.allowedTaskTypes = form.allowedTaskTypes.includes(type)
    ? form.allowedTaskTypes.filter((value) => value !== type)
    : [...form.allowedTaskTypes, type]
}

function toggleAudience(uuid: string) {
  form.audienceUuids = form.audienceUuids.includes(uuid)
    ? form.audienceUuids.filter((value) => value !== uuid)
    : [...form.audienceUuids, uuid]
}

function validateModuleSettings(): string | undefined {
  if (form.allowedTaskTypes.length === 0) return 'Choose at least one allowed task type.'
  if (form.allowedTaskTypes.includes('INSTANCE')) {
    if (form.instanceMaxTtlSeconds < form.instanceDefaultTtlSeconds) {
      return 'Maximum TTL must be at least the default TTL.'
    }
    if (form.instanceDefaultTtlSeconds < 60 || form.instanceMaxTtlSeconds > 604800) {
      return 'Instance TTL must be between 60 and 604800 seconds.'
    }
  }
  return undefined
}

async function submit() {
  error.value = null
  const window = parseDateWindow(form.startsAt, form.endsAt)
  problems.value = {
    name: validateModuleName(form.name.trim()),
    window: window.problem ?? validateModuleSettings(),
    order: Number.isInteger(form.order) && form.order >= 0 ? undefined : 'Order must be a non-negative whole number.',
  }
  if (Object.values(problems.value).some(Boolean)) return

  pending.value = true
  try {
    const taskInstanceSettings = form.allowedTaskTypes.includes('INSTANCE')
      ? {
          maxConcurrentInstancesPerSubject: form.instanceMaxConcurrent,
          defaultTtlSeconds: form.instanceDefaultTtlSeconds,
          maxTtlSeconds: form.instanceMaxTtlSeconds,
          resetEnabled: form.instanceResetEnabled,
        }
      : null
    const body = {
      name: form.name.trim(),
      description: form.description,
      allowedTaskTypes: form.allowedTaskTypes,
      audienceUuids: form.audienceUuids,
      enabled: form.enabled,
      order: form.order,
      startsAt: window.startsAt,
      endsAt: window.endsAt,
      taskInstanceSettings,
    }
    if (props.module) {
      await boardsApi.updateModule(props.boardUuid, props.module.uuid, body)
    } else {
      await boardsApi.createModule(props.boardUuid, {
        ...body,
        type: form.moduleType,
      })
    }
    emit('saved')
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = { name: caught.fieldIssue('name') }
    }
    error.value = caught
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit module' : 'Create module' }}</DialogTitle>
        <DialogDescription>
          Native module types: TASKS (FLAG/MANUAL/INSTANCE), ATTACK_DEFENSE (EXPLOIT/PATCH), PENTEST (report).
          EXTENSION modules are shown only when backend plugin capability metadata exists; no arbitrary type strings are accepted.
        </DialogDescription>
      </DialogHeader>

      <form class="space-y-4" novalidate @submit.prevent="submit">
        <ErrorAlert v-if="error" :error="error" title="Could not save module" />
        <FormField label="Name" for-id="module-name" :error="problems.name">
          <Input id="module-name" v-model="form.name" />
        </FormField>
        <FormField label="Description" for-id="module-description">
          <Textarea id="module-description" v-model="form.description" rows="3" />
        </FormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField label="Module type" for-id="module-type" hint="Immutable after creation.">
            <Select v-model="form.moduleType" :disabled="isEdit">
              <SelectTrigger id="module-type" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="TASKS">TASKS</SelectItem>
                <SelectItem value="ATTACK_DEFENSE">ATTACK_DEFENSE</SelectItem>
                <SelectItem value="PENTEST">PENTEST</SelectItem>
              </SelectContent>
            </Select>
          </FormField>
          <FormField label="Allowed task types" for-id="module-task-types" :error="problems.window">
            <div class="space-y-1">
              <label v-for="type in taskTypeOptions[form.moduleType] ?? []" :key="type" class="flex min-h-9 items-center gap-2 text-sm">
                <Checkbox
                  :model-value="form.allowedTaskTypes.includes(type)"
                  @update:model-value="toggleTaskType(type)"
                />
                {{ type }}
              </label>
              <p v-if="(taskTypeOptions[form.moduleType] ?? []).length === 0" class="text-xs text-muted-foreground">
                EXTENSION requires installed backend plugin metadata; it is not offered as a free-form type.
              </p>
            </div>
          </FormField>
        </div>
        <div v-if="audiences && audiences.length > 0" class="space-y-2 border border-border p-3">
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Audience gating</p>
          <p class="text-xs text-muted-foreground">Empty selection means every joined participant/subject can see the module.</p>
          <label v-for="audience in audiences" :key="audience.uuid" class="flex min-h-9 items-center gap-2 text-sm">
            <Checkbox
              :model-value="form.audienceUuids.includes(audience.uuid)"
              @update:model-value="toggleAudience(audience.uuid)"
            />
            {{ audience.name }}
          </label>
        </div>
        <div v-if="form.allowedTaskTypes.includes('INSTANCE')" class="grid gap-3 border border-border p-3 sm:grid-cols-2">
          <p class="sm:col-span-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Instance settings</p>
          <FormField label="Max concurrent per subject" for-id="module-instance-concurrent">
            <Input id="module-instance-concurrent" v-model.number="form.instanceMaxConcurrent" type="number" min="1" max="100" />
          </FormField>
          <FormField label="Default TTL seconds" for-id="module-instance-default-ttl">
            <Input id="module-instance-default-ttl" v-model.number="form.instanceDefaultTtlSeconds" type="number" min="60" max="604800" />
          </FormField>
          <FormField label="Max TTL seconds" for-id="module-instance-max-ttl">
            <Input id="module-instance-max-ttl" v-model.number="form.instanceMaxTtlSeconds" type="number" min="60" max="604800" />
          </FormField>
          <label class="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox v-model="form.instanceResetEnabled" />
            Allow reset
          </label>
        </div>
        <FormField label="Order" for-id="module-order" :error="problems.order">
          <Input id="module-order" v-model.number="form.order" type="number" min="0" step="1" />
        </FormField>
        <label class="flex min-h-11 items-center gap-2 text-sm">
          <Checkbox v-model="form.enabled" />
          Enabled
        </label>
        <FormField label="Starts at (optional)" for-id="module-starts">
          <Input id="module-starts" v-model="form.startsAt" type="datetime-local" />
        </FormField>
        <FormField label="Ends at (optional)" for-id="module-ends" :error="problems.window">
          <Input id="module-ends" v-model="form.endsAt" type="datetime-local" />
        </FormField>
        <DialogFooter>
          <Button type="button" variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : isEdit ? 'Save module' : 'Create module' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>