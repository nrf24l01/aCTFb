<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import EmptyState from '@/components/layout/EmptyState.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import { adApi } from '@/features/attack-defense/api'
import { integrationsApi } from '@/features/integrations/api'
import { isUuid } from '@/shared/lib/validate'
import type {
  ADConfig,
  ADRepositoryAssignment,
  ADRunSummary,
  ADVulnerabilityAdmin,
  IntegrationSummary,
  SubjectType,
} from '@/shared/types/api'

const props = defineProps<{ boardUuid: string; moduleUuid: string }>()

const config = ref<ADConfig | null>(null)
const configForm = reactive({
  defaultSourceBranch: 'main',
  defaultExploitBranch: 'main',
  runnerImage: '',
  runTimeoutSeconds: 300,
  maxConcurrentRunsPerSubject: 1,
  manualRerunEnabled: false,
})
const vulnerabilities = ref<ADVulnerabilityAdmin[]>([])
const repositories = ref<ADRepositoryAssignment[]>([])
const runs = ref<ADRunSummary[]>([])
const integrations = ref<IntegrationSummary[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const busy = ref(false)

const vulnOpen = ref(false)
const vulnEdit = ref<ADVulnerabilityAdmin | null>(null)
const vulnForm = reactive({
  key: '', title: '', description: '', exploitPoints: 100, patchPoints: 50,
  exploitTestSelector: '', patchTestSelector: '',
})
const vulnDelete = ref<ADVulnerabilityAdmin | null>(null)

const repoOpen = ref(false)
const repoForm = reactive({
  subjectType: 'USER' as SubjectType,
  subjectUuid: '',
  sourceIntegrationUuid: '',
  sourceRepository: '',
  sourceBranch: 'main',
  exploitIntegrationUuid: '',
  exploitRepository: '',
  exploitBranch: 'main',
})
const repoDelete = ref<ADRepositoryAssignment | null>(null)

const runAction = ref<ADRunSummary | null>(null)
const runActionKind = ref<'CANCEL' | 'RETRY'>('CANCEL')

const gitIntegrations = computed(() =>
  integrations.value.filter((integration) => ['GITHUB', 'GITLAB', 'GITEA'].includes(integration.type)),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    integrations.value = (await integrationsApi.list(100, 0)).items
    try {
      config.value = await adApi.adminGetConfig(props.boardUuid, props.moduleUuid)
      Object.assign(configForm, config.value)
    } catch {
      config.value = null
    }
    vulnerabilities.value = (await adApi.adminListVulnerabilities(props.boardUuid, props.moduleUuid, 100, 0)).items
    repositories.value = (await adApi.adminListRepositories(props.boardUuid, props.moduleUuid, 100, 0)).items
    runs.value = (await adApi.adminListRuns(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  busy.value = true
  error.value = null
  try {
    config.value = await adApi.adminPutConfig(props.boardUuid, props.moduleUuid, {
      defaultSourceBranch: configForm.defaultSourceBranch,
      defaultExploitBranch: configForm.defaultExploitBranch,
      runnerImage: configForm.runnerImage,
      runTimeoutSeconds: configForm.runTimeoutSeconds,
      maxConcurrentRunsPerSubject: configForm.maxConcurrentRunsPerSubject,
      manualRerunEnabled: configForm.manualRerunEnabled,
    })
    Object.assign(configForm, config.value)
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

function openVuln(vulnerability?: ADVulnerabilityAdmin) {
  vulnEdit.value = vulnerability ?? null
  Object.assign(vulnForm, vulnerability ?? {
    key: '', title: '', description: '', exploitPoints: 100, patchPoints: 50,
    exploitTestSelector: '', patchTestSelector: '',
  })
  vulnOpen.value = true
}

async function saveVuln() {
  busy.value = true
  error.value = null
  try {
    if (vulnEdit.value) {
      await adApi.adminUpdateVulnerability(props.boardUuid, props.moduleUuid, vulnEdit.value.uuid, {
        key: vulnForm.key, title: vulnForm.title, description: vulnForm.description,
        exploitPoints: vulnForm.exploitPoints, patchPoints: vulnForm.patchPoints,
        exploitTestSelector: vulnForm.exploitTestSelector, patchTestSelector: vulnForm.patchTestSelector,
      })
    } else {
      await adApi.adminCreateVulnerability(props.boardUuid, props.moduleUuid, {
        key: vulnForm.key, title: vulnForm.title, description: vulnForm.description,
        exploitPoints: vulnForm.exploitPoints, patchPoints: vulnForm.patchPoints,
        exploitTestSelector: vulnForm.exploitTestSelector, patchTestSelector: vulnForm.patchTestSelector,
      })
    }
    vulnOpen.value = false
    vulnerabilities.value = (await adApi.adminListVulnerabilities(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function deleteVuln() {
  if (!vulnDelete.value) return
  busy.value = true
  try {
    await adApi.adminDeleteVulnerability(props.boardUuid, props.moduleUuid, vulnDelete.value.uuid)
    vulnDelete.value = null
    vulnerabilities.value = (await adApi.adminListVulnerabilities(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function saveRepo() {
  busy.value = true
  error.value = null
  try {
    if (!isUuid(repoForm.subjectUuid)) throw new Error('Subject UUID is invalid.')
    if (!repoForm.sourceIntegrationUuid || !repoForm.exploitIntegrationUuid) throw new Error('Choose Git integrations.')
    await adApi.adminCreateRepository(props.boardUuid, props.moduleUuid, {
      subjectType: repoForm.subjectType,
      subjectUuid: repoForm.subjectUuid,
      sourceRepository: {
        integrationUuid: repoForm.sourceIntegrationUuid,
        repository: repoForm.sourceRepository,
        branch: repoForm.sourceBranch,
      },
      exploitRepository: {
        integrationUuid: repoForm.exploitIntegrationUuid,
        repository: repoForm.exploitRepository,
        branch: repoForm.exploitBranch,
      },
    })
    repoOpen.value = false
    repositories.value = (await adApi.adminListRepositories(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function deleteRepo() {
  if (!repoDelete.value) return
  busy.value = true
  try {
    await adApi.adminDeleteRepository(props.boardUuid, props.moduleUuid, repoDelete.value.uuid)
    repoDelete.value = null
    repositories.value = (await adApi.adminListRepositories(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

async function doRunAction() {
  if (!runAction.value) return
  busy.value = true
  try {
    await adApi.adminRunAction(props.boardUuid, props.moduleUuid, runAction.value.uuid, runActionKind.value)
    runAction.value = null
    runs.value = (await adApi.adminListRuns(props.boardUuid, props.moduleUuid, 100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    busy.value = false
  }
}

void load()
</script>

<template>
  <div class="space-y-4 border-t border-border p-4">
    <ErrorAlert v-if="error" :error="error" title="A&D admin action failed" />
    <LoadingState v-if="loading" :rows="3" />
    <template v-else>
      <section class="space-y-3 border border-border p-3">
        <h3 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">A&D config</h3>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField label="Default source branch" for-id="ad-source-branch"><Input id="ad-source-branch" v-model="configForm.defaultSourceBranch" /></FormField>
          <FormField label="Default exploit branch" for-id="ad-exploit-branch"><Input id="ad-exploit-branch" v-model="configForm.defaultExploitBranch" /></FormField>
          <FormField label="Runner image" for-id="ad-runner-image" hint="Pinned digest strongly recommended."><Input id="ad-runner-image" v-model="configForm.runnerImage" /></FormField>
          <FormField label="Run timeout seconds" for-id="ad-timeout"><Input id="ad-timeout" v-model.number="configForm.runTimeoutSeconds" type="number" min="1" max="3600" /></FormField>
          <FormField label="Max concurrent per subject" for-id="ad-concurrent"><Input id="ad-concurrent" v-model.number="configForm.maxConcurrentRunsPerSubject" type="number" min="1" max="10" /></FormField>
        </div>
        <label class="flex min-h-11 items-center gap-2 text-sm"><input v-model="configForm.manualRerunEnabled" type="checkbox" class="size-4 accent-[var(--text)]" /> Manual rerun enabled</label>
        <Button :disabled="busy" @click="saveConfig">Save config</Button>
      </section>

      <section class="space-y-2 border border-border p-3">
        <div class="flex items-center justify-between gap-2">
          <h3 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Vulnerabilities</h3>
          <Button size="sm" @click="openVuln()">Create vulnerability</Button>
        </div>
        <EmptyState v-if="vulnerabilities.length === 0" title="No vulnerabilities" description="Criteria are admin-scoped and never exposed to participants unless discovered." />
        <div v-for="vulnerability in vulnerabilities" :key="vulnerability.uuid" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-2 text-sm last:border-0">
          <span>{{ vulnerability.key }} · {{ vulnerability.title }} · E{{ vulnerability.exploitPoints }}/P{{ vulnerability.patchPoints }}</span>
          <span class="flex gap-1">
            <Button variant="outline" size="sm" @click="openVuln(vulnerability)">Edit</Button>
            <Button variant="destructive" size="sm" @click="vulnDelete = vulnerability">Delete</Button>
          </span>
        </div>
      </section>

      <section class="space-y-2 border border-border p-3">
        <div class="flex items-center justify-between gap-2">
          <h3 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Repository assignments</h3>
          <Button size="sm" @click="repoOpen = true">Assign repositories</Button>
        </div>
        <EmptyState v-if="repositories.length === 0" title="No assignments" description="Assign source and exploit repositories to a participant subject." />
        <div v-for="assignment in repositories" :key="assignment.uuid" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-2 text-xs last:border-0">
          <span>{{ assignment.subjectType }} {{ assignment.subjectUuid }} · {{ assignment.sourceRepository.repository }} → {{ assignment.exploitRepository.repository }}</span>
          <Button variant="destructive" size="sm" @click="repoDelete = assignment">Delete</Button>
        </div>
      </section>

      <section class="space-y-2 border border-border p-3">
        <h3 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Runs</h3>
        <EmptyState v-if="runs.length === 0" title="No runs" description="Runs are created by participants/webhooks." />
        <div v-for="run in runs" :key="run.uuid" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-2 text-sm last:border-0">
          <span><StatusBadge :label="run.status" :tone="run.status === 'PASSED' ? 'ok' : run.status === 'FAILED' ? 'broken' : 'running'" /> {{ run.kind }}</span>
          <span class="flex gap-1">
            <Button variant="outline" size="sm" @click="runAction = run; runActionKind = 'CANCEL'">Cancel</Button>
            <Button variant="outline" size="sm" @click="runAction = run; runActionKind = 'RETRY'">Retry</Button>
          </span>
        </div>
      </section>
    </template>

    <Dialog v-model:open="vulnOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-lg">
        <DialogHeader><DialogTitle>{{ vulnEdit ? 'Edit vulnerability' : 'Create vulnerability' }}</DialogTitle></DialogHeader>
        <form class="space-y-3" @submit.prevent="saveVuln">
          <FormField label="Key" for-id="vuln-key"><Input id="vuln-key" v-model="vulnForm.key" /></FormField>
          <FormField label="Title" for-id="vuln-title"><Input id="vuln-title" v-model="vulnForm.title" /></FormField>
          <FormField label="Description" for-id="vuln-desc"><Textarea id="vuln-desc" v-model="vulnForm.description" rows="3" /></FormField>
          <div class="grid gap-3 sm:grid-cols-2">
            <FormField label="Exploit points" for-id="vuln-exploit"><Input id="vuln-exploit" v-model.number="vulnForm.exploitPoints" type="number" min="0" /></FormField>
            <FormField label="Patch points" for-id="vuln-patch"><Input id="vuln-patch" v-model.number="vulnForm.patchPoints" type="number" min="0" /></FormField>
          </div>
          <FormField label="Exploit test selector" for-id="vuln-exploit-sel"><Input id="vuln-exploit-sel" v-model="vulnForm.exploitTestSelector" /></FormField>
          <FormField label="Patch test selector" for-id="vuln-patch-sel"><Input id="vuln-patch-sel" v-model="vulnForm.patchTestSelector" /></FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" @click="vulnOpen = false">Cancel</Button>
            <Button type="submit" :disabled="busy">Save</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="repoOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-xl">
        <DialogHeader><DialogTitle>Assign repositories</DialogTitle><DialogDescription>Provider-native repository paths only, never URLs.</DialogDescription></DialogHeader>
        <form class="space-y-3" @submit.prevent="saveRepo">
          <div class="grid gap-3 sm:grid-cols-2">
            <FormField label="Subject type" for-id="repo-subject-type">
              <Select v-model="repoForm.subjectType">
                <SelectTrigger id="repo-subject-type"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="USER">USER</SelectItem><SelectItem value="TEAM">TEAM</SelectItem></SelectContent>
              </Select>
            </FormField>
            <FormField label="Subject UUID" for-id="repo-subject-uuid"><Input id="repo-subject-uuid" v-model="repoForm.subjectUuid" /></FormField>
          </div>
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Source repository</p>
          <div class="grid gap-3 sm:grid-cols-3">
            <FormField label="Integration" for-id="repo-source-integration">
              <Select v-model="repoForm.sourceIntegrationUuid">
                <SelectTrigger id="repo-source-integration"><SelectValue placeholder="Git integration" /></SelectTrigger>
                <SelectContent><SelectItem v-for="item in gitIntegrations" :key="item.uuid" :value="item.uuid">{{ item.name }}</SelectItem></SelectContent>
              </Select>
            </FormField>
            <FormField label="Repository" for-id="repo-source-path"><Input id="repo-source-path" v-model="repoForm.sourceRepository" placeholder="group/project" /></FormField>
            <FormField label="Branch" for-id="repo-source-branch"><Input id="repo-source-branch" v-model="repoForm.sourceBranch" /></FormField>
          </div>
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Exploit repository</p>
          <div class="grid gap-3 sm:grid-cols-3">
            <FormField label="Integration" for-id="repo-exploit-integration">
              <Select v-model="repoForm.exploitIntegrationUuid">
                <SelectTrigger id="repo-exploit-integration"><SelectValue placeholder="Git integration" /></SelectTrigger>
                <SelectContent><SelectItem v-for="item in gitIntegrations" :key="item.uuid" :value="item.uuid">{{ item.name }}</SelectItem></SelectContent>
              </Select>
            </FormField>
            <FormField label="Repository" for-id="repo-exploit-path"><Input id="repo-exploit-path" v-model="repoForm.exploitRepository" placeholder="group/project" /></FormField>
            <FormField label="Branch" for-id="repo-exploit-branch"><Input id="repo-exploit-branch" v-model="repoForm.exploitBranch" /></FormField>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" @click="repoOpen = false">Cancel</Button>
            <Button type="submit" :disabled="busy">Assign</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog :open="Boolean(vulnDelete)" title="Delete vulnerability" description="The backend refuses while retained runs/awards require it." confirm-label="Delete" destructive :pending="busy" @update:open="(open) => { if (!open) vulnDelete = null }" @confirm="deleteVuln" />
    <ConfirmDialog :open="Boolean(repoDelete)" title="Delete repository assignment" description="The backend refuses while active runs/bindings require it." confirm-label="Delete" destructive :pending="busy" @update:open="(open) => { if (!open) repoDelete = null }" @confirm="deleteRepo" />
    <ConfirmDialog :open="Boolean(runAction)" title="Run action" :description="`${runActionKind} ${runAction?.uuid ?? ''}? The backend enforces allowed transitions.`" :confirm-label="runActionKind === 'CANCEL' ? 'Cancel run' : 'Retry run'" destructive :pending="busy" @update:open="(open) => { if (!open) runAction = null }" @confirm="doRunAction" />
  </div>
</template>