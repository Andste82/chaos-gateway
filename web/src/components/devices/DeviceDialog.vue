<script setup lang="ts">
// Edit a configured device (plan §2.3, §2.7, §2.17 "Devices"; #73): its name, its network (a local test
// network) and its fixed IP, the DHCP reservation for its first MAC. The checks of the gateway stand
// under their fields; the notes under the fixed IP do not stop the change (an address in the dynamic
// pool, leased to another client now, held by the device with a MAC other than the reserved one (#96),
// DHCP off in the network, when the reservation takes effect, which MAC it is for). A new or changed reservation puts the MAC of the current lease first. "Preview & apply"
// puts the device into the configuration draft and opens the preview-and-apply drawer, against the
// revision the dialog was opened on, so a revision applied meanwhile meets the edit in the
// concurrent-change dialog. A device the gateway refused opens again with its messages under their
// fields, until the form is changed.
//
// The Reserve action of the DHCP leases card (#75) opens the dialog with `reserve`: the lease's address as
// the fixed IP in the lease's network, and the lease's MAC as the one put first. A lease whose MAC is not
// among the device's MACs (the gateway names the device by the lease's address, say) would be reserved
// for a MAC that does not hold it: the dialog says so and offers to add the lease's MAC to the device as
// its first MAC, checked (decision on #98), unless the MAC identifies another device already; removing the
// fixed IP withdraws the offer.
//
// The dialog lists the device's MACs in the order the change gives them and can make any of them the
// first one, the MAC the reservation is for ("Use for the reservation", #111; reordering only, no MAC is
// added or removed here); the hint of a lease the device holds with another of its MACs (#96) offers the
// same for that MAC. A MAC chosen so stays first: a new reservation no longer puts the lease's MAC first.
import { computed, ref, useId, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { Device, DeviceView, LanNetwork, WireGuardNetwork } from '../../api/generated/model';
import { useGatewayState, useLeases } from '../../composables/gateway';
import {
  deviceFieldOf,
  deviceFormErrors,
  deviceFromForm,
  draftDevices,
  fixedIpHints,
  foreignLeaseMac,
  formFromDevice,
  macOwner,
  ownMac,
  warningHint,
  withFirstMac,
  type DeviceField,
  type DeviceForm,
  type DeviceFormContext,
} from '../../lib/deviceForm';
import { subnetText } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{
  device: DeviceView;
  /** A lease to make static (the leases card): its address is prefilled as the fixed IP, in its network. */
  reserve?: { ip: string; mac: string; network: string };
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();
const uid = useId();

const form = ref<DeviceForm>({ name: '', network: '', fixedIp: '' });
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const refused = ref<Record<string, string>>({});
let refusedForm = '';
/** The revision the form was built on (a revision applied meanwhile meets the edit in the conflict dialog). */
let origin: EditOrigin | undefined;
/** The device as the configuration has it when the dialog opened (the draft's edit of it included). */
const before = ref<Device>({ name: '' });
/** Its fixed IP and network in the draft's base: a reservation that is not new takes no renewal to take effect. */
const baseFixedIp = ref<string>();
const baseNetwork = ref<string>();
/** Whether the lease's MAC, when it is not among the device's MACs, is added as its first (#98). */
const addMac = ref(true);
/** The MAC the admin made the first one (#111); undefined while the order is the configuration's. */
const firstMac = ref<string>();

/** The device as the draft has it: its edit, the draft's base, or the configuration the gateway sent with the view. */
function current(): Device {
  const id = props.device.id;
  return draft.devices[id] ?? draft.baseConfig?.devices?.[id] ?? props.device.config ?? { name: props.device.name };
}

watch(
  open,
  (o) => {
    if (!o) {
      apiPanel.draft = undefined;
      return;
    }
    before.value = current();
    const base = draft.baseConfig?.devices?.[props.device.id] ?? props.device.config;
    baseFixedIp.value = base?.fixed_ip;
    baseNetwork.value = base?.network;
    form.value = formFromDevice(before.value);
    if (props.reserve) form.value = { ...form.value, network: props.reserve.network, fixedIp: props.reserve.ip };
    origin = draft.editOrigin();
    refused.value = draft.serverErrors[props.device.id] ?? {};
    addMac.value = true;
    firstMac.value = undefined;
    refusedForm = JSON.stringify(form.value);
    tried.value = Object.keys(refused.value).length > 0;
    error.value = '';
  },
);

/** The networks a device can be in: the local test networks, and a WireGuard network it is in already. */
const networks = computed(() =>
  Object.entries(draft.effectiveNetworks)
    .filter(([id, n]) => n.type === 'lan' || id === before.value.network)
    .map(([id, n]) => ({ id, name: n.name, label: n.type === 'lan' ? `${n.name} · ${subnetText((n as LanNetwork).address)}` : `${n.name} (WireGuard)` })),
);
const network = computed(() => (form.value.network ? draft.effectiveNetworks[form.value.network] : undefined));
const leases = useLeases(() => (network.value?.type === 'lan' ? form.value.network : undefined));
const configuredMacs = computed(() => before.value.identifiers?.macs ?? []);
const leaseMac = computed(() => (props.reserve ? props.reserve.mac : props.device.observed.lease?.mac));
/** The address of the current lease, when it is in the form's network (a reservation of it keeps the address). */
const leaseIp = computed(() => {
  const l = props.reserve ?? props.device.observed.lease;
  return l && (!l.network || l.network === form.value.network) ? l.ip : undefined;
});

/** The devices as the draft has them (the other reservations, the names taken). */
const devices = computed<Record<string, Device>>(() => draftDevices(draft.baseConfig?.devices, draft.devices));
/**
 * The reserved lease's MAC when it is not among the device's MACs; the dialog offers to add it (#98) while
 * the form keeps a fixed IP (with the fixed IP removed there is no reservation to add it for).
 */
const foreignMac = computed(() => (props.reserve && form.value.fixedIp.trim() ? foreignLeaseMac(configuredMacs.value, props.reserve.mac) : undefined));
/** The other device the foreign MAC identifies already: it cannot be added to this one. */
const foreignOwner = computed(() => (foreignMac.value ? macOwner(foreignMac.value, props.device.id, devices.value) : undefined));
const addingMac = computed(() => !!foreignMac.value && !foreignOwner.value && addMac.value);
/** The device the form changes: with the lease's MAC added first when the offer is taken, and the MAC chosen first. */
const edited = computed(() => {
  const d = addingMac.value ? withFirstMac(before.value, foreignMac.value!) : before.value;
  const first = ownMac(d.identifiers?.macs ?? [], firstMac.value);
  return first ? withFirstMac(d, first) : d;
});
/** Whether the admin chose the first MAC (it is among the device's MACs): the lease's MAC is not put first then. */
const ordered = computed(() => !!ownMac(edited.value.identifiers?.macs ?? [], firstMac.value));
const macs = computed(() => edited.value.identifiers?.macs ?? []);
const takenNames = computed(() => {
  const out = Object.entries(devices.value)
    .filter(([id]) => id !== props.device.id)
    .map(([, d]) => d.name);
  for (const [id, n] of Object.entries(draft.effectiveNetworks)) {
    if (n.type === 'wireguard' && (n as WireGuardNetwork).kind === 'hub') out.push(...Object.values(draft.effectiveClients(id)).map((c) => c.name));
  }
  out.push(...Object.values(draft.baseConfig?.probes ?? {}).map((p) => p.name));
  return out;
});

const ctx = computed<DeviceFormContext>(() => ({
  device: props.device.id,
  network: network.value,
  networkId: form.value.network || undefined,
  macs: macs.value,
  devices: devices.value,
  takenNames: takenNames.value,
}));
const formErrors = computed(() => deviceFormErrors(form.value, ctx.value));
const result = computed(() => deviceFromForm(form.value, edited.value, ordered.value ? undefined : leaseMac.value));
/** The device's MACs in the order the change gives them (the first is the one a reservation is for). */
const resultMacs = computed(() => result.value.identifiers?.macs ?? []);
const hints = computed(() =>
  formErrors.value.fixedIp
    ? []
    : fixedIpHints(form.value.fixedIp, {
        ...ctx.value,
        before: baseFixedIp.value,
        beforeNetwork: baseNetwork.value,
        leases: leases.data.value,
        leaseMac: leaseMac.value,
        leaseIp: leaseIp.value,
        // the notes speak of the order the request carries: the lease's MAC is put first only when
        // `deviceFromForm` puts it there (a draft's chosen order, the reservation already in it, stays)
        ordered: ordered.value || resultMacs.value[0] === macs.value[0],
      }),
);
const server = computed(() => {
  const byField: Partial<Record<DeviceField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = deviceFieldOf(pointer);
    if (f) byField[f] = message;
    else general.push(message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
// the form's own checks speak for a field once it was changed, and for all of them once "Preview & apply" was pressed
function shown(f: DeviceField): string | undefined {
  if (server.value.byField[f]) return server.value.byField[f];
  const changed = JSON.stringify(form.value[f]) !== JSON.stringify(formFromDevice(before.value)[f]);
  return tried.value || changed ? formErrors.value[f] : undefined;
}

const title = computed(() => `Edit ${before.value.name || props.device.name}`);
/** The first MAC changes with this edit (by the admin's choice or a new reservation on the lease's MAC). */
const firstChanged = computed(() => !!resultMacs.value.length && !!configuredMacs.value.length && resultMacs.value[0] !== configuredMacs.value[0]);

/** Make one of the device's MACs its first, the one the reservation is for (#111). */
function useFirst(mac: string) {
  firstMac.value = ownMac(edited.value.identifiers?.macs ?? [], mac) ?? mac;
}
watch(
  [open, result],
  ([o]) => {
    if (!o) return;
    apiPanel.draft = {
      title: `${title.value} (configuration)`,
      calls: [
        {
          method: 'POST',
          path: '/revisions',
          headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base ?? state.data.value?.active_revision}"` },
          body: { devices: { [props.device.id]: result.value } },
          note: 'the candidate; then …/preview and …/apply',
        },
      ],
    };
  },
  { deep: true, immediate: true },
);

async function submit() {
  tried.value = true;
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty && !draft.baseConfig) draft.track(await getActiveRevision());
    const value = result.value;
    draft.editFrom(origin, () => draft.setDevice(props.device.id, value));
    open.value = false;
    if (draft.dirty) draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}

const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);
</script>

<template>
  <ModalDialog
    v-model:open="open"
    :title="title"
    description="The device's name, its network and its fixed IP, a DHCP reservation for its first MAC. The change goes into a new revision."
    width="560px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="device-form" @submit.prevent="submit">
      <div>
        <label class="label" :for="fid('name')">Name</label>
        <input
          :id="fid('name')"
          v-model="form.name"
          class="input"
          :aria-invalid="!!shown('name')"
          :aria-describedby="shown('name') ? eid('name') : undefined"
        />
        <p v-if="shown('name')" :id="eid('name')" class="field-error">{{ shown('name') }}</p>
      </div>
      <div>
        <label class="label" :for="fid('network')">Network</label>
        <select
          :id="fid('network')"
          v-model="form.network"
          class="input"
          :aria-invalid="!!shown('network')"
          :aria-describedby="shown('network') ? eid('network') : undefined"
        >
          <option value="">No network</option>
          <option v-for="n in networks" :key="n.id" :value="n.id">{{ n.label }}</option>
        </select>
        <p v-if="shown('network')" :id="eid('network')" class="field-error">{{ shown('network') }}</p>
      </div>
      <div>
        <label class="label" :for="fid('fixedIp')">Fixed IP (DHCP reservation)</label>
        <div class="flex items-center gap-2">
          <input
            :id="fid('fixedIp')"
            v-model="form.fixedIp"
            class="input mono"
            :placeholder="network?.type === 'lan' ? 'e.g. 10.10.0.50; empty for an address from the pool' : 'empty for none'"
            :aria-invalid="!!shown('fixedIp')"
            :aria-describedby="shown('fixedIp') ? eid('fixedIp') : hints.length ? fid('fixedIp-hints') : undefined"
          />
          <button v-if="form.fixedIp" type="button" class="btn btn-sm" :aria-label="`Remove the fixed IP ${form.fixedIp}`" @click="form.fixedIp = ''">
            Remove
          </button>
        </div>
        <p v-if="shown('fixedIp')" :id="eid('fixedIp')" class="field-error" data-testid="fixed-ip-error">{{ shown('fixedIp') }}</p>
        <ul v-if="hints.length" :id="fid('fixedIp-hints')" class="m-0 mt-1.5 flex list-none flex-col gap-1 p-0 text-xs text-text-2" aria-live="polite">
          <li
            v-for="h in hints"
            :key="h.kind"
            :class="warningHint(h.kind) ? 'text-fault-text' : ''"
            data-testid="fixed-ip-hint"
            :data-hint="h.kind"
          >
            {{ h.text }}
            <button
              v-if="h.kind === 'other_mac' && h.mac"
              type="button"
              class="btn btn-sm ml-1"
              data-testid="put-mac-first"
              @click="useFirst(h.mac)"
            >
              Put <span class="mono">{{ h.mac }}</span> first
            </button>
          </li>
        </ul>
      </div>
      <div v-if="resultMacs.length">
        <p class="label" :id="fid('macs')">MAC addresses</p>
        <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm" :aria-labelledby="fid('macs')" data-testid="device-macs">
          <li v-for="(m, i) in resultMacs" :key="m" class="flex items-center gap-2" data-testid="device-mac">
            <span class="mono">{{ m }}</span>
            <span v-if="i === 0" class="text-xs text-text-2" data-testid="device-mac-first">first · for the reservation</span>
            <button v-else type="button" class="btn btn-sm ml-auto" :aria-label="`Use ${m} for the reservation`" @click="useFirst(m)">Use for the reservation</button>
          </li>
        </ul>
        <p v-if="firstChanged" class="m-0 mt-1.5 text-xs text-text-2" data-testid="device-macs-effect">
          {{ resultMacs[0] }} becomes the device's first MAC (it was {{ configuredMacs[0] }}): a DHCP reservation is for it.
        </p>
      </div>
      <div v-if="foreignMac && reserve" class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="reserve-mac">
        <p class="m-0">
          The lease of <span class="mono">{{ reserve.ip }}</span> is held by MAC <span class="mono">{{ foreignMac }}</span>, which is not among the device's
          MACs<template v-if="configuredMacs.length"> (<span class="mono">{{ configuredMacs.join(', ') }}</span>)</template>. A DHCP reservation is for the
          device's first MAC.
        </p>
        <p v-if="foreignOwner" class="m-0 mt-2 text-fault-text" data-testid="reserve-mac-effect">
          {{ foreignMac }} already identifies device {{ foreignOwner.name }}, so it cannot be added to this one<template v-if="configuredMacs.length"
            >: the reservation is for {{ configuredMacs[0] }}, which does not hold this lease</template
          >.
        </p>
        <template v-else>
          <label class="mt-2 flex items-center gap-2 text-text">
            <input v-model="addMac" type="checkbox" />
            <span>Add <span class="mono">{{ foreignMac }}</span> to the device as its first MAC</span>
          </label>
          <p class="m-0 mt-1.5 text-xs" :class="addMac && macs[0] === foreignMac ? 'text-text-2' : 'text-fault-text'" data-testid="reserve-mac-effect">
            <template v-if="!addMac && configuredMacs.length">
              The reservation is for {{ macs[0] }}, which does not hold this lease: it takes effect only when the device asks for an address with that MAC.
            </template>
            <template v-else-if="!addMac">Without a MAC the device cannot have a reservation.</template>
            <template v-else-if="macs[0] !== foreignMac">
              {{ foreignMac }} is added as another MAC of the device; the reservation is for {{ macs[0] }}, which does not hold this lease.
            </template>
            <template v-else-if="configuredMacs.length">
              The reservation is for {{ foreignMac }}, the MAC that holds the lease; the device keeps {{ configuredMacs.join(', ') }} as its other
              {{ configuredMacs.length === 1 ? 'MAC' : 'MACs' }}.
            </template>
            <template v-else>The reservation is for {{ foreignMac }}, the MAC that holds the lease.</template>
          </p>
        </template>
      </div>

      <p v-if="server.general.length" class="field-error" role="alert" data-testid="device-form-refused">The gateway refused the device: {{ server.general.join(' ') }}</p>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; change the device after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
