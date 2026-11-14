<script setup lang="ts">
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { ScoreboardItem } from '@/shared/types/api'

defineProps<{ items: ScoreboardItem[] }>()
</script>

<template>
  <div class="overflow-x-auto border border-border">
    <Table>
      <TableHeader>
        <TableRow class="border-border hover:bg-transparent">
          <TableHead class="w-16">Place</TableHead>
          <TableHead>Subject</TableHead>
          <TableHead class="text-right">Score</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="entry in items" :key="`${entry.subject.type}:${entry.subject.uuid}`" class="border-border">
          <TableCell class="font-semibold">{{ entry.place }}</TableCell>
          <TableCell>
            {{ entry.subject.name }}
            <span class="text-xs text-muted-foreground">{{ entry.subject.type }}</span>
          </TableCell>
          <TableCell class="text-right">{{ entry.score }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
</template>