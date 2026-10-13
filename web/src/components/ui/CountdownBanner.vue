<script setup lang="ts">
// Countdown banner (plan §2.17, commit-confirm): orange, with a live countdown that screen readers
// hear (aria-live), and the actions in the slot. It emits `expired` once when the deadline passes.
import { computed, watch } from 'vue';
import { useNow } from '../../composables/useNow';
import { formatRemaining } from '../../lib/format';

const props = defineProps<{ deadline: string | Date; text: string }>();
const emit = defineEmits<{ expired: [] }>();

const now = useNow(250);
const remaining = computed(() => new Date(props.deadline).getTime() - now.value.getTime());
watch(
  () => remaining.value <= 0,
  (over) => {
    if (over) emit('expired');
  },
  { immediate: true },
);
</script>

<template>
  <div
    role="region"
    aria-label="Waiting for confirmation"
    class="flex items-center justify-between gap-4 border-b border-fault-border bg-fault-tint px-6 py-3 text-fault-text"
    data-testid="countdown-banner"
  >
    <div class="text-sm">
      {{ text }}
      <span class="mono ml-2 font-medium" role="timer" aria-live="polite" aria-atomic="true"
        >{{ formatRemaining(remaining) }} left</span
      >
    </div>
    <div class="flex gap-2"><slot /></div>
  </div>
</template>
