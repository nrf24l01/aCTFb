<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { templatesApi } from '@/features/templates/api'
import { useSession } from '@/features/auth/session'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import type { DeploymentTemplate } from '@/shared/types/api'

const { state } = useSession()
const router = useRouter()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)
const items = ref<DeploymentTemplate[]>([])
const loading = ref(false)
const creating = ref(false)
const error = ref<unknown>(null)
const createError = ref<unknown>(null)

// An explicit FEATURE_DISABLED answer is an operator state, not a load failure:
// the catalog itself is unavailable on this deployment. Anything else (403,
// 404 for a wrong route, 500) is a real error and must not be shown as an empty
// catalog.
const featureDisabled = computed(() => isApiError(error.value) && error.value.code === 'FEATURE_DISABLED')
const canCreate = computed(() => isAdmin.value && error.value === null)

async function load() {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    items.value = (await templatesApi.list(100, 0)).items
  } catch (caught) {
    items.value = []
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function createTemplate() {
  creating.value = true
  createError.value = null
  try {
    const template = await templatesApi.create()
    await router.push(`/admin/templates/${template.uuid}`)
  } catch (caught) {
    createError.value = caught
  } finally {
    creating.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Deployment templates" description="YAML manifests and bounded package files. Published versions are immutable.">
      <template #actions>
        <Button v-if="canCreate && items.length > 0" variant="outline" :disabled="creating" @click="createTemplate">
          {{ creating ? 'Creating…' : 'Create template' }}
        </Button>
        <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>
    <EmptyState v-if="!isAdmin" title="Administrator access required" description="Only a global admin can publish template versions and manage allowlists." />
    <template v-else>
      <ErrorAlert v-if="createError" :error="createError" title="Could not create deployment template" class="mb-4" />
      <LoadingState v-if="loading && items.length === 0" />
      <Alert v-else-if="featureDisabled" class="rounded-none border-border bg-black">
        <AlertTitle>Template management is disabled</AlertTitle>
        <AlertDescription>
          <p>This deployment does not expose the deployment template catalog. An operator can enable it in the
            deployment settings; runtime instances stay a separate switch.</p>
          <RouterLink to="/admin" class="mt-2 inline-block underline underline-offset-4">Back to administration</RouterLink>
        </AlertDescription>
      </Alert>
      <ErrorAlert v-else-if="error" :error="error" title="Could not load deployment templates">
        <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
      </ErrorAlert>
      <EmptyState
        v-else-if="items.length === 0"
        title="No deployment templates yet"
        description="Create a template to start a draft, then edit its manifest and publish an immutable version."
      >
        <Button variant="outline" :disabled="creating" @click="createTemplate">
          {{ creating ? 'Creating…' : 'Create template' }}
        </Button>
      </EmptyState>
      <div v-else class="workspace-cards workspace-cards--admin" data-slot="admin-cards">
        <div class="workspace-card-grid gap-2">
          <RouterLink
            v-for="template in items"
            :key="template.uuid"
            :to="`/admin/templates/${template.uuid}`"
            class="block border border-border bg-black px-4 py-3 hover:border-muted-foreground"
          >
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h2 class="font-semibold">{{ template.name }}</h2>
              <span class="text-xs text-muted-foreground">{{ template.slug }}</span>
            </div>
            <p class="text-sm text-muted-foreground">{{ template.description || 'No description' }}</p>
            <p class="mt-1 text-xs text-muted-foreground">Updated {{ formatDateTime(template.updatedAt) }}</p>
          </RouterLink>
        </div>
      </div>
    </template>
  </PageContainer>
</template>
