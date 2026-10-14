<script setup lang="ts">
// Networks (plan §2.17 "Networks", M14): cards for the test networks, the WireGuard networks, the
// management network (owned by the OS) and the uplink; the settings of the selected network (subnet,
// gateway, attachment, DHCP with its pool and leases, DNS, IPv6 blocked; for a WireGuard hub its clients
// with their status, the add-client dialog and the QR code of a client's configuration; for a link its
// peer); the access matrix between the networks, whose changes go through the preview and the apply like
// any other (commit-confirm only when the gateway says the change could lock the admin out). Add network
// opens the network form, and the selected network is edited and deleted through the draft (P3-M14-01): a
// network of the draft is marked NEW, CHANGED or DELETED until it is applied, and Undo takes the edit back.
// The routing view and the technical view are pages of their own.
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { errorMessage } from '../api/fetcher';
import { useGetUplink } from '../api/generated/hooks/config/config';
import type { LanNetwork, NetworkView, WireGuardNetwork } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import AccessMatrix from '../components/networks/AccessMatrix.vue';
import ClientsCard from '../components/networks/ClientsCard.vue';
import LeasesCard from '../components/networks/LeasesCard.vue';
import NetworkDialog from '../components/networks/NetworkDialog.vue';
import NetworkSettings from '../components/networks/NetworkSettings.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { firstError, useActiveConfig, useDevices, useNetworks } from '../composables/gateway';
import { subnetText } from '../lib/networks';
import { useApiScreen } from '../stores/apiPanel';
import { useDraftStore } from '../stores/draft';

const props = defineProps<{ id?: string }>();

const networks = useNetworks();
const devices = useDevices();
const uplink = useGetUplink();
const active = useActiveConfig();
const route = useRoute();
const router = useRouter();
const draft = useDraftStore();

/** The networks of the gateway. */
const applied = computed(() => networks.data.value ?? []);
/** A network of the draft: added, changed or deleted, not applied yet. */
const marks = computed<Record<string, 'NEW' | 'CHANGED' | 'DELETED'>>(() => {
  const out: Record<string, 'NEW' | 'CHANGED' | 'DELETED'> = {};
  for (const [id, n] of Object.entries(draft.networks)) out[id] = n === null ? 'DELETED' : draft.baseNetwork(id) ? 'CHANGED' : 'NEW';
  return out;
});
/**
 * The networks as the draft has them: those of the gateway with the draft's settings (a hub keeps its
 * clients), and the networks the draft adds, pending until they are applied.
 */
const list = computed<NetworkView[]>(() => {
  const out = applied.value.map((n) => {
    const edit = draft.networks[n.id];
    if (!edit) return n;
    const clients = n.config.type === 'wireguard' ? (n.config as WireGuardNetwork).clients : undefined;
    return { ...n, config: clients && edit.type === 'wireguard' ? ({ ...edit, clients } as WireGuardNetwork) : edit };
  });
  for (const [id, n] of Object.entries(draft.networks)) {
    if (n && !applied.value.some((x) => x.id === id)) out.push({ id, config: n, status: { state: 'pending' } });
  }
  return out;
});
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
  if (n?.config.type === 'lan' && n.status.state !== 'pending') calls.push({ method: 'GET', path: `/networks/${n.id}/leases`, note: 'the DHCP leases' });
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
  mark?: 'NEW' | 'CHANGED' | 'DELETED';
}

const cards = computed<Card[]>(() =>
  list.value.map((n) => {
    const devs = deviceCounts.value.get(n.id) ?? 0;
    if (n.config.type === 'lan') {
      const c = n.config as LanNetwork;
      const meta = [`${devs} ${devs === 1 ? 'device' : 'devices'}`, c.dhcp?.enabled === false ? 'no DHCP' : 'DHCP', 'DNS'];
      return { id: n.id, name: c.name, tag: 'test network', addr: subnetText(c.address), meta: meta.join(' · '), degraded: n.status.state === 'degraded', mark: marks.value[n.id] };
    }
    const w = n.config as WireGuardNetwork;
    const meta =
      w.kind === 'hub'
        ? `${n.status.peers_online ?? 0} of ${n.status.peers_total ?? Object.keys(w.clients ?? {}).length} clients online`
        : n.status.link_peer?.online
          ? 'peer online'
          : 'peer offline';
    const role = w.role === 'management' ? ' · management' : '';
    return { id: n.id, name: w.name, tag: `WireGuard ${w.kind}${role}`, addr: subnetText(w.address), meta: n.status.state === 'pending' ? 'not applied yet' : meta, degraded: n.status.state === 'degraded', mark: marks.value[n.id] };
  }),
);

const management = computed(() => active.data.value?.configuration.management);
const up = computed(() => uplink.data.value);
const unknownNetwork = computed(() => !!props.id && list.value.length > 0 && !selected.value);
const hub = computed(() => {
  const n = selected.value;
  return n?.config.type === 'wireguard' && (n.config as WireGuardNetwork).kind === 'hub' && n.status.state !== 'pending' ? n : undefined;
});

// the network form: Add network, or Edit (and Fix) of the selected network
const formOpen = ref(false);
const editing = ref<string>();
function addNetwork() {
  editing.value = undefined;
  formOpen.value = true;
}
function editNetwork(id: string) {
  editing.value = id;
  formOpen.value = true;
}
function saved(id: string) {
  if (id !== selected.value?.id) void router.push(`/networks/${id}`);
}
// a network deleted here and applied is gone: the page goes back to the list instead of saying "not found"
const deletedHere = new Set<string>();
watch(marks, (m) => {
  for (const [id, mark] of Object.entries(m)) if (mark === 'DELETED') deletedHere.add(id);
});
watch(unknownNetwork, (unknown) => {
  if (unknown && props.id && deletedHere.has(props.id)) void router.replace('/networks');
});
</script>

<template>
  <PageHeader title="Networks">
    <template #actions>
      <RouterLink class="btn" to="/networks/routing">Routing</RouterLink>
      <RouterLink class="btn" to="/linux">Linux view</RouterLink>
      <button type="button" class="btn btn-primary" @click="addNetwork">Add network</button>
    </template>
  </PageHeader>
  <p class="-mt-3 mb-0 text-[13px] text-text-2">Test networks are IPv4-only in V1 · the gateway routes between them and the uplink</p>

  <p v-if="networks.isLoading.value" class="text-text-2">Loading the networks…</p>
  <LoadError v-else-if="firstError(networks)" :error="firstError(networks)" what="The networks are not loaded" :retry="networks.refetch" />
  <template v-else>
    <EmptyState v-if="list.length === 0" text="No test network yet. The setup assigns the first ones; Add network makes more.">
      <button type="button" class="btn btn-primary btn-sm" @click="addNetwork">Add network</button>
    </EmptyState>
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
        <span class="flex items-center gap-2 text-base font-semibold text-text" :class="c.mark === 'DELETED' ? 'line-through opacity-60' : ''">
          <span data-testid="network-name">{{ c.name }}</span>
          <span class="rounded border border-border-strong px-1.5 text-[11px] font-normal text-text-2">{{ c.tag }}</span>
        </span>
        <span
          v-if="c.mark"
          class="rounded border px-1.5 text-[11px]"
          :class="c.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
          data-testid="network-mark"
          >{{ c.mark }}</span
        >
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
      <NetworkSettings :network="selected" :mark="marks[selected.id]" @edit="editNetwork(selected.id)" />
      <AccessMatrix :networks="applied" />
    </div>
    <div v-else-if="applied.length" class="grid grid-cols-1 gap-5"><AccessMatrix :networks="applied" /></div>

    <LeasesCard v-if="selected?.config.type === 'lan' && selected.status.state !== 'pending'" :network="selected" />
    <ClientsCard v-if="hub" :network="hub" />
  </template>
  <NetworkDialog v-model:open="formOpen" :network="editing" @saved="saved" />
</template>
