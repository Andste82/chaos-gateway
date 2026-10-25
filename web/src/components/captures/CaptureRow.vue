<script setup lang="ts">
// One capture in a list (plan §2.17 "Captures"): its name, scope and state with the reason it ended early, its size,
// packets and the packets the kernel dropped, when it started and the run it belongs to, and its actions. The Captures
// screen lists them in a grid; the device detail's panel in a compact form.
import { computed } from 'vue';
import type { Capture } from '../../api/generated/model';
import type { Names } from '../../lib/faults';
import { captureState, describeCaptureScope, formatSize } from '../../lib/captures';
import { formatAgo, formatClock, formatCount } from '../../lib/format';
import StatusDot from '../ui/StatusDot.vue';
import CaptureActions from './CaptureActions.vue';

const props = defineProps<{ capture: Capture; names: Names; now: Date; compact?: boolean; cols?: string }>();

const state = computed(() => captureState(props.capture));
const title = computed(() => props.capture.name || `capture ${props.capture.id.slice(0, 8)}`);
const scope = computed(() => describeCaptureScope(props.capture.scope, props.names));
const dropped = computed(() => props.capture.dropped ?? 0);
</script>

<template>
  <div v-if="compact" class="flex flex-col gap-1.5 border-b border-border py-2.5 text-[13px] last:border-b-0" role="listitem" data-testid="capture-row" :data-capture="capture.id">
    <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
      <span class="mono min-w-0 break-words" data-testid="capture-name">{{ title }}</span>
      <StatusDot :tone="state.tone" :label="state.label" :title="state.hint" data-testid="capture-state" />
    </div>
    <div class="text-text-2">
      {{ scope }} · <span class="mono" data-testid="capture-size">{{ formatSize(capture.size_bytes ?? 0) }}</span> ·
      <span class="mono">{{ formatCount(capture.packets ?? 0) }}</span> packets · {{ formatAgo(capture.started_at, now) }}
    </div>
    <div v-if="state.early" class="text-block-text" data-testid="capture-reason">{{ state.hint }}</div>
    <CaptureActions :capture="capture" />
  </div>
  <div v-else role="row" class="data-row grid items-center gap-3 px-4 py-2.5 text-sm" :class="cols" data-testid="capture-row" :data-capture="capture.id">
    <span role="cell" class="flex min-w-0 flex-col items-start gap-0.5">
      <span class="mono max-w-full min-w-0 truncate" data-testid="capture-name">{{ title }}</span>
      <span class="mono text-xs text-text-3">{{ capture.format ?? 'pcapng' }}</span>
      <span
        v-if="capture.sensitive"
        class="rounded border border-fault-border px-1.5 text-xs text-fault-text"
        title="Holds a TLS key log or decrypted traffic: keep it safe"
        data-testid="capture-sensitive"
        >SENSITIVE</span
      >
    </span>
    <span role="cell" class="min-w-0 break-words text-text-2" data-testid="capture-scope">{{ scope }}</span>
    <span role="cell" class="flex min-w-0 flex-col items-start gap-0.5">
      <StatusDot :tone="state.tone" :label="state.label" :title="state.hint" data-testid="capture-state" />
      <span v-if="state.early" class="text-xs text-block-text" data-testid="capture-reason">{{ state.hint }}</span>
    </span>
    <span role="cell" class="mono text-[13px]" data-testid="capture-size">{{ formatSize(capture.size_bytes ?? 0) }}</span>
    <span role="cell" class="mono text-[13px]" data-testid="capture-packets">{{ formatCount(capture.packets ?? 0) }}</span>
    <span role="cell" class="mono text-[13px]" :class="dropped ? 'text-block-text' : 'text-text-3'" :title="dropped ? 'Packets the kernel dropped because the reader was too slow: the capture has gaps' : undefined" data-testid="capture-dropped">{{ formatCount(dropped) }}</span>
    <span role="cell" class="text-[13px] text-text-2">
      <span class="mono">{{ formatClock(capture.started_at) }}</span>
      <span class="block text-xs text-text-3">{{ formatAgo(capture.started_at, now) }}</span>
    </span>
    <span role="cell" class="min-w-0 text-[13px]" data-testid="capture-run">
      <RouterLink v-if="capture.run" :to="`/runs/${capture.run}`" :aria-label="`Open the run of ${title}`">Run {{ capture.run.slice(0, 8) }}</RouterLink>
      <span v-else class="text-text-3">—</span>
    </span>
    <span role="cell" class="flex justify-end"><CaptureActions :capture="capture" /></span>
  </div>
</template>
