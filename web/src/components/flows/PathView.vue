<script setup lang="ts">
// The path view of the device detail (plan §2.17 "Device detail": "path view MQTT → broker with fault badge"; M26, P3-AUDIT-37): the
// device, the gateway, the uplink and one destination of the device's connections, with the fault badge of the faults that win for
// that path (`GET /explain` for the device and the destination: one fault per family) on the way out of the device, where the gateway
// impairs it. The line is blue as configured, orange while a fault impairs the path and red while an access rule stops it; the
// sentence under it says the same in words, so the state is never in the colour alone. The destination is chosen among those of the
// device's connections, the one that moved the most first.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref, useId } from 'vue';
import { explain } from '../../api/generated/hooks/observability/observability';
import { useFlows } from '../../composables/gateway';
import { pathState, pathTargets } from '../../lib/flows';
import FaultChip from '../ui/FaultChip.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ device: { id: string; name: string } }>();

const uid = useId();
const flows = useFlows(() => ({ device: props.device.id, limit: 500 }));
const targets = computed(() => pathTargets(flows.data.value ?? []));
const chosen = ref('');
/** The destination chosen, else the first; a choice that went away with its connections falls back to the first. */
const target = computed(() => targets.value.find((t) => t.key === chosen.value) ?? targets.value[0]);

const explained = useQuery({
  queryKey: computed(() => ['explain', 'path', props.device.id, target.value?.key]),
  queryFn: ({ signal }) => {
    const t = target.value!;
    return explain({ device: props.device.id, dst: t.dst, protocol: t.protocol, ...(t.port ? { port: t.port } : {}) }, { signal });
  },
  enabled: computed(() => !!target.value),
  // a fault applied or expired changes it; the events read it at once
  refetchInterval: 5_000,
});
const state = computed(() => pathState(explained.data.value));
const tone = computed(() => (state.value.blocked ? 'block' : state.value.faults.length ? 'fault' : 'normal'));
const badge = computed(() => {
  if (state.value.blocked) return state.value.blocked;
  const f = state.value.faults;
  return f.length === 1 ? f[0]!.text : `${f.length} faults`;
});
const lineClass = computed(() => ({ normal: 'bg-accent', fault: 'bg-fault', block: 'bg-block-text' })[tone.value]);
const badgeClass = computed(() =>
  tone.value === 'block' ? 'border-block-border bg-block-tint text-block-text' : 'border-fault-border bg-fault-tint text-fault-text',
);
const sentence = computed(() => {
  const t = target.value;
  if (!t) return '';
  if (state.value.blocked) return `${state.value.blocked}: ${props.device.name} cannot reach ${t.label}.`;
  if (state.value.faults.length) return `${state.value.faults.length === 1 ? 'A fault acts' : 'Faults act'} on the way from ${props.device.name} to ${t.label}.`;
  return `No fault acts on the way: ${props.device.name}'s traffic to ${t.label} passes unimpaired.`;
});
const families: Record<string, string> = { impairment: 'Impairment', mtu: 'MTU', dns: 'DNS', tls: 'TLS', dhcp: 'DHCP', tunnel: 'Tunnel' };
</script>

<template>
  <SectionCard title="Path">
    <template v-if="targets.length > 1" #actions>
      <label class="text-text-2" :for="`${uid}-to`">To</label>
      <select :id="`${uid}-to`" v-model="chosen" class="input !h-8 max-w-[320px]" data-testid="path-target">
        <option v-for="t in targets" :key="t.key" :value="t.key">{{ t.label }}</option>
      </select>
    </template>
    <LoadError v-if="flows.error.value && !flows.data.value" :error="flows.error.value" what="The path is not drawn: the connections are not loaded" :retry="flows.refetch" />
    <p v-else-if="flows.isPending.value && !flows.data.value" class="m-0 text-sm text-text-2" role="status">Loading the connections…</p>
    <p v-else-if="!target" class="m-0 text-sm text-text-2" data-testid="path-empty">
      The path is drawn to a destination of {{ device.name }}'s connections, and the gateway tracks none of them right now.
    </p>
    <template v-else>
      <ol class="m-0 flex list-none items-center p-0" :aria-label="`The path from ${device.name} to ${target.label}`" data-testid="path">
        <li class="rounded-md border border-border-strong bg-surface-2 px-3 py-2 text-[13px]" data-testid="path-device">{{ device.name }}</li>
        <li class="relative mx-2.5 h-0.5 min-w-24 grow-[3] basis-36" :class="lineClass" :data-tone="tone" data-testid="path-line">
          <span
            v-if="tone !== 'normal'"
            class="mono absolute top-[-13px] left-1/2 h-6 max-w-[220px] -translate-x-1/2 truncate rounded-xl border px-2 text-xs leading-[22px] whitespace-nowrap"
            :class="badgeClass"
            :title="badge"
            data-testid="path-badge"
            >{{ badge }}</span
          >
        </li>
        <li class="rounded-md border border-border-strong bg-surface-2 px-3 py-2 text-[13px]" data-testid="path-gateway">Gateway</li>
        <li class="mx-2.5 h-0.5 min-w-12 grow bg-border-strong" aria-hidden="true" />
        <li class="rounded-md border border-border bg-surface-2 px-3 py-2 text-[13px]" data-testid="path-uplink">Uplink</li>
        <li class="mx-2.5 h-0.5 min-w-12 grow bg-border-strong" aria-hidden="true" />
        <li class="mono min-w-0 truncate rounded-md border border-border bg-surface-2 px-3 py-2 text-xs" :title="target.label" data-testid="path-destination">{{ target.label }}</li>
      </ol>
      <p v-if="explained.isPending.value && !explained.data.value" class="m-0 text-[13px] text-text-2" role="status">Reading what acts on the path…</p>
      <LoadError v-else-if="explained.error.value && !explained.data.value" :error="explained.error.value" what="What acts on the path is not read" :retry="explained.refetch" />
      <template v-else>
        <p class="m-0 text-[13px]" :class="tone === 'block' ? 'text-block-text' : tone === 'fault' ? 'text-fault-text' : 'text-text-2'" role="status" data-testid="path-sentence">{{ sentence }}</p>
        <ul v-if="state.faults.length" class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[13px]" data-testid="path-faults">
          <li v-for="f in state.faults" :key="f.family" class="flex items-center gap-2" data-testid="path-fault">
            <span class="text-text-2">{{ families[f.family] ?? f.family }}</span
            ><FaultChip :text="f.text" />
          </li>
        </ul>
      </template>
    </template>
  </SectionCard>
</template>
