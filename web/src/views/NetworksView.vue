<script setup lang="ts">
// Networks (plan §2.17 "Networks", M14): cards for the test networks, the WireGuard networks, the
// management network (owned by the OS) and the uplink; the settings of the selected network (subnet,
// gateway, attachment, DHCP with its pool and leases, DNS, IPv6 blocked; for a WireGuard hub its clients
// with their status, the add-client dialog and the QR code of a client's configuration; for a link its
// peer); the access matrix between the networks, whose changes go through the preview and the apply like
// any other (commit-confirm only when the gateway says the change could lock the admin out). The routing
// view and the technical view are pages of their own.
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { errorMessage } from '../api/fetcher';
import { useGetUplink } from '../api/generated/hooks/config/config';
import type { LanNetwork, NetworkView, WireGuardNetwork } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import AccessMatrix from '../components/networks/AccessMatrix.vue';
import ClientsCard from '../components/networks/ClientsCard.vue';
import LeasesCard from '../components/networks/LeasesCard.vue';
import NetworkSettings from '../components/networks/NetworkSettings.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { firstError, useActiveConfig, useDevices, useNetworks } from '../composables/gateway';
import { subnetText } from '../lib/networks';
import { useApiScreen } from '../stores/apiPanel';

const props = defineProps<{ id?: string }>();

const networks = useNetworks();
const devices = useDevices();
const uplink = useGetUplink();
const active = useActiveConfig();
const route = useRoute();

const list = computed(() => networks.data.value ?? []);
const selected = computed<NetworkView | undefined>(() => {
  const id = props.id;
  return list.value.find((n) => n.id === id || (n.config as { name: string }).name === id) ?? (id ? undefined : list.value[0]);
});

useApiScreen('the networks', () => {
  const calls = [
    { method: 'GET' as const, path: '/networks', note: 'local test networks and WireGuard networks with their status' },
    { method: 'GET' as const, path: '/uplink' },
    { method: 'GET' as const, path: '/revisions/active', note: 'the access matrix of the active revision' },
  ];
  const n = selected.value;
  if (n?.config.type === 'lan') calls.push({ method: 'GET', path: `/networks/${n.id}/leases`, note: 'the DHCP leases' });
  if (n?.config.type === 'wireguard' && (n.config as WireGuardNetwork).kind === 'hub')
    calls.push({ method: 'GET', path: `/networks/${n.id}/clients`, note: 'the clients with their status' });
  return calls;
});

const deviceCounts = computed(() => {
  const m = new Map<string, number>();
  for (const d of devices.data.value ?? []) if (d.network) m.set(d.network, (m.get(d.network) ?? 0) + 1);
  return m;
});

interface Card {
  id: string;
  name: string;
  tag: string;
  addr: string;
  meta: string;
  degraded: boolean;
}

const cards = computed<Card[]>(() =>
  list.value.map((n) => {
    const devs = deviceCounts.value.get(n.id) ?? 0;
    if (n.config.type === 'lan') {
      const c = n.config as LanNetwork;
      const meta = [`${devs} ${devs === 1 ? 'device' : 'devices'}`, c.dhcp?.enabled === false ? 'no DHCP' : 'DHCP', 'DNS'];
      return { id: n.id, name: c.name, tag: 'test network', addr: subnetText(c.address), meta: meta.join(' · '), degraded: n.status.state === 'degraded' };
    }
    const w = n.config as WireGuardNetwork;
    const meta =
      w.kind === 'hub'
        ? `${n.status.peers_online ?? 0} of ${n.status.peers_total ?? Object.keys(w.clients ?? {}).length} clients online`
        : n.status.link_peer?.online
          ? 'peer online'
          : 'peer offline';
    const role = w.role === 'management' ? ' · management' : '';
    return { id: n.id, name: w.name, tag: `WireGuard ${w.kind}${role}`, addr: subnetText(w.address), meta, degraded: n.status.state === 'degraded' };
  }),
);

const management = computed(() => active.data.value?.configuration.management);
const up = computed(() => uplink.data.value);
const unknownNetwork = computed(() => !!props.id && list.value.length > 0 && !selected.value);
const hub = computed(() => {
  const n = selected.value;
  return n?.config.type === 'wireguard' && (n.config as WireGuardNetwork).kind === 'hub' ? n : undefined;
});
</script>

<template>
  <PageHeader title="Networks">
    <template #actions>
      <RouterLink class="btn" to="/networks/routing">Routing</RouterLink>
      <RouterLink class="btn" to="/linux">Linux view</RouterLink>
    </template>
  </PageHeader>
  <p class="-mt-3 mb-0 text-[13px] text-text-2">Test networks are IPv4-only in V1 · the gateway routes between them and the uplink</p>

  <p v-if="networks.isLoading.value" class="text-text-2">Loading the networks…</p>
  <LoadError v-else-if="firstError(networks)" :error="firstError(networks)" what="The networks are not loaded" :retry="networks.refetch" />
  <template v-else>
    <EmptyState v-if="list.length === 0" text="No test network yet. The setup assigns the first ones; more follow with a configuration import or the API." />
    <nav aria-label="Networks" class="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-4" data-testid="network-cards">
      <RouterLink
        v-for="c in cards"
        :key="c.id"
        :to="`/networks/${c.id}`"
        class="card flex flex-col items-start gap-1.5 !px-4 !py-3.5 text-text no-underline hover:!border-[#3a4552]"
        :class="selected?.id === c.id ? '!border-accent-strong !bg-surface-2' : ''"
        :aria-current="selected?.id === c.id ? 'page' : undefined"
        data-testid="network-card"
      >
        <span class="flex items-center gap-2 text-base font-semibold text-text">
          {{ c.name }}
          <span class="rounded border border-border-strong px-1.5 text-[11px] font-normal text-text-2">{{ c.tag }}</span>
        </span>
        <span class="mono text-xs text-text-2">{{ c.addr }}</span>
        <span class="text-[13px] text-text-2">{{ c.meta }}</span>
        <StatusDot v-if="c.degraded" tone="block" label="degraded: a port is missing" />
      </RouterLink>
      <div class="card flex flex-col items-start gap-1.5 !px-4 !py-3.5" data-testid="management-card">
        <span class="flex items-center gap-2 text-base font-semibold">
          Management <span class="rounded border border-border-strong px-1.5 text-[11px] font-normal text-text-2">OS</span>
        </span>
        <span v-if="active.isLoading.value" class="text-xs text-text-2">Loading…</span>
        <span v-else-if="firstError(active)" class="text-xs text-block-text" data-testid="management-error">
          Not loaded: {{ errorMessage(firstError(active)) }}
        </span>
        <span v-else class="mono text-xs text-text-2">
          {{ management?.interface.name ?? management?.interface.mac ?? '—' }}<template v-if="management?.allowed_sources?.length">
            · {{ management.allowed_sources.join(', ') }}</template
          >
        </span>
        <span class="text-[13px] text-text-2">UI, API, SSH · owned by the OS</span>
      </div>
      <div class="card flex flex-col items-start gap-1.5 !px-4 !py-3.5" data-testid="uplink-card">
        <span class="flex items-center gap-2 text-base font-semibold">
          Uplink <span class="rounded border border-border-strong px-1.5 text-[11px] font-normal text-text-2">OS</span>
        </span>
        <span v-if="uplink.isLoading.value" class="text-xs text-text-2">Loading…</span>
        <template v-else-if="firstError(uplink)">
          <span class="text-xs text-block-text" role="alert" data-testid="uplink-error">Not loaded: {{ errorMessage(firstError(uplink)) }}</span>
          <button type="button" class="btn btn-sm" @click="uplink.refetch()">Try again</button>
        </template>
        <template v-else>
          <span class="mono text-xs text-text-2">{{ up?.status.address ?? 'no address' }} · {{ up?.status.interface ?? up?.config.interface.name ?? '—' }}</span>
          <span class="text-[13px] text-text-2">
            <template v-if="up?.status.gateway">via {{ up.status.gateway }} · </template>masquerade<template v-if="up && up.status.link_up === false">
              · link down</template
            >
          </span>
        </template>
      </div>
    </nav>

    <LoadError v-if="unknownNetwork" :error="`no network ${route.params.id}`" what="Not found" />

    <div v-if="selected" class="grid grid-cols-1 gap-5 xl:grid-cols-[440px_minmax(0,1fr)]">
      <NetworkSettings :network="selected" />
      <AccessMatrix :networks="list" />
    </div>
    <div v-else-if="list.length" class="grid grid-cols-1 gap-5"><AccessMatrix :networks="list" /></div>

    <LeasesCard v-if="selected?.config.type === 'lan'" :network="selected" />
    <ClientsCard v-if="hub" :network="hub" />
  </template>
</template>
