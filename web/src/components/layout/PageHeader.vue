<script setup lang="ts">
// The header of every page (plan §2.17, Navigation): title, context line (revision, sync state),
// health, the active-faults counter, the `</> API` toggle and the page's primary action. The API
// panel opens below the header.
import { computed } from 'vue';
import { useActiveFaults, useGatewayState, useHealth } from '../../composables/gateway';
import { errorMessage } from '../../api/fetcher';
import { formatHM } from '../../lib/format';
import { useApiPanelStore } from '../../stores/apiPanel';
import StatusDot, { type DotTone } from '../ui/StatusDot.vue';
import ApiPanel from './ApiPanel.vue';

const props = defineProps<{ title: string; context?: string }>();

const state = useGatewayState();
const health = useHealth();
const faults = useActiveFaults();
const apiPanel = useApiPanelStore();

/** "Revision 42 · applied 12:40 · kernel state in sync" */
const contextLine = computed(() => {
  if (props.context) return props.context;
  const s = state.data.value;
  if (!s) return '';
  const parts = [`Revision ${s.active_revision}`];
  const last = s.last_apply;
  if (last?.at) parts.push(`applied ${formatHM(last.at)}`);
  if (s.pending_confirm?.revision) parts.push(`revision ${s.pending_confirm.revision} waits for confirmation`);
  else if (last?.result === 'failed') parts.push('last apply failed');
  else if (last?.result === 'rolled_back') parts.push('last apply rolled back');
  else if (s.degraded_networks?.length) parts.push(`${s.degraded_networks.length} network(s) degraded`);
  else if (last) parts.push('kernel state in sync');
  return parts.join(' · ');
});

const healthDot = computed<{ tone: DotTone; label: string }>(() => {
  const h = health.data.value?.status;
  if (h === 'healthy') return { tone: 'normal', label: 'Healthy' };
  if (h === 'degraded') return { tone: 'fault', label: 'Degraded' };
  if (h === 'unhealthy') return { tone: 'block', label: 'Unhealthy' };
  return { tone: 'unknown', label: 'Health unknown' };
});

const faultCount = computed(() => faults.items.value.length);
// "No faults active" is an answer of the gateway: until it gave one, the counter does not say so
const faultsUnknown = computed(() => !!faults.error.value || faults.isLoading.value);
</script>

<template>
  <header class="flex flex-wrap items-center justify-between gap-4">
    <div class="min-w-0">
      <slot name="breadcrumb" />
      <h1 class="m-0 text-2xl font-semibold">{{ title }}</h1>
      <div class="mt-1 text-[13px] text-text-2" data-testid="context-line">{{ contextLine }}</div>
    </div>
    <div class="flex items-center gap-2.5">
      <span class="inline-flex h-8 items-center rounded-full border border-[#262e39] px-3" data-testid="health">
        <StatusDot :tone="healthDot.tone" :label="healthDot.label" />
      </span>
      <span
        v-if="faultsUnknown"
        class="inline-flex h-8 items-center rounded-full border border-[#262e39] px-3 text-[13px] text-text-2"
        data-testid="faults-counter"
        :title="faults.error.value ? errorMessage(faults.error.value) : undefined"
        >{{ faults.error.value ? 'Faults unknown' : 'Loading faults…' }}</span
      >
      <RouterLink
        v-else-if="faultCount > 0"
        :to="{ path: '/', hash: '#active-faults' }"
        class="inline-flex h-8 items-center rounded-full border border-fault-border bg-fault-tint px-3 text-[13px] text-fault-text no-underline"
        data-testid="faults-counter"
        >{{ faultCount }} {{ faultCount === 1 ? 'fault' : 'faults' }} active</RouterLink
      >
      <span
        v-else
        class="inline-flex h-8 items-center rounded-full border border-[#262e39] px-3 text-[13px] text-text-2"
        data-testid="faults-counter"
        >No faults active</span
      >
      <button
        type="button"
        class="btn mono text-[13px]"
        :aria-pressed="apiPanel.open"
        aria-controls="api-panel"
        :class="{ '!border-accent-strong !bg-accent-tint': apiPanel.open }"
        @click="apiPanel.open = !apiPanel.open"
      >
        &lt;/&gt; API
      </button>
      <slot name="actions" />
    </div>
  </header>
  <div id="api-panel" class="contents"><ApiPanel /></div>
</template>
