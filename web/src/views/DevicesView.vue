<script setup lang="ts">
// Devices (plan §2.17): name, IP, MAC, network, traffic, faults, status; search; the filter chips
// All / Online / With faults / Not adopted / Adopted; adopt a discovered device; a row opens the device.
// A discovered device is listed under the host name of its DHCP lease, its generated name below it; a
// configured device carries the ADOPTED badge, with its host name below it when that differs (#78).
// The Faults column shows the winning faults the gateway reports per device (DeviceView.effective) as
// the same compact chips as the overview. A configured device is edited in the device dialog (#73) or
// removed from the configuration (references listed first); an adoption or a removal that is in the
// draft but not applied yet is marked, and Undo takes it back.
import { computed, nextTick, ref } from 'vue';
import { useRouter } from 'vue-router';
import type { DeviceView } from '../api/generated/model';
import AdoptDialog from '../components/devices/AdoptDialog.vue';
import DeviceDialog from '../components/devices/DeviceDialog.vue';
import RemoveDeviceDialog from '../components/devices/RemoveDeviceDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import AppIcon from '../components/ui/AppIcon.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import FaultChip from '../components/ui/FaultChip.vue';
import LoadError from '../components/ui/LoadError.vue';
import SegmentedControl from '../components/ui/SegmentedControl.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { firstError, useConfigFaults, useGatewayState, useNames, useOverlays } from '../composables/gateway';
import {
  compareDevices,
  deviceFaults,
  deviceStatus,
  displayName,
  isAdopted,
  isConfigured,
  matchesFilter,
  matchesSearch,
  nameDetail,
  type DeviceFilter,
  type FaultBodies,
} from '../lib/devices';
import { refocusAfterSwap } from '../lib/focus';
import { formatByteRate } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDraftStore } from '../stores/draft';

const { names, devices } = useNames();
const overlays = useOverlays();
const configFaults = useConfigFaults();
const state = useGatewayState();
const connection = useConnectionStore();
const draft = useDraftStore();
const router = useRouter();
useApiScreen('the devices', () => [
  { method: 'GET', path: '/devices?limit=500', note: 'every device with its winning faults (effective), page by page (next_cursor)' },
  { method: 'GET', path: '/overlays', note: 'the parameters of the winning overlays, for the fault chips' },
  { method: 'GET', path: '/faults', note: 'the parameters of the winning configured faults' },
]);

const search = ref('');
const filter = ref<DeviceFilter>('all');

const devicesError = computed(() => firstError(devices));
const all = computed(() => [...(devices.data.value ?? [])].sort(compareDevices));
const filters: { value: DeviceFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'online', label: 'Online' },
  { value: 'faulted', label: 'With faults' },
  { value: 'unadopted', label: 'Not adopted' },
  { value: 'adopted', label: 'Adopted' },
];
const chips = computed(() => filters.map((f) => ({ ...f, count: all.value.filter((d) => matchesFilter(d, f.value)).length })));
const rows = computed(() => all.value.filter((d) => matchesFilter(d, filter.value) && matchesSearch(d, search.value)));

const bodies = computed<FaultBodies>(() => {
  const ov = new Map((overlays.data.value ?? []).map((o) => [o.id, o]));
  const cf = new Map((configFaults.data.value ?? []).map((f) => [f.id, f.config]));
  return { overlay: (id) => ov.get(id), config: (id) => cf.get(id) };
});

/** The name a discovered device is adopted under in the draft, while that is not applied yet. */
function adoptedAs(d: DeviceView): string | undefined {
  return isAdopted(d) ? undefined : (draft.devices[d.id]?.name ?? undefined);
}

const adopting = ref<DeviceView>();
const adoptOpen = ref(false);
function adopt(d: DeviceView) {
  adopting.value = d;
  adoptOpen.value = true;
}
function undoAdoption(e: Event, d: DeviceView) {
  refocusAfterSwap(e);
  draft.setDevice(d.id, null);
}
/** A configured device whose removal from the configuration is in the draft, not applied yet. */
function removed(d: DeviceView): boolean {
  return isConfigured(d) && draft.devices[d.id] === null;
}
function undoRemoval(e: Event, d: DeviceView) {
  refocusAfterSwap(e);
  draft.restoreDevice(d.id);
}

// the device dialog (#73) and the removal dialog, for a configured device; mounted for the device
// first, then opened, so the dialog builds its form from that device
const editing = ref<DeviceView>();
const editOpen = ref(false);
const removing = ref<DeviceView>();
const removeOpen = ref(false);
async function edit(d: DeviceView) {
  editing.value = d;
  await nextTick();
  editOpen.value = true;
}
async function remove(d: DeviceView) {
  removing.value = d;
  await nextTick();
  removeOpen.value = true;
}
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

const networkName = (id: string | undefined) => (id ? (names.value.network?.(id) ?? '—') : '—');
const cols = 'grid-cols-[minmax(150px,1.3fr)_130px_160px_minmax(80px,0.7fr)_170px_minmax(140px,1.2fr)_90px_136px]';
</script>

<template>
  <PageHeader title="Devices" />
  <p class="-mt-3 mb-0 text-[13px] text-text-3">Identified by MAC address or IP · discovered from DHCP, the neighbor table and conntrack</p>

  <div class="flex flex-wrap items-center gap-3">
    <!-- the input has no outline of its own: the box around it shows the focus ring (plan §2.17) -->
    <div
      class="flex h-[38px] w-[360px] max-w-full items-center gap-2 rounded-md border border-border-strong bg-surface px-3 focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent"
      data-testid="device-search"
    >
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
    <p v-if="devices.isLoading.value" class="m-0 p-4 text-sm text-text-2" role="status">Loading devices…</p>
    <div v-else-if="devicesError" class="p-4">
      <LoadError :error="devicesError" what="The devices are not loaded" :retry="devices.refetch" />
    </div>
    <div v-else-if="all.length === 0" class="p-4">
      <EmptyState text="No devices yet: a device appears as soon as it gets a DHCP lease or sends traffic through a test network.">
        <RouterLink to="/networks" class="btn btn-primary">Check the test networks</RouterLink>
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
        <span role="columnheader"><span class="sr-only">Actions</span></span>
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
          <span role="cell" class="flex min-w-0 flex-col items-start gap-0.5">
            <span class="flex max-w-full min-w-0 items-center gap-1.5">
              <RouterLink
                :to="`/devices/${d.id}`"
                class="min-w-0 truncate no-underline"
                :class="[isAdopted(d) ? 'font-medium text-text' : 'text-text-2 italic', removed(d) ? 'line-through opacity-60' : '']"
                data-testid="device-name"
                @click.stop
                >{{ displayName(d) }}</RouterLink
              >
              <span
                v-if="isConfigured(d)"
                class="shrink-0 rounded border border-border-strong px-1.5 text-xs text-text-2"
                title="In the configuration: adopted here, or configured via YAML or the API"
                data-testid="device-adopted-badge"
                >ADOPTED</span
              >
            </span>
            <span v-if="nameDetail(d)" class="mono max-w-full truncate text-xs text-text-3" data-testid="device-name-detail">{{ nameDetail(d) }}</span>
            <span
              v-if="removed(d)"
              class="rounded border border-block-border px-1.5 text-xs text-block-text"
              data-testid="device-removed-mark"
              >REMOVED · not applied yet</span
            >
            <span
              v-if="adoptedAs(d)"
              class="rounded border border-accent-strong px-1.5 text-xs text-accent-text"
              data-testid="device-adopted-mark"
              >ADOPTED as {{ adoptedAs(d) }} · not applied yet</span
            >
          </span>
          <span role="cell" class="mono truncate text-[13px]">{{ d.observed.addresses?.join(', ') || '—' }}</span>
          <span role="cell" class="mono truncate text-[13px] text-text-2">{{ d.observed.macs?.join(', ') || '—' }}</span>
          <span role="cell" class="truncate text-text-2">
            <RouterLink v-if="d.network && networkName(d.network) !== '—'" :to="`/networks/${d.network}`" class="text-text-2" @click.stop>{{
              networkName(d.network)
            }}</RouterLink>
            <template v-else>—</template>
          </span>
          <span role="cell" class="mono text-[13px] text-text-2"
            >{{ formatByteRate(d.observed.upload_bps) }} / {{ formatByteRate(d.observed.download_bps) }}</span
          >
          <span role="cell" class="flex min-w-0 flex-wrap items-center gap-1.5" data-testid="device-faults">
            <FaultChip v-for="f in deviceFaults(d, bodies)" :key="f" :text="f" />
          </span>
          <span role="cell"><StatusDot v-bind="deviceStatus(d)" /></span>
          <span role="cell" class="flex justify-end gap-1.5" data-focus-group>
            <template v-if="isConfigured(d)">
              <button
                v-if="removed(d)"
                type="button"
                class="btn btn-sm"
                :aria-label="`Undo the removal of ${d.name}`"
                data-focus-swap
                @click.stop="undoRemoval($event, d)"
              >
                Undo
              </button>
              <template v-else>
                <button type="button" class="btn btn-sm" :disabled="blocked" :aria-label="`Edit ${d.name}`" @click.stop="edit(d)">Edit</button>
                <button
                  type="button"
                  class="btn btn-sm"
                  :disabled="blocked"
                  :aria-label="`Remove ${d.name} from the configuration`"
                  data-focus-swap
                  @click.stop="remove(d)"
                >
                  Remove
                </button>
              </template>
            </template>
            <template v-else-if="!isAdopted(d)">
              <button
                v-if="adoptedAs(d)"
                type="button"
                class="btn btn-sm"
                :aria-label="`Undo the adoption of ${displayName(d)}`"
                data-focus-swap
                @click.stop="undoAdoption($event, d)"
              >
                Undo
              </button>
              <button
                v-else
                type="button"
                class="btn btn-sm bg-surface-2"
                :disabled="blocked"
                :aria-label="`Adopt ${displayName(d)}`"
                data-focus-swap
                @click.stop="adopt(d)"
              >
                Adopt
              </button>
            </template>
          </span>
        </div>
        <p v-if="rows.length === 0" class="m-0 px-4 py-12 text-center text-sm text-text-2" data-testid="no-match">
          No devices match<template v-if="search"> “{{ search }}”</template><template v-if="filter !== 'all'"> in this filter</template>.
        </p>
      </div>
    </div>
  </section>

  <AdoptDialog v-model:open="adoptOpen" :device="adopting" :network-name="networkName(adopting?.network)" />
  <DeviceDialog v-if="editing" v-model:open="editOpen" :device="editing" />
  <RemoveDeviceDialog v-if="removing" v-model:open="removeOpen" :device="removing" />
</template>
