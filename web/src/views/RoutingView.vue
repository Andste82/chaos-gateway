<script setup lang="ts">
// Routing (plan §2.2.2 "Observability", M14 "routing view"): the managed BIRD instance, its sessions
// (BGP, OSPF, Babel on the WireGuard links) with their state, the prefixes each announces and receives
// and how many it imported, filtered and exported; and the routes in Chaos Gateway's routing tables by
// origin. Sessions and counts are polled, route changes also arrive as events. What the status does
// not say (ASN, the link of a session, the external mode) comes from the routing configuration; when
// that is not loaded, the BIRD card says so.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { getGetRoutingStatusQueryKey, getRouting, getRoutingStatus, listRoutes } from '../api/generated/hooks/routing/routing';
import type { RouteEntry, RoutingStatusProtocolsItem } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import SegmentedControl from '../components/ui/SegmentedControl.vue';
import StatusDot, { type DotTone } from '../components/ui/StatusDot.vue';
import { fetchAll, firstError, useNames } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { formatAgo, formatClock } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';

const status = useQuery({ queryKey: getGetRoutingStatusQueryKey(), queryFn: ({ signal }) => getRoutingStatus({ signal }), refetchInterval: 5_000 });
const config = useQuery({ queryKey: ['routing', 'config'], queryFn: ({ signal }) => getRouting(undefined, { signal }) });
const routes = useQuery({
  queryKey: ['routing', 'routes', 'all'],
  queryFn: ({ signal }) => fetchAll<RouteEntry>((cursor) => listRoutes({ limit: 500, cursor }, { signal }), signal),
  refetchInterval: 10_000,
});
const { names } = useNames();
const now = useNow(1000);

useApiScreen('the routing', () => [
  { method: 'GET', path: '/routing/status', note: 'BIRD and its sessions' },
  { method: 'GET', path: '/routing/routes', note: "the routes of Chaos Gateway's tables" },
  { method: 'GET', path: '/routing', note: 'the routing configuration' },
]);

const bird = computed(() => status.data.value?.bird);
const protocols = computed(() => status.data.value?.protocols ?? []);
const configured = computed(() => Object.values(config.data.value?.protocols ?? {}));

function tone(p: RoutingStatusProtocolsItem): DotTone {
  switch (p.state) {
    case 'up':
      return 'normal';
    case 'starting':
      return 'fault';
    case 'disabled':
      return 'offline';
    default:
      return 'block';
  }
}

/** The link the protocol runs on and whether it has a custom snippet, from the configuration. */
function configOf(p: RoutingStatusProtocolsItem) {
  const c = config.data.value?.protocols?.[p.id] ?? configured.value.find((x) => x.name === p.name);
  if (!c) return undefined;
  return { link: names.value.network?.(c.link) ?? c.link, snippet: !!c.custom_snippet };
}

const origin = ref('all');
const origins = computed(() => {
  const all = routes.data.value ?? [];
  const count = (o: string) => all.filter((r) => r.origin === o).length;
  return [
    { value: 'all', label: 'All', count: all.length },
    ...(['connected', 'static', 'client', 'learned', 'external'] as const).map((o) => ({ value: o, label: o[0]!.toUpperCase() + o.slice(1), count: count(o) })),
  ];
});
const shownRoutes = computed(() => (routes.data.value ?? []).filter((r) => origin.value === 'all' || r.origin === origin.value));
const external = computed(() => config.data.value?.external);
</script>

<template>
  <PageHeader title="Routing">
    <template #breadcrumb>
      <nav aria-label="Breadcrumb" class="mb-1 text-[13px] text-text-2"><RouterLink to="/networks">Networks</RouterLink> / Routing</nav>
    </template>
  </PageHeader>

  <SectionCard title="BIRD">
    <p v-if="status.isLoading.value" class="m-0 text-sm text-text-2">Loading the routing status…</p>
    <LoadError v-else-if="firstError(status)" :error="firstError(status)" what="The routing status is not loaded" :retry="status.refetch" />
    <dl v-else class="m-0 grid grid-cols-[160px_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm" data-testid="bird">
      <dt class="text-text-2">Instance</dt>
      <dd class="m-0">
        <StatusDot :tone="bird?.running ? 'normal' : 'offline'" :label="bird?.running ? 'Running' : configured.length ? 'Not running' : 'Not running: no dynamic routing configured'" />
      </dd>
      <template v-if="bird?.version">
        <dt class="text-text-2">Version</dt>
        <dd class="mono m-0 text-[13px]">{{ bird.version }}</dd>
      </template>
      <dt class="text-text-2">Router id</dt>
      <dd class="mono m-0 text-[13px]">{{ bird?.router_id ?? config.data.value?.router_id ?? '—' }}</dd>
      <template v-if="config.data.value?.asn">
        <dt class="text-text-2">ASN</dt>
        <dd class="mono m-0 text-[13px]">{{ config.data.value.asn }}</dd>
      </template>
      <template v-if="bird?.last_reconfigure">
        <dt class="text-text-2">Last configured</dt>
        <dd class="m-0">{{ formatClock(bird.last_reconfigure) }}</dd>
      </template>
      <template v-if="bird?.last_error">
        <dt class="text-text-2">Last error</dt>
        <dd class="m-0 text-block-text">{{ bird.last_error }}</dd>
      </template>
      <template v-if="external?.enabled">
        <dt class="text-text-2">External mode</dt>
        <dd class="m-0">routes another daemon writes into kernel table <span class="mono">{{ external.table }}</span>, with the import filters</dd>
      </template>
    </dl>
    <LoadError
      v-if="firstError(config)"
      :error="firstError(config)"
      what="The routing configuration is not loaded (ASN, links and external mode are missing)"
      :retry="config.refetch"
    />
  </SectionCard>

  <SectionCard title="Sessions">
    <LoadError v-if="firstError(status)" :error="firstError(status)" what="The sessions are not loaded" :retry="status.refetch" />
    <EmptyState
      v-else-if="!status.isLoading.value && protocols.length === 0"
      text="No routing session. Dynamic routing runs on WireGuard links; its protocols are configured with the routing configuration (API or import)."
    />
    <div v-else class="flex flex-col gap-3" data-testid="sessions">
      <article v-for="p in protocols" :key="p.id" class="rounded-lg border border-[#2a323d] bg-surface-2 px-4 py-3" data-testid="session">
        <div class="flex flex-wrap items-center gap-3">
          <span class="text-[15px] font-semibold">{{ p.name }}</span>
          <span class="rounded border border-border-strong px-1.5 text-[11px] text-text-2 uppercase">{{ p.type }}</span>
          <span v-if="configOf(p)" class="text-[13px] text-text-2">on link {{ configOf(p)!.link }}</span>
          <span v-if="configOf(p)?.snippet" class="rounded border border-fault-border px-1.5 text-[11px] text-fault-text" title="Raw BIRD configuration added by hand">unmanaged snippet</span>
          <span class="flex-1" />
          <StatusDot :tone="tone(p)" :label="`${p.state}${p.info ? ` · ${p.info}` : ''}`" />
          <span v-if="p.since" class="text-xs text-text-3">since {{ formatAgo(p.since, now) }}</span>
        </div>
        <div class="mt-2 grid grid-cols-1 gap-2 text-[13px] md:grid-cols-3">
          <div>
            <div class="text-xs text-text-3">Routes</div>
            <span class="mono">{{ p.routes?.imported ?? 0 }} imported · {{ p.routes?.filtered ?? 0 }} filtered · {{ p.routes?.exported ?? 0 }} exported</span>
            <span v-if="p.routes?.limit_hit" class="block text-fault-text">import limit reached: further routes are not imported</span>
          </div>
          <div>
            <div class="text-xs text-text-3">Announced</div>
            <span class="mono" data-testid="announced">{{ p.announced?.join(', ') || '—' }}</span>
          </div>
          <div>
            <div class="text-xs text-text-3">Received</div>
            <span class="mono" data-testid="received">{{ p.received?.join(', ') || '—' }}</span>
          </div>
        </div>
        <p v-if="p.last_error" class="mt-2 mb-0 text-[13px] text-block-text">{{ p.last_error }}</p>
      </article>
    </div>
  </SectionCard>

  <SectionCard title="Routes">
    <template #actions>
      <SegmentedControl v-model="origin" :options="origins" label="Routes by origin" variant="chips" />
    </template>
    <p v-if="routes.isLoading.value" class="m-0 text-sm text-text-2">Loading the routes…</p>
    <LoadError v-else-if="firstError(routes)" :error="firstError(routes)" what="The routes are not loaded" :retry="routes.refetch" />
    <EmptyState v-else-if="shownRoutes.length === 0" text="No route of this origin." />
    <table v-else class="w-full border-collapse text-sm" data-testid="routes">
      <thead>
        <tr class="data-head text-left">
          <th class="py-2 font-normal">Destination</th>
          <th class="py-2 font-normal">Via</th>
          <th class="py-2 font-normal">Network</th>
          <th class="py-2 font-normal">Origin</th>
          <th class="py-2 font-normal">Session</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in shownRoutes" :key="i" class="data-row">
          <td class="mono py-2 text-[13px]">{{ r.destination }}</td>
          <td class="mono py-2 text-[13px]">{{ r.gateway ?? 'direct' }}</td>
          <td class="py-2">{{ r.network ? (names.network?.(r.network) ?? r.network) : '—' }}</td>
          <td class="py-2">{{ r.origin }}</td>
          <td class="py-2">{{ r.protocol?.name ?? '—' }}</td>
        </tr>
      </tbody>
    </table>
  </SectionCard>
</template>
