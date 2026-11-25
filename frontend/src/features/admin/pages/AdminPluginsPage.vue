<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import ConfirmDialog from '@/components/layout/ConfirmDialog.vue'
import StatusBadge from '@/components/layout/StatusBadge.vue'
import { pluginsApi } from '@/features/plugins/api'
import { useSession } from '@/features/auth/session'
import { isUuid } from '@/shared/lib/validate'
import type {
  PermissionGrant,
  Plugin,
  PluginEventDelivery,
  PluginValidation,
} from '@/shared/types/api'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)
const items = ref<Plugin[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
const selected = ref<Plugin | null>(null)

const packagePath = ref('')
const validatedPath = ref('')
const validation = ref<PluginValidation | null>(null)
const validPkg = ref(false)
const approvedCapabilities = ref<string[]>([])
const approvedPermissions = ref<string[]>([])
const approveTrustedJS = ref(false)
const installError = ref<unknown>(null)
const installing = ref(false)

const actionError = ref<unknown>(null)
const busy = ref(false)
const removeOpen = ref(false)
const upgradeOpen = ref(false)
const upgradePath = ref('')
const upgradeTrustedJS = ref(false)

const logs = ref<string[]>([])
const events = ref<PluginEventDelivery[]>([])
const grants = ref<PermissionGrant[]>([])
const grantForm = reactive({ userUuid: '', permission: '', boardUuid: '', moduleUuid: '', expiresAt: '' })
const grantError = ref<unknown>(null)
const grantPending = ref(false)

const permissionOptions = computed(() => selected.value?.permissions.map((permission) => permission.id) ?? [])

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    items.value = (await pluginsApi.list(100, 0)).items
    if (selected.value) {
      selected.value = items.value.find((plugin) => plugin.id === selected.value?.id) ?? null
    }
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function validatePackage() {
  validation.value = null
  validatedPath.value = ''
  installError.value = null
  busy.value = true
  try {
    const path = packagePath.value.trim()
    validation.value = await pluginsApi.validate(path)
    validPkg.value = validation.value.valid
    validatedPath.value = validation.value.valid ? path : ''
    approvedCapabilities.value = [...(validation.value.capabilities ?? [])]
    approvedPermissions.value = (validation.value.permissions ?? []).map((permission) => permission.id)
    approveTrustedJS.value = false
    await load()
  } catch (caught) {
    installError.value = caught
  } finally {
    busy.value = false
  }
}

function toggleCapability(value: string) {
  approvedCapabilities.value = approvedCapabilities.value.includes(value)
    ? approvedCapabilities.value.filter((item) => item !== value)
    : [...approvedCapabilities.value, value]
}
function togglePermission(value: string) {
  approvedPermissions.value = approvedPermissions.value.includes(value)
    ? approvedPermissions.value.filter((item) => item !== value)
    : [...approvedPermissions.value, value]
}

async function install() {
  const current = validation.value
  if (!current?.valid || !current.id) {
    installError.value = new Error('Validate the exact package first; install cannot approve capabilities it cannot enumerate.')
    return
  }
  if (packagePath.value.trim() !== validatedPath.value) {
    installError.value = new Error('The package path changed after validation. Validate it again before installing.')
    return
  }
  const allCaps = (current.capabilities ?? []).every((capability) => approvedCapabilities.value.includes(capability))
  const allPerms = (current.permissions ?? []).every((permission) => approvedPermissions.value.includes(permission.id))
  if (!allCaps || !allPerms) {
    installError.value = new Error('Explicitly approve every requested capability and permission.')
    return
  }
  if (current.frontendMode === 'trusted-js' && !approveTrustedJS.value) {
    installError.value = new Error('Approve the trusted-js risk explicitly; it is not an iframe sandbox.')
    return
  }
  installing.value = true
  installError.value = null
  try {
    await pluginsApi.install({
      packagePath: validatedPath.value,
      approveCapabilities: approvedCapabilities.value,
      approvePermissions: approvedPermissions.value,
      ...(current.frontendMode === 'trusted-js' ? { approveTrustedJS: approveTrustedJS.value } : {}),
    })
    validation.value = null
    validatedPath.value = ''
    await load()
  } catch (caught) {
    installError.value = caught
  } finally {
    installing.value = false
  }
}

async function selectPlugin(plugin: Plugin) {
  selected.value = plugin
  actionError.value = null
  logs.value = []
  events.value = []
  grants.value = []
  grantError.value = null
  try {
    const [logResponse, eventPage, grantPage] = await Promise.all([
      pluginsApi.logs(plugin.id),
      pluginsApi.eventDeliveries(plugin.id, 100, 0),
      pluginsApi.listGrants(plugin.id, 100, 0),
    ])
    logs.value = logResponse.lines
    events.value = eventPage.items
    grants.value = grantPage.items
  } catch (caught) {
    actionError.value = caught
  }
}

async function enable() {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    selected.value = await pluginsApi.enable(selected.value.id)
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function disable() {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    selected.value = await pluginsApi.disable(selected.value.id)
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function upgrade() {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    selected.value = await pluginsApi.upgrade(selected.value.id, {
      packagePath: upgradePath.value.trim(),
      ...(upgradeTrustedJS.value ? { approveTrustedJS: true } : {}),
    })
    upgradeOpen.value = false
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    await pluginsApi.remove(selected.value.id)
    selected.value = null
    removeOpen.value = false
    await load()
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function replay(event: PluginEventDelivery) {
  if (!selected.value) return
  busy.value = true
  actionError.value = null
  try {
    await pluginsApi.replayEvent(selected.value.id, event.eventId)
    const page = await pluginsApi.eventDeliveries(selected.value.id, 100, 0)
    events.value = page.items
  } catch (caught) {
    actionError.value = caught
  } finally {
    busy.value = false
  }
}

async function createGrant() {
  if (!selected.value) return
  grantError.value = null
  if (!isUuid(grantForm.userUuid)) {
    grantError.value = new Error('Enter the user UUID.')
    return
  }
  if (!grantForm.permission) {
    grantError.value = new Error('Choose a declared plugin permission.')
    return
  }
  grantPending.value = true
  try {
    await pluginsApi.createGrant(selected.value.id, {
      userUuid: grantForm.userUuid.trim(),
      permission: grantForm.permission,
      ...(grantForm.boardUuid.trim() ? { boardUuid: grantForm.boardUuid.trim() } : {}),
      ...(grantForm.moduleUuid.trim() ? { moduleUuid: grantForm.moduleUuid.trim() } : {}),
      ...(grantForm.expiresAt ? { expiresAt: new Date(grantForm.expiresAt).toISOString() } : {}),
    })
    grantForm.userUuid = ''
    grantForm.permission = ''
    grantForm.boardUuid = ''
    grantForm.moduleUuid = ''
    grantForm.expiresAt = ''
    grants.value = (await pluginsApi.listGrants(selected.value.id, 100, 0)).items
  } catch (caught) {
    grantError.value = caught
  } finally {
    grantPending.value = false
  }
}

async function revokeGrant(grant: PermissionGrant) {
  if (!selected.value) return
  try {
    await pluginsApi.revokeGrant(selected.value.id, grant.uuid)
    grants.value = (await pluginsApi.listGrants(selected.value.id, 100, 0)).items
  } catch (caught) {
    grantError.value = caught
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Plugins" description="Installable HTTP-v1 addons from operator folders. Install never enables; grants are explicit.">
      <template #actions>
        <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState v-if="!isAdmin" title="Administrator access required" description="plugins.manage is global-admin only." />
    <template v-else>
      <ErrorAlert v-if="error" :error="error" title="Could not load plugins" />
      <LoadingState v-if="loading && items.length === 0" />

      <section class="mb-6 border border-border p-4">
        <h2 class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Validate / install local package</h2>
        <p class="mt-1 text-xs text-muted-foreground">
          Server-side package path relative to <code>ACTFB_PLUGINS_DIR/incoming</code> only. This UI never uploads or executes an executable.
          Install leaves the plugin disabled; enabling is a separate explicit action.
        </p>
        <div class="mt-3 flex flex-wrap gap-2">
          <Input v-model="packagePath" placeholder="hello" class="max-w-md" />
          <Button variant="outline" :disabled="busy || !packagePath.trim()" @click="validatePackage">Validate</Button>
        </div>
        <ErrorAlert v-if="installError" :error="installError" title="Package validation/install failed" class="mt-3" />
        <div v-if="validation" class="mt-3 space-y-2">
          <p class="text-sm">Validation: {{ validation.valid ? 'valid' : 'invalid' }}</p>
          <p v-for="(finding, index) in validation.findings" :key="index" class="border-l-2 pl-3 text-xs" :class="finding.severity === 'error' ? 'border-love' : 'border-gold'">
            {{ finding.severity }} · {{ finding.code }} · {{ finding.path }} · {{ finding.message }}
          </p>
          <div v-if="validation.valid && validation.id" class="border border-gold/60 p-3">
            <p class="text-sm font-semibold">{{ validation.name || validation.id }} {{ validation.version }}</p>
            <p class="text-xs text-gold">Installing third-party code is a trust decision. Approve every capability and permission; installation alone does not enable it.</p>
            <div v-if="(validation.capabilities?.length ?? 0) > 0 || (validation.permissions?.length ?? 0) > 0" class="mt-2 grid gap-2 sm:grid-cols-2">
              <div>
                <p class="text-xs uppercase text-muted-foreground">Capabilities</p>
                <label v-for="capability in validation.capabilities ?? []" :key="capability" class="flex min-h-9 items-center gap-2 text-sm">
                  <Checkbox :model-value="approvedCapabilities.includes(capability)" @update:model-value="toggleCapability(capability)" />
                  {{ capability }}
                </label>
              </div>
              <div>
                <p class="text-xs uppercase text-muted-foreground">Permissions</p>
                <label v-for="permission in validation.permissions ?? []" :key="permission.id" class="flex min-h-9 items-center gap-2 text-sm">
                  <Checkbox :model-value="approvedPermissions.includes(permission.id)" @update:model-value="togglePermission(permission.id)" />
                  {{ permission.id }} ({{ permission.scope }})
                </label>
              </div>
            </div>
            <p v-else class="mt-2 text-xs text-muted-foreground">This package requests no capabilities or permissions.</p>
            <div v-if="(validation.dependencies?.length ?? 0) > 0" class="mt-2">
              <p class="text-xs uppercase text-muted-foreground">Dependencies</p>
              <p v-for="dependency in validation.dependencies" :key="dependency.id" class="text-xs">
                {{ dependency.id }} {{ dependency.version }}
              </p>
            </div>
            <label v-if="validation.frontendMode === 'trusted-js'" class="mt-3 flex items-start gap-2 border border-love/60 p-3 text-xs text-love">
              <Checkbox :model-value="approveTrustedJS" @update:model-value="approveTrustedJS = $event === true" />
              <span>
                This package requests trusted-js. It is not a sandbox and executes with platform-origin capabilities.
                I explicitly accept that risk for this installation.
              </span>
            </label>
            <Button class="mt-3" variant="destructive" :disabled="installing" @click="install">
              {{ installing ? 'Installing…' : 'Install without enabling' }}
            </Button>
          </div>
          <p v-else-if="validation.valid" class="text-xs text-gold">
            The backend did not return manifest approval metadata, so install is blocked rather than auto-approving an
            unknown capability set.
          </p>
        </div>
      </section>

      <EmptyState v-if="items.length === 0" title="No plugins" description="Place a package in the operator folder and refresh." />
      <div v-else class="overflow-x-auto border border-border">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>Plugin</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Health</TableHead>
              <TableHead>Capabilities / permissions</TableHead>
              <TableHead class="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="plugin in items" :key="plugin.id">
              <TableCell>
                <span class="font-medium">{{ plugin.name }}</span>
                <span class="block text-xs text-muted-foreground">{{ plugin.id }} · v{{ plugin.version }} · API {{ plugin.apiVersion }}</span>
              </TableCell>
              <TableCell><StatusBadge :status="plugin.status" /></TableCell>
              <TableCell class="text-xs">{{ plugin.health.status }}<span v-if="plugin.health.message" class="block text-muted-foreground">{{ plugin.health.message }}</span></TableCell>
              <TableCell class="text-xs">{{ plugin.capabilities.length }} / {{ plugin.permissions.length }}</TableCell>
              <TableCell class="text-right"><Button variant="outline" size="sm" @click="selectPlugin(plugin)">Manage</Button></TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

      <div v-if="selected" class="mt-6 space-y-4 border border-border p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 class="text-lg font-semibold">{{ selected.name }}</h2>
            <p class="text-xs text-muted-foreground">{{ selected.id }} · {{ selected.status }} · {{ selected.author }}</p>
          </div>
          <Button variant="ghost" @click="selected = null">Close</Button>
        </div>
        <ErrorAlert v-if="actionError" :error="actionError" title="Plugin action failed" />
        <div class="flex flex-wrap gap-2">
          <Button v-if="selected.status !== 'enabled'" :disabled="busy" @click="enable">Enable</Button>
          <Button v-if="selected.status === 'enabled'" variant="outline" :disabled="busy" @click="disable">Disable</Button>
          <Button variant="outline" :disabled="busy" @click="upgradeOpen = true; upgradeTrustedJS = false">Upgrade</Button>
          <Button variant="destructive" :disabled="busy" @click="removeOpen = true">Remove</Button>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Declared routes</p>
            <p v-for="route in selected.routes" :key="`${route.method}:${route.path}`" class="text-xs">
              {{ route.method }} {{ route.path }} · {{ route.scope }} · {{ route.permission }}
            </p>
          </div>
          <div>
            <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Logs</p>
            <pre class="max-h-48 overflow-auto border border-border bg-base p-2 text-xs">{{ logs.join('\n') || 'No log lines returned.' }}</pre>
          </div>
        </div>

        <div class="space-y-2">
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Event deliveries</p>
          <EmptyState v-if="events.length === 0" title="No event deliveries" description="No delivery receipts returned." />
          <div v-for="event in events" :key="event.eventId" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-1 text-xs">
            <span>{{ event.eventId }} · {{ event.status }} · attempts {{ event.attempts ?? 0 }} <span v-if="event.lastError" class="text-love">{{ event.lastError }}</span></span>
            <Button variant="outline" size="sm" :disabled="busy" @click="replay(event)">Replay</Button>
          </div>
        </div>

        <div class="space-y-2 border-t border-border pt-3">
          <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Permission grants (explicit only)</p>
          <p class="text-xs text-muted-foreground">No plugin permission or UI page is auto-visible, even to admins, until granted.</p>
          <ErrorAlert v-if="grantError" :error="grantError" title="Grant action failed" />
          <div v-for="grant in grants" :key="grant.uuid" class="flex flex-wrap items-center justify-between gap-2 border-b border-border py-1 text-xs">
            <span>{{ grant.permission }} · user {{ grant.userUuid }} <span v-if="grant.boardUuid"> · board {{ grant.boardUuid }}</span><span v-if="grant.moduleUuid"> · module {{ grant.moduleUuid }}</span></span>
            <Button variant="destructive" size="sm" @click="revokeGrant(grant)">Revoke</Button>
          </div>
          <form class="grid gap-2 sm:grid-cols-3" @submit.prevent="createGrant">
            <FormField label="User UUID" for-id="grant-user"><Input id="grant-user" v-model="grantForm.userUuid" /></FormField>
            <FormField label="Declared permission" for-id="grant-permission">
              <Select v-model="grantForm.permission">
                <SelectTrigger id="grant-permission"><SelectValue placeholder="Choose permission" /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="permission in permissionOptions" :key="permission" :value="permission">{{ permission }}</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Board UUID (if scoped)" for-id="grant-board"><Input id="grant-board" v-model="grantForm.boardUuid" /></FormField>
            <FormField label="Module UUID (if scoped)" for-id="grant-module"><Input id="grant-module" v-model="grantForm.moduleUuid" /></FormField>
            <FormField label="Expiry (optional)" for-id="grant-expiry"><Input id="grant-expiry" v-model="grantForm.expiresAt" type="datetime-local" /></FormField>
            <Button type="submit" :disabled="grantPending">{{ grantPending ? 'Granting…' : 'Grant' }}</Button>
          </form>
        </div>
      </div>
    </template>

    <ConfirmDialog v-model:open="removeOpen" title="Remove plugin" description="Removal is explicit. Disabling preserves plugin data; the backend governs removal retention. This cannot be undone from the UI." confirm-label="Remove plugin" destructive :pending="busy" @confirm="remove" />
    <ConfirmDialog v-model:open="upgradeOpen" title="Upgrade plugin" description="Provide a server-side package path. Installation/upgrade never runs package code before validation." confirm-label="Upgrade" :pending="busy" @confirm="upgrade">
      <Input v-model="upgradePath" placeholder="hello" class="mt-2" />
      <label class="mt-2 flex items-start gap-2 text-xs text-love">
        <Checkbox :model-value="upgradeTrustedJS" @update:model-value="upgradeTrustedJS = $event === true" />
        <span>Approve trusted-js risk for this upgrade (only valid when the new manifest requests it).</span>
      </label>
    </ConfirmDialog>
  </PageContainer>
</template>