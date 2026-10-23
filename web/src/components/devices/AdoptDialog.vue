<script setup lang="ts">
// Adopt a discovered device (plan §2.3, §2.17): it becomes a configured device under the same UUID,
// with a name and its MAC (or addresses) and its network. The dialog asks for the name and says in
// words what will change; "Preview & apply" puts the device into the configuration draft and opens
// the preview-and-apply drawer, like every configuration change: the dry run, then the apply with
// the verified toast. A configuration changed by someone else meanwhile leads to the
// concurrent-change dialog (reload and reapply). Backing out of the drawer keeps the adoption in the
// unapplied-changes bar.
//
// A device with a current lease in a local test network with DHCP on can keep its address (#74): the
// box "Keep this address" adds a fixed IP, the DHCP reservation for the device's first MAC, prefilled
// with the lease's address, and puts the lease's MAC first (decision on #77). The address is checked
// as in the device dialog (lib/deviceForm.ts), with the same notes that do not stop it (an address in
// the dynamic pool is allowed, decision on #76). The Reserve action of the DHCP leases card (#75) opens
// the dialog with that lease (`lease`) and the box checked.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { Device, DeviceView, DhcpLease } from '../../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../../api/generated/zod/system/system';
import { useActiveConfig, useGatewayState, useLeases } from '../../composables/gateway';
import { adoptReservation, draftDevices, fixedIpError, fixedIpHints, reservationMacs, type ReservationContext } from '../../lib/deviceForm';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{
  device: DeviceView | undefined;
  networkName?: string;
  /** A lease of the device to keep (the leases card's Reserve): it stands for the device's current lease, and the box is checked. */
  lease?: DhcpLease;
}>();
const open = defineModel<boolean>('open', { required: true });

const state = useGatewayState();
const draft = useDraftStore();
// the networks of the configuration: whether the device's network hands out addresses by DHCP
useActiveConfig();

const name = ref('');
const error = ref('');
const busy = ref(false);
const keep = ref(false);
const fixedIp = ref('');

const ips = computed(() => props.device?.observed.addresses ?? []);
const base = computed(() => draft.base ?? state.data.value?.active_revision);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);

const networkId = computed(() => props.device?.network);
const network = computed(() => (networkId.value ? draft.effectiveNetworks[networkId.value] : undefined));
/** The current lease the device can keep; undefined when no reservation is offered. */
const lease = computed(() => {
  const d = props.device;
  if (!d) return undefined;
  return adoptReservation(props.lease ? { ...d, observed: { ...d.observed, lease: props.lease } } : d, network.value);
});
const reserving = computed(() => keep.value && !!lease.value);
/** The MACs as they go into the configuration: with a reservation, the lease's MAC first (it is for the first MAC). */
const macs = computed(() => {
  const observed = props.device?.observed.macs ?? [];
  return reserving.value ? reservationMacs(observed, lease.value?.mac) : observed;
});

watch(open, (o) => {
  if (!o || !props.device) return;
  // the host name of the lease to keep, else of the device's current lease
  const hostname = (props.lease ?? props.device.observed.lease)?.hostname;
  name.value = hostname && nameRegExp.test(hostname) ? hostname : props.device.name;
  error.value = '';
  keep.value = !!props.lease;
  fixedIp.value = lease.value?.ip ?? '';
});
// the configuration can come after the dialog opened; keyed on the address, so a refresh of the device
// that keeps its lease does not fill a field the user has just emptied
watch(
  () => lease.value?.ip,
  (ip) => {
    if (ip && (!keep.value || !fixedIp.value)) fixedIp.value = ip;
  },
);

const nameError = computed(() =>
  nameRegExp.test(name.value) ? '' : 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.',
);

const leases = useLeases(() => (reserving.value ? networkId.value : undefined));
const ctx = computed<ReservationContext>(() => ({
  device: props.device?.id,
  network: network.value,
  networkId: networkId.value,
  macs: props.device?.observed.macs ?? [],
  devices: draftDevices(draft.baseConfig?.devices, draft.devices),
}));
const ipError = computed(() => (reserving.value ? fixedIpError(fixedIp.value, ctx.value) : undefined));
const hints = computed(() =>
  reserving.value && !ipError.value
    ? fixedIpHints(fixedIp.value, { ...ctx.value, leases: leases.data.value, leaseMac: lease.value?.mac, leaseIp: lease.value?.ip })
    : [],
);
/** The fixed IP the device gets; empty for none. */
const reserved = computed(() => (reserving.value && !ipError.value ? fixedIp.value.trim() : ''));

async function adopt() {
  const d = props.device;
  if (!d || nameError.value || ipError.value) return;
  busy.value = true;
  error.value = '';
  try {
    // the draft's base: the active revision now, unless the draft already has edits on another one
    if (!draft.dirty) draft.track(await getActiveRevision());
    const identifiers = macs.value.length ? { macs: macs.value } : { ipv4: ips.value };
    const device: Device = { name: name.value, identifiers, ...(d.network ? { network: d.network } : {}) };
    if (reserved.value) device.fixed_ip = reserved.value;
    draft.setDevice(d.id, device);
    open.value = false;
    draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog v-model:open="open" title="Adopt device" description="Add the discovered device to the configuration, so rules and faults can name it.">
    <form class="flex flex-col gap-4" novalidate @submit.prevent="adopt">
      <div>
        <label class="label" for="adopt-name">Name</label>
        <input id="adopt-name" v-model="name" class="input" :aria-invalid="!!nameError" aria-describedby="adopt-name-error" />
        <p v-if="nameError" id="adopt-name-error" class="field-error">{{ nameError }}</p>
      </div>
      <div v-if="lease" data-testid="adopt-reservation">
        <label class="flex items-center gap-2 text-sm text-text">
          <input v-model="keep" type="checkbox" />
          Keep this address (DHCP reservation)
          <span v-if="!keep" class="mono text-text-2">{{ lease.ip }}</span>
        </label>
        <div v-if="keep" class="mt-2">
          <label class="label" for="adopt-fixed-ip">Fixed IP (DHCP reservation)</label>
          <input
            id="adopt-fixed-ip"
            v-model="fixedIp"
            class="input mono"
            :aria-invalid="!!ipError"
            :aria-describedby="ipError ? 'adopt-fixed-ip-error' : hints.length ? 'adopt-fixed-ip-hints' : undefined"
          />
          <p v-if="ipError" id="adopt-fixed-ip-error" class="field-error" data-testid="fixed-ip-error">{{ ipError }}</p>
          <ul v-if="hints.length" id="adopt-fixed-ip-hints" class="m-0 mt-1.5 flex list-none flex-col gap-1 p-0 text-xs text-text-2" aria-live="polite">
            <li
              v-for="h in hints"
              :key="h.kind"
              :class="h.kind === 'pool' || h.kind === 'leased' || h.kind === 'dhcp_off' ? 'text-fault-text' : ''"
              data-testid="fixed-ip-hint"
              :data-hint="h.kind"
            >
              {{ h.text }}
            </li>
          </ul>
        </div>
      </div>
      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="adopt-preview">
        <div class="mb-1 text-xs text-text-3">What changes</div>
        Device <strong>{{ name || '…' }}</strong> is added
        <template v-if="macs.length">with MAC <span class="mono">{{ macs.join(', ') }}</span></template>
        <template v-else>with address <span class="mono">{{ ips.join(', ') }}</span></template>
        <template v-if="networkName"> in network {{ networkName }}</template>
        <template v-if="reserved"> and always gets <span class="mono">{{ reserved }}</span> from DHCP</template>
        in a new revision based on revision {{ base }}.<template v-if="!reserved"> Traffic is not changed.</template>
      </div>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; adopt after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || !!nameError || !!ipError || pendingConfirm">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
