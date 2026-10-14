<script setup lang="ts">
// The kernel state was read before, but the last read again failed (an executor that went away while
// the gateway still answers): the old counters stay on screen, greyed out, and this says that they
// are not live and since when (plan §2.17 States: an error is never hidden behind old data).
import { computed } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { formatClock } from '../../lib/format';

const props = defineProps<{ error: unknown; readAt: number }>();
const text = computed(() => `Not updated: ${errorMessage(props.error)}; last read ${props.readAt ? formatClock(new Date(props.readAt)) : '—'}.`);
</script>

<template>
  <p class="m-0 rounded-md border border-block-border bg-block-tint px-3 py-2 text-[13px] text-block-text" role="status" data-testid="linux-stale">
    {{ text }}
  </p>
</template>
