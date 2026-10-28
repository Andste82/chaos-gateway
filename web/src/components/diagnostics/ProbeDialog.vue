<script setup lang="ts">
// Add or edit a probe (plan §2.12, §2.17 "Probes", M26a): its name, the local test network whose bridge it is a port of, and
// whether it trusts the test CA (a diagnostic from it then verifies the certificates the gateway's CA signed). Each check stands
// under its field; the probe goes into the configuration draft and the preview-and-apply drawer opens. The gateway builds the
// probe when the revision is applied; its DHCP address shows in the list after that. A probe the gateway refused opens again here
// with the gateway's messages under their fields, until the form is changed.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { LanNetwork } from '../../api/generated/model';
import { useDevices, useGatewayState } from '../../composables/gateway';
import { newId } from '../../lib/ids';
import { emptyProbeForm, formFromProbe, probeFieldOf, probeFormErrors, probeFromForm, type ProbeField, type ProbeForm } from '../../lib/probes';
import { subnetText } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{
  /** The probe edited, by id; none to add one. */
  probe?: string;
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();
const devices = useDevices();

const form = ref<ProbeForm>(emptyProbeForm());
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const refused = ref<Record<string, string>>({});
let refusedForm = '';

const before = computed(() => (props.probe ? draft.effectiveProbes[props.probe] : undefined));
const lans = computed(() =>
  Object.entries(draft.effectiveNetworks)
    .filter(([, n]) => n.type === 'lan')
    .map(([id, n]) => ({ id, name: n.name, address: (n as LanNetwork).address })),
);
/** The names a probe cannot take: devices, WireGuard clients and probes share one namespace. */
const taken = computed(() => {
  const names = new Set<string>();
  for (const d of devices.data.value ?? []) if (d.id !== props.probe) names.add(d.name);
  for (const d of Object.values(draft.baseConfig?.devices ?? {})) names.add(d.name);
  for (const d of Object.values(draft.devices)) if (d) names.add(d.name);
  for (const [id, p] of Object.entries(draft.effectiveProbes)) if (id !== props.probe) names.add(p.name);
  return [...names];
});

/** The revision the form of a probe was built on (a revision applied meanwhile meets the edit in the conflict dialog). */
let origin: EditOrigin | undefined;

// immediate: the dialog may be made open already
watch(open, (o) => {
  if (!o) return;
  // a test network of its own is the obvious choice
  form.value = before.value ? formFromProbe(before.value) : emptyProbeForm(lans.value.length === 1 ? lans.value[0]!.id : '');
  origin = props.probe ? draft.editOrigin() : undefined;
  refused.value = (props.probe && draft.serverErrors[props.probe]) || {};
  refusedForm = JSON.stringify(form.value);
  tried.value = Object.keys(refused.value).length > 0;
  error.value = '';
}, { immediate: true });

const formErrors = computed(() => probeFormErrors(form.value, { taken: taken.value, networks: lans.value }));
const server = computed(() => {
  const byField: Partial<Record<ProbeField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = probeFieldOf(pointer);
    if (f) byField[f] = message;
    else general.push(message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
function shown(f: ProbeField): string | undefined {
  return tried.value ? errors.value[f] : undefined;
}

const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);

const title = computed(() => (props.probe ? `Edit ${before.value?.name ?? 'probe'}` : 'Add probe'));
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
              body: { probes: { [props.probe ?? '<new uuid>']: probeFromForm(form.value, before.value) } },
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
    draft.editFrom(origin, () => draft.setProbe(props.probe ?? newId(), probeFromForm(form.value, props.probe ? draft.effectiveProbes[props.probe] : undefined)));
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
    description="A virtual client of the gateway: a port of a test network's bridge in a namespace of its own, with an address from the network's DHCP. Faults, rules and profiles that target it act on its traffic; diagnostics and measurements run from it."
    width="560px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="probe-form" @submit.prevent="submit">
      <TextField v-model="form.name" label="Name" :error="shown('name')" hint="Devices, WireGuard clients and probes share their names." />
      <div>
        <label class="label" for="probe-network">Network</label>
        <select
          id="probe-network"
          v-model="form.network"
          class="input"
          :aria-invalid="!!shown('network')"
          :aria-describedby="shown('network') ? 'probe-network-error' : 'probe-network-hint'"
        >
          <option value="" disabled>Choose a network</option>
          <option v-for="n in lans" :key="n.id" :value="n.id">{{ n.name }} ({{ subnetText(n.address) }})</option>
        </select>
        <p v-if="shown('network')" id="probe-network-error" class="field-error">{{ shown('network') }}</p>
        <p v-else id="probe-network-hint" class="m-0 mt-1 text-xs text-text-3">
          The probe is a port of this network's bridge and gets its address from the network's DHCP, so the network needs DHCP.
        </p>
      </div>
      <div>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.trustsTestCa" type="checkbox" /> Trusts the test CA</label>
        <p class="m-0 mt-1 text-xs text-text-3">A diagnostic from the probe then verifies certificates that the gateway's test CA signed (the interception, the TLS responder).</p>
      </div>

      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text" data-testid="probe-pending-confirm">Another revision waits for confirmation; change the probe after it is resolved.</p>
      <p v-if="server.general.length" class="field-error" role="alert" data-testid="probe-form-refused">The gateway refused the probe: {{ server.general.join(' ') }}</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
