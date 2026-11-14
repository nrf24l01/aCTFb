<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { pluginIframeOrigin } from '@/shared/config/env'
import { PluginBridge, createChannelNonce, isAllowedPluginIframeUrl } from '@/features/plugins/bridge'
import type { PluginContext } from '@/features/plugins/bridge'

const props = defineProps<{
  pluginId: string
  iframeUrl: string
  context: PluginContext
}>()

const router = useRouter()
const frame = ref<HTMLIFrameElement | null>(null)
let bridge: PluginBridge | null = null

const appHost = typeof window !== 'undefined' ? window.location.hostname : ''
const allowed = computed(() => isAllowedPluginIframeUrl(props.iframeUrl, pluginIframeOrigin, appHost))
const bridgeNonce = createChannelNonce()
const iframeSrc = computed(() => {
  if (!allowed.value) return props.iframeUrl
  const url = new URL(props.iframeUrl)
  url.hash = new URLSearchParams({ actfbChannel: bridgeNonce, pluginId: props.pluginId }).toString()
  return url.toString()
})

onMounted(() => {
  if (!frame.value || !allowed.value) return
  bridge = new PluginBridge({
    iframe: frame.value,
    pluginId: props.pluginId,
    context: props.context,
    nonce: bridgeNonce,
    navigate: (path) => void router.push(path),
  })
  bridge.start()
})

onUnmounted(() => bridge?.stop())
</script>

<template>
  <div class="space-y-3">
    <Alert class="rounded-none border-gold/60 bg-black">
      <AlertTitle>Opaque plugin iframe</AlertTitle>
      <AlertDescription>
        <span class="block">sandbox="allow-scripts allow-forms"; no allow-same-origin. The host never sends JWTs, refresh cookies, service tokens or provider secrets.</span>
        <span class="mt-1 block">trusted-js packages use a separate full-trust host and are rendered only after explicit operator approval; this iframe page remains sandboxed and receives no tokens.</span>
      </AlertDescription>
    </Alert>
    <div v-if="!allowed" class="border border-love/60 bg-black p-4 text-sm text-love">
      Plugin iframe URL rejected: it must use the configured separate origin
      <code>{{ pluginIframeOrigin }}</code>, which must differ from the app host. No package-supplied origin is trusted.
    </div>
    <div v-else class="space-y-2">
      <div class="flex flex-wrap items-center gap-2">
        <Badge variant="outline" class="rounded-none">{{ pluginId }}</Badge>
        <span class="break-all text-xs text-muted-foreground">{{ iframeUrl }}</span>
      </div>
      <iframe
        ref="frame"
        :src="iframeSrc"
        sandbox="allow-scripts allow-forms"
        referrerpolicy="no-referrer"
        class="h-[70dvh] w-full border border-border bg-black"
        title="Plugin page"
      />
    </div>
  </div>
</template>