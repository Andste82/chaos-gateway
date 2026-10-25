<script setup lang="ts">
// One exchange of the interception proxy (plan §2.17 "Interception UI"; M23c): when, which device, what kind (HTTP, WebSocket, a stream
// that is no HTTP), what it asked for, how it ended, the rules that acted on it (a word each, never by color alone), its size and
// duration, and under "Details" the rest the gateway logged (the client, the address the device meant, the whole path, the rule's case,
// why it ended without an answer). The table of the screen and the compact list of the device detail use the same row.
import { computed, useId } from 'vue';
import type { InterceptFlow } from '../../api/generated/model';
import { actionLabels, durationText, kindLabels, outcomeText, requestText, sizeText } from '../../lib/intercept';
import { formatAgo, formatClock } from '../../lib/format';
import DisclosureButton from '../ui/DisclosureButton.vue';

const props = defineProps<{ flow: InterceptFlow; device?: string; now: Date; compact?: boolean; cols?: string }>();
const open = defineModel<boolean>('open', { default: false });
const uid = useId();

const kind = computed(() => kindLabels[props.flow.protocol]);
const acted = computed(() => (props.flow.actions ?? []).map((a) => ({ key: a, ...actionLabels[a] })));
const outcome = computed(() => outcomeText(props.flow));
const failed = computed(() => props.flow.status !== undefined && props.flow.status >= 500);
</script>

<template>
  <div class="data-row" :class="{ 'py-2.5': compact }" role="row" data-testid="flow-row" :data-kind="flow.protocol" :data-acted="acted.length ? 'true' : 'false'">
    <div class="grid items-center gap-3 px-4 py-2 text-sm" :class="cols">
      <span role="cell" class="text-[13px] text-text-2">
        <span class="mono" data-testid="flow-time">{{ formatClock(flow.time) }}</span>
        <span class="block text-xs text-text-3">{{ formatAgo(flow.time, now) }}</span>
      </span>
      <span v-if="!compact" role="cell" class="min-w-0 truncate" data-testid="flow-device">
        <RouterLink v-if="flow.device" :to="`/devices/${flow.device}`" class="text-text no-underline hover:underline">{{ device ?? flow.device }}</RouterLink>
        <span v-else class="mono text-text-2" :title="'No device of the gateway has this address'">{{ flow.client }}</span>
      </span>
      <span role="cell" class="text-[13px] text-text-2" :title="kind.hint" data-testid="flow-kind">{{ kind.label }}</span>
      <span role="cell" class="mono min-w-0 break-words text-[13px]" data-testid="flow-request">{{ requestText(flow) }}</span>
      <span role="cell" class="mono text-[13px]" :class="failed ? 'text-block-text' : ''" data-testid="flow-status">{{ outcome }}</span>
      <span role="cell" class="flex min-w-0 flex-wrap items-center gap-1" data-testid="flow-acted">
        <span
          v-for="a in acted"
          :key="a.key"
          class="mono rounded border px-1.5 py-0.5 text-xs"
          :class="a.blocking ? 'border-block-border bg-block-tint text-block-text' : 'border-fault-border bg-fault-tint text-fault-text'"
          :data-action="a.key"
          >{{ a.label }}</span
        >
        <span v-if="!acted.length" class="text-text-3">—</span>
      </span>
      <span role="cell" class="mono text-xs text-text-2" data-testid="flow-size">{{ sizeText(flow) }}</span>
      <span role="cell" class="mono text-[13px] text-text-2" data-testid="flow-duration">{{ durationText(flow) }}</span>
      <span role="cell" class="flex justify-end">
        <DisclosureButton v-model:open="open" :controls="`${uid}-details`" small>Details</DisclosureButton>
      </span>
    </div>
    <dl v-if="open" :id="`${uid}-details`" class="m-0 mb-2 grid grid-cols-[150px_1fr] gap-x-4 gap-y-1 px-4 text-[13px]" data-testid="flow-details">
      <dt class="text-text-2">Time</dt>
      <dd class="mono m-0">{{ flow.time }}</dd>
      <dt class="text-text-2">Client</dt>
      <dd class="mono m-0">{{ flow.client }}</dd>
      <dt class="text-text-2">The device meant</dt>
      <dd class="mono m-0">{{ flow.original_dst }}</dd>
      <template v-if="flow.path !== undefined">
        <dt class="text-text-2">Path</dt>
        <dd class="mono m-0 break-all">{{ flow.path }}</dd>
      </template>
      <template v-if="flow.overlay">
        <dt class="text-text-2">Case</dt>
        <dd class="mono m-0">{{ flow.overlay }}</dd>
      </template>
      <dt class="text-text-2">Answer</dt>
      <dd class="m-0">{{ flow.status !== undefined ? `status ${flow.status}` : 'none' }}</dd>
      <template v-if="flow.error">
        <dt class="text-text-2">Ended with</dt>
        <dd class="m-0 text-block-text" data-testid="flow-error">{{ flow.error }}</dd>
      </template>
    </dl>
  </div>
</template>
