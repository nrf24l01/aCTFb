<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { integrationsApi } from '@/features/integrations/api'
import { useSession } from '@/features/auth/session'
import { parseJsonObject } from '@/shared/lib/json'
import type { IntegrationCreateRequest, IntegrationSummary } from '@/shared/types/api'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const items = ref<IntegrationSummary[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
const dialogOpen = ref(false)
const editTarget = ref<IntegrationSummary | null>(null)
const pending = ref(false)
const formError = ref<unknown>(null)
const testResult = ref<{ ok: boolean; message?: string } | null>(null)
const testError = ref<unknown>(null)
const deleteTarget = ref<IntegrationSummary | null>(null)
const deleting = ref(false)

const form = reactive({
  type: 'GITHUB',
  kind: '' as '' | 'VM' | 'CONTAINER',
  driver: '' as '' | 'proxmox' | 'kubevirt' | 'docker' | 'podman' | 'kubernetes',
  name: '',
  baseUrl: '',
  credentials: '',
  settings: '{}',
  publicTcpHost: '',
  publicPortFrom: '',
  publicPortTo: '',
  httpBaseDomain: '',
  forceHttps: true,
})

const needsDriver = computed(() => form.type === 'CONTAINER')
const isCompute = computed(() => ['PROXMOX', 'KUBEVIRT', 'CONTAINER', 'DOCKER', 'PODMAN', 'KUBERNETES'].includes(form.type))
const isGit = computed(() => ['GITLAB', 'GITHUB', 'GITEA'].includes(form.type))

function defaultKind(): '' | 'VM' | 'CONTAINER' {
  if (form.type === 'PROXMOX' || form.type === 'KUBEVIRT' || form.type === 'VM') return 'VM'
  if (['CONTAINER', 'DOCKER', 'PODMAN', 'KUBERNETES'].includes(form.type)) return 'CONTAINER'
  return ''
}
function defaultDriver(): '' | 'proxmox' | 'kubevirt' | 'docker' | 'podman' | 'kubernetes' {
  if (form.type === 'PROXMOX') return 'proxmox'
  if (form.type === 'KUBEVIRT') return 'kubevirt'
  if (form.type === 'DOCKER') return 'docker'
  if (form.type === 'PODMAN') return 'podman'
  if (form.type === 'KUBERNETES') return 'kubernetes'
  return ''
}

function syncType() {
  form.kind = defaultKind()
  form.driver = defaultDriver()
}

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    items.value = (await integrationsApi.list(100, 0)).items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editTarget.value = null
  Object.assign(form, {
    type: 'GITHUB',
    kind: '',
    driver: '',
    name: '',
    baseUrl: '',
    credentials: '',
    settings: '{}',
    publicTcpHost: '',
    publicPortFrom: '',
    publicPortTo: '',
    httpBaseDomain: '',
    forceHttps: true,
  })
  formError.value = null
  testResult.value = null
  testError.value = null
  dialogOpen.value = true
}

function openEdit(integration: IntegrationSummary) {
  editTarget.value = integration
  Object.assign(form, {
    type: integration.type,
    kind: integration.kind ?? '',
    driver: integration.driver ?? '',
    name: integration.name,
    baseUrl: integration.baseUrl ?? '',
    credentials: '',
    settings: '{}',
    publicTcpHost: '',
    publicPortFrom: '',
    publicPortTo: '',
    httpBaseDomain: '',
    forceHttps: true,
  })
  formError.value = null
  testResult.value = null
  testError.value = null
  dialogOpen.value = true
}

function credentialsObject(): Record<string, string> {
  const result: Record<string, string> = {}
  for (const line of form.credentials.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const index = trimmed.indexOf('=')
    if (index <= 0) throw new Error('Credentials must be one key=value per line.')
    result[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
  }
  return result
}

async function save() {
  formError.value = null
  if (!form.name.trim()) {
    formError.value = new Error('Enter a name.')
    return
  }
  if (needsDriver.value && !form.driver) {
    formError.value = new Error('CONTAINER integrations require a driver (docker, podman or kubernetes).')
    return
  }
  const settings = parseJsonObject(form.settings)
  if (settings === null) {
    formError.value = new Error('Settings must be a JSON object.')
    return
  }
  const credentials = credentialsObject()
  const body: IntegrationCreateRequest = {
    type: form.type,
    kind: isGit.value ? null : form.kind || null,
    driver: isGit.value ? null : form.driver || null,
    name: form.name.trim(),
    ...(form.baseUrl.trim() ? { baseUrl: form.baseUrl.trim() } : {}),
    ...(Object.keys(credentials).length > 0 ? { credentials } : {}),
    settings,
    ...(isCompute.value
      ? {
          instancePublicationPolicy: {
            ...(form.publicTcpHost ? { publicTcpHost: form.publicTcpHost } : {}),
            ...(form.publicPortFrom ? { publicPortFrom: Number(form.publicPortFrom) } : {}),
            ...(form.publicPortTo ? { publicPortTo: Number(form.publicPortTo) } : {}),
            ...(form.httpBaseDomain ? { httpBaseDomain: form.httpBaseDomain } : {}),
            forceHttps: form.forceHttps,
          },
        }
      : {}),
  }
  pending.value = true
  try {
    if (editTarget.value) {
      const { type: _type, ...patch } = body
      await integrationsApi.update(editTarget.value.uuid, patch)
    } else {
      await integrationsApi.create(body)
    }
    form.credentials = ''
    dialogOpen.value = false
    await load()
  } catch (caught) {
    formError.value = caught
  } finally {
    pending.value = false
  }
}

async function testConnection(integration: IntegrationSummary) {
  testResult.value = null
  testError.value = null
  try {
    testResult.value = await integrationsApi.test(integration.uuid)
  } catch (caught) {
    testError.value = caught
  }
}

async function remove() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await integrationsApi.remove(deleteTarget.value.uuid)
    deleteTarget.value = null
    await load()
  } catch (caught) {
    error.value = caught
  } finally {
    deleting.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Integrations" description="Git and compute providers. Credentials are write-only and never returned by GET/list.">
      <template #actions>
        <Button v-if="isAdmin" @click="openCreate">Create integration</Button>
        <Button v-if="isAdmin" variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState v-if="!isAdmin" title="Administrator access required" description="Integrations are global-admin managed." />
    <template v-else>
      <LoadingState v-if="loading && items.length === 0" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load integrations" />
      <EmptyState v-else-if="items.length === 0" title="No integrations" description="Create a Git or compute provider. External health is only shown from real backend test results." />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Name</TableHead>
              <TableHead>Type / driver</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Credentials</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="integration in items" :key="integration.uuid">
              <TableCell>
                <span class="font-medium">{{ integration.name }}</span>
                <span class="block text-xs text-muted-foreground">{{ integration.baseUrl || 'No base URL' }}</span>
              </TableCell>
              <TableCell class="text-xs">
                {{ integration.type }}<span v-if="integration.kind"> · {{ integration.kind }}</span>
                <span v-if="integration.driver"> · {{ integration.driver }}</span>
              </TableCell>
              <TableCell><StatusBadge :label="integration.status" :tone="integration.status === 'CONNECTED' ? 'ok' : integration.status === 'ERROR' ? 'broken' : 'unknown'" /></TableCell>
              <TableCell class="text-xs">{{ integration.credentialsConfigured ? 'configured (hidden)' : 'none' }}</TableCell>
              <TableCell class="text-right">
                <div class="flex justify-end gap-1">
                  <Button variant="outline" size="sm" @click="testConnection(integration)">Test</Button>
                  <Button variant="outline" size="sm" @click="openEdit(integration)">Edit</Button>
                  <Button variant="destructive" size="sm" @click="deleteTarget = integration">Delete</Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <div v-if="testResult" class="mt-4 border border-border p-3 text-sm">
        Test: {{ testResult.ok ? 'OK' : 'Failed' }} <span class="text-muted-foreground">{{ testResult.message }}</span>
      </div>
      <ErrorAlert v-if="testError" :error="testError" title="Integration test failed" class="mt-4" />
    </template>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto rounded-none border-border bg-black sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{{ editTarget ? 'Edit integration' : 'Create integration' }}</DialogTitle>
          <DialogDescription>
            Credentials are write-only: leave the field blank to keep existing values. They are reset after save and
            never placed in Web Storage.
          </DialogDescription>
        </DialogHeader>
        <form class="space-y-4" novalidate @submit.prevent="save">
          <ErrorAlert v-if="formError" :error="formError" title="Could not save integration" />
          <div class="grid gap-3 sm:grid-cols-2">
            <FormField label="Type" for-id="integration-type">
              <Select v-model="form.type" :disabled="Boolean(editTarget)" @update:model-value="syncType">
                <SelectTrigger id="integration-type"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="GITHUB">GITHUB</SelectItem>
                  <SelectItem value="GITLAB">GITLAB</SelectItem>
                  <SelectItem value="GITEA">GITEA</SelectItem>
                  <SelectItem value="PROXMOX">PROXMOX</SelectItem>
                  <SelectItem value="KUBEVIRT">KUBEVIRT</SelectItem>
                  <SelectItem value="CONTAINER">CONTAINER</SelectItem>
                  <SelectItem value="DOCKER">DOCKER</SelectItem>
                  <SelectItem value="PODMAN">PODMAN</SelectItem>
                  <SelectItem value="KUBERNETES">KUBERNETES</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Name" for-id="integration-name">
              <Input id="integration-name" v-model="form.name" />
            </FormField>
          </div>
          <div v-if="!isGit" class="grid gap-3 sm:grid-cols-2">
            <FormField label="Kind" for-id="integration-kind" hint="VM and CONTAINER cannot mix in one template.">
              <Select v-model="form.kind">
                <SelectTrigger id="integration-kind"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="VM">VM</SelectItem>
                  <SelectItem value="CONTAINER">CONTAINER</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Driver" for-id="integration-driver" hint="Driver is separate from kind.">
              <Select v-model="form.driver">
                <SelectTrigger id="integration-driver"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="proxmox">proxmox</SelectItem>
                  <SelectItem value="kubevirt">kubevirt</SelectItem>
                  <SelectItem value="docker">docker</SelectItem>
                  <SelectItem value="podman">podman</SelectItem>
                  <SelectItem value="kubernetes">kubernetes</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>
          <FormField label="Base URL" for-id="integration-base-url" hint="Admin-configured provider URL; never a per-request client URL.">
            <Input id="integration-base-url" v-model="form.baseUrl" />
          </FormField>
          <FormField label="Credentials (write-only)" for-id="integration-credentials" hint="One key=value per line. Blank keeps existing on edit.">
            <Textarea id="integration-credentials" v-model="form.credentials" rows="3" autocomplete="off" spellcheck="false" />
          </FormField>
          <FormField label="Settings JSON" for-id="integration-settings">
            <Textarea id="integration-settings" v-model="form.settings" rows="3" />
          </FormField>
          <div v-if="isCompute" class="space-y-3 border border-border p-3">
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Instance publication policy</p>
            <div class="grid gap-3 sm:grid-cols-2">
              <FormField label="Public TCP host" for-id="integration-public-host">
                <Input id="integration-public-host" v-model="form.publicTcpHost" />
              </FormField>
              <FormField label="HTTP base domain" for-id="integration-http-domain">
                <Input id="integration-http-domain" v-model="form.httpBaseDomain" />
              </FormField>
              <FormField label="Port from" for-id="integration-port-from">
                <Input id="integration-port-from" v-model="form.publicPortFrom" type="number" min="1" max="65535" />
              </FormField>
              <FormField label="Port to" for-id="integration-port-to">
                <Input id="integration-port-to" v-model="form.publicPortTo" type="number" min="1" max="65535" />
              </FormField>
            </div>
            <label class="flex min-h-11 items-center gap-2 text-sm">
              <input v-model="form.forceHttps" type="checkbox" class="size-4 accent-[var(--text)]" />
              Force HTTPS
            </label>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" :disabled="pending" @click="dialogOpen = false">Cancel</Button>
            <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save integration' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete integration"
      description="The backend refuses while templates, bindings or active instances require this integration."
      confirm-label="Delete integration"
      destructive
      :pending="deleting"
      @update:open="(open) => { if (!open) deleteTarget = null }"
      @confirm="remove"
    />
  </PageContainer>
</template>