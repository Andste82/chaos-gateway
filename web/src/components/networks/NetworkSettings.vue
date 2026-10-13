<script setup lang="ts">
// The settings of the selected network (plan §2.17 "Networks": subnet, gateway, attachment, DHCP, DNS,
// IPv6 blocked), in the user's words; a WireGuard network shows its tunnel, endpoint and peers instead.
// "Show Linux view" opens what the kernel holds for the network: its bridge or WireGuard interface and
// its subnet in nftables, tc, the routes and WireGuard (plan §2.17 "Technical view").
import { computed, ref, watch } from 'vue';
import type { LanNetwork, NetworkView, WireGuardNetwork } from '../../api/generated/model';
import { useNow } from '../../composables/useNow';
import { formatAgo, formatBytes, formatDuration } from '../../lib/format';
import { effectivePools, hostOf, subnetText } from '../../lib/networks';
import StatusDot from '../ui/StatusDot.vue';
import LinuxExcerpt from './LinuxExcerpt.vue';

const props = defineProps<{ network: NetworkView }>();
const now = useNow(1000);

const lan = computed(() => (props.network.config.type === 'lan' ? (props.network.config as LanNetwork) : undefined));
const wg = computed(() => (props.network.config.type === 'wireguard' ? (props.network.config as WireGuardNetwork) : undefined));
const title = computed(() => `${(props.network.config as { name: string }).name} ${lan.value ? 'network' : wg.value?.kind === 'hub' ? 'WireGuard hub' : 'WireGuard link'}`);

const linux = ref(false);
watch(
  () => props.network.id,
  () => (linux.value = false),
);
const terms = computed(() => {
  const c = props.network.config as { address: string; client_networks?: string[] };
  const t = [props.network.status.interface ?? '', subnetText(c.address), hostOf(c.address)];
  if (wg.value?.kind === 'link') t.push(...(wg.value.routes ?? []));
  for (const cl of Object.values(wg.value?.clients ?? {})) t.push(...(cl.client_networks ?? []));
  return t.filter(Boolean);
});

const pools = computed(() => effectivePools(lan.value?.address, lan.value?.dhcp?.pools));
const dhcpOn = computed(() => lan.value?.dhcp?.enabled !== false);
const dns = computed(() => {
  const d = lan.value?.dns;
  const parts = ['Chaos Gateway proxy'];
  if (d?.redirect_hardcoded) parts.push('hardcoded resolvers redirected');
  if (d?.block_dot) parts.push('DoT blocked');
  if (d?.static_entries?.length) parts.push(`${d.static_entries.length} static ${d.static_entries.length === 1 ? 'entry' : 'entries'}`);
  return parts.join(' · ');
});
const ports = computed(() =>
  (props.network.status.ports?.length ? props.network.status.ports : (lan.value?.interfaces ?? []).map((i) => ({ name: i.name ?? i.mac, present: undefined, link_up: undefined }))).map(
    (p) => `${p.name ?? '?'}${p.present === false ? ' (missing)' : p.link_up === false ? ' (link down)' : p.link_up ? ' (link up)' : ''}`,
  ),
);
const peer = computed(() => props.network.status.link_peer);
</script>

<template>
  <section class="card flex min-w-0 flex-col gap-3" aria-labelledby="network-settings-title" data-testid="network-settings">
    <div class="flex items-center justify-between gap-3">
      <h2 id="network-settings-title" class="card-title">{{ title }}</h2>
      <StatusDot
        :tone="network.status.state === 'ok' ? 'normal' : network.status.state === 'degraded' ? 'block' : 'unknown'"
        :label="network.status.state === 'ok' ? 'Applied' : network.status.state === 'degraded' ? 'Degraded: a port is missing' : 'Not applied yet'"
      />
    </div>
    <dl v-if="lan" class="m-0 grid grid-cols-[130px_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
      <dt class="text-text-2">Subnet</dt>
      <dd class="mono m-0 text-[13px]">{{ subnetText(lan.address) }}</dd>
      <dt class="text-text-2">Gateway</dt>
      <dd class="mono m-0 text-[13px]">{{ hostOf(lan.address) }}</dd>
      <dt class="text-text-2">Attachment</dt>
      <dd class="m-0">{{ ports.length === 1 ? 'physical interface' : 'physical interfaces' }} <span class="mono text-[13px]">{{ ports.join(', ') }}</span></dd>
      <dt class="text-text-2">DHCP</dt>
      <dd class="m-0" data-testid="dhcp-settings">
        <template v-if="dhcpOn">
          on · lease time {{ formatDuration(lan.dhcp?.lease_time ?? '1h') }}
          <span v-if="network.status.dhcp?.leases_active !== undefined"> · {{ network.status.dhcp.leases_active }} active</span>
        </template>
        <template v-else>off: devices have static addresses, or another server answers</template>
      </dd>
      <template v-if="dhcpOn">
        <dt class="text-text-2">DHCP pool</dt>
        <dd class="mono m-0 text-[13px]" data-testid="dhcp-pool">
          <span v-for="(p, i) in pools.pools" :key="i" class="block">{{ p.start }} – {{ p.end }}</span>
          <span v-if="pools.isDefault" class="font-sans text-xs text-text-3">the default: the second half of the subnet</span>
          <span v-if="network.status.dhcp?.pool_free !== undefined" class="block font-sans text-xs text-text-3">{{ network.status.dhcp.pool_free }} addresses free</span>
        </dd>
        <dt class="text-text-2">Router · DNS</dt>
        <dd class="mono m-0 text-[13px]">
          {{ lan.dhcp?.options?.router ?? hostOf(lan.address) }} · {{ (lan.dhcp?.options?.dns_servers ?? [hostOf(lan.address)]).join(', ') }}
        </dd>
      </template>
      <dt class="text-text-2">DNS</dt>
      <dd class="m-0">{{ dns }}</dd>
      <dt class="text-text-2">Uplink</dt>
      <dd class="m-0">{{ lan.nat === false ? 'routed without masquerade' : 'masquerade' }}</dd>
      <template v-if="lan.routes?.length">
        <dt class="text-text-2">Routes</dt>
        <dd class="mono m-0 text-[13px]">
          <span v-for="r in lan.routes" :key="r.destination" class="block">{{ r.destination }} via {{ r.via }}</span>
        </dd>
      </template>
      <dt class="text-text-2">IPv6</dt>
      <dd class="m-0">blocked (no RA, no forwarding)</dd>
    </dl>

    <dl v-else-if="wg" class="m-0 grid grid-cols-[130px_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
      <dt class="text-text-2">Kind</dt>
      <dd class="m-0">{{ wg.kind === 'hub' ? 'hub: many clients' : 'link: one remote site' }} · role {{ wg.role ?? 'test' }}</dd>
      <dt class="text-text-2">Tunnel subnet</dt>
      <dd class="mono m-0 text-[13px]">{{ subnetText(wg.address) }}</dd>
      <dt class="text-text-2">Gateway</dt>
      <dd class="mono m-0 text-[13px]">{{ hostOf(wg.address) }}</dd>
      <dt class="text-text-2">Endpoint</dt>
      <dd class="mono m-0 text-[13px]">{{ wg.endpoint ?? `uplink address:${wg.listen_port}` }}</dd>
      <dt class="text-text-2">MTU</dt>
      <dd class="mono m-0 text-[13px]">{{ wg.mtu ?? 1420 }}</dd>
      <dt class="text-text-2">Uplink</dt>
      <dd class="m-0">{{ wg.nat === false ? 'routed without masquerade' : 'masquerade' }}</dd>
      <template v-if="network.status.public_key">
        <dt class="text-text-2">Public key</dt>
        <dd class="mono m-0 text-xs break-all">{{ network.status.public_key }}</dd>
      </template>
      <template v-if="wg.kind === 'hub'">
        <dt class="text-text-2">Clients</dt>
        <dd class="m-0">{{ network.status.peers_online ?? 0 }} of {{ network.status.peers_total ?? Object.keys(wg.clients ?? {}).length }} online</dd>
      </template>
      <template v-else>
        <dt class="text-text-2">Remote side</dt>
        <dd class="m-0" data-testid="link-peer">
          <span class="mono text-[13px]">{{ wg.peer?.address }}</span>
          <template v-if="wg.peer?.endpoint"> at <span class="mono text-[13px]">{{ wg.peer.endpoint }}</span> (the gateway connects)</template>
          <span class="mt-1 block">
            <StatusDot :tone="peer?.online ? 'normal' : 'offline'" :label="peer?.online ? 'Online' : 'Offline'" />
            <span class="text-[13px] text-text-2">
              · handshake {{ peer?.last_handshake ? formatAgo(peer.last_handshake, now) : 'never' }}
              <template v-if="peer?.rx_bytes !== undefined"> · ↓ {{ formatBytes(peer.rx_bytes) }} ↑ {{ formatBytes(peer.tx_bytes) }}</template>
            </span>
          </span>
        </dd>
        <dt class="text-text-2">Routes</dt>
        <dd class="mono m-0 text-[13px]">
          <span v-for="r in wg.routes ?? []" :key="r" class="block">{{ r }} (static)</span>
          <span v-for="r in peer?.learned_routes ?? []" :key="r" class="block">{{ r }} (learned)</span>
          <RouterLink class="font-sans text-[13px]" to="/networks/routing">Routing sessions →</RouterLink>
        </dd>
      </template>
    </dl>

    <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="linux" @click="linux = !linux">
      {{ linux ? 'Hide Linux view' : 'Show Linux view (compiled)' }}
    </button>
    <LinuxExcerpt v-if="linux" :terms="terms" />
  </section>
</template>
