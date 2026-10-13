<script setup lang="ts">
// Devices (plan §2.17): name, IP, MAC, network, traffic, faults, status; search; the filter chips
// All / Online / With faults / Not adopted; adopt a discovered device; a row opens the device.
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import type { DeviceView } from '../api/generated/model';
import AdoptDialog from '../components/devices/AdoptDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import AppIcon from '../components/ui/AppIcon.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import FaultChip from '../components/ui/FaultChip.vue';
import LoadError from '../components/ui/LoadError.vue';
import SegmentedControl from '../components/ui/SegmentedControl.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { firstError, useGatewayState, useNames } from '../composables/gateway';
import { compareDevices, deviceFaults, deviceStatus, isAdopted, matchesFilter, matchesSearch, type DeviceFilter } from '../lib/devices';
import { formatByteRate } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';

const { names, devices } = useNames();
const state = useGatewayState();
const connection = useConnectionStore();
const router = useRouter();
useApiScreen('the devices', () => [{ method: 'GET', path: '/devices?limit=500', note: 'every device, page by page (next_cursor)' }]);

const search = ref('');
const filter = ref<DeviceFilter>('all');

const devicesError = computed(() => firstError(devices));
const all = computed(() => [...(devices.data.value ?? [])].sort(compareDevices));
const filters: { value: DeviceFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'online', label: 'Online' },
  { value: 'faulted', label: 'With faults' },
  { value: 'unadopted', label: 'Not adopted' },
];
const chips = computed(() => filters.map((f) => ({ ...f, count: all.value.filter((d) => matchesFilter(d, f.value)).length })));
const rows = computed(() => all.value.filter((d) => matchesFilter(d, filter.value) && matchesSearch(d, search.value)));

const adopting = ref<DeviceView>();
const adoptOpen = ref(false);
function adopt(d: DeviceView) {
  adopting.value = d;
  adoptOpen.value = true;
}
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

const networkName = (id: string | undefined) => (id ? (names.value.network?.(id) ?? '—') : '—');
const cols = 'grid-cols-[minmax(150px,1.3fr)_130px_160px_minmax(80px,0.7fr)_170px_minmax(140px,1.2fr)_110px]';
</script>

<template>
  <PageHeader title="Devices" />
  <p class="-mt-3 mb-0 text-[13px] text-text-3">Identified by MAC address or IP · discovered from DHCP, the neighbor table and conntrack</p>

  <div class="flex flex-wrap items-center gap-3">
    <div class="flex h-[38px] w-[360px] max-w-full items-center gap-2 rounded-md border border-border-strong bg-surface px-3">
      <AppIcon name="search" class="text-text-2" />
      <input
        v-model="search"
        type="search"
        aria-label="Search devices"
        placeholder="Search name, IP or MAC"
        class="h-[34px] min-w-0 flex-1 border-0 bg-transparent text-sm text-text outline-none"
      />
    </div>
    <SegmentedControl v-model="filter" :options="chips" label="Filter" variant="chips" />
  </div>

  <section aria-label="Device list" class="card min-h-0 flex-1 overflow-hidden p-0" data-testid="device-list">
    <p v-if="devices.isLoading.value" class="m-0 p-4 text-sm text-text-2">Loading devices…</p>
    <div v-else-if="devicesError" class="p-4">
      <LoadError :error="devicesError" what="The devices are not loaded" :retry="devices.refetch" />
    </div>
    <div v-else-if="all.length === 0" class="p-4">
      <EmptyState text="No devices yet. A device appears here as soon as it gets a DHCP lease or sends traffic through the gateway.">
        <RouterLink to="/" class="btn">Back to the overview</RouterLink>
      </EmptyState>
    </div>
    <div v-else role="table" aria-label="Devices" :aria-rowcount="rows.length + 1" class="flex h-full flex-col">
      <div role="row" class="data-head grid h-10 items-center gap-3 px-4" :class="cols">
        <span role="columnheader">Name</span>
        <span role="columnheader">IP</span>
        <span role="columnheader">MAC</span>
        <span role="columnheader">Network</span>
        <span role="columnheader">Traffic ↑ / ↓</span>
        <span role="columnheader">Faults</span>
        <span role="columnheader">Status</span>
      </div>
      <div class="flex-1 overflow-auto">
        <div
          v-for="d in rows"
          :key="d.id"
          role="row"
          class="data-row grid cursor-pointer items-center gap-3 px-4 py-2 text-sm"
          :class="cols"
          data-testid="device-row"
          @click="router.push(`/devices/${d.id}`)"
        >
          <span role="cell" class="min-w-0 truncate">
            <RouterLink
              :to="`/devices/${d.id}`"
              class="no-underline"
              :class="isAdopted(d) ? 'font-medium text-text' : 'text-text-2 italic'"
              @click.stop
              >{{ d.name }}</RouterLink
            >
          </span>
          <span role="cell" class="mono truncate text-[13px]">{{ d.observed.addresses?.join(', ') || '—' }}</span>
          <span role="cell" class="mono truncate text-[13px] text-text-2">{{ d.observed.macs?.join(', ') || '—' }}</span>
          <span role="cell" class="truncate text-text-2">{{ networkName(d.network) }}</span>
          <span role="cell" class="mono text-[13px] text-text-2"
            >{{ formatByteRate(d.observed.upload_bps) }} / {{ formatByteRate(d.observed.download_bps) }}</span
          >
          <span role="cell" class="flex min-w-0 flex-wrap items-center gap-1.5">
            <FaultChip v-for="f in deviceFaults(d)" :key="f" :text="f" />
            <button
              v-if="!isAdopted(d)"
              type="button"
              class="btn btn-sm bg-surface-2"
              :disabled="blocked"
              :aria-label="`Adopt ${d.name}`"
              @click.stop="adopt(d)"
            >
              Adopt
            </button>
          </span>
          <span role="cell"><StatusDot v-bind="deviceStatus(d)" /></span>
        </div>
        <p v-if="rows.length === 0" class="m-0 px-4 py-12 text-center text-sm text-text-2" data-testid="no-match">
          No devices match<template v-if="search"> “{{ search }}”</template><template v-if="filter !== 'all'"> in this filter</template>.
        </p>
      </div>
    </div>
  </section>

  <AdoptDialog v-model:open="adoptOpen" :device="adopting" :network-name="networkName(adopting?.network)" />
</template>
