<script setup lang="ts">
// Edit a configured device (plan §2.3, §2.7, §2.17 "Devices"; #73): its name, its network (a local test
// network) and its fixed IP, the DHCP reservation for its first MAC. The checks of the gateway stand
// under their fields; the notes under the fixed IP do not stop the change (an address in the dynamic
// pool, leased to another client now, DHCP off in the network, when the reservation takes effect, which
// MAC it is for). A new or changed reservation puts the MAC of the current lease first. "Preview & apply"
// puts the device into the configuration draft and opens the preview-and-apply drawer, against the
// revision the dialog was opened on, so a revision applied meanwhile meets the edit in the
// concurrent-change dialog. A device the gateway refused opens again with its messages under their
// fields, until the form is changed.
import { computed, ref, useId, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { Device, DeviceView, LanNetwork, WireGuardNetwork } from '../../api/generated/model';
import { useGatewayState, useLeases } from '../../composables/gateway';
import {
  deviceFieldOf,
  deviceFormErrors,
  deviceFromForm,
  fixedIpHints,
  formFromDevice,
  type DeviceField,
  type DeviceForm,
  type DeviceFormContext,
} from '../../lib/deviceForm';
import { subnetText } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ device: DeviceView }>();
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
    origin = draft.editOrigin();
    refused.value = draft.serverErrors[props.device.id] ?? {};
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
const macs = computed(() => before.value.identifiers?.macs ?? []);
const leaseMac = computed(() => props.device.observed.lease?.mac);

/** The devices as the draft has them (the other reservations, the names taken). */
const devices = computed<Record<string, Device>>(() => {
  const out: Record<string, Device> = { ...(draft.baseConfig?.devices ?? {}) };
  for (const [id, d] of Object.entries(draft.devices)) {
    if (d === null) delete out[id];
    else out[id] = d;
  }
  return out;
});
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
const hints = computed(() =>
  formErrors.value.fixedIp
    ? []
    : fixedIpHints(form.value.fixedIp, { ...ctx.value, before: baseFixedIp.value, beforeNetwork: baseNetwork.value, leases: leases.data.value, leaseMac: leaseMac.value }),
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
const result = computed(() => deviceFromForm(form.value, before.value, leaseMac.value));
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
            :class="h.kind === 'pool' || h.kind === 'leased' || h.kind === 'dhcp_off' ? 'text-fault-text' : ''"
            data-testid="fixed-ip-hint"
            :data-hint="h.kind"
          >
            {{ h.text }}
          </li>
        </ul>
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
