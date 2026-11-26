<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import { useSession } from '@/features/auth/session'
import { siteApi } from '@/features/site/api'
import { renderMarkdown } from '@/shared/lib/markdown'
import { SITE_INTRO_MAX_CHARACTERS, validateDescriptionLength } from '@/shared/lib/text'
import { MarkdownEditorLazy } from '@/shared/components/MarkdownEditorLazy'
import { isApiError } from '@/shared/api/errors'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const form = reactive({
  title: '',
  introMarkdown: '',
  published: true,
})
const loading = ref(true)
const error = ref<unknown>(null)
const saveError = ref<unknown>(null)
const problems = ref<Record<string, string | undefined>>({})
const pending = ref(false)
const saved = ref(false)
const preview = computed(() => renderMarkdown(form.introMarkdown))
// One live message, owned by the page form: it stays visible on both tabs, so
// an over-limit save from Preview is never a silent no-op.
const descriptionError = computed(() =>
  validateDescriptionLength(form.introMarkdown, SITE_INTRO_MAX_CHARACTERS),
)

async function load() {
  if (!isAdmin.value) {
    loading.value = false
    return
  }
  loading.value = true
  error.value = null
  try {
    const site = await siteApi.adminGet()
    form.title = site.title
    form.introMarkdown = site.introMarkdown
    form.published = site.published
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

async function save() {
  saveError.value = null
  saved.value = false
  problems.value = {
    title: form.title.trim() ? undefined : 'Enter a title.',
    description: validateDescriptionLength(form.introMarkdown, SITE_INTRO_MAX_CHARACTERS),
  }
  if (Object.values(problems.value).some(Boolean)) return

  pending.value = true
  try {
    const site = await siteApi.update({
      title: form.title.trim(),
      introMarkdown: form.introMarkdown,
      published: form.published,
    })
    form.title = site.title
    form.introMarkdown = site.introMarkdown
    form.published = site.published
    saved.value = true
  } catch (caught) {
    if (isApiError(caught)) {
      problems.value = { title: caught.fieldIssue('title') }
    }
    saveError.value = caught
  } finally {
    pending.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Site" description="Organizer-controlled introduction shown on the public landing page." />

    <div v-if="!isAdmin" class="border border-border px-4 py-3 text-sm text-muted-foreground">
      A global admin can edit the site introduction.
    </div>

    <template v-else>
      <LoadingState v-if="loading" :rows="5" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load site settings">
        <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
      </ErrorAlert>

      <form v-else class="max-w-3xl space-y-5" novalidate @submit.prevent="save">
        <ErrorAlert v-if="saveError" :error="saveError" title="Could not save site settings" />

        <FormField label="Title" for-id="site-title" :error="problems.title">
          <Input id="site-title" v-model="form.title" maxlength="128" />
        </FormField>

        <div class="space-y-2">
          <p class="text-sm font-medium">Introduction</p>
          <p
            v-if="descriptionError"
            id="site-intro-error"
            role="alert"
            class="text-xs text-status-error-ink"
          >
            {{ descriptionError }}
          </p>
          <Tabs default-value="write">
            <TabsList variant="line">
              <TabsTrigger value="write">Write</TabsTrigger>
              <TabsTrigger value="preview">Preview</TabsTrigger>
            </TabsList>
            <TabsContent value="write">
              <MarkdownEditorLazy
                id="site-intro"
                v-model="form.introMarkdown"
                label="Introduction Markdown"
                :max-length="SITE_INTRO_MAX_CHARACTERS"
                placeholder="# Welcome&#10;&#10;Write the CTF introduction in Markdown."
                :show-length-error="false"
                :invalid="Boolean(descriptionError)"
                described-by="site-intro-error"
              />
              <p class="mt-1 text-xs text-muted-foreground">
                Markdown only. Raw HTML is escaped and links are limited to http, https, mailto and relative paths.
              </p>
            </TabsContent>
            <TabsContent value="preview">
              <div class="min-h-40 border border-border px-4 py-3">
                <!-- Sanitised by renderMarkdown. -->
                <article class="markdown-body space-y-3 text-sm leading-relaxed" v-html="preview" />
                <p v-if="!form.introMarkdown.trim()" class="text-sm text-muted-foreground">Nothing to preview yet.</p>
              </div>
            </TabsContent>
          </Tabs>
        </div>

        <label class="flex min-h-11 items-center gap-2 text-sm">
          <Checkbox v-model="form.published" />
          Published on the public landing page
        </label>

        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="pending">{{ pending ? 'Saving…' : 'Save changes' }}</Button>
          <span v-if="saved" class="text-xs text-muted-foreground">Saved.</span>
        </div>
      </form>
    </template>
  </PageContainer>
</template>
