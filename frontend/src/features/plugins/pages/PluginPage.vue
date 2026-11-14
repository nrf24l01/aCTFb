<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import PluginFrame from '@/features/plugins/components/PluginFrame.vue'
import TrustedPluginHost from '@/features/plugins/components/TrustedPluginHost.vue'
import { pluginsApi } from '@/features/plugins/api'
import { isUuid } from '@/shared/lib/validate'
import type { PluginVisible } from '@/shared/types/api'

const route = useRoute()
const pluginId = computed(() => String(route.params.pluginId))
const currentPath = computed(() => route.path)

const plugins = ref<PluginVisible[]>([])
const loading = ref(true)
const error = ref<unknown>(null)
const visible = computed(() => plugins.value.find((plugin) => plugin.id === pluginId.value) ?? null)
const page = computed(() => {
  if (!visible.value) return null
  return visible.value.pages.find((item) => item.path === currentPath.value) ??
    visible.value.pages.find((item) => currentPath.value.startsWith(item.path)) ??
    null
})

const context = computed(() => ({
  theme: 'rose-pine' as const,
  ...(typeof route.query.boardUuid === 'string' && isUuid(route.query.boardUuid) ? { boardUuid: route.query.boardUuid } : {}),
  ...(typeof route.query.moduleUuid === 'string' && isUuid(route.query.moduleUuid) ? { moduleUuid: route.query.moduleUuid } : {}),
  ...(typeof route.query.taskUuid === 'string' && isUuid(route.query.taskUuid) ? { taskUuid: route.query.taskUuid } : {}),
}))

async function load() {
  loading.value = true
  error.value = null
  try {
    plugins.value = (await pluginsApi.visible()).items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader :title="visible?.name ?? 'Plugin'" :description="`Visible plugin page · ${pluginId}`" />
    <LoadingState v-if="loading" :rows="3" />
    <ErrorAlert v-else-if="error" :error="error" title="Could not load plugin metadata" />
    <EmptyState
      v-else-if="!visible"
      title="Plugin not visible"
      description="This plugin is disabled, incompatible or not visible to your effective permissions."
    />
    <EmptyState
      v-else-if="!page"
      title="Plugin page not declared"
      description="The requested page is not in the backend-visible page list. No arbitrary iframe URL is accepted."
    />
    <TrustedPluginHost
      v-else-if="visible?.trustedJS"
      :plugin-id="pluginId"
      :bundle-url="visible.trustedJS.url"
      :integrity="visible.trustedJS.integrity"
      :context="context"
    />
    <PluginFrame
      v-else
      :plugin-id="pluginId"
      :iframe-url="page.iframeUrl"
      :context="context"
    />
  </PageContainer>
</template>