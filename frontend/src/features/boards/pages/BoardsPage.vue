<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import FormField from '@/components/layout/FormField.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import BoardCard from '@/features/boards/components/BoardCard.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { useSession } from '@/features/auth/session'
import { isUuid, normalizeUuidInput } from '@/shared/lib/validate'
import type { BoardListItem } from '@/shared/types/api'

const router = useRouter()
const { state } = useSession()
const authenticated = computed(() => state.status === 'authenticated')
const items = ref<BoardListItem[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref<unknown>(null)
const joinInput = ref('')
const inviteCode = ref('')
const joinProblem = ref<string | undefined>()
const joinError = ref<unknown>(null)
const joining = ref(false)

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

async function join() {
  joinError.value = null
  const uuid = normalizeUuidInput(joinInput.value)
  joinProblem.value = isUuid(uuid) ? undefined : 'Paste the board UUID or its full /boards/<uuid> link.'
  if (joinProblem.value) return

  joining.value = true
  try {
    await boardsApi.joinBoard(uuid, inviteCode.value.trim() || undefined)
    await router.push(`/boards/${uuid}`)
  } catch (caught) {
    joinError.value = caught
  } finally {
    joining.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <PageHeader title="Boards" description="Published public boards, plus private boards you joined or where you are staff.">
      <template #actions>
        <Button variant="outline" :disabled="loading" @click="load(true)">Refresh</Button>
      </template>
    </PageHeader>

    <Alert v-if="$route.query.notice === 'registered'" class="mb-6 rounded-none border-pine/60 bg-black">
      <AlertTitle>Account created</AlertTitle>
      <AlertDescription>Check your inbox if email verification is required for this deployment.</AlertDescription>
    </Alert>

    <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
      <section aria-labelledby="board-list-heading">
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

      <section class="border border-border self-start" aria-labelledby="join-heading">
        <div class="border-b border-border px-4 py-3">
          <h2 id="join-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            Join by link
          </h2>
        </div>
        <form v-if="authenticated" class="space-y-4 px-4 py-4" novalidate @submit.prevent="join">
          <p class="text-sm text-muted-foreground">
            Paste the UUID from a board link to join an open or invite-only board.
          </p>
          <ErrorAlert v-if="joinError" :error="joinError" title="Could not join board" />
          <FormField
            label="Board UUID or link"
            for-id="join-board-uuid"
            :error="joinProblem"
            hint="Example: 4ff4c0e2-2b59-4a3f-9d9a-2b9f1f2a3c4d"
          >
            <Input
              id="join-board-uuid"
              v-model="joinInput"
              autocomplete="off"
              spellcheck="false"
              placeholder="/boards/4ff4c0e2-…"
              :aria-invalid="Boolean(joinProblem)"
            />
          </FormField>
          <FormField label="Invite code" for-id="join-board-invite" hint="Only needed when the organizer provided one.">
            <Input id="join-board-invite" v-model="inviteCode" autocomplete="off" spellcheck="false" />
          </FormField>
          <Button type="submit" class="w-full" :disabled="joining">
            {{ joining ? 'Joining…' : 'Join board' }}
          </Button>
          <p class="text-xs text-muted-foreground">
            The server decides whether joining is allowed for this board.
          </p>
        </form>
        <div v-else class="space-y-3 px-4 py-4 text-sm">
          <p class="text-muted-foreground">Sign in to join a board or submit answers.</p>
          <Button as-child class="w-full">
            <RouterLink :to="{ name: 'sign-in', query: { redirect: '/boards' } }">Sign in</RouterLink>
          </Button>
        </div>
      </section>
    </div>
  </PageContainer>
</template>