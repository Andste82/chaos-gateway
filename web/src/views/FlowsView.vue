<script setup lang="ts">
// The Flows screen (plan §2.12, §2.17 "Flows"; M26): the connections the gateway tracks (`GET /flows`, the conntrack of the test
// networks), newest first, with the filters device and network (the gateway's), a text over addresses, names, ports and services, the
// protocol, and the connections that came in through a port forward. The filters are kept in the address (a link from the device detail
// lands on the device's connections). The table is read every 3 s and "Pause" holds what is shown while the gateway goes on. A
// row's "Explain" opens the explain card of the Faults screen for that connection, a forwarded one included.
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import type { Flow } from '../api/generated/model';
import ConnectionTable from '../components/flows/ConnectionTable.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { useFlows, useNames } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { filterFlows, flowPageSize, flowParams, flowsFiltered, noFlowFilters, sortFlows, type FlowFilters } from '../lib/flows';
import { focusAfterUpdate } from '../lib/focus';
import { formatCount } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';

const route = useRoute();
const router = useRouter();
const { names, devices, networks } = useNames();
const now = useNow();

// ---- the filters, in the address (?device=&network=&q=&protocol=&forwarded=1)
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '');
const fromRoute = (): FlowFilters => ({ device: q('device'), network: q('network'), text: q('q'), protocol: ['tcp', 'udp', 'icmp'].includes(q('protocol')) ? q('protocol') : '', forwarded: q('forwarded') === '1' });
const same = (a: FlowFilters, b: FlowFilters) =>
  a.device === b.device && a.network === b.network && a.text.trim() === b.text.trim() && a.protocol === b.protocol && a.forwarded === b.forwarded;
const filters = ref<FlowFilters>(fromRoute());
const applied = ref<FlowFilters>({ ...filters.value });
let settle: ReturnType<typeof setTimeout> | undefined;
watch(
  filters,
  (f) => {
    clearTimeout(settle);
    settle = setTimeout(() => {
      applied.value = { ...f };
      void router.replace({
        query: {
          ...(f.device ? { device: f.device } : {}),
          ...(f.network ? { network: f.network } : {}),
          ...(f.text.trim() ? { q: f.text.trim() } : {}),
          ...(f.protocol ? { protocol: f.protocol } : {}),
          ...(f.forwarded ? { forwarded: '1' } : {}),
        },
      });
    }, 250);
  },
  { deep: true },
);
onBeforeUnmount(() => clearTimeout(settle));
// the address changes from outside (the navigation entry, a link of the device detail, Back): the filters follow it
watch(
  () => route.query,
  () => {
    const next = fromRoute();
    if (same(next, applied.value)) return;
    clearTimeout(settle);
    filters.value = next;
    applied.value = { ...next };
  },
);
function clearFilters() {
  filters.value = noFlowFilters();
}

// ---- the table
const paused = ref(false);
const frozen = ref<Flow[]>([]);
const shown = ref(flowPageSize);
const flows = useFlows(
  () => flowParams(applied.value, 500),
  () => true,
  () => !paused.value,
);
// the gateway's filters start a new list; the rest only select from it
watch(
  () => [applied.value.device, applied.value.network, applied.value.text, applied.value.protocol, applied.value.forwarded],
  () => {
    shown.value = flowPageSize;
    paused.value = false;
  },
);
const live = computed(() => sortFlows(filterFlows(flows.data.value ?? [], applied.value)));
const items = computed(() => (paused.value ? frozen.value : live.value));
function togglePause() {
  if (!paused.value) frozen.value = [...live.value];
  paused.value = !paused.value;
}
// the button goes away with the last page: the focus moves to the count line instead of falling to the page start (P6-AUDIT-48)
function showMore() {
  shown.value += flowPageSize;
  if (shown.value >= items.value.length) focusAfterUpdate('[data-testid="flows-count"]');
}
const visible = computed(() => items.value.slice(0, shown.value));
// a read of the live table that fails keeps the rows of the last good one: they say they are not updating (P6-AUDIT-47)
const stale = computed(() => !paused.value && !!flows.error.value && !!flows.data.value);
const total = computed(() => flows.data.value?.length ?? 0);

useApiScreen('the flows', () => [
  {
    method: 'GET',
    path: `/flows?${new URLSearchParams(Object.entries(flowParams(applied.value, 500)).map(([k, v]) => [k, String(v)])).toString()}`,
    note: 'the tracked connections of the test networks, a forwarded one included; device and network filter, cursor and limit page',
  },
  { method: 'GET', path: '/dns/queries', note: 'the DNS proxy’s query log; device, name and since filter' },
]);
</script>

<template>
  <PageHeader title="Flows" />
  <p class="-mt-3 mb-0 text-[13px] text-text-3">The connections the gateway tracks in the test networks, with the bytes they moved</p>

  <SectionCard title="Connections">
    <template #actions>
      <StatusDot :tone="paused ? 'offline' : stale ? 'fault' : 'normal'" :label="paused ? 'Paused' : stale ? 'Not updating' : 'Live'" data-testid="flows-live" />
      <button type="button" class="btn btn-sm" :aria-pressed="paused" data-testid="flows-pause" @click="togglePause">{{ paused ? 'Resume' : 'Pause' }}</button>
    </template>

    <div class="grid grid-cols-[minmax(150px,200px)_minmax(150px,200px)_minmax(200px,1fr)_110px_auto] items-start gap-3" role="search" aria-label="Filter the connections">
      <div>
        <label class="label" for="flows-device">Device</label>
        <select id="flows-device" v-model="filters.device" class="input">
          <option value="">All devices</option>
          <option v-for="d in devices.data.value ?? []" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
      </div>
      <div>
        <label class="label" for="flows-network">Network</label>
        <select id="flows-network" v-model="filters.network" class="input">
          <option value="">All networks</option>
          <option v-for="n in networks.data.value ?? []" :key="n.id" :value="n.id">{{ (n.config as { name?: string }).name ?? n.id }}</option>
        </select>
      </div>
      <div>
        <label class="label" for="flows-text">Search</label>
        <input id="flows-text" v-model="filters.text" class="input mono" placeholder="address, host name, port or service" autocomplete="off" />
      </div>
      <div>
        <label class="label" for="flows-protocol">Protocol</label>
        <select id="flows-protocol" v-model="filters.protocol" class="input">
          <option value="">Any</option>
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
          <option value="icmp">ICMP</option>
        </select>
      </div>
      <label class="mt-[30px] flex items-center gap-2 text-sm">
        <input v-model="filters.forwarded" type="checkbox" />
        Only forwarded connections
      </label>
    </div>

    <LoadError v-if="stale" :error="flows.error.value" what="The connections are not updating" :retry="flows.refetch" />
    <p v-if="flows.isPending.value && !flows.data.value" class="m-0 text-sm text-text-2" role="status">Loading the connections…</p>
    <LoadError v-else-if="flows.error.value && !flows.data.value" :error="flows.error.value" what="The connections are not loaded" :retry="flows.refetch" />
    <EmptyState v-else-if="items.length === 0 && flowsFiltered(applied)" text="No connection matches these filters." data-testid="flows-none-match">
      <button type="button" class="btn" @click="clearFilters">Clear the filters</button>
    </EmptyState>
    <EmptyState v-else-if="items.length === 0" text="No connection is tracked: a device that talks to a server shows up here while its connection lasts." data-testid="flows-empty" />
    <template v-else>
      <ConnectionTable :items="visible" :device-name="(id: string) => names.device?.(id)" :network-name="(id: string) => names.network?.(id)" :now="now" />
      <div class="flex flex-wrap items-center justify-between gap-3 text-[13px] text-text-3">
        <span data-testid="flows-count" tabindex="-1"
          >{{ formatCount(items.length) }} connection{{ items.length === 1 ? '' : 's' }}<template v-if="flowsFiltered(applied) && total > items.length"> of {{ formatCount(total) }}</template
          >, newest first</span
        >
        <button v-if="items.length > shown" type="button" class="btn btn-sm" data-testid="flows-more" @click="showMore">Show more</button>
      </div>
    </template>
  </SectionCard>
</template>
