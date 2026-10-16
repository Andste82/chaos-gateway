<script setup lang="ts">
// Add or edit a port forward (plan §2.17 "Networks", M35a): the protocol, the entry side (the uplink or,
// in the three-port topology, the management network), the external port or range, the target (a device,
// whose address the forward follows, or an IPv4 address in a local test network), the target port or
// range (empty: the external one; a range is shifted one to one), enabled, name and description. Each
// check stands under its field; the forward goes into the configuration draft and the preview-and-apply
// drawer opens. A forward never waits for commit-confirm: the ports of the control plane are refused.
// A forward the gateway refused opens again here with the gateway's messages under their fields, until
// the form is changed.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { LanNetwork, PortForwardEntry } from '../../api/generated/model';
import { useDevices, useGatewayState } from '../../composables/gateway';
import {
  emptyForwardForm,
  formFromForward,
  forwardDeviceProblem,
  forwardFieldOf,
  forwardFormErrors,
  forwardFromForm,
  forwardTitle,
  type ForwardField,
  type ForwardForm,
} from '../../lib/forwards';
import { newId } from '../../lib/ids';
import { subnetText } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{
  /** The forward edited, by id; none to add one. */
  forward?: string;
  /** The entry side of a new forward. */
  entry?: PortForwardEntry;
  /** The target device of a new forward. */
  device?: string;
  /** The management interface is the uplink interface (two-port topology). */
  twoPort?: boolean;
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();
const devices = useDevices();

const form = ref<ForwardForm>(emptyForwardForm());
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const refused = ref<Record<string, string>>({});
let refusedForm = '';

const before = computed(() => (props.forward ? draft.effectiveForwards[props.forward] : undefined));
const lans = computed(() =>
  Object.entries(draft.effectiveNetworks)
    .filter(([, n]) => n.type === 'lan')
    .map(([id, n]) => ({ id, name: n.name, address: (n as LanNetwork).address })),
);
/**
 * The devices a forward can go to: the configured ones not in another kind of network, a configured
 * device without a network included (a discovered device is adopted first, or its address is the target),
 * and the target the forward edited has whatever it is. A new forward offers no other device: the gateway
 * refuses a forward to it.
 */
const deviceChoices = computed(() => {
  const out = (devices.data.value ?? [])
    .filter((d) => !forwardDeviceProblem(d, draft.devices[d.id], (id) => draft.effectiveNetworks[id]?.type))
    .map((d) => ({ id: d.id, label: `${draft.devices[d.id]?.name ?? d.name}${d.observed.addresses?.length ? ` (${d.observed.addresses[0]})` : ' (no address)'}` }));
  const current = before.value?.target.device;
  if (current && !out.some((d) => d.id === current)) out.push({ id: current, label: (devices.data.value ?? []).find((d) => d.id === current)?.name ?? current });
  return out;
});
const others = computed(() =>
  Object.entries(draft.effectiveForwards)
    .filter(([id]) => id !== props.forward)
    .map(([, p]) => p),
);

/** The revision the form of a forward was built on (a revision applied meanwhile meets the edit in the conflict dialog). */
let origin: EditOrigin | undefined;

// immediate: the dialog may be made open already (the device detail's link to edit a forward)
watch(open, (o) => {
  if (!o) return;
  form.value = before.value ? formFromForward(before.value) : emptyForwardForm(props.twoPort ? 'uplink' : (props.entry ?? 'uplink'), props.device ?? '');
  origin = props.forward ? draft.editOrigin() : undefined;
  refused.value = (props.forward && draft.serverErrors[props.forward]) || {};
  refusedForm = JSON.stringify(form.value);
  tried.value = Object.keys(refused.value).length > 0;
  error.value = '';
}, { immediate: true });

const protocols = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
];
const entries = computed(() => [
  { value: 'uplink', label: 'Uplink' },
  ...(props.twoPort && form.value.entry !== 'management' ? [] : [{ value: 'management', label: 'Management network' }]),
]);
const targetKinds = [
  { value: 'device', label: 'Device' },
  { value: 'address', label: 'Address' },
];
const protocol = computed({ get: () => form.value.protocol, set: (v: string) => (form.value.protocol = v as ForwardForm['protocol']) });
const entry = computed({ get: () => form.value.entry, set: (v: string) => (form.value.entry = v as ForwardForm['entry']) });
const targetKind = computed({ get: () => form.value.targetKind, set: (v: string) => (form.value.targetKind = v as ForwardForm['targetKind']) });

const formErrors = computed(() =>
  forwardFormErrors(form.value, {
    others: others.value,
    networks: lans.value,
    twoPort: !!props.twoPort,
    devices: devices.data.value ? deviceChoices.value.map((d) => d.id) : undefined,
  }),
);
const server = computed(() => {
  const byField: Partial<Record<ForwardField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = forwardFieldOf(pointer);
    if (f) byField[f] = message;
    else general.push(message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
function shown(f: ForwardField): string | undefined {
  return tried.value ? errors.value[f] : undefined;
}

const title = computed(() => (props.forward ? `Edit ${before.value ? forwardTitle(before.value) : 'port forward'}` : 'Add port forward'));
watch(
  [open, form],
  ([o]) => {
    apiPanel.draft = o
      ? {
          title: title.value,
          calls: [
            {
              method: 'POST',
              path: '/revisions',
              headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base ?? state.data.value?.active_revision}"` },
              body: { port_forwards: { [props.forward ?? '<new uuid>']: forwardFromForm(form.value, before.value) } },
              note: 'the candidate; then …/preview and …/apply',
            },
          ],
        }
      : undefined;
  },
  { deep: true },
);

async function submit() {
  tried.value = true;
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty) draft.track(await getActiveRevision());
    draft.editFrom(origin, () =>
      draft.setForward(props.forward ?? newId(), forwardFromForm(form.value, props.forward ? draft.effectiveForwards[props.forward] : undefined)),
    );
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
  <ModalDialog
    v-model:open="open"
    :title="title"
    description="Connections from behind the uplink or the management network to a device in a test network. The access rules apply to them with the device as responder; faults do not."
    width="620px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="forward-form" @submit.prevent="submit">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <span class="label">Protocol</span>
          <SegmentedControl v-model="protocol" :options="protocols" label="Protocol" />
        </div>
        <div>
          <span class="label">Entry</span>
          <SegmentedControl v-model="entry" :options="entries" label="Entry" :described-by="shown('entry') ? 'forward-entry-error' : twoPort ? 'forward-entry-hint' : undefined" />
          <p v-if="shown('entry')" id="forward-entry-error" class="field-error" role="alert">{{ shown('entry') }}</p>
          <p v-else-if="twoPort" id="forward-entry-hint" class="m-0 mt-1 text-xs text-text-3">Two-port topology: the management network is the uplink.</p>
        </div>
      </div>
      <TextField v-model="form.external" label="External port" mono placeholder="8080 or 8000-8010" :error="shown('external')" hint="On the entry side's addresses." />

      <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
        <legend class="label">Target</legend>
        <SegmentedControl v-model="targetKind" :options="targetKinds" label="Target" />
        <div v-if="form.targetKind === 'device'">
          <label class="label" for="forward-device">Device</label>
          <select
            id="forward-device"
            v-model="form.device"
            class="input"
            :aria-invalid="!!shown('target')"
            :aria-describedby="shown('target') ? 'forward-device-error' : 'forward-device-hint'"
          >
            <option value="" disabled>Choose a device</option>
            <option v-for="d in deviceChoices" :key="d.id" :value="d.id">{{ d.label }}</option>
          </select>
          <p v-if="shown('target')" id="forward-device-error" class="field-error">{{ shown('target') }}</p>
          <p v-else id="forward-device-hint" class="m-0 mt-1 text-xs text-text-3">
            The forward follows the device's address. A discovered device is adopted first, on its page.
          </p>
        </div>
        <TextField
          v-else
          v-model="form.address"
          label="Address"
          mono
          placeholder="10.10.0.20"
          :error="shown('target')"
          :hint="lans.length ? `In a local test network: ${lans.map((n) => `${n.name} ${subnetText(n.address)}`).join(', ')}.` : undefined"
        />
      </fieldset>
      <TextField
        v-model="form.targetPort"
        label="Target port"
        mono
        :placeholder="form.external || '80'"
        :error="shown('targetPort')"
        hint="Empty: the external port. A range is shifted one to one (8000-8010 to 9000 forwards 8000 to 9000 … 8010 to 9010)."
      />
      <div class="grid grid-cols-2 gap-4">
        <TextField v-model="form.name" label="Name (optional)" :error="shown('name')" />
        <label class="mt-6 flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" /> Enabled</label>
      </div>
      <TextField v-model="form.description" label="Description (optional)" :error="shown('description')" />

      <p v-if="server.general.length" class="field-error" role="alert" data-testid="forward-form-refused">The gateway refused the forward: {{ server.general.join(' ') }}</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
