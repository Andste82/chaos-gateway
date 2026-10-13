<script lang="ts">
export type DotTone = 'normal' | 'fault' | 'block' | 'offline' | 'unknown' | 'probe';
</script>

<script setup lang="ts">
// Status dot + label (plan §2.17): status is never shown by color alone, the label always says it.
import { computed } from 'vue';

const props = defineProps<{ tone: DotTone; label: string }>();

const color = computed(
  () =>
    ({
      normal: 'var(--color-accent)',
      fault: 'var(--color-fault)',
      block: 'var(--color-block-text)',
      offline: 'var(--color-offline)',
      unknown: 'var(--color-unknown)',
      probe: 'var(--color-probe)',
    })[props.tone],
);
</script>

<template>
  <span class="inline-flex items-center gap-2 text-[13px] text-text-2" :data-tone="tone">
    <span class="inline-block h-2 w-2 shrink-0 rounded-full" :style="{ background: color }" aria-hidden="true" />
    {{ label }}
  </span>
</template>
