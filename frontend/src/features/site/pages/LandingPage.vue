<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import { useSession } from '@/features/auth/session'
import { siteApi } from '@/features/site/api'
import { renderMarkdown } from '@/shared/lib/markdown'
import type { SitePublic } from '@/shared/types/api'

const { state } = useSession()
const site = ref<SitePublic | null>(null)
const loading = ref(true)
const error = ref<unknown>(null)

const authenticated = computed(() => state.status === 'authenticated')
// renderMarkdown already escapes raw HTML and allowlists URL protocols before
// this string is bound. Never pass unrendered Markdown to v-html.
const intro = computed(() => renderMarkdown(site.value?.introMarkdown ?? ''))

async function load() {
  loading.value = true
  error.value = null
  try {
    site.value = await siteApi.getPublic()
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

void load()
</script>

<template>
  <div class="mx-auto w-full max-w-3xl px-4 py-10 sm:px-6">
    <LoadingState v-if="loading" :rows="5" />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load the introduction">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else>
      <header class="border-b border-border pb-6">
        <p class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Challenge board</p>
        <h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ site?.title ?? 'Welcome' }}</h1>
      </header>

      <div v-if="site && !site.published" class="mt-6 border border-border px-4 py-3 text-sm text-muted-foreground">
        This site has no published introduction yet.
      </div>

      <!-- Sanitised by renderMarkdown (raw HTML disabled, URL protocols allowlisted). -->
      <article class="markdown-body mt-6 space-y-4 text-sm leading-relaxed" v-html="intro" />

      <div class="mt-10 flex flex-wrap gap-2 border-t border-border pt-6">
        <Button as-child>
          <RouterLink to="/boards">Browse boards</RouterLink>
        </Button>
        <Button v-if="!authenticated" as-child variant="outline">
          <RouterLink to="/sign-in">Sign in</RouterLink>
        </Button>
        <Button v-else as-child variant="outline">
          <RouterLink to="/profile">{{ state.user?.username ?? 'Profile' }}</RouterLink>
        </Button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3) {
  font-weight: 600;
  letter-spacing: -0.01em;
  margin-top: 1.25em;
}

.markdown-body :deep(h1) {
  font-size: 1.5rem;
}

.markdown-body :deep(h2) {
  font-size: 1.25rem;
}

.markdown-body :deep(h3) {
  font-size: 1.1rem;
}

.markdown-body :deep(p) {
  margin: 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 1.25rem;
  list-style: revert;
}

.markdown-body :deep(a) {
  text-decoration: underline;
  text-underline-offset: 4px;
}

.markdown-body :deep(code) {
  border: 1px solid var(--color-border);
  padding: 0.1rem 0.3rem;
}

.markdown-body :deep(pre) {
  border: 1px solid var(--color-border);
  padding: 0.75rem;
  overflow-x: auto;
}
</style>
