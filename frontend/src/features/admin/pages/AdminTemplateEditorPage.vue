<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { templatesApi } from '@/features/templates/api'
import { integrationsApi } from '@/features/integrations/api'
import { isApiError } from '@/shared/api/errors'
import { fileToBase64 } from '@/shared/lib/base64'
import { validateRelativePath } from '@/features/templates/validation'
import { formatDateTime } from '@/shared/lib/format'
import type {
  DeploymentTemplate,
  DeploymentTemplateAllowlist,
  DeploymentTemplateDiff,
  DeploymentTemplateDraft,
  DeploymentTemplateFile,
  DeploymentTemplatePreview,
  DeploymentTemplateValidation,
  DeploymentTemplateVersion,
  IntegrationSummary,
} from '@/shared/types/api'

const route = useRoute()
const templateUuid = computed(() => String(route.params.templateUuid))

const template = ref<DeploymentTemplate | null>(null)
const draft = ref<DeploymentTemplateDraft | null>(null)
const yaml = ref('')
const files = ref<DeploymentTemplateFile[]>([])
const versions = ref<DeploymentTemplateVersion[]>([])
const allowlists = ref<DeploymentTemplateAllowlist[]>([])
const integrations = ref<IntegrationSummary[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const busy = ref(false)
const validation = ref<DeploymentTemplateValidation | null>(null)
const preview = ref<DeploymentTemplatePreview | null>(null)
const actionError = ref<unknown>(null)
const fromVersion = ref('')
const toVersion = ref('')
const diff = ref<DeploymentTemplateDiff | null>(null)
const publishOpen = ref(false)
const versionNote = ref('')
const allowlistVersion = ref('')
const allowlistKind = ref<'VM' | 'CONTAINER'>('CONTAINER')
const allowlistIntegration = ref('')
const allowlistBoard = ref('')
const allowlistModule = ref('')
const allowlistError = ref<unknown>(null)
const allowlistPending = ref(false)
// Allowlists bind a published version to a live integration target, so they
// only exist while runtime instances and integrations are enabled. The template
// catalog itself stays usable without them.
const allowlistUnavailable = ref(false)

const fileInputKey = ref(0)

async function load() {
  loading.value = true
  error.value = null
  allowlistUnavailable.value = false
  try {
    const [loadedTemplate, loadedDraft, versionPage] = await Promise.all([
      templatesApi.get(templateUuid.value),
      templatesApi.getDraft(templateUuid.value),
      templatesApi.listVersions(templateUuid.value, 100, 0),
    ])
    template.value = loadedTemplate
    draft.value = loadedDraft
    yaml.value = loadedDraft.manifestYaml
    files.value = [...loadedDraft.files]
    versions.value = versionPage.items
    if (versions.value.length > 1) {
      fromVersion.value = String(versions.value[1].version)
      toVersion.value = String(versions.value[0].version)
    }
    if (versions.value.length > 0 && !allowlistVersion.value) allowlistVersion.value = versions.value[0].uuid
    try {
      const [allowlistResponse, integrationPage] = await Promise.all([
        templatesApi.listAllowlists(templateUuid.value),
        integrationsApi.list(100, 0),
      ])
      allowlists.value = allowlistResponse.items
      integrations.value = integrationPage.items
    } catch (caught) {
      // Runtime-gated sub-resources: a 404/403 here means the deployment does
      // not expose them. That is an operator state, not a template error.
      if (isApiError(caught) && (caught.status === 404 || caught.status === 403 || caught.code === 'FEATURE_DISABLED')) {
        allowlistUnavailable.value = true
      } else {
        throw caught
      }
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function saveDraft(): Promise<boolean> {
  actionError.value = null
  const bad = files.value.map((file) => validateRelativePath(file.path)).find(Boolean)
  if (bad) {
    actionError.value = new Error(bad)
    return false
  }
  busy.value = true
  try {
    draft.value = await templatesApi.putDraft(templateUuid.value, {
      manifestYaml: yaml.value,
      files: files.value,
    })
    files.value = [...draft.value.files]
    yaml.value = draft.value.manifestYaml
    return true
  } catch (caught) {
    actionError.value = caught
    return false
  } finally {
    busy.value = false
  }
}

async function validate() {
  if (!(await saveDraft())) return
  busy.value = true
  try {
    validation.value = await templatesApi.validate(templateUuid.value)
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function runPreview() {
  if (!(await saveDraft())) return
  busy.value = true
  try {
    preview.value = await templatesApi.preview(templateUuid.value)
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function publish() {
  if (!(await saveDraft())) return
  busy.value = true
  try {
    await templatesApi.publish(templateUuid.value, versionNote.value.trim())
    publishOpen.value = false
    versionNote.value = ''
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function addFiles(event: Event) {
  const target = event.target as HTMLInputElement
  const selected = Array.from(target.files ?? [])
  target.value = ''
  fileInputKey.value += 1
  if (selected.length === 0) return
  actionError.value = null
  if (files.value.length + selected.length > 256) {
    actionError.value = new Error('At most 256 package files are allowed.')
    return
  }
  for (const file of selected) {
    const path = file.webkitRelativePath || file.name
    const problem = validateRelativePath(path)
    if (problem) {
      actionError.value = new Error(problem)
      return
    }
    files.value.push({ path, contentBase64: await fileToBase64(file) })
  }
}

function removeFile(index: number) {
  files.value.splice(index, 1)
}

async function loadDiff() {
  const from = Number(fromVersion.value)
  const to = Number(toVersion.value)
  if (!Number.isInteger(from) || !Number.isInteger(to)) return
  busy.value = true
  try {
    diff.value = await templatesApi.diff(templateUuid.value, to, from)
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function createAllowlist() {
  allowlistError.value = null
  allowlistPending.value = true
  try {
    const version = versions.value.find((item) => item.uuid === allowlistVersion.value)
    if (!version) throw new Error('Choose a published version.')
    if (!allowlistIntegration.value) throw new Error('Choose an integration target.')
    if (!allowlistBoard.value.trim()) throw new Error('Enter the board UUID this allowlist applies to.')
    if (allowlistModule.value.trim() && !allowlistBoard.value.trim()) throw new Error('A module UUID requires a board UUID.')
    await templatesApi.createAllowlist(templateUuid.value, {
      templateVersionUuid: version.uuid,
      boardUuid: allowlistBoard.value.trim(),
      ...(allowlistModule.value.trim() ? { moduleUuid: allowlistModule.value.trim() } : {}),
      targets: [{ kind: allowlistKind.value, integrationUuid: allowlistIntegration.value }],
    })
    allowlists.value = (await templatesApi.listAllowlists(templateUuid.value)).items
  } catch (caught) {
    allowlistError.value = caught
  } finally {
    allowlistPending.value = false
  }
}

async function revokeAllowlist(allowlistUuid: string) {
  allowlistError.value = null
  try {
    await templatesApi.revokeAllowlist(templateUuid.value, allowlistUuid)
    allowlists.value = (await templatesApi.listAllowlists(templateUuid.value)).items
  } catch (caught) {
    allowlistError.value = caught
  }
}

const integrationOptions = computed(() =>
  integrations.value.filter((item) => {
    if (item.kind) return item.kind === allowlistKind.value
    return allowlistKind.value === 'VM' ? item.type === 'PROXMOX' : item.type === 'CONTAINER'
  }),
)

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 text-sm">
      <RouterLink to="/admin/templates" class="text-muted-foreground underline decoration-border underline-offset-4">‹ Templates</RouterLink>
    </div>
    <PageHeader :title="template?.name ?? 'Template'" :description="template?.slug ?? templateUuid">
      <template #actions>
        <Button variant="outline" :disabled="busy" @click="saveDraft">Save draft</Button>
        <Button variant="outline" :disabled="busy" @click="validate">Validate</Button>
        <Button variant="outline" :disabled="busy" @click="runPreview">Preview</Button>
        <Button :disabled="busy" @click="publishOpen = true">Publish</Button>
      </template>
    </PageHeader>
    <LoadingState v-if="loading" :rows="6" />
    <ErrorAlert v-else-if="error" :error="error" title="Could not load template" />
    <div v-else class="space-y-6">
      <ErrorAlert v-if="actionError" :error="actionError" title="Template action failed" />
      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Manifest YAML</h2>
        </div>
        <Textarea v-model="yaml" rows="18" class="rounded-none font-mono" aria-label="Template manifest YAML" />
      </section>

      <section class="border border-border">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
          <div>
            <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Package files</h2>
            <p class="mt-1 text-xs text-muted-foreground">Bounded relative paths only; no traversal, symlink or arbitrary upload.</p>
          </div>
          <Input :key="fileInputKey" type="file" multiple class="max-w-sm" @change="addFiles" />
        </div>
        <div class="space-y-2 px-4 py-4">
          <EmptyState v-if="files.length === 0" title="No package files" description="Add files that the bounded manifest references." />
          <div v-for="(file, index) in files" :key="index" class="flex flex-wrap items-center gap-2 border border-border px-3 py-2">
            <Input v-model="file.path" class="min-w-64 flex-1" aria-label="Package file path" />
            <span class="text-xs text-muted-foreground">{{ file.contentBase64.length }} base64 bytes</span>
            <Button variant="destructive" size="sm" @click="removeFile(index)">Remove</Button>
          </div>
        </div>
      </section>

      <section v-if="validation" class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Validation: {{ validation.valid ? 'valid' : 'invalid' }}
          </h2>
        </div>
        <div class="space-y-2 px-4 py-4 text-sm">
          <p v-if="validation.findings.length === 0" class="text-muted-foreground">No findings.</p>
          <div v-for="(finding, index) in validation.findings" :key="index" class="border-l-2 pl-3" :class="finding.severity === 'error' ? 'border-love' : 'border-gold'">
            <p class="text-xs uppercase text-muted-foreground">{{ finding.severity }} · {{ finding.code }} · {{ finding.path }}</p>
            <p>{{ finding.message }}</p>
          </div>
        </div>
      </section>

      <section v-if="preview" class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Preview (redacted)</h2>
        </div>
        <div class="grid gap-4 px-4 py-4 md:grid-cols-2">
          <div>
            <p class="mb-2 text-sm font-semibold">Nodes</p>
            <p v-for="node in preview.nodes" :key="node.key" class="text-sm">{{ node.key }} · {{ node.name }} · {{ node.kind }} · {{ node.action }}</p>
          </div>
          <div>
            <p class="mb-2 text-sm font-semibold">Endpoints</p>
            <p v-for="endpoint in preview.endpoints" :key="`${endpoint.nodeKey}:${endpoint.key}`" class="text-sm">
              {{ endpoint.nodeKey }}/{{ endpoint.key }} · {{ endpoint.protocol }} · {{ endpoint.exposure }}
            </p>
          </div>
        </div>
        <p v-for="(warning, index) in preview.warnings" :key="index" class="px-4 pb-2 text-xs text-gold">{{ warning }}</p>
      </section>

      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Immutable versions</h2>
        </div>
        <div class="space-y-2 px-4 py-4">
          <EmptyState v-if="versions.length === 0" title="No published versions" description="Publish a validated draft to create an immutable version." />
          <div v-for="version in versions" :key="version.uuid" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-2 text-sm last:border-0">
            <span>v{{ version.version }} · {{ version.digest }} · {{ formatDateTime(version.publishedAt) }}</span>
            <span class="text-xs text-muted-foreground">{{ version.publishedBy }}</span>
          </div>
          <div v-if="versions.length > 1" class="flex flex-wrap items-end gap-2 border-t border-border pt-3">
            <div class="min-w-32">
              <FormField label="From" for-id="diff-from">
                <Select v-model="fromVersion">
                  <SelectTrigger id="diff-from"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="version in versions" :key="version.uuid" :value="String(version.version)">v{{ version.version }}</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </div>
            <div class="min-w-32">
              <FormField label="To" for-id="diff-to">
                <Select v-model="toVersion">
                  <SelectTrigger id="diff-to"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="version in versions" :key="version.uuid" :value="String(version.version)">v{{ version.version }}</SelectItem>
                  </SelectContent>
                </Select>
              </FormField>
            </div>
            <Button variant="outline" :disabled="busy" @click="loadDiff">Diff</Button>
          </div>
          <div v-if="diff" class="space-y-1 border border-border p-3 text-sm">
            <p class="text-xs text-muted-foreground">v{{ diff.fromVersion }} → v{{ diff.toVersion }}</p>
            <p v-for="(change, index) in diff.changes" :key="index" class="text-xs">
              <span :class="change.change === 'REMOVED' ? 'text-love' : change.change === 'ADDED' ? 'text-foam' : 'text-gold'">{{ change.change }}</span>
              {{ change.kind }} {{ change.path }}
              <span v-if="change.before"> · before: {{ change.before }}</span>
              <span v-if="change.after"> · after: {{ change.after }}</span>
            </p>
          </div>
        </div>
      </section>

      <section class="border border-border">
        <div class="border-b border-border px-4 py-3">
          <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Allowlists</h2>
          <p class="mt-1 text-xs text-muted-foreground">Only exact published versions and exact integration targets. Owners bind from these rows; no raw image/driver options.</p>
        </div>
        <div v-if="allowlistUnavailable" class="px-4 py-4 text-sm text-muted-foreground">
          Allowlists require runtime instances and integrations. This deployment keeps template authoring available
          without them, so there is nothing to bind yet.
        </div>
        <div v-else class="space-y-3 px-4 py-4">
          <ErrorAlert v-if="allowlistError" :error="allowlistError" title="Allowlist action failed" />
          <EmptyState v-if="allowlists.length === 0" title="No allowlist rows" description="Create one to let a board/module bind this template version." />
          <div v-for="entry in allowlists" :key="entry.uuid" class="flex flex-wrap items-center justify-between gap-2 border border-border p-2 text-xs">
            <span>
              {{ entry.templateVersionUuid }} · {{ entry.targets.map((target) => `${target.kind}:${target.integrationUuid}`).join(', ') }}
              <span v-if="entry.revokedAt" class="text-love">revoked</span>
            </span>
            <Button v-if="!entry.revokedAt" variant="destructive" size="sm" @click="revokeAllowlist(entry.uuid)">Revoke</Button>
          </div>
          <div class="grid gap-3 border-t border-border pt-3 sm:grid-cols-3">
            <FormField label="Version" for-id="allowlist-version">
              <Select v-model="allowlistVersion">
                <SelectTrigger id="allowlist-version"><SelectValue placeholder="Published version" /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="version in versions" :key="version.uuid" :value="version.uuid">v{{ version.version }} {{ version.digest.slice(0, 12) }}</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Board UUID" for-id="allowlist-board">
              <Input id="allowlist-board" v-model="allowlistBoard" placeholder="Board that may bind this version" />
            </FormField>
            <FormField label="Module UUID (optional)" for-id="allowlist-module">
              <Input id="allowlist-module" v-model="allowlistModule" placeholder="Defaults to the whole board" />
            </FormField>
            <FormField label="Target kind" for-id="allowlist-kind">
              <Select v-model="allowlistKind">
                <SelectTrigger id="allowlist-kind"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="CONTAINER">CONTAINER</SelectItem>
                  <SelectItem value="VM">VM</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Integration target" for-id="allowlist-integration">
              <Select v-model="allowlistIntegration">
                <SelectTrigger id="allowlist-integration"><SelectValue placeholder="Choose integration" /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="integration in integrationOptions" :key="integration.uuid" :value="integration.uuid">{{ integration.name }}</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>
          <Button :disabled="allowlistPending || versions.length === 0" @click="createAllowlist">{{ allowlistPending ? 'Creating…' : 'Create allowlist' }}</Button>
        </div>
      </section>
    </div>

    <Dialog v-model:open="publishOpen">
      <DialogContent class="rounded-none border-border bg-black">
        <DialogHeader>
          <DialogTitle>Publish immutable version</DialogTitle>
          <DialogDescription>The saved draft becomes an immutable, digested version. No edit/delete is offered afterward.</DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="publish">
          <FormField label="Version note" for-id="version-note" hint="1–2000 characters.">
            <Textarea id="version-note" v-model="versionNote" rows="3" maxlength="2000" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="busy" @click="publishOpen = false">Cancel</Button>
            <Button type="submit" :disabled="busy || !versionNote.trim()">{{ busy ? 'Publishing…' : 'Publish' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </PageContainer>
</template>