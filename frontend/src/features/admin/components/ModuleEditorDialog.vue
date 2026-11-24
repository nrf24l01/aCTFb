<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
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
import { parseDateWindow, validateModuleName } from '@/features/admin/validation'
import { formatDateTimeLocal } from '@/shared/lib/format'
import { renderMarkdown } from '@/shared/lib/markdown'
import { isApiError } from '@/shared/api/errors'
import { useDiscardGuard } from '@/features/admin/useDiscardGuard'
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

type FormatKey = 'FLAG' | 'MANUAL' | 'INSTANCE' | 'PENTEST' | 'ATTACK_DEFENSE' | 'EXTENSION'

// Only formats backed by a native workflow are offered for a NEW module. An
// installed plugin format is not a free-form choice: it needs registered
// capability metadata, so an existing EXTENSION module is edited in place with
// its format read-only instead of being re-mapped to a native type.
const formats: Array<{ key: FormatKey; label: string; type: ModuleType; allowed: TaskType[] }> = [
  { key: 'FLAG', label: 'Flag challenges', type: 'TASKS', allowed: ['FLAG'] },
  { key: 'MANUAL', label: 'Manual review', type: 'TASKS', allowed: ['MANUAL'] },
  { key: 'INSTANCE', label: 'Instance tasks', type: 'TASKS', allowed: ['INSTANCE'] },
  { key: 'PENTEST', label: 'Pentest', type: 'PENTEST', allowed: ['PENTEST_REPORT'] },
  { key: 'ATTACK_DEFENSE', label: 'Attack & Defense', type: 'ATTACK_DEFENSE', allowed: ['EXPLOIT', 'PATCH'] },
]

function formatForModule(module: AdminModule | null | undefined): FormatKey {
  if (!module) return 'FLAG'
  if (module.type === 'EXTENSION') return 'EXTENSION'
  if (module.type === 'PENTEST') return 'PENTEST'
  if (module.type === 'ATTACK_DEFENSE') return 'ATTACK_DEFENSE'
  const single = module.allowedTaskTypes.length === 1 ? module.allowedTaskTypes[0] : undefined
  if (single === 'MANUAL') return 'MANUAL'
  if (single === 'INSTANCE') return 'INSTANCE'
  return 'FLAG'
}

const form = reactive({
  name: '',
  description: '',
  format: 'FLAG' as FormatKey,
  legacyTaskType: '' as TaskType | '',
  audienceMode: 'EVERYONE' as 'EVERYONE' | 'SELECTED',
  audienceUuids: [] as string[],
  enabled: true,
  order: 0,
  startsAt: '',
  endsAt: '',
  instanceMaxConcurrent: 1,
  instanceDefaultTtlHours: 1,
  instanceMaxTtlHours: 4,
  instanceResetEnabled: true,
  instanceExtensionEnabled: true,
  instanceExtensionMinutes: 30,
  instanceExtensionMaxCount: 3,
})
const {
  discardOpen: discardConfirmOpen,
  reset: resetGuard,
  attemptClose,
  keepEditing,
  discardAndClose,
} = useDiscardGuard({
  snapshot: () => ({ ...form }),
  close: () => emit('update:open', false),
})
const problems = ref<Record<string, string | undefined>>({})
const error = ref<unknown>(null)
const pending = ref(false)
const isEdit = computed(() => Boolean(props.module))
const isLegacyMixed = computed(() => Boolean(props.module?.legacyMixed))
const selectedFormat = computed(() => formats.find((entry) => entry.key === form.format) ?? formats[0])
const isExtension = computed(() => form.format === 'EXTENSION')
// Read-only identity for an installed plugin module. No new extension selector
// is invented: the value is the actual stored type, key and task-type list.
const extensionLabel = computed(
  () => `Extension: ${props.module?.extensionKey || 'unknown'} (unavailable)`,
)
const isInstance = computed(() => form.format === 'INSTANCE')
const preview = computed(() => renderMarkdown(form.description))

watch(
  () => [props.open, props.module] as const,
  ([open, module]) => {
    if (!open) return
    form.name = module?.name ?? ''
    form.description = module?.description ?? ''
    form.format = formatForModule(module)
    // Never preselect a type: an unrelated name/enabled save must not silently
    // resolve a legacy mixed module. Only an explicit choice narrows it.
    form.legacyTaskType = ''
    form.audienceMode = module && module.audienceUuids.length > 0 ? 'SELECTED' : 'EVERYONE'
    form.audienceUuids = [...(module?.audienceUuids ?? [])]
    form.enabled = module?.enabled ?? true
    form.order = module?.order ?? 0
    form.instanceMaxConcurrent = module?.taskInstanceSettings?.maxConcurrentInstancesPerSubject ?? 1
    form.instanceDefaultTtlHours = (module?.taskInstanceSettings?.defaultTtlSeconds ?? 3600) / 3600
    form.instanceMaxTtlHours = (module?.taskInstanceSettings?.maxTtlSeconds ?? 14400) / 3600
    form.instanceResetEnabled = module?.taskInstanceSettings?.resetEnabled ?? true
    form.instanceExtensionEnabled = module?.taskInstanceSettings?.extension?.enabled ?? true
    form.instanceExtensionMinutes = (module?.taskInstanceSettings?.extension?.seconds ?? 1800) / 60
    form.instanceExtensionMaxCount = module?.taskInstanceSettings?.extension?.maxCount ?? 3
    form.startsAt = module?.startsAt ? formatDateTimeLocal(new Date(module.startsAt)) : ''
    form.endsAt = module?.endsAt ? formatDateTimeLocal(new Date(module.endsAt)) : ''
    problems.value = {}
    error.value = null
    resetGuard()
  },
  { immediate: true },
)

function toggleAudience(uuid: string) {
  form.audienceUuids = form.audienceUuids.includes(uuid)
    ? form.audienceUuids.filter((value) => value !== uuid)
    : [...form.audienceUuids, uuid]
}

function validateSettings(): string | undefined {
  if (form.audienceMode === 'SELECTED' && form.audienceUuids.length === 0) {
    return 'Choose at least one audience, or select Everyone with board access.'
  }
  if (isLegacyMixed.value && !form.legacyTaskType) {
    return 'Choose one task type to resolve this legacy mixed module.'
  }
  if (isInstance.value) {
    if (form.instanceMaxTtlHours < form.instanceDefaultTtlHours) {
      return 'Maximum TTL must be at least the default TTL.'
    }
    if (form.instanceDefaultTtlHours < 1 / 60 || form.instanceMaxTtlHours > 168) {
      return 'Instance TTL must be between 1 minute and 168 hours.'
    }
    if (form.instanceExtensionMinutes < 1 || form.instanceExtensionMinutes > 1440) {
      return 'One extension step must be between 1 minute and 24 hours.'
    }
    if (
      !Number.isInteger(form.instanceExtensionMaxCount) ||
      form.instanceExtensionMaxCount < 0 ||
      form.instanceExtensionMaxCount > 100
    ) {
      return 'Extensions per participant must be a whole number between 0 and 100.'
    }
  }
  return undefined
}

function allowedTaskTypes(): TaskType[] {
  if (isExtension.value) {
    // Preserve exactly what the installed provider declared. Changing it would
    // be a format change, which is not offered here.
    const existing = props.module?.allowedTaskTypes ?? []
    return existing.length > 0 ? [...existing] : ['EXTENSION']
  }
  if (isLegacyMixed.value) {
    return form.legacyTaskType ? [form.legacyTaskType] : (props.module?.allowedTaskTypes ?? [])
  }
  return selectedFormat.value.allowed
}

async function submit() {
  error.value = null
  const window = parseDateWindow(form.startsAt, form.endsAt)
  problems.value = {
    name: validateModuleName(form.name.trim()),
    window: window.problem ?? validateSettings(),
    order: Number.isInteger(form.order) && form.order >= 0 ? undefined : 'Order must be a non-negative whole number.',
  }
  if (Object.values(problems.value).some(Boolean)) return

  pending.value = true
  try {
    const taskInstanceSettings = isInstance.value || props.module?.taskInstanceSettings
      ? {
          maxConcurrentInstancesPerSubject: form.instanceMaxConcurrent,
          defaultTtlSeconds: Math.round(form.instanceDefaultTtlHours * 3600),
          maxTtlSeconds: Math.round(form.instanceMaxTtlHours * 3600),
          resetEnabled: form.instanceResetEnabled,
          extension: {
            enabled: form.instanceExtensionEnabled,
            seconds: Math.round(form.instanceExtensionMinutes * 60),
            maxCount: form.instanceExtensionMaxCount,
          },
        }
      : null
    const body = {
      name: form.name.trim(),
      description: form.description,
      allowedTaskTypes: allowedTaskTypes(),
      audienceUuids: form.audienceMode === 'SELECTED' ? form.audienceUuids : [],
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
        type: selectedFormat.value.type,
      })
    }
    emit('saved')
    emit('update:open', false)
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = {
        name: caught.fieldIssue('name'),
        window: caught.fieldIssue('allowedTaskTypes') ?? caught.fieldIssue('audienceUuids'),
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
    <DialogContent class="flex max-h-[90dvh] flex-col rounded-none border-border bg-black max-md:h-dvh max-md:max-h-dvh max-md:max-w-none sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{{ isEdit ? 'Edit module' : 'Create module' }}</DialogTitle>
        <DialogDescription>
          A module has one format. Tasks inside it inherit that format; you do not choose a task type again.
        </DialogDescription>
      </DialogHeader>

      <form class="flex min-h-0 flex-1 flex-col" novalidate @submit.prevent="submit">
        <div class="min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
          <ErrorAlert v-if="error" :error="error" title="Could not save module" />

          <FormField label="Name" for-id="module-name" :error="problems.name">
            <Input id="module-name" v-model="form.name" />
          </FormField>

          <div class="space-y-2">
            <p class="text-sm font-medium">Short description</p>
            <Tabs default-value="write">
              <TabsList variant="line">
                <TabsTrigger value="write">Write</TabsTrigger>
                <TabsTrigger value="preview">Preview</TabsTrigger>
              </TabsList>
              <TabsContent value="write">
                <Textarea id="module-description" v-model="form.description" rows="3" />
              </TabsContent>
              <TabsContent value="preview">
                <div class="min-h-20 border border-border px-3 py-2">
                  <!-- Sanitised by renderMarkdown. -->
                  <article class="space-y-2 text-sm" v-html="preview" />
                  <p v-if="!form.description.trim()" class="text-sm text-muted-foreground">Nothing to preview yet.</p>
                </div>
              </TabsContent>
            </Tabs>
          </div>

          <FormField v-if="isExtension" label="Format" for-id="module-format" hint="Installed plugin format. It cannot be changed here.">
            <p id="module-format" class="border border-border px-2 py-1.5 text-sm text-muted-foreground">
              {{ extensionLabel }}
            </p>
          </FormField>
          <FormField v-else label="Format" for-id="module-format" :error="problems.window" hint="Immutable after creation.">
            <select
              id="module-format"
              v-model="form.format"
              :disabled="isEdit"
              class="h-9 w-full border border-border bg-black px-2 text-sm disabled:opacity-60"
            >
              <option v-for="entry in formats" :key="entry.key" :value="entry.key">{{ entry.label }}</option>
            </select>
          </FormField>

          <div v-if="isLegacyMixed" class="space-y-2 border border-gold/60 p-3">
            <p class="text-xs text-gold">
              This existing module mixes {{ props.module?.allowedTaskTypes.join(', ') }} tasks. Its data and history are
              unchanged. Choose one task type to resolve it, or leave it as-is. Existing tasks of other types block the change.
            </p>
            <FormField label="Resolve to one task type" for-id="module-legacy-type">
              <select id="module-legacy-type" v-model="form.legacyTaskType" class="h-9 w-full border border-border bg-black px-2 text-sm">
                <option value="">Keep current mixed types</option>
                <option v-for="type in props.module?.allowedTaskTypes ?? []" :key="type" :value="type">{{ type }}</option>
              </select>
            </FormField>
          </div>

          <fieldset class="space-y-2 border border-border p-3">
            <legend class="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Audience</legend>
            <label class="flex min-h-9 items-center gap-2 text-sm">
              <input v-model="form.audienceMode" type="radio" value="EVERYONE" />
              Everyone with board access
            </label>
            <label class="flex min-h-9 items-center gap-2 text-sm">
              <input v-model="form.audienceMode" type="radio" value="SELECTED" />
              Selected board audiences
            </label>
            <div v-if="form.audienceMode === 'SELECTED'" class="space-y-1 border-t border-border pt-2">
              <label v-for="audience in audiences ?? []" :key="audience.uuid" class="flex min-h-9 items-center gap-2 text-sm">
                <Checkbox
                  :model-value="form.audienceUuids.includes(audience.uuid)"
                  @update:model-value="toggleAudience(audience.uuid)"
                />
                {{ audience.name }}
              </label>
              <p v-if="(audiences ?? []).length === 0" class="text-xs text-muted-foreground">
                This board has no audiences yet. Create one on the board page first.
              </p>
            </div>
          </fieldset>

          <div class="grid gap-3 sm:grid-cols-2">
            <FormField label="Starts at (optional)" for-id="module-starts" hint="Defaults to the board schedule.">
              <Input id="module-starts" v-model="form.startsAt" type="datetime-local" />
            </FormField>
            <FormField label="Ends at (optional)" for-id="module-ends" :error="problems.window">
              <Input id="module-ends" v-model="form.endsAt" type="datetime-local" />
            </FormField>
          </div>

          <details class="border border-border p-3">
            <summary class="cursor-pointer text-sm font-medium">Advanced</summary>
            <div class="mt-3 space-y-3">
              <FormField label="Order" for-id="module-order" :error="problems.order">
                <Input id="module-order" v-model.number="form.order" type="number" min="0" step="1" />
              </FormField>
              <label class="flex min-h-11 items-center gap-2 text-sm">
                <Checkbox v-model="form.enabled" />
                Enabled
              </label>
              <div v-if="isInstance" class="space-y-3 border-t border-border pt-3">
                <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Instance limits</p>
                <FormField label="Max concurrent per participant" for-id="module-instance-concurrent">
                  <Input id="module-instance-concurrent" v-model.number="form.instanceMaxConcurrent" type="number" min="1" max="100" />
                </FormField>
                <div class="grid gap-3 sm:grid-cols-2">
                  <FormField label="Default lifetime (hours)" for-id="module-instance-default-ttl">
                    <Input id="module-instance-default-ttl" v-model.number="form.instanceDefaultTtlHours" type="number" min="0.02" max="168" step="0.5" />
                  </FormField>
                  <FormField label="Maximum lifetime (hours)" for-id="module-instance-max-ttl">
                    <Input id="module-instance-max-ttl" v-model.number="form.instanceMaxTtlHours" type="number" min="0.02" max="168" step="0.5" />
                  </FormField>
                </div>
                <label class="flex min-h-11 items-center gap-2 text-sm">
                  <Checkbox v-model="form.instanceResetEnabled" />
                  Allow participants to reset
                </label>
                <div class="space-y-3 border-t border-border pt-3">
                  <label class="flex min-h-11 items-center gap-2 text-sm">
                    <Checkbox v-model="form.instanceExtensionEnabled" />
                    Allow participants to extend
                  </label>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <FormField label="Extension step (minutes)" for-id="module-instance-extension-minutes">
                      <Input id="module-instance-extension-minutes" v-model.number="form.instanceExtensionMinutes" type="number" min="1" max="1440" step="1" />
                    </FormField>
                    <FormField label="Max extensions per participant" for-id="module-instance-extension-count">
                      <Input id="module-instance-extension-count" v-model.number="form.instanceExtensionMaxCount" type="number" min="0" max="100" step="1" />
                    </FormField>
                  </div>
                  <p class="text-xs text-muted-foreground">
                    Lifetime starts when the environment is requested; provisioning time counts. Tasks can
                    lower these limits but never raise them.
                  </p>
                </div>
              </div>
            </div>
          </details>
        </div>

        <DialogFooter class="mt-4 border-t border-border pt-4">
          <Button type="button" variant="ghost" :disabled="pending" @click="attemptClose()">Cancel</Button>
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : isEdit ? 'Save changes' : 'Create module' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>

  <ConfirmDialog
    :open="discardConfirmOpen"
    title="Discard module changes?"
    description="Your unsaved edits will be lost. This cannot be undone."
    confirm-label="Discard changes"
    destructive
    @update:open="(value) => { if (!value) keepEditing() }"
    @confirm="discardAndClose()"
  />
</template>
