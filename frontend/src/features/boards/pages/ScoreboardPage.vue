<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ChevronLeftIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import ScoreboardTable from '@/features/boards/components/ScoreboardTable.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { isApiError } from '@/shared/api/errors'
import { formatDateTime } from '@/shared/lib/format'
import type { Board, ScoreboardItem, ScoreboardResponse } from '@/shared/types/api'

const route = useRoute()
const boardUuid = computed(() => String(route.params.boardUuid))

const board = ref<Board | null>(null)
const scoreboard = ref<ScoreboardResponse | null>(null)
const mine = ref<ScoreboardItem | null>(null)
const loading = ref(true)
const error = ref<unknown>(null)
const hidden = ref(false)

async function load() {
  loading.value = true
  error.value = null
  hidden.value = false
  try {
    const loadedBoard = await boardsApi.getBoard(boardUuid.value)
    board.value = loadedBoard
    if (loadedBoard.rating.visibility === 'HIDDEN') {
      hidden.value = true
      return
    }
    const [loadedScoreboard, loadedMine] = await Promise.all([
      boardsApi.getScoreboard(boardUuid.value),
      boardsApi.getMyScoreboardEntry(boardUuid.value).catch(() => null),
    ])
    scoreboard.value = loadedScoreboard
    mine.value = loadedMine
  } catch (caught) {
    if (isApiError(caught) && caught.code === 'RATING_HIDDEN') {
      hidden.value = true
    } else {
      error.value = caught
    }
  } finally {
    loading.value = false
  }
}

void load()
</script>

<template>
  <PageContainer>
    <div class="mb-4 text-sm">
      <RouterLink
        :to="`/boards/${boardUuid}`"
        class="inline-flex items-center gap-1 text-muted-foreground underline decoration-border underline-offset-4"
      >
        <ChevronLeftIcon aria-hidden="true" class="size-3.5 shrink-0" />
        {{ board?.name ?? 'Board' }}
      </RouterLink>
    </div>

    <PageHeader title="Scoreboard" description="Derived from accepted submissions.">
      <template #actions>
        <Button variant="outline" :disabled="loading" @click="load">Refresh</Button>
      </template>
    </PageHeader>

    <LoadingState v-if="loading" :rows="5" />

    <EmptyState
      v-else-if="hidden"
      title="Rating is hidden"
      description="The organizer hides scores and places for this board. The backend refuses this scoreboard with 403 RATING_HIDDEN."
    />

    <ErrorAlert v-else-if="error" :error="error" title="Could not load scoreboard">
      <Button variant="ghost" class="mt-2" @click="load">Retry</Button>
    </ErrorAlert>

    <template v-else-if="scoreboard">
      <div class="mb-4 grid gap-3 border border-border px-4 py-3 text-sm sm:grid-cols-3">
        <div>
          <p class="text-xs text-muted-foreground">Your score</p>
          <p class="text-lg font-semibold">{{ mine?.score ?? '—' }}</p>
        </div>
        <div>
          <p class="text-xs text-muted-foreground">Your place</p>
          <p class="text-lg font-semibold">{{ mine?.place ?? '—' }}</p>
        </div>
        <div>
          <p class="text-xs text-muted-foreground">Updated</p>
          <p>{{ formatDateTime(scoreboard.updatedAt) }}</p>
        </div>
      </div>

      <EmptyState
        v-if="scoreboard.items.length === 0"
        title="No accepted submissions yet"
        description="The scoreboard fills as participants solve tasks."
      />
      <ScoreboardTable v-else :items="scoreboard.items" />
    </template>
  </PageContainer>
</template>