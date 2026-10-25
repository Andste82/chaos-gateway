<script setup lang="ts">
// The Interception screen (plan §2.8, §2.17 "Interception UI"; M23c): the exchanges the interception proxy saw (`GET
// /intercept/flows`), newest first, with the filters of the API (device, host, only the exchanges a rule acted on), kept in the
// address so that a link from the device detail lands on the device's exchanges; the table is read every 3 s and at once when a
// rule acts (the event `http_fault_triggered`), and "Pause" holds what is shown while the gateway goes on. Above it the interception
// cases in force, edited and removed as faults are, "Add interception" (the Add fault dialog with the case `intercept`), and the
// test CA (download, replacement after a question). A gateway without the interception, or whose proxy does not run, says so.
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import type { InterceptFlow, Overlay } from '../api/generated/model';
import FaultDialog from '../components/faults/FaultDialog.vue';
import ActiveFaultList from '../components/faults/ActiveFaultList.vue';
import FlowTable from '../components/intercept/FlowTable.vue';
import TestCaCard from '../components/intercept/TestCaCard.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { useActiveFaults, useCanIntercept, useGatewayState, useHealth, useInterceptFlows, useNames } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { filtered, flowParams, hostFilterError, maxShown, noFilters, pageSize, type FlowFilters } from '../lib/intercept';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';

const route = useRoute();
const router = useRouter();
const canIntercept = useCanIntercept();
const connection = useConnectionStore();
const state = useGatewayState();
const health = useHealth();
const { names, devices } = useNames();
const now = useNow();
const faults = useActiveFaults();

// ---- the filters, in the address (?device=&host=&faulted=1)
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '');
const filters = ref<FlowFilters>({ device: q('device'), host: q('host'), faulted: q('faulted') === '1' });
const hostError = computed(() => hostFilterError(filters.value.host));
/** The filters as the request has them: a host is applied when it has settled and is a name. */
const applied = ref<FlowFilters>({ ...filters.value });
let settle: ReturnType<typeof setTimeout> | undefined;
watch(
  filters,
  (f) => {
    clearTimeout(settle);
    settle = setTimeout(() => {
      applied.value = { ...f };
      void router.replace({ query: { ...(f.device ? { device: f.device } : {}), ...(f.host.trim() ? { host: f.host.trim() } : {}), ...(f.faulted ? { faulted: '1' } : {}) } });
    }, 250);
  },
  { deep: true },
);
onBeforeUnmount(() => clearTimeout(settle));
// the address changes from outside (the navigation entry, a link of the device detail, Back): the filters follow it
watch(
  () => route.query,
  () => {
    const next: FlowFilters = { device: q('device'), host: q('host'), faulted: q('faulted') === '1' };
    const same = (a: FlowFilters, b: FlowFilters) => a.device === b.device && a.host.trim() === b.host.trim() && a.faulted === b.faulted;
    if (same(next, applied.value)) return;
    clearTimeout(settle);
    filters.value = next;
    applied.value = { ...next };
  },
);
function clearFilters() {
  filters.value = noFilters();
}

// ---- the table
const limit = ref(pageSize);
watch(applied, () => {
  limit.value = pageSize;
  paused.value = false;
});
const paused = ref(false);
const frozen = ref<InterceptFlow[]>([]);
const flows = useInterceptFlows(
  () => flowParams(applied.value, limit.value),
  () => canIntercept.value === true,
  () => !paused.value,
);
const live = computed(() => flows.data.value?.items ?? []);
const items = computed(() => (paused.value ? frozen.value : live.value));
const more = computed(() => !!flows.data.value?.next_cursor && limit.value < maxShown && !paused.value);
const atMost = computed(() => !!flows.data.value?.next_cursor && limit.value >= maxShown);
function togglePause() {
  if (!paused.value) frozen.value = [...live.value];
  paused.value = !paused.value;
}

// ---- the cases in force, and the dialog that makes and edits them
const cases = computed(() => faults.items.value.filter((f) => f.kind === 'tls' && f.overlay?.tls?.case === 'intercept'));
const faultOpen = ref(false);
const editing = ref<Overlay>();
function addCase() {
  editing.value = undefined;
  faultOpen.value = true;
}
function editCase(o: Overlay) {
  editing.value = o;
  faultOpen.value = true;
}
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

/** What the health check says of the proxy when it does not run (the gateway's own sentence). */
const proxy = computed(() => health.data.value?.components?.find((c) => c.name === 'tls_proxy'));
const proxyProblem = computed(() => (proxy.value && proxy.value.status !== 'healthy' ? (proxy.value.detail ?? `The interception proxy is ${proxy.value.status}.`) : undefined));

useApiScreen('the interception', () => [
  {
    method: 'GET',
    path: `/intercept/flows?${new URLSearchParams(Object.entries(flowParams(applied.value, limit.value)).map(([k, v]) => [k, String(v)])).toString()}`,
    note: 'newest first; device, host, since and faulted filter, cursor and limit page',
  },
  { method: 'GET', path: '/tls/ca', note: 'the test CA certificate' },
  { method: 'POST', path: '/tls/ca/regenerate', note: 'replace the test CA' },
]);
</script>

<template>
  <PageHeader title="Interception">
    <template #actions>
      <button type="button" class="btn btn-primary" :disabled="canIntercept === false || blocked" data-testid="add-interception" @click="addCase">Add interception</button>
    </template>
  </PageHeader>
  <p class="-mt-3 mb-0 text-[13px] text-text-3">What the interception proxy saw of the devices' HTTPS, WebSocket and TLS streams, and the rules that acted on it</p>

  <EmptyState v-if="canIntercept === false" text="This gateway has no interception, so there is nothing to read here." data-testid="intercept-unsupported" />
  <template v-else>
    <p v-if="proxyProblem" class="m-0 rounded-md border border-fault-border bg-fault-tint p-3 text-[13px] text-fault-text" role="status" data-testid="proxy-problem">{{ proxyProblem }}</p>

    <SectionCard title="Interception in force">
      <p v-if="faults.isLoading.value" class="m-0 text-sm text-text-2" role="status">Loading…</p>
      <LoadError v-else-if="faults.error.value && !faults.items.value.length" :error="faults.error.value" what="The cases in force are not loaded" />
      <p v-else-if="cases.length === 0" class="m-0 text-sm text-text-2" data-testid="no-cases">
        No interception case is in force: no device's traffic goes through the proxy. Add one to read and change what a device says to its servers.
      </p>
      <ActiveFaultList v-else :items="cases" show-target removable editable :remove-disabled="blocked" @edit="editCase" />
    </SectionCard>

    <SectionCard title="Exchanges">
      <template #actions>
        <StatusDot :tone="paused ? 'offline' : 'normal'" :label="paused ? 'Paused' : 'Live'" data-testid="flow-live" />
        <button type="button" class="btn btn-sm" :aria-pressed="paused" data-testid="flow-pause" @click="togglePause">{{ paused ? 'Resume' : 'Pause' }}</button>
      </template>

      <div class="grid grid-cols-[minmax(160px,220px)_minmax(220px,1fr)_auto] items-start gap-3" role="search" aria-label="Filter the exchanges">
        <div>
          <label class="label" for="flow-device">Device</label>
          <select id="flow-device" v-model="filters.device" class="input">
            <option value="">All devices</option>
            <option v-for="d in devices.data.value ?? []" :key="d.id" :value="d.id">{{ d.name }}</option>
          </select>
        </div>
        <div>
          <label class="label" for="flow-host">Host</label>
          <input
            id="flow-host"
            v-model="filters.host"
            class="input mono"
            placeholder="api.example.com or *.example.com"
            :aria-invalid="!!hostError"
            :aria-describedby="hostError ? 'flow-host-error' : undefined"
          />
          <p v-if="hostError" id="flow-host-error" class="field-error">{{ hostError }}</p>
        </div>
        <label class="mt-[30px] flex items-center gap-2 text-sm">
          <input v-model="filters.faulted" type="checkbox" />
          Only exchanges a rule acted on
        </label>
      </div>

      <p v-if="flows.isLoading.value && !flows.data.value" class="m-0 text-sm text-text-2" role="status">Loading the exchanges…</p>
      <LoadError v-else-if="flows.error.value && !flows.data.value" :error="flows.error.value" what="The exchanges are not loaded" :retry="flows.refetch" />
      <EmptyState v-else-if="items.length === 0 && filtered(applied)" text="No exchange matches these filters." data-testid="flows-none-match">
        <button type="button" class="btn" @click="clearFilters">Clear the filters</button>
      </EmptyState>
      <EmptyState
        v-else-if="items.length === 0"
        text="No exchange yet: the log fills when a device whose traffic an interception case selects makes a request. The gateway keeps the newest 20000 in memory and forgets them when it restarts."
        data-testid="flows-empty"
      />
      <template v-else>
        <FlowTable :items="items" :device-names="(id: string) => names.device?.(id)" :now="now" />
        <div class="flex flex-wrap items-center justify-between gap-3 text-[13px] text-text-3">
          <span data-testid="flow-count">{{ items.length }} exchange{{ items.length === 1 ? '' : 's' }}, newest first</span>
          <button v-if="more" type="button" class="btn btn-sm" data-testid="flow-more" @click="limit = Math.min(limit + pageSize, maxShown)">Show older exchanges</button>
          <span v-else-if="atMost">The newest {{ maxShown }} are shown: narrow the filters to see older ones.</span>
        </div>
      </template>
    </SectionCard>

    <TestCaCard />
  </template>

  <FaultDialog v-model:open="faultOpen" :overlay="editing" preset="intercept" />
</template>
