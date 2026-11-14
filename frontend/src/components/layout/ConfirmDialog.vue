<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    description: string
    confirmLabel: string
    destructive?: boolean
    pending?: boolean
    typedName?: string | null
  }>(),
  { destructive: false, pending: false, typedName: null },
)

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'confirm'): void
}>()

const typed = ref('')
const canConfirm = computed(
  () => !props.typedName || typed.value.trim() === props.typedName.trim(),
)

watch(
  () => props.open,
  (open) => {
    if (open) typed.value = ''
  },
)
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="rounded-none border-border bg-black">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ description }}</DialogDescription>
      </DialogHeader>
      <slot />
      <div v-if="typedName" class="space-y-1.5">
        <label for="confirm-name" class="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          Type “{{ typedName }}” to confirm
        </label>
        <Input id="confirm-name" v-model="typed" autocomplete="off" />
      </div>
      <DialogFooter>
        <Button variant="ghost" :disabled="pending" @click="emit('update:open', false)">Cancel</Button>
        <Button
          :variant="destructive ? 'destructive' : 'default'"
          :disabled="!canConfirm || pending"
          @click="emit('confirm')"
        >
          {{ pending ? 'Working…' : confirmLabel }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>