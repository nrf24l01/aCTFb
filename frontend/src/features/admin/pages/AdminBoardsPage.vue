<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import StatusPill from '@/components/layout/StatusPill.vue'
import BoardSettingsDialog from '@/features/admin/components/BoardSettingsDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { useSession } from '@/features/auth/session'
import { accessModeLabel, participationLabel } from '@/features/boards/accessMode'
import { formatDateTime, plural } from '@/shared/lib/format'
import type { Board } from '@/shared/types/api'

const route = useRoute()
const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const items = ref<Board[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
const createOpen = ref(false)

const hasMore = computed(() => items.value.length < total.value)

async function load(reset = true) {
  if (!isAdmin.value) return
  loading.value = true
  error.value = null
  try {
    const page = await boardsApi.listAdminBoards({
      limit: 50,
      offset: reset ? 0 : items.value.length,
      query: typeof route.query.q === 'string' ? route.query.q : undefined,
    })
    items.value = reset ? page.items : [...items.value, ...page.items]
    total.value = page.total
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function onCreated(_board: Board) {
  void load(true)
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Admin boards" description="Create and manage boards. Global administrator only.">
      <template #actions>
        <Button v-if="isAdmin" :disabled="loading" @click="createOpen = true">Create board</Button>
        <Button v-if="isAdmin" variant="outline" :disabled="loading" @click="load(true)">Refresh</Button>
      </template>
    </PageHeader>

    <EmptyState
      v-if="!isAdmin"
      title="Administrator access required"
      description="Only a global admin can create boards or list every board. Board owners use the board's own Manage link."
    />

    <template v-else>
      <LoadingState v-if="loading && items.length === 0" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load admin boards">
        <Button variant="ghost" class="mt-2" @click="load(true)">Retry</Button>
      </ErrorAlert>
      <EmptyState
        v-else-if="items.length === 0"
        title="No boards"
        description="Create a private individual board, add a TASKS module and a FLAG task, then publish."
      >
        <Button @click="createOpen = true">Create board</Button>
      </EmptyState>

      <div v-else class="space-y-2">
        <RouterLink
          v-for="board in items"
          :key="board.uuid"
          :to="`/admin/boards/${board.uuid}`"
          class="block border border-border bg-black px-4 py-3 hover:border-muted-foreground focus-visible:outline-offset-4"
        >
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="font-semibold">{{ board.name }}</h2>
              <p class="text-xs text-muted-foreground">{{ board.slug }} · {{ accessModeLabel(board.accessMode) }}</p>
            </div>
            <StatusPill :status="board.status" />
          </div>
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <Badge variant="outline" class="rounded-none">{{ participationLabel(board.participationMode) }}</Badge>
            <Badge variant="outline" class="rounded-none">{{ accessModeLabel(board.accessMode) }}</Badge>
            <Badge variant="outline" class="rounded-none">{{ board.rating.visibility }}</Badge>
            <span class="text-xs text-muted-foreground">{{ plural(board.modulesCount, 'module') }}</span>
            <span class="text-xs text-muted-foreground">{{ formatDateTime(board.startsAt) }} → {{ formatDateTime(board.endsAt) }}</span>
          </div>
        </RouterLink>
        <Button v-if="hasMore" variant="outline" :disabled="loading" @click="load(false)">
          {{ loading ? 'Loading…' : 'Load more' }}
        </Button>
      </div>
    </template>

    <BoardSettingsDialog v-model:open="createOpen" :can-manage-access-groups="true" @saved="onCreated" />
  </PageContainer>
</template>