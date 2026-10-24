<script setup lang="ts">
// The DHCP reservations of a local test network (plan §2.7: every scope has its own reservations; #75):
// the devices with a fixed IP in it as the draft has them, with MAC, address, device and status (active:
// the device holds a lease on the address; pending: not applied yet, or the device has not renewed its
// lease yet; conflict: the address is leased to another MAC). A reservation the draft adds, changes or
// takes away is marked NEW, CHANGED or DELETED until it is applied, and Undo takes back only the reservation
// (the fixed IP and the MAC order) and keeps the device's other edits, an adoption or a new name (#97).
// Edit opens the device dialog.
import { computed, nextTick, ref } from 'vue';
import type { DeviceView, NetworkView } from '../../api/generated/model';
import { useDevices, useLeases } from '../../composables/gateway';
import { refocusAfterSwap } from '../../lib/focus';
import { reservationRows, undoReservation, type ReservationRow, type ReservationStatus } from '../../lib/reservations';
import { useDraftStore } from '../../stores/draft';
import DeviceDialog from '../devices/DeviceDialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import SectionCard from '../ui/SectionCard.vue';
import StatusDot, { type DotTone } from '../ui/StatusDot.vue';

const props = defineProps<{ network: NetworkView }>();
const draft = useDraftStore();
const devices = useDevices();
// a network the draft adds has no leases yet
const leases = useLeases(() => (props.network.status.state === 'pending' ? undefined : props.network.id));

const rows = computed(() => reservationRows(props.network.id, draft.baseConfig?.devices, draft.devices, leases.data.value ?? []));
const tone: Record<ReservationStatus, DotTone> = { active: 'normal', pending: 'offline', conflict: 'fault' };

function view(r: ReservationRow): DeviceView | undefined {
  return devices.data.value?.find((d) => d.id === r.id);
}
function undo(r: ReservationRow) {
  draft.setDevice(r.id, undoReservation(props.network.id, draft.baseConfig?.devices?.[r.id], draft.devices[r.id]));
}

const editOpen = ref(false);
const editing = ref<DeviceView>();
async function edit(d: DeviceView) {
  editing.value = d;
  await nextTick();
  editOpen.value = true;
}
</script>

<template>
  <SectionCard title="DHCP reservations">
    <template #actions><span class="text-xs text-text-3">{{ rows.length }} {{ rows.length === 1 ? 'reservation' : 'reservations' }}</span></template>
    <p v-if="!draft.baseConfig" class="m-0 text-sm text-text-2" role="status">Loading the reservations…</p>
    <EmptyState v-else-if="rows.length === 0" text="No device has a fixed IP in this network. Reserve in the leases makes an address static." />
    <table v-else class="w-full border-collapse text-sm" data-testid="reservations">
      <thead>
        <tr class="data-head text-left">
          <th class="py-2 font-normal">Address</th>
          <th class="py-2 font-normal">MAC</th>
          <th class="py-2 font-normal">Device</th>
          <th class="py-2 font-normal">Status</th>
          <th class="py-2 font-normal"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.id" class="data-row" :class="r.mark === 'DELETED' ? 'opacity-60' : ''" data-testid="reservation">
          <td class="mono py-2.5 text-[13px]">
            <span :class="r.mark === 'DELETED' ? 'line-through' : ''">{{ r.ip }}</span>
            <span
              v-if="r.mark"
              class="ml-1.5 rounded border px-1.5 font-sans text-xs"
              :class="r.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
              data-testid="reservation-mark"
              >{{ r.mark }}</span
            >
          </td>
          <td class="mono py-2.5 text-[13px]">{{ r.mac ?? '—' }}</td>
          <td class="py-2.5">
            <RouterLink v-if="view(r)" :to="`/devices/${r.id}`">{{ r.device.name }}</RouterLink>
            <span v-else>{{ r.device.name }}</span>
          </td>
          <td class="py-2.5"><StatusDot :tone="tone[r.status]" :label="r.label" :data-status="r.status" data-testid="reservation-status" /></td>
          <td class="py-2.5 text-right">
            <span class="inline-flex gap-1.5" data-focus-group>
              <button
                v-if="r.mark !== 'DELETED' && view(r)"
                type="button"
                class="btn btn-sm"
                :aria-label="`Edit the reservation of ${r.device.name}`"
                @click="edit(view(r)!)"
              >
                Edit
              </button>
              <button
                v-if="r.mark"
                type="button"
                class="btn btn-sm"
                :aria-label="`Undo the reservation change of ${r.device.name}`"
                @click="
                  refocusAfterSwap($event);
                  undo(r);
                "
              >
                Undo
              </button>
            </span>
          </td>
        </tr>
      </tbody>
    </table>
    <DeviceDialog v-if="editing" v-model:open="editOpen" :device="editing" />
  </SectionCard>
</template>
