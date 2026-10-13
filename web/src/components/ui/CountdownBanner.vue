<script setup lang="ts">
// Countdown banner (plan §2.17, commit-confirm): orange, with a live countdown and the actions in
// the slot. The timer itself is not a live region (screen readers would read it every second);
// a hidden live region announces the milestones instead: 30 s left, 10 s left, time is up. The
// countdown runs on the gateway's clock (useNow). It emits `expired` once when the deadline passes.
import { computed, ref, watch } from 'vue';
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

/** The milestones screen readers hear, from the last (0) to before the first (3). */
const milestones = [0, 10_000, 30_000];
const stage = computed(() => {
  const i = milestones.findIndex((m) => remaining.value <= m);
  return i < 0 ? milestones.length : i;
});
const announcement = ref('');
watch(stage, (s, before) => {
  if (s >= before) return;
  announcement.value = s === 0 ? 'The time to confirm is up.' : `${Math.ceil(remaining.value / 1000)} seconds left to confirm.`;
});
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
      <span class="mono ml-2 font-medium" role="timer" aria-live="off">{{ formatRemaining(remaining) }} left</span>
      <span class="sr-only" aria-live="polite" aria-atomic="true" data-testid="countdown-announcement">{{ announcement }}</span>
    </div>
    <div class="flex gap-2"><slot /></div>
  </div>
</template>
