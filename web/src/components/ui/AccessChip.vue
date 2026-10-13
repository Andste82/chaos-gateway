<script setup lang="ts">
// Access chip (plan §2.17): Allow is blue (as configured), Drop, Reject and TCP reset are red (blocked).
import { computed } from 'vue';
const props = defineProps<{ action: 'allow' | 'drop' | 'reject' | 'reset' }>();

const labels: Record<typeof props.action, string> = { allow: 'Allow', drop: 'Drop', reject: 'Reject', reset: 'TCP reset' };
const blocking = computed(() => props.action !== 'allow');
</script>

<template>
  <span
    class="inline-block rounded border px-2 py-0.5 text-xs font-medium whitespace-nowrap"
    :class="
      blocking
        ? 'border-block-border bg-block-tint text-block-text'
        : 'border-accent-strong bg-accent-tint text-accent-text'
    "
    :data-blocking="blocking"
    >{{ labels[action] }}</span
  >
</template>
