<script setup lang="ts">
// New capture (plan §2.11, §2.17 "Captures"; M23a): the scope as the capture request has it — a device, a network, a
// selector (a source with traffic: protocol, ports and a destination, in both directions) or the uplink — and the
// limits: a duration and a size limit; under *Advanced* the format, the packet length and a ring buffer that keeps
// the newest files instead of a size limit. A capture starts at once and the dialog closes; the file is there to
// download while it runs. Each message stands under its field and Start stays disabled until the form is valid; the
// gateway's refusal (422: a device without an address, a management network, a host name nobody asked for yet, a
// size above the quota) is put under the field it names, and the others (the disk is low, eight captures run, the
// quota is used by running captures) below the form. From a device's page the scope is the device.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { startCapture } from '../../api/generated/hooks/captures/captures';
import type { Capture, WireGuardNetwork } from '../../api/generated/model';
import { useGroups, useNames } from '../../composables/gateway';
import {
  captureErrors,
  captureFieldOf,
  emptyCaptureForm,
  formToCaptureRequest,
  type CaptureField,
  type CaptureForm,
  type ScopeKind,
} from '../../lib/captures';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useToastStore } from '../../stores/toasts';
import TrafficFields from '../faults/TrafficFields.vue';
import DraftApi from '../scenarios/DraftApi.vue';
import DisclosureButton from '../ui/DisclosureButton.vue';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';

const props = defineProps<{
  /** The scope is this device (the device's page). */
  fixedDevice?: { id: string; name: string };
}>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ started: [capture: Capture] }>();

const { names, devices, networks } = useNames();
const groups = useGroups();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const uid = useId();

const form = ref<CaptureForm>(emptyCaptureForm());
const serverErrors = ref<Partial<Record<CaptureField, string>>>({});
const error = ref('');
const busy = ref(false);
const advancedOpen = ref(false);
const advancedId = `${uid}-advanced`;

watch(open, (o) => {
  if (!o) {
    apiPanel.draft = undefined;
    return;
  }
  form.value = props.fixedDevice ? emptyCaptureForm('device', props.fixedDevice.id) : emptyCaptureForm('device');
  serverErrors.value = {};
  error.value = '';
  advancedOpen.value = false;
});

const kinds: { value: ScopeKind; label: string }[] = [
  { value: 'device', label: 'Device' },
  { value: 'network', label: 'Network' },
  { value: 'selector', label: 'Selector' },
  { value: 'uplink', label: 'Uplink' },
];
const kindHints: Record<ScopeKind, string> = {
  device: "The device's frames on its network's interface, with their replies; a device without a MAC address is found by its addresses.",
  network: "Everything on the network's bridge (a WireGuard network: its tunnel interfaces, raw IP packets).",
  selector: 'The packets of a source — everything, a network, a group or a device — to a destination, protocol and ports, in both directions.',
  uplink: 'Every packet of the uplink interface after NAT. It is not attributable to a device, and in a two-port setup it holds the management traffic too.',
};

/** The WireGuard networks of the role management are the admin's tunnel: the gateway never records them. */
const recordable = (n: { config: { type?: string; role?: string } }) => !(n.config.type === 'wireguard' && (n.config as WireGuardNetwork).role === 'management');
const deviceOptions = computed(() => (devices.data.value ?? []).map((d) => ({ value: d.id, label: d.name })));
const networkOptions = computed(() => (networks.data.value ?? []).filter(recordable).map((n) => ({ value: n.id, label: names.value.network?.(n.id) ?? n.id })));
const sourceOptions = computed(() => {
  const out: { value: string; label: string }[] = [{ value: 'global', label: 'Everything' }];
  for (const n of networkOptions.value) out.push({ value: `network:${n.value}`, label: `Network · ${n.label}` });
  for (const g of groups.data.value ?? []) out.push({ value: `group:${g.id}`, label: `Group · ${g.config.name}` });
  for (const d of deviceOptions.value) out.push({ value: `device:${d.value}`, label: `Device · ${d.label}` });
  return out;
});

const clientErrors = computed(() => captureErrors(form.value));
const errors = computed<Partial<Record<CaptureField, string>>>(() => ({ ...serverErrors.value, ...clientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const request = computed(() => formToCaptureRequest(form.value));
const blocked = computed(() => connection.offline);

watch(
  [open, request],
  () => {
    if (!open.value) return;
    apiPanel.draft = { title: 'New capture', calls: [{ method: 'POST', path: '/captures', body: request.value }] };
  },
  { deep: true, immediate: true },
);

/** The form as the gateway refused it; an edit takes the messages away. */
let refusedForm = '';
watch(
  form,
  (f) => {
    if (JSON.stringify(f) !== refusedForm) serverErrors.value = {};
  },
  { deep: true },
);

const advancedFields = new Set<CaptureField>(['format', 'snaplen', 'ringFiles', 'ringFileSize']);

function showRefusal(list: { path?: string; message?: string }[]) {
  const out: Partial<Record<CaptureField, string>> = {};
  const rest: string[] = [];
  for (const x of list) {
    const f = captureFieldOf(x.path);
    if (f) out[f] = x.message ?? 'not valid';
    else rest.push(x.message ?? '');
  }
  serverErrors.value = out;
  refusedForm = JSON.stringify(form.value);
  if (Object.keys(out).some((f) => advancedFields.has(f as CaptureField))) advancedOpen.value = true;
  error.value = rest.length ? rest.join(' ') : 'The gateway refused the capture; see the marked fields.';
}

async function submit() {
  if (!valid.value || busy.value) return;
  busy.value = true;
  error.value = '';
  const req = request.value;
  try {
    const c = await startCapture(req);
    apiPanel.record('New capture', [{ method: 'POST', path: '/captures', body: req }]);
    toasts.push('success', `${c.name || 'Capture'} is recording`, 'The file can be downloaded while it runs.');
    open.value = false;
    await queryClient.invalidateQueries({ queryKey: ['captures'] });
    emit('started', c);
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem?.errors?.length) showRefusal(e.problem.errors);
    else error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}

const fieldId = (f: string) => `${uid}-${f}`;
const errId = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <ModalDialog v-model:open="open" title="New capture" width="640px">
    <form class="flex flex-col gap-4" novalidate data-testid="capture-dialog" @submit.prevent="submit">
      <div>
        <label class="label" :for="fieldId('name')">Name</label>
        <input
          :id="fieldId('name')"
          v-model="form.name"
          class="input"
          placeholder="optional, names the file"
          :aria-invalid="!!errors.name"
          :aria-describedby="errors.name ? errId('name') : undefined"
        />
        <p v-if="errors.name" :id="errId('name')" class="field-error">{{ errors.name }}</p>
      </div>

      <div v-if="fixedDevice" data-testid="capture-fixed-scope">
        <span class="label">Scope</span>
        <p class="m-0 text-sm">Device <span class="mono">{{ fixedDevice.name }}</span></p>
        <p class="mt-1 mb-0 text-[13px] text-text-2">{{ kindHints.device }}</p>
      </div>
      <template v-else>
        <div>
          <span class="label">Scope</span>
          <SegmentedControl v-model="form.kind" label="Scope" :options="kinds" />
          <p class="mt-1.5 mb-0 text-[13px] text-text-2" data-testid="capture-scope-hint">{{ kindHints[form.kind] }}</p>
        </div>

        <div v-if="form.kind === 'device'">
          <label class="label" :for="fieldId('device')">Device</label>
          <select :id="fieldId('device')" v-model="form.device" class="input" :aria-invalid="!!errors.device" :aria-describedby="errors.device ? errId('device') : undefined">
            <option value="" disabled>Choose a device</option>
            <option v-for="o in deviceOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
          <p v-if="errors.device" :id="errId('device')" class="field-error">{{ errors.device }}</p>
        </div>
        <div v-else-if="form.kind === 'network'">
          <label class="label" :for="fieldId('network')">Network</label>
          <select :id="fieldId('network')" v-model="form.network" class="input" :aria-invalid="!!errors.network" :aria-describedby="errors.network ? errId('network') : undefined">
            <option value="" disabled>Choose a network</option>
            <option v-for="o in networkOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
          <p v-if="errors.network" :id="errId('network')" class="field-error">{{ errors.network }}</p>
        </div>
        <template v-else-if="form.kind === 'selector'">
          <div>
            <label class="label" :for="fieldId('source')">Source</label>
            <select :id="fieldId('source')" v-model="form.source" class="input" :aria-invalid="!!errors.source" :aria-describedby="errors.source ? errId('source') : undefined">
              <option v-for="o in sourceOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
              <option v-if="!sourceOptions.some((o) => o.value === form.source)" :value="form.source">{{ form.source }}</option>
            </select>
            <p v-if="errors.source" :id="errId('source')" class="field-error">{{ errors.source }}</p>
          </div>
          <TrafficFields v-model="form.traffic" :errors="errors" />
        </template>
      </template>

      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fieldId('duration')">Duration</label>
          <input
            :id="fieldId('duration')"
            v-model="form.duration"
            class="input mono"
            placeholder="until stopped"
            :aria-invalid="!!errors.duration"
            :aria-describedby="errors.duration ? errId('duration') : undefined"
          />
          <p v-if="errors.duration" :id="errId('duration')" class="field-error">{{ errors.duration }}</p>
          <p v-else class="mt-1.5 mb-0 text-xs text-text-3">Like 90s or 5m.</p>
        </div>
        <div v-if="form.limit === 'size'">
          <label class="label" :for="fieldId('maxSize')">Size limit</label>
          <input
            :id="fieldId('maxSize')"
            v-model="form.maxSize"
            class="input mono"
            placeholder="1GiB"
            :aria-invalid="!!errors.maxSize"
            :aria-describedby="errors.maxSize ? errId('maxSize') : undefined"
          />
          <p v-if="errors.maxSize" :id="errId('maxSize')" class="field-error">{{ errors.maxSize }}</p>
          <p v-else class="mt-1.5 mb-0 text-xs text-text-3">The capture ends there. Empty: 1 GiB.</p>
        </div>
        <div v-else class="text-[13px] text-text-2" data-testid="capture-ring-note">A ring buffer has no size limit: it keeps the newest files.</div>
      </div>

      <DisclosureButton v-model:open="advancedOpen" :controls="advancedId" data-testid="capture-advanced">Advanced</DisclosureButton>
      <div v-if="advancedOpen" :id="advancedId" class="flex flex-col gap-4" data-testid="capture-advanced-fields">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <span class="label">Format</span>
            <SegmentedControl
              v-model="form.format"
              label="Format"
              :options="[
                { value: 'pcapng', label: 'pcapng' },
                { value: 'pcap', label: 'pcap' },
              ]"
              :described-by="errors.format ? errId('format') : undefined"
            />
            <p v-if="errors.format" :id="errId('format')" class="field-error">{{ errors.format }}</p>
            <p v-else class="mt-1.5 mb-0 text-xs text-text-3">A pcap file holds one link type: a scope of Ethernet and tunnel interfaces needs pcapng.</p>
          </div>
          <div>
            <label class="label" :for="fieldId('snaplen')">Packet length</label>
            <input
              :id="fieldId('snaplen')"
              v-model="form.snaplen"
              class="input mono"
              inputmode="numeric"
              placeholder="whole packets"
              :aria-invalid="!!errors.snaplen"
              :aria-describedby="errors.snaplen ? errId('snaplen') : undefined"
            />
            <p v-if="errors.snaplen" :id="errId('snaplen')" class="field-error">{{ errors.snaplen }}</p>
            <p v-else class="mt-1.5 mb-0 text-xs text-text-3">Bytes kept of each packet, 64 or more.</p>
          </div>
        </div>
        <div>
          <span class="label">Limit</span>
          <SegmentedControl
            v-model="form.limit"
            label="Limit"
            :options="[
              { value: 'size', label: 'Size limit' },
              { value: 'ring', label: 'Ring buffer' },
            ]"
          />
        </div>
        <div v-if="form.limit === 'ring'" class="grid grid-cols-2 gap-3" data-testid="capture-ring-fields">
          <div>
            <label class="label" :for="fieldId('ringFiles')">Files</label>
            <input
              :id="fieldId('ringFiles')"
              v-model="form.ringFiles"
              class="input mono"
              inputmode="numeric"
              :aria-invalid="!!errors.ringFiles"
              :aria-describedby="errors.ringFiles ? errId('ringFiles') : undefined"
            />
            <p v-if="errors.ringFiles" :id="errId('ringFiles')" class="field-error">{{ errors.ringFiles }}</p>
          </div>
          <div>
            <label class="label" :for="fieldId('ringFileSize')">Size of a file</label>
            <input
              :id="fieldId('ringFileSize')"
              v-model="form.ringFileSize"
              class="input mono"
              :aria-invalid="!!errors.ringFileSize"
              :aria-describedby="errors.ringFileSize ? errId('ringFileSize') : undefined"
            />
            <p v-if="errors.ringFileSize" :id="errId('ringFileSize')" class="field-error">{{ errors.ringFileSize }}</p>
          </div>
          <p class="col-span-2 m-0 text-xs text-text-3">The ring keeps the newest files and deletes the oldest when a new one starts. A download joins them into one file.</p>
        </div>
      </div>

      <DraftApi what="New capture" />

      <p v-if="error" class="field-error m-0" role="alert" data-testid="capture-error">{{ error }}</p>
      <p v-if="blocked" class="m-0 text-[13px] text-fault-text">The gateway does not answer.</p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="!valid || busy || blocked" data-testid="capture-start">{{ busy ? 'Starting…' : 'Start capture' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
