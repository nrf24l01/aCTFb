<script setup lang="ts">
import { createApp, h, onUnmounted, ref, type App, type Component } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import type { PluginContext } from '@/features/plugins/bridge'
import { getAccessToken, refreshAccessToken } from '@/shared/api/client'

const props = defineProps<{
  pluginId: string
  bundleUrl: string
  integrity: string
  context: PluginContext
}>()

const container = ref<HTMLDivElement | null>(null)
const error = ref<string | null>(null)
const loading = ref(true)
let app: App | null = null
let disposed = false

function hex(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes), (byte) => byte.toString(16).padStart(2, '0')).join('')
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const request = async (): Promise<Response> => fetch(props.bundleUrl, {
      credentials: 'same-origin',
      headers: {
        Accept: 'application/javascript',
        ...(getAccessToken() ? { Authorization: `Bearer ${getAccessToken()}` } : {}),
      },
      cache: 'no-store',
    })
    let response = await request()
    if (response.status === 401) {
      await refreshAccessToken()
      response = await request()
    }
    if (!response.ok) throw new Error(`Bundle request failed (${response.status}).`)
    const contentType = response.headers.get('content-type') ?? ''
    if (!contentType.startsWith('application/javascript')) throw new Error('Bundle content type is not JavaScript.')
    const raw = await response.arrayBuffer()
    const digest = `sha256:${hex(await crypto.subtle.digest('SHA-256', raw))}`
    if (!props.integrity || digest.toLowerCase() !== props.integrity.toLowerCase()) {
      throw new Error('Bundle integrity does not match the approved digest.')
    }
    const source = new TextDecoder('utf-8', { fatal: true }).decode(raw)
    const blobURL = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }))
    try {
      const module = (await import(/* @vite-ignore */ blobURL)) as {
        default?: Component | ((element: HTMLElement, props: Record<string, unknown>) => void)
        mount?: (element: HTMLElement, props: Record<string, unknown>) => void
      }
      if (disposed || !container.value) return
      const mountProps = { pluginId: props.pluginId, context: props.context }
      if (typeof module.mount === 'function') {
        await module.mount(container.value, mountProps)
      } else {
        const entry = module.default
        if (!entry) throw new Error('Trusted bundle has no default export or mount function.')
        if (typeof entry === 'function' && !('render' in entry) && !('setup' in entry)) {
          await (entry as (element: HTMLElement, props: Record<string, unknown>) => void)(container.value, mountProps)
        } else {
          app = createApp({ render: () => h(entry as Component, mountProps) })
          app.mount(container.value)
        }
      }
    } finally {
      URL.revokeObjectURL(blobURL)
    }
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : 'Trusted bundle failed to load.'
  } finally {
    loading.value = false
  }
}

onUnmounted(() => {
  disposed = true
  app?.unmount()
})

void load()
</script>

<template>
  <div class="space-y-3">
    <Alert class="rounded-none border-love/70 bg-black">
      <AlertTitle>Trusted plugin JavaScript — full platform trust</AlertTitle>
      <AlertDescription>
        <span class="block">
          This bundle is explicitly approved by the operator and executes in the platform origin. It is not a sandbox:
          it has the same browser capabilities as the aCTFb frontend and must be reviewed as trusted code.
        </span>
        <span class="mt-1 block">No JWT, refresh cookie, service token or provider secret is passed as props.</span>
      </AlertDescription>
    </Alert>
    <div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <Badge variant="outline" class="rounded-none">{{ pluginId }}</Badge>
      <span class="break-all">integrity {{ integrity }}</span>
    </div>
    <div v-if="loading" class="border border-border px-4 py-6 text-sm text-muted-foreground">Verifying and loading trusted bundle…</div>
    <div v-else-if="error" class="border border-love/60 bg-black px-4 py-6 text-sm text-love">{{ error }}</div>
    <div ref="container" v-show="!loading && !error" />
  </div>
</template>