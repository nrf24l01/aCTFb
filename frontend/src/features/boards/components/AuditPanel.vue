<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import EmptyState from '@/components/layout/EmptyState.vue'
import ErrorAlert from '@/components/layout/ErrorAlert.vue'
import LoadingState from '@/components/layout/LoadingState.vue'
import { boardsApi } from '@/features/boards/api/boards'
import { formatDateTime } from '@/shared/lib/format'
import type { BoardAuditEvent } from '@/shared/types/api'

const props = defineProps<{ boardUuid: string }>()
const items = ref<BoardAuditEvent[]>([])
const loading = ref(true)
const error = ref<unknown>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    const page = await boardsApi.listBoardAudit(props.boardUuid, { limit: 100 })
    items.value = page.items
  } catch (caught) {
    error.value = caught
  } finally {
    loading.value = false
  }
}

void load()
</script>

<template>
  <section class="border border-border" aria-labelledby="audit-heading">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
      <h2 id="audit-heading" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Audit</h2>
      <Button variant="outline" size="sm" :disabled="loading" @click="load">Refresh</Button>
    </div>
    <div class="px-4 py-4">
      <LoadingState v-if="loading" :rows="3" />
      <ErrorAlert v-else-if="error" :error="error" title="Could not load audit events" />
      <EmptyState v-else-if="items.length === 0" title="No audit events" description="Role grants and revocations appear here." />
      <div v-else class="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow class="border-border hover:bg-transparent">
              <TableHead>When</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Resource</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="event in items" :key="event.uuid" class="border-border">
              <TableCell class="whitespace-nowrap text-xs text-muted-foreground">{{ formatDateTime(event.createdAt) }}</TableCell>
              <TableCell>{{ event.actor.username }}</TableCell>
              <TableCell>{{ event.action }}</TableCell>
              <TableCell class="text-xs text-muted-foreground">
                {{ event.resourceType }}<span v-if="event.moduleUuid"> · module {{ event.moduleUuid }}</span>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>
  </section>
</template>