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
import BoardCard from '@/features/boards/components/BoardCard.vue'
import JoinBoardDialog from '@/features/boards/components/JoinBoardDialog.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { useSession } from '@/features/auth/session'
import type { BoardJoinResponse, BoardListItem } from '@/shared/types/api'

const router = useRouter()
const { state } = useSession()
const authenticated = computed(() => state.status === 'authenticated')
const items = ref<BoardListItem[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<unknown>(null)
const joinOpen = ref(false)

const hasMore = computed(() => items.value.length < total.value)

async function load(reset = true) {
  loading.value = true
  error.value = null
  try {
    const page = await boardsApi.listBoards({ limit: 50, offset: reset ? 0 : items.value.length })
    items.value = reset ? page.items : [...items.value, ...page.items]
    total.value = page.total
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

function openJoin() {
  if (!authenticated.value) {
    void router.push({ name: 'sign-in', query: { redirect: '/boards' } })
    return
  }
  joinOpen.value = true
}

async function onJoined(result: BoardJoinResponse) {
  joinOpen.value = false
  await router.push(`/boards/${result.boardUuid}`)
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Boards" description="Published public boards, plus private boards you joined or where you are staff.">
      <template #actions>
        <Button :disabled="loading" @click="openJoin">Join a board</Button>
        <Button variant="outline" :disabled="loading" @click="load(true)">Refresh</Button>
      </template>
    </PageHeader>

    <Alert v-if="$route.query.notice === 'registered'" class="mb-6 rounded-none border-pine/60 bg-black">
      <AlertTitle>Account created</AlertTitle>
      <AlertDescription>Check your inbox if email verification is required for this deployment.</AlertDescription>
    </Alert>

    <div class="workspace-layout" data-slot="workspace">
      <section class="workspace-main" data-slot="workspace-main" aria-labelledby="board-list-heading">
        <h2 id="board-list-heading" class="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Your boards
        </h2>

        <LoadingState v-if="loading && items.length === 0" />
        <ErrorAlert v-else-if="error" :error="error" title="Could not load boards">
          <Button variant="ghost" class="mt-2" @click="load(true)">Retry</Button>
        </ErrorAlert>
        <EmptyState
          v-else-if="items.length === 0"
          title="No boards yet"
          description="Public boards appear here when they are published. Private boards are not listed until you join."
        />
        <div v-else class="space-y-3">
          <BoardCard v-for="board in items" :key="board.uuid" :board="board" />
          <Button v-if="hasMore" variant="outline" :disabled="loading" @click="load(false)">
            {{ loading ? 'Loading…' : 'Load more' }}
          </Button>
        </div>
      </section>

      <section class="workspace-aside border border-border self-start" data-slot="workspace-aside" aria-labelledby="join-heading">
        <div class="border-b border-border px-4 py-3">
          <h2 id="join-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Join a board
          </h2>
        </div>
        <div class="space-y-3 px-4 py-4 text-sm">
          <p class="text-muted-foreground">
            Enter the board code from the organizer. Public boards stay browsable, but joining always needs a code.
          </p>
          <Button v-if="authenticated" class="w-full" @click="openJoin">Enter board code</Button>
          <Button v-else as-child class="w-full">
            <RouterLink :to="{ name: 'sign-in', query: { redirect: '/boards' } }">Sign in to join</RouterLink>
          </Button>
        </div>
      </section>
    </div>

    <JoinBoardDialog v-model:open="joinOpen" @joined="onJoined" />
  </PageContainer>
</template>
