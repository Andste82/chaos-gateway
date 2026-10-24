<script setup lang="ts">
// The DHCP leases of a local network (plan §2.7: DHCP is shown in the network and device views): address,
// MAC, host name, the device it belongs to and the remaining time on the gateway's clock.
//
// Reserve makes a lease static (#75), in one dialog: for a discovered device the adopt dialog with "Keep
// this address" checked (#74), for a configured device the device dialog with the lease's address as its
// fixed IP (#73); a lease whose MAC the gateway names no device for goes to the discovered device with
// that MAC. Both put the lease's MAC first, since the reservation is for the device's first MAC (decision
// on #77); for a lease whose MAC is not among its device's MACs the device dialog offers to add it first
// (decision on #98). A lease whose address is the fixed IP of its device is marked "reserved".
import { computed, nextTick, ref } from 'vue';
import type { DeviceView, DhcpLease, NetworkView } from '../../api/generated/model';
import { firstError, useDevices, useLeases } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { formatRemaining } from '../../lib/format';
import { parseIPv4 } from '../../lib/networks';
import { leaseDevice, leaseReserved, leaseTarget } from '../../lib/reservations';
import { useDraftStore } from '../../stores/draft';
import AdoptDialog from '../devices/AdoptDialog.vue';
import DeviceDialog from '../devices/DeviceDialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ network: NetworkView }>();
const leases = useLeases(() => props.network.id);
const devices = useDevices();
const now = useNow(1000);
const draft = useDraftStore();

const rows = computed(() =>
  [...(leases.data.value ?? [])]
    .sort((a, b) => (parseIPv4(a.ip) ?? 0) - (parseIPv4(b.ip) ?? 0))
    .map((l) => {
      const device = leaseDevice(l, devices.data.value ?? []);
      return { l, device, reserved: leaseReserved(l, device), target: leaseTarget(l, device, device ? draft.devices[device.id] : undefined) };
    }),
);

// the dialogs of Reserve: the device is set first, so the dialog has it when it opens
const adoptOpen = ref(false);
const editOpen = ref(false);
const target = ref<{ device: DeviceView; lease: DhcpLease }>();
async function reserve(l: DhcpLease, device: DeviceView, kind: 'adopt' | 'edit') {
  // a discovered device the gateway has not placed in a network yet is in the lease's
  target.value = { device: device.network ? device : { ...device, network: l.network }, lease: l };
  await nextTick();
  if (kind === 'adopt') adoptOpen.value = true;
  else editOpen.value = true;
}
const networkName = computed(() => (props.network.config as { name: string }).name);
const dhcpOff = computed(() => (props.network.config as { dhcp?: { enabled?: boolean } }).dhcp?.enabled === false);
</script>

<template>
  <SectionCard title="DHCP leases">
    <template #actions><span class="text-xs text-text-3">{{ rows.length }} {{ rows.length === 1 ? 'lease' : 'leases' }}</span></template>
    <p v-if="leases.isLoading.value" class="m-0 text-sm text-text-2">Loading the leases…</p>
    <LoadError v-else-if="firstError(leases)" :error="firstError(leases)" what="The leases are not loaded" :retry="leases.refetch" />
    <EmptyState v-else-if="rows.length === 0" :text="dhcpOff ? 'DHCP is off in this network: devices have static addresses.' : 'No device has a lease yet.'" />
    <table v-else class="w-full border-collapse text-sm" data-testid="leases">
      <thead>
        <tr class="data-head text-left">
          <th class="py-2 font-normal">Address</th>
          <th class="py-2 font-normal">MAC</th>
          <th class="py-2 font-normal">Host name</th>
          <th class="py-2 font-normal">Device</th>
          <th class="py-2 font-normal">State</th>
          <th class="py-2 font-normal">Expires in</th>
          <th class="py-2 font-normal"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="{ l, device, reserved, target: kind } in rows" :key="l.ip + l.mac" class="data-row" data-testid="lease">
          <td class="mono py-2.5 text-[13px]">
            {{ l.ip }}
            <span
              v-if="reserved"
              class="ml-1.5 rounded border border-accent-strong px-1.5 font-sans text-xs text-accent-text"
              title="The address is the fixed IP (DHCP reservation) of the device"
              data-testid="lease-reserved"
              >reserved</span
            >
          </td>
          <td class="mono py-2.5 text-[13px]">{{ l.mac }}</td>
          <td class="py-2.5">{{ l.hostname || '—' }}</td>
          <td class="py-2.5">
            <RouterLink v-if="device" :to="`/devices/${device.id}`">{{ device.name }}</RouterLink>
            <span v-else class="text-text-3">—</span>
          </td>
          <td class="py-2.5">{{ l.state ?? 'active' }}</td>
          <td class="mono py-2.5 text-[13px]">{{ formatRemaining(new Date(l.expires_at).getTime() - now.getTime()) }}</td>
          <td class="py-2.5 text-right">
            <button
              v-if="device && kind"
              type="button"
              class="btn btn-sm"
              :aria-label="`Reserve ${l.ip} for ${l.mac}`"
              title="Make the address static: a DHCP reservation for the device"
              @click="reserve(l, device, kind)"
            >
              Reserve
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <AdoptDialog v-model:open="adoptOpen" :device="target?.device" :lease="target?.lease" :network-name="networkName" />
    <DeviceDialog
      v-if="target"
      v-model:open="editOpen"
      :device="target.device"
      :reserve="{ ip: target.lease.ip, mac: target.lease.mac, network: target.lease.network }"
    />
  </SectionCard>
</template>
