<script setup lang="ts">
// The add/edit fault dialog (plan §2.17 "Add fault"): target, traffic, direction, presets to start
// from, latency, jitter, loss and duration; rate, reorder, duplicate, corrupt and keep packet order
// under Advanced; a live preview sentence. Each message stands under its field and Apply stays
// disabled until the form is valid; the gateway's refusal (422) is put under the fields it names.
// An overlay is written at once (`POST /overlays`, the answer comes after the verify) and appears
// with its TTL; on the faults screen a fault can instead go into the configuration, through the
// unapplied-changes bar and the preview. Editing an overlay writes it again under the same key (it
// keeps its id); a changed target or traffic is a new overlay, and the old one is removed.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { createOverlay, deleteOverlay } from '../../api/generated/hooks/overlays/overlays';
import type { ConfigFault, Overlay, OverlayRequest } from '../../api/generated/model';
import { useGatewayState, useGroups, useNames } from '../../composables/gateway';
import {
  durations,
  emptyForm,
  faultFormErrors,
  faultToForm,
  formToConfigFault,
  formToOverlay,
  hiddenParameters,
  overlayKey,
  presets,
  previewSentence,
  trafficPresets,
  type FaultField,
  type FaultForm,
} from '../../lib/faultForm';
import { describeScope } from '../../lib/faults';
import { newId } from '../../lib/ids';
import { useApiPanelStore, type ApiCall } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useDraftStore } from '../../stores/draft';
import { useToastStore } from '../../stores/toasts';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';

const props = defineProps<{
  /** A fixed target (the device page): `device:<id>`. */
  fixedTarget?: string;
  /** The overlay to edit. */
  overlay?: Overlay;
  /** The configured fault to edit (the faults screen). */
  config?: { id: string; fault: ConfigFault };
  /** The faults screen offers to save a new fault in the configuration. */
  allowConfig?: boolean;
}>();
const open = defineModel<boolean>('open', { required: true });

const { names, devices, networks } = useNames();
const groups = useGroups();
const state = useGatewayState();
const connection = useConnectionStore();
const draft = useDraftStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const uid = useId();

const form = ref<FaultForm>(emptyForm());
const layer = ref<'overlay' | 'config'>('overlay');
const advanced = ref(false);
const serverErrors = ref<Partial<Record<FaultField, string>>>({});
const error = ref('');
const busy = ref(false);

const editing = computed(() => !!props.overlay || !!props.config);
const title = computed(() => (editing.value ? 'Edit fault' : 'Add fault'));

watch(open, (o) => {
  if (!o) {
    apiPanel.draft = undefined;
    return;
  }
  error.value = '';
  serverErrors.value = {};
  if (props.overlay) {
    form.value = faultToForm(props.overlay.fault, props.overlay.target, { ttl: props.overlay.ttl });
    layer.value = 'overlay';
  } else if (props.config) {
    form.value = faultToForm(props.config.fault, props.config.fault.source, { name: props.config.fault.name });
    layer.value = 'config';
  } else {
    form.value = emptyForm(props.fixedTarget ?? 'global');
    form.value.latency = '200';
    form.value.jitter = '50';
    layer.value = 'overlay';
  }
  const f = form.value;
  advanced.value = !!(f.rate || f.reorder || f.duplicate || f.corrupt || f.keepOrder);
});

const hidden = computed(() => hiddenParameters(props.overlay?.fault ?? props.config?.fault));

const targets = computed(() => {
  const n = names.value;
  const out: { value: string; label: string }[] = [{ value: 'global', label: 'Everything' }];
  for (const net of networks.data.value ?? []) out.push({ value: `network:${net.id}`, label: `Network · ${n.network?.(net.id) ?? net.id}` });
  for (const g of groupList.value) out.push({ value: `group:${g.id}`, label: `Group · ${g.name}` });
  for (const d of devices.data.value ?? []) {
    // a configured fault can only name a configured device; an overlay any device the gateway knows
    if (layer.value === 'config' && d.origin === 'discovered') continue;
    out.push({ value: `device:${d.id}`, label: `Device · ${d.name}` });
  }
  if (!out.some((o) => o.value === form.value.target)) out.push({ value: form.value.target, label: describeScope(scopeOfTarget.value, n) });
  return out;
});

const groupList = computed(() => (groups.data.value ?? []).map((g) => ({ id: g.id, name: g.config.name })));
const scopeOfTarget = computed(() => formToOverlay(form.value).target);
const targetName = computed(() => describeScope(scopeOfTarget.value, names.value));

const clientErrors = computed(() => faultFormErrors(form.value, layer.value));
const errors = computed<Partial<Record<FaultField, string>>>(() => ({ ...serverErrors.value, ...clientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const preview = computed(() => previewSentence(form.value, targetName.value, layer.value));
const request = computed<OverlayRequest>(() => formToOverlay(form.value));

// what the dialog would send, for the `</> API` panel
watch(
  [open, request, layer],
  () => {
    if (!open.value) return;
    apiPanel.draft =
      layer.value === 'overlay'
        ? { title: title.value, calls: [{ method: 'POST', path: '/overlays', body: request.value }] }
        : { title: `${title.value} (configuration)`, calls: [{ method: 'POST', path: '/revisions', headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base}"` }, body: { faults: { [props.config?.id ?? '<new uuid>']: formToConfigFault(form.value, props.config?.fault) } } }] };
  },
  { deep: true, immediate: true },
);

watch(
  form,
  () => {
    // an edited field is no longer what the gateway refused
    serverErrors.value = {};
  },
  { deep: true },
);

function pick(values: Partial<FaultForm>) {
  form.value = { ...form.value, ...values };
  if (values.rate) advanced.value = true;
}

/** "/fault/jitter", "/fault/upload/latency", "/target" → the form's field. */
function fieldOf(path: string | undefined): FaultField | undefined {
  const last = (path ?? '').split('/').filter(Boolean).at(-1) ?? '';
  const map: Record<string, FaultField> = {
    latency: 'latency',
    jitter: 'jitter',
    loss: 'loss',
    rate: 'rate',
    reorder: 'reorder',
    duplicate: 'duplicate',
    corrupt: 'corrupt',
    ports: 'ports',
    port_ranges: 'ports',
    destination: 'destination',
    cidr: 'destination',
    target: 'target',
    device: 'target',
    network: 'target',
    group: 'target',
  };
  return map[last];
}

async function submit() {
  if (!valid.value || busy.value) return;
  error.value = '';
  if (layer.value === 'config') {
    const id = props.config?.id ?? newId();
    draft.setFault(id, formToConfigFault(form.value, props.config?.fault));
    open.value = false;
    return;
  }
  busy.value = true;
  const req = request.value;
  try {
    const ov = await createOverlay(req);
    const calls: ApiCall[] = [{ method: 'POST', path: '/overlays', body: req }];
    const old = props.overlay;
    if (old && overlayKey(old) !== overlayKey(req) && old.id !== ov.id) {
      await deleteOverlay(old.id);
      calls.push({ method: 'DELETE', path: `/overlays/${old.id}`, note: 'the fault before the edit' });
    }
    apiPanel.record(editing.value ? 'Edit fault' : 'Add fault', calls);
    const d = durations.find((x) => x.value === form.value.duration);
    toasts.push('success', `Fault active on ${targetName.value}`, d?.ttl ? `Expires after ${d.label}.` : 'Until removed.');
    open.value = false;
    await Promise.all(['overlays', 'devices', 'faults', 'profiles', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem?.errors?.length) {
      const out: Partial<Record<FaultField, string>> = {};
      const rest: string[] = [];
      for (const x of e.problem.errors) {
        const f = fieldOf(x.path);
        if (f) out[f] = x.message ?? 'not valid';
        else rest.push(x.message ?? '');
      }
      serverErrors.value = out;
      if (Object.keys(out).length && (form.value.rate || form.value.reorder || form.value.duplicate || form.value.corrupt)) advanced.value = true;
      error.value = rest.length ? rest.join(' ') : 'The gateway refused the fault; see the marked fields.';
    } else {
      error.value = errorMessage(e);
    }
  } finally {
    busy.value = false;
  }
}

const blocked = computed(() => layer.value === 'overlay' && (!!state.data.value?.pending_confirm || connection.offline));
const fieldId = (f: string) => `${uid}-${f}`;
const errId = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="640px">
    <form class="flex flex-col gap-4" novalidate data-testid="fault-dialog" @submit.prevent="submit">
      <div v-if="allowConfig && !editing">
        <span :id="fieldId('layer')" class="label">Save as</span>
        <SegmentedControl
          v-model="layer"
          label="Save as"
          :options="[
            { value: 'overlay', label: 'Overlay · applies now' },
            { value: 'config', label: 'Configuration · after preview' },
          ]"
        />
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fieldId('target')">Target</label>
          <select
            :id="fieldId('target')"
            v-model="form.target"
            class="input"
            :disabled="!!fixedTarget || !!overlay"
            :aria-invalid="!!errors.target"
            :aria-describedby="errors.target ? errId('target') : undefined"
          >
            <option v-for="t in targets" :key="t.value" :value="t.value">{{ t.label }}</option>
          </select>
          <p v-if="errors.target" :id="errId('target')" class="field-error">{{ errors.target }}</p>
        </div>
        <div>
          <label class="label" :for="fieldId('traffic')">Traffic</label>
          <select :id="fieldId('traffic')" v-model="form.traffic" class="input">
            <option v-for="(t, k) in trafficPresets" :key="k" :value="k">{{ t.label }}</option>
            <option value="custom">Other…</option>
          </select>
        </div>
      </div>

      <div v-if="form.traffic === 'custom'" class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fieldId('protocol')">Protocol</label>
          <select :id="fieldId('protocol')" v-model="form.protocol" class="input">
            <option value="any">Any</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="icmp">ICMP</option>
          </select>
        </div>
        <div>
          <label class="label" :for="fieldId('ports')">Ports</label>
          <input
            :id="fieldId('ports')"
            v-model="form.ports"
            class="input mono"
            placeholder="any"
            :aria-invalid="!!errors.ports"
            :aria-describedby="errors.ports ? errId('ports') : undefined"
          />
          <p v-if="errors.ports" :id="errId('ports')" class="field-error">{{ errors.ports }}</p>
        </div>
        <div>
          <label class="label" :for="fieldId('destination')">Destination</label>
          <input
            :id="fieldId('destination')"
            v-model="form.destination"
            class="input mono"
            placeholder="any"
            :aria-invalid="!!errors.destination"
            :aria-describedby="errors.destination ? errId('destination') : undefined"
          />
          <p v-if="errors.destination" :id="errId('destination')" class="field-error">{{ errors.destination }}</p>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-4">
        <div>
          <span class="label">Direction</span>
          <SegmentedControl
            v-model="form.direction"
            label="Direction"
            :options="[
              { value: 'both', label: 'Both' },
              { value: 'upload', label: 'Upload' },
              { value: 'download', label: 'Download' },
            ]"
          />
        </div>
        <div role="group" aria-label="Start from" class="flex flex-wrap items-center gap-1.5">
          <span class="mr-1 text-[13px] text-text-2">Start from</span>
          <button v-for="p in presets" :key="p.label" type="button" class="btn btn-sm" @click="pick(p.values)">{{ p.label }}</button>
        </div>
      </div>

      <div class="grid grid-cols-4 gap-3">
        <div v-for="f in ['latency', 'jitter', 'loss'] as const" :key="f">
          <label class="label" :for="fieldId(f)">{{ { latency: 'Latency (ms)', jitter: 'Jitter (ms)', loss: 'Loss (%)' }[f] }}</label>
          <input
            :id="fieldId(f)"
            v-model="form[f]"
            class="input mono"
            inputmode="decimal"
            :aria-invalid="!!errors[f]"
            :aria-describedby="errors[f] ? errId(f) : undefined"
          />
          <p v-if="errors[f]" :id="errId(f)" class="field-error">{{ errors[f] }}</p>
        </div>
        <div v-if="layer === 'overlay'">
          <label class="label" :for="fieldId('duration')">Duration</label>
          <select :id="fieldId('duration')" v-model="form.duration" class="input">
            <option v-for="d in durations" :key="d.value" :value="d.value">{{ d.label }}</option>
          </select>
        </div>
        <div v-else>
          <label class="label" :for="fieldId('name')">Name</label>
          <input
            :id="fieldId('name')"
            v-model="form.name"
            class="input"
            placeholder="optional"
            :aria-invalid="!!errors.name"
            :aria-describedby="errors.name ? errId('name') : undefined"
          />
          <p v-if="errors.name" :id="errId('name')" class="field-error">{{ errors.name }}</p>
        </div>
      </div>

      <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="advanced" @click="advanced = !advanced">
        {{ advanced ? 'Hide advanced' : 'Advanced: rate, reorder, duplicate, corrupt' }}
      </button>
      <template v-if="advanced">
        <div class="grid grid-cols-4 gap-3">
          <div v-for="f in ['rate', 'reorder', 'duplicate', 'corrupt'] as const" :key="f">
            <label class="label" :for="fieldId(f)">{{ { rate: 'Rate limit', reorder: 'Reorder (%)', duplicate: 'Duplicate (%)', corrupt: 'Corrupt (%)' }[f] }}</label>
            <input
              :id="fieldId(f)"
              v-model="form[f]"
              class="input mono"
              :placeholder="f === 'rate' ? 'e.g. 2 Mbit/s' : ''"
              :aria-invalid="!!errors[f]"
              :aria-describedby="errors[f] ? errId(f) : undefined"
            />
            <p v-if="errors[f]" :id="errId(f)" class="field-error">{{ errors[f] }}</p>
          </div>
        </div>
        <label class="flex items-center gap-2 text-[13px]">
          <input v-model="form.keepOrder" type="checkbox" />
          Keep packet order <span class="text-text-2">— jitter reorders packets unless this is on; it narrows the delay spread</span>
        </label>
      </template>

      <p v-if="hidden.length" class="m-0 text-[13px] text-fault-text">
        This fault has {{ hidden.join(', ') }}, which this dialog does not show; saving replaces them with the parameters above.
      </p>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" aria-live="polite" data-testid="fault-preview">
        <div class="mb-1 text-xs text-text-3">Preview</div>
        {{ preview }}
      </div>

      <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>
      <p v-if="blocked" class="m-0 text-[13px] text-fault-text">
        {{ connection.offline ? 'The gateway does not answer.' : 'A revision waits for confirmation; apply the fault after it is resolved.' }}
      </p>

      <div class="flex justify-end gap-2">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || blocked || !valid">
          {{ busy ? 'Applying…' : layer === 'config' ? 'Add to the changes' : 'Apply fault' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
