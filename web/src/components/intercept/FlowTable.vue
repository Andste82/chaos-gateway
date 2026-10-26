<script setup lang="ts">
// The table of the exchanges of the interception proxy (plan §2.17 "Interception UI"; M23c), newest first. The Interception screen
// shows it whole, with the device column; the device detail shows the device's latest ones without it. A row opens its details;
// which rows are open stays with the exchange, not with its place in the table, so a new exchange at the top does not move it.
import { computed, ref } from 'vue';
import type { InterceptFlow } from '../../api/generated/model';
import { flowKeys } from '../../lib/intercept';
import FlowRow from './FlowRow.vue';

const props = defineProps<{ items: InterceptFlow[]; deviceNames: (id: string) => string | undefined; now: Date; compact?: boolean }>();

const keys = computed(() => flowKeys(props.items));
const opened = ref(new Set<string>());
function toggle(key: string, v: boolean) {
  const next = new Set(opened.value);
  if (v) next.add(key);
  else next.delete(key);
  opened.value = next;
}
const cols = computed(() =>
  props.compact
    ? 'grid-cols-[84px_70px_minmax(0,1fr)_70px_minmax(0,130px)_minmax(90px,120px)_64px_80px]'
    : 'grid-cols-[96px_minmax(100px,1fr)_80px_minmax(180px,2.2fr)_80px_minmax(110px,1fr)_minmax(150px,1.1fr)_80px_80px]',
);
</script>

<template>
  <div class="overflow-x-auto" :class="compact ? '' : '-mx-5'">
    <div role="table" aria-label="Exchanges of the interception proxy" :aria-rowcount="items.length + 1" :class="compact ? 'min-w-[700px]' : 'min-w-[1060px]'" data-testid="flow-table">
      <div role="row" class="data-head grid h-10 items-center gap-3 px-4" :class="cols">
        <span role="columnheader">Time</span>
        <span v-if="!compact" role="columnheader">Device</span>
        <span role="columnheader">Kind</span>
        <span role="columnheader">Request</span>
        <span role="columnheader">Status</span>
        <span role="columnheader">Rules acted</span>
        <span role="columnheader">Size</span>
        <span role="columnheader">Took</span>
        <span role="columnheader"><span class="sr-only">Details</span></span>
      </div>
      <FlowRow
        v-for="(f, i) in items"
        :key="keys[i]"
        :flow="f"
        :device="f.device ? deviceNames(f.device) : undefined"
        :now="now"
        :compact="compact"
        :cols="cols"
        :open="opened.has(keys[i]!)"
        @update:open="toggle(keys[i]!, $event)"
      />
    </div>
  </div>
</template>
