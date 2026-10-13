<script setup lang="ts">
// Device detail (plan §2.17) with the data of Phase 2: identity, DHCP lease, active faults with
// their parameters, affected packets and remaining TTL, and the counters. Adding a fault, applying
// a profile and the panels whose backend comes later (flows view, DNS, captures, DHCP and TLS test
// actions, diagnostics) arrive with their milestones.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { ApiError } from '../api/fetcher';
import { useGetDevice } from '../api/generated/hooks/devices/devices';
import { listOverlays } from '../api/generated/hooks/overlays/overlays';
import type { Overlay } from '../api/generated/model';
import AdoptDialog from '../components/devices/AdoptDialog.vue';
import ActiveFaultList from '../components/faults/ActiveFaultList.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { fetchAll, useGatewayState, useNames, type ActiveFault } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { deviceStatus, isAdopted, originLabel, sourcesLabel } from '../lib/devices';
import { describeMatch, describeScope, overlayChips } from '../lib/faults';
import { formatAgo, formatByteRate, formatClock, formatRemaining, subnetOf } from '../lib/format';
import { useConnectionStore } from '../stores/connection';

const props = defineProps<{ id: string }>();

const device = useGetDevice(() => props.id, undefined, { query: { refetchInterval: 5_000 } });
const overlays = useQuery({
  queryKey: ['overlays', 'device', computed(() => props.id)],
  queryFn: ({ signal }) => fetchAll<Overlay>((cursor) => listOverlays({ device: props.id, limit: 500, cursor }, { signal }), signal),
  refetchInterval: 5_000,
});
const { names, networks } = useNames();
const state = useGatewayState();
const connection = useConnectionStore();
const now = useNow();

const d = computed(() => device.data.value);
const notFound = computed(() => device.error.value instanceof ApiError && device.error.value.status === 404);
const network = computed(() => networks.data.value?.find((n) => n.id === d.value?.network));
const networkConfig = computed(() => network.value?.config as { name?: string; address?: string } | undefined);
const groupNames = computed(() => (d.value?.groups ?? []).map((g) => names.value.group?.(g) ?? g));

const identity = computed(() => {
  const v = d.value;
  if (!v) return '';
  return [
    v.observed.addresses?.join(', '),
    v.observed.macs?.join(', '),
    networkConfig.value?.name ? `${networkConfig.value.name} network` : undefined,
    groupNames.value.length ? `group ${groupNames.value.join(', ')}` : undefined,
  ]
    .filter(Boolean)
    .join(' · ');
});

const faults = computed<ActiveFault[]>(() => {
  const n = names.value;
  const out: ActiveFault[] = [];
  for (const o of overlays.data.value ?? []) {
    if (o.kind === 'rule') continue;
    const own = o.target?.device === d.value?.id;
    out.push({
      id: o.id,
      layer: 'overlay',
      kind: o.kind,
      target: own ? (d.value?.name ?? '') : describeScope(o.target, n),
      destination: [own ? '' : `via ${describeScope(o.target, n)}`, describeMatch(o.fault, n)].filter(Boolean).join(' '),
      chips: overlayChips(o, n.profile),
      packets: o.counters?.packets,
      expiresAt: o.expires_at,
      state: o.state,
      overlay: o,
      widerScope: own ? undefined : describeScope(o.target, n),
    });
  }
  // configured faults that win for this device (the overlays are listed above already)
  for (const f of d.value?.effective ?? []) {
    if (f.layer !== 'config') continue;
    out.push({ id: f.id, layer: 'config', kind: 'fault', target: f.name ?? '', destination: f.name ? `fault ${f.name}` : '', chips: [f.summary ?? f.family] });
  }
  return out;
});

const lease = computed(() => d.value?.observed.lease);
const leaseLeft = computed(() => (lease.value ? new Date(lease.value.expires_at).getTime() - now.value.getTime() : 0));

const adoptOpen = ref(false);
</script>

<template>
  <p v-if="device.isLoading.value" class="text-text-2">Loading the device…</p>
  <div v-else-if="notFound" class="card" role="alert">
    <h1 class="m-0 text-xl font-semibold">No such device</h1>
    <p class="text-text-2">The gateway knows no device “{{ id }}”. A discovered device that left may be gone.</p>
    <RouterLink to="/devices">All devices</RouterLink>
  </div>
  <template v-else-if="d">
    <PageHeader :title="d.name">
      <template #breadcrumb>
        <nav aria-label="Breadcrumb" class="mb-1 text-[13px] text-text-2"><RouterLink to="/devices">Devices</RouterLink> / {{ d.name }}</nav>
      </template>
      <template #actions>
        <button
          v-if="!isAdopted(d)"
          type="button"
          class="btn btn-primary"
          :disabled="!!state.data.value?.pending_confirm || connection.offline"
          @click="adoptOpen = true"
        >
          Adopt
        </button>
      </template>
    </PageHeader>
    <div class="-mt-2 flex flex-wrap items-center gap-3 text-sm" data-testid="device-identity">
      <StatusDot v-bind="deviceStatus(d)" />
      <span class="mono text-text-2">{{ identity }}</span>
    </div>

    <div class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
      <div class="flex min-w-0 flex-col gap-5">
        <SectionCard title="Active faults">
          <p v-if="overlays.isLoading.value" class="m-0 text-sm text-text-2">Loading…</p>
          <EmptyState v-else-if="faults.length === 0" text="No active faults: this device's traffic passes unimpaired." />
          <ActiveFaultList
            v-else
            :items="faults"
            removable
            :remove-disabled="!!state.data.value?.pending_confirm || connection.offline"
          />
        </SectionCard>

        <SectionCard title="Traffic">
          <dl class="m-0 grid grid-cols-[160px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="device-counters">
            <dt class="text-text-2">Upload</dt>
            <dd class="mono m-0">{{ formatByteRate(d.observed.upload_bps) }}</dd>
            <dt class="text-text-2">Download</dt>
            <dd class="mono m-0">{{ formatByteRate(d.observed.download_bps) }}</dd>
            <dt class="text-text-2">Active connections</dt>
            <dd class="mono m-0">{{ d.observed.flows_active ?? 0 }}</dd>
            <dt class="text-text-2">Last seen</dt>
            <dd class="m-0">
              <span class="mono">{{ d.observed.last_seen ? formatClock(d.observed.last_seen) : '—' }}</span>
              <span v-if="d.observed.last_seen" class="text-text-3"> · {{ formatAgo(d.observed.last_seen, now) }}</span>
            </dd>
          </dl>
        </SectionCard>
      </div>

      <aside class="flex min-w-0 flex-col gap-5">
        <SectionCard title="Network">
          <dl class="m-0 grid grid-cols-[100px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="device-lease">
            <dt class="text-text-2">Network</dt>
            <dd class="m-0">
              {{ networkConfig?.name ?? '—' }}
              <span v-if="networkConfig?.address" class="mono text-text-3">{{ subnetOf(networkConfig.address) }}</span>
            </dd>
            <dt class="text-text-2">Lease</dt>
            <dd class="m-0">
              <template v-if="lease">
                <span class="mono">{{ lease.ip }}</span> · {{ lease.state ?? 'active' }}
                <template v-if="leaseLeft > 0"> · <span class="mono">{{ formatRemaining(leaseLeft) }}</span> left</template>
                <template v-else> · expired</template>
                <span v-if="lease.hostname" class="block text-text-3">host name {{ lease.hostname }}</span>
              </template>
              <template v-else>no DHCP lease</template>
            </dd>
            <dt class="text-text-2">Gateway</dt>
            <dd class="mono m-0">{{ networkConfig?.address?.split('/')[0] ?? '—' }}</dd>
            <dt class="text-text-2">DNS</dt>
            <dd class="m-0">
              <span class="mono">{{ networkConfig?.address?.split('/')[0] ?? '—' }}</span>
              <span v-if="networkConfig?.address" class="text-text-3"> (proxy)</span>
            </dd>
          </dl>
        </SectionCard>

        <SectionCard title="Identity">
          <dl class="m-0 grid grid-cols-[100px_1fr] gap-x-4 gap-y-2 text-sm">
            <dt class="text-text-2">Origin</dt>
            <dd class="m-0">{{ originLabel(d) }}</dd>
            <dt class="text-text-2">MAC</dt>
            <dd class="mono m-0">{{ d.observed.macs?.join(', ') || '—' }}</dd>
            <dt class="text-text-2">Addresses</dt>
            <dd class="mono m-0">{{ d.observed.addresses?.join(', ') || '—' }}</dd>
            <dt class="text-text-2">Seen by</dt>
            <dd class="m-0">{{ sourcesLabel(d) || '—' }}</dd>
            <template v-if="d.config?.fixed_ip">
              <dt class="text-text-2">Fixed IP</dt>
              <dd class="mono m-0">{{ d.config.fixed_ip }}</dd>
            </template>
            <template v-if="groupNames.length">
              <dt class="text-text-2">Groups</dt>
              <dd class="m-0">{{ groupNames.join(', ') }}</dd>
            </template>
            <template v-if="d.config">
              <dt class="text-text-2">Test CA</dt>
              <dd class="m-0">{{ d.config.trusts_test_ca ? 'trusted' : 'not trusted' }}</dd>
            </template>
          </dl>
        </SectionCard>
      </aside>
    </div>

    <AdoptDialog v-model:open="adoptOpen" :device="d" :network-name="networkConfig?.name" />
  </template>
</template>
