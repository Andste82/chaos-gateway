<script setup lang="ts">
// The network topology of the overview (plan §2.17): test networks → gateway → uplink, left to
// right like the traffic, with a fault badge on the path while faults are active. Management
// networks (a WireGuard hub of role management) are no test networks and faults never apply to
// them: they get a row of their own below the gateway, off the faulted path (P3-AUDIT-38).
import { computed } from 'vue';
import { useGetUplink } from '../../api/generated/hooks/config/config';
import type { NetworkView } from '../../api/generated/model';
import type { ActiveFault } from '../../composables/gateway';
import { errorMessage } from '../../api/fetcher';
import { subnetOf } from '../../lib/format';
import SectionCard from '../ui/SectionCard.vue';
import StatusDot, { type DotTone } from '../ui/StatusDot.vue';

const props = defineProps<{
  networks: NetworkView[];
  /** The networks could not be loaded: the card must not say there are none. */
  networksError?: unknown;
  /** The networks are still loading: the card must not say there are none yet. */
  networksLoading?: boolean;
  deviceCounts: Map<string, number>;
  faults: ActiveFault[];
  /** The faults could not be loaded: their count is unknown. */
  faultsError?: unknown;
}>();

const uplink = useGetUplink();

interface Box {
  id: string;
  name: string;
  detail: string;
  meta: string;
  degraded: boolean;
  management: boolean;
}

const allBoxes = computed<Box[]>(() =>
  props.networks.map((n) => {
    const c = n.config as { type: string; name: string; address?: string; dhcp?: { enabled?: boolean }; role?: string };
    const management = c.type === 'wireguard' && c.role === 'management';
    const devices = props.deviceCounts.get(n.id) ?? 0;
    const tags = management ? [] : [`${devices} ${devices === 1 ? 'device' : 'devices'}`];
    if (c.type === 'lan' && c.dhcp?.enabled !== false) tags.push('DHCP');
    if (c.type === 'wireguard') tags.push(`WireGuard${management ? ' · management' : ''}`);
    return {
      id: n.id,
      name: c.name,
      detail: [subnetOf(c.address), n.status.interface].filter(Boolean).join(' · '),
      meta: tags.join(' · '),
      degraded: n.status.state === 'degraded',
      management,
    };
  }),
);
const boxes = computed(() => allBoxes.value.filter((b) => !b.management));
const managementBoxes = computed(() => allBoxes.value.filter((b) => b.management));

const profiles = computed(() => props.faults.filter((f) => f.kind === 'profile'));
const faultCount = computed(() => props.faults.length);
const up = computed(() => uplink.data.value?.status);
// no status is claimed without the gateway's answer: loading and failed read as such, not as "up"
const uplinkDot = computed<{ tone: DotTone; label: string }>(() => {
  if (!uplink.data.value) {
    if (uplink.error.value) return { tone: 'unknown', label: `Uplink not loaded: ${errorMessage(uplink.error.value)}` };
    return { tone: 'unknown', label: 'Loading…' };
  }
  if (up.value?.link_up === false) return { tone: 'block', label: 'no link' };
  return { tone: 'normal', label: up.value?.gateway ? `via ${up.value.gateway}` : 'up' };
});
</script>

<template>
  <SectionCard title="Network">
    <template #actions><span class="text-xs text-text-3">Traffic flows left to right</span></template>
    <div class="flex items-center" data-testid="topology">
      <div class="flex flex-col gap-2.5">
        <div v-for="b in boxes" :key="b.id" class="w-[210px] rounded-lg border border-[#2a323d] bg-surface-2 px-3.5 py-3" data-testid="topology-network">
          <RouterLink :to="`/networks/${b.id}`" class="block text-base font-semibold text-text no-underline hover:underline">{{ b.name }}</RouterLink>
          <div class="mono mt-0.5 text-xs text-text-2">{{ b.detail }}</div>
          <div class="mt-1.5 text-[13px] text-text-2">{{ b.meta }}</div>
          <StatusDot v-if="b.degraded" class="mt-1" tone="block" label="degraded: a port is missing" />
        </div>
        <div
          v-if="boxes.length === 0 && networksError"
          class="w-[210px] rounded-lg border border-dashed border-block-border px-3.5 py-3 text-[13px] text-block-text"
          data-testid="topology-networks-error"
        >
          Networks not loaded: {{ errorMessage(networksError) }}
        </div>
        <div
          v-else-if="boxes.length === 0 && networksLoading"
          class="w-[210px] rounded-lg border border-dashed border-border-strong px-3.5 py-3 text-[13px] text-text-2"
          role="status"
          data-testid="topology-networks-loading"
        >
          Loading the networks…
        </div>
        <div v-else-if="boxes.length === 0" class="w-[210px] rounded-lg border border-dashed border-border-strong px-3.5 py-3 text-[13px] text-text-2">
          No test network
          <RouterLink to="/networks" class="mt-1 block">Add a test network</RouterLink>
        </div>
      </div>
      <div class="relative mx-3 h-0.5 flex-1 bg-border-strong">
        <span
          v-if="faultsError"
          class="absolute -top-3.5 left-1/2 h-[26px] -translate-x-1/2 rounded-full border border-border-strong bg-surface-2 px-2.5 text-xs leading-[26px] whitespace-nowrap text-text-2"
          data-testid="path-fault-badge"
          >faults unknown</span
        >
        <span
          v-else-if="faultCount > 0"
          class="absolute -top-3.5 left-1/2 h-[26px] -translate-x-1/2 rounded-full border border-fault-border bg-fault-tint px-2.5 text-xs leading-[26px] whitespace-nowrap text-fault-text"
          data-testid="path-fault-badge"
          >↑↓ {{ faultCount }} {{ faultCount === 1 ? 'fault' : 'faults' }} on the path</span
        >
      </div>
      <div class="w-[250px] rounded-lg border border-[#3a4552] bg-surface-2 px-4 py-3.5" data-testid="topology-gateway">
        <RouterLink to="/linux" class="block text-base font-semibold text-text no-underline hover:underline">Chaos Gateway</RouterLink>
        <div class="mt-1.5 text-[13px] leading-relaxed text-text-2">
          <template v-if="networksError && !networks.length">networks unknown</template>
          <template v-else-if="networksLoading && !networks.length">networks loading</template>
          <template v-else
            >{{ boxes.length }} test {{ boxes.length === 1 ? 'network' : 'networks'
            }}<template v-if="managementBoxes.length"> · {{ managementBoxes.length }} management</template></template
          >
          ·
          <template v-if="faultsError">faults unknown</template>
          <template v-else>{{ faultCount }} {{ faultCount === 1 ? 'fault' : 'faults' }}</template>
          <template v-for="p in profiles" :key="p.id"><br />{{ p.chips[0] }} on {{ p.target }}</template>
        </div>
      </div>
      <div class="mx-3 h-0.5 flex-1 bg-border-strong" />
      <div class="w-[200px] rounded-lg border border-[#2a323d] bg-surface-2 px-3.5 py-3" data-testid="topology-uplink">
        <RouterLink to="/networks" class="block text-base font-semibold text-text no-underline hover:underline">Uplink</RouterLink>
        <div class="mono mt-0.5 text-xs text-text-2">{{ [up?.address, up?.interface].filter(Boolean).join(' · ') || '—' }}</div>
        <StatusDot class="mt-1.5" v-bind="uplinkDot" />
      </div>
    </div>
    <!-- management access: below the gateway, off the faulted path, no fault badge -->
    <div v-if="managementBoxes.length" class="mt-4 flex items-start gap-3 border-t border-border pt-3" data-testid="topology-management">
      <div class="w-[210px] pt-3 text-xs text-text-2">Management access · no faults apply</div>
      <div class="flex flex-wrap gap-2.5">
        <div v-for="b in managementBoxes" :key="b.id" class="w-[210px] rounded-lg border border-dashed border-border-strong bg-surface-2 px-3.5 py-3" data-testid="topology-management-network">
          <RouterLink :to="`/networks/${b.id}`" class="block text-base font-semibold text-text no-underline hover:underline">{{ b.name }}</RouterLink>
          <div class="mono mt-0.5 text-xs text-text-2">{{ b.detail }}</div>
          <div class="mt-1.5 text-[13px] text-text-2">{{ b.meta }}</div>
          <StatusDot v-if="b.degraded" class="mt-1" tone="block" label="degraded: a port is missing" />
        </div>
      </div>
    </div>
  </SectionCard>
</template>
