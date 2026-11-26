<script setup lang="ts">
import { Button } from '@/components/ui/button'

/**
 * Shown when the editor chunk fails to load. It is a notice with an explicit
 * reload action, never an editable surface: a partially loaded editor could let
 * an organizer save a stale description. Reloading is the only safe recovery.
 */
const reload = (): void => {
  window.location.reload()
}
</script>

<template>
  <div
    data-slot="markdown-editor-error"
    role="alert"
    class="border border-status-attention-border bg-status-attention-surface px-2.5 py-2 text-xs text-status-attention-ink"
  >
    The Markdown editor could not load. Your saved text is unchanged.
    <Button variant="link" size="xs" type="button" class="h-auto px-1 text-xs" @click="reload">
      Reload
    </Button>
  </div>
</template>
