<script setup lang="ts">
// The add/edit fault dialog (plan §2.17 "Add fault"): every fault kind the API offers (P3-M13-04) —
// an impairment fault (target, traffic, direction, presets to start from, latency, jitter, loss and
// duration; under Advanced the delay distribution, keep packet order, burst loss, loss correlation,
// rate, queue limit, reorder, duplicate, corrupt and flapping; each direction with parameters of its
// own), an MTU/PMTUD fault (target, traffic, size and mode), a tunnel fault (the WireGuard client
// or link whose encrypted traffic it impairs, with the parameters a tunnel takes), a DNS fault (the
// proxy's action, the names, the delay, the addresses or the TTL) and a TLS case (the responder's
// case, ports, destination, server names, cutting existing connections, with the download of the
// test CA; M23b); the last two are overlays only, as the API has no configured fault of their
// families; a live preview sentence. A fault is edited with all its parameters: saving never drops one. Each message stands under its field and Apply stays
// disabled until the form is valid; the gateway's refusal (422) is put under the fields it names.
// An overlay is written at once (`POST /overlays`, the answer comes after the verify) and appears
// with its TTL; on the faults screen a fault can instead go into the configuration, through the
// unapplied-changes bar and the preview. Editing an overlay writes it again under the same key (it
// keeps its id); whenever the gateway answers with another overlay — a changed target or traffic,
// or an overlay of another owner (a token, a run), whose key includes its owner (§2.1.1) — the
// old one is removed, so the edit replaces it. An edit keeps the overlay's remaining time unless
// another duration is chosen (sent as `expires_in` beside its own TTL, which it keeps; an own TTL the dialog does not list is offered
// as such; P3-M13-06). A configured fault the gateway refused in a
// candidate opens with the refusal under its fields. The dialog's requests are shown in it under
// `</> API`.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { createOverlay, deleteOverlay } from '../../api/generated/hooks/overlays/overlays';
import type { ConfigFault, Overlay, OverlayRequest } from '../../api/generated/model';
import { useCapabilities, useGatewayState, useGroups, useNames } from '../../composables/gateway';
import {
  durationChoices,
  keepError,
  durations,
  durationTtl,
  emptyForm,
  faultFormErrors,
  faultToForm,
  fieldOf,
  formToConfigFault,
  formToOverlay,
  familyLabels,
  mtuModes,
  overlayOnlyFamilies,
  overlayToForm,
  previewSentence,
  trafficPresets,
  ttlText,
  type FaultField,
  type FaultForm,
} from '../../lib/faultForm';
import { describeOwner, describeScope, describeTunnel } from '../../lib/faults';
import { remoteScopeOptions, tunnelOptions } from '../../lib/scopes';
import { gatewayNow } from '../../lib/clock';
import { formatHM } from '../../lib/format';
import { useNow } from '../../composables/useNow';
import { newId } from '../../lib/ids';
import { httpOf, useApiPanelStore, type ApiCall } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import { useToastStore } from '../../stores/toasts';
import DisclosureButton from '../ui/DisclosureButton.vue';
import ModalDialog from '../ui/ModalDialog.vue';
import ScopeSelect from '../ui/ScopeSelect.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import DnsFields from './DnsFields.vue';
import ImpairmentFields from './ImpairmentFields.vue';
import TlsFields from './TlsFields.vue';
import TrafficFields from './TrafficFields.vue';

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
/** The `</> API` part: what Apply (or the preview) sends. */
const apiOpen = ref(false);

const form = ref<FaultForm>(emptyForm());
const layer = ref<'overlay' | 'config'>('overlay');
const serverErrors = ref<Partial<Record<FaultField, string>>>({});
const error = ref('');
const busy = ref(false);
/** The form as the dialog opened it: a configured fault saved unchanged records nothing (the defaults it fills in are no change). */
let openedForm = '';
/** The revision the form of a configured fault was built on (a revision applied meanwhile meets the edit in the conflict dialog). */
let origin: EditOrigin | undefined;

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
    form.value = overlayToForm(props.overlay);
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
  openedForm = JSON.stringify(form.value);
  origin = props.config ? draft.editOrigin() : undefined;
  // what the gateway refused in the last candidate, until the fault is edited
  const refused = props.config ? draft.serverErrors[props.config.id] : undefined;
  if (refused) showRefusal(Object.entries(refused).map(([path, message]) => ({ path, message })));
});

const owner = computed(() => (props.overlay && props.overlay.owner.type !== 'user' ? describeOwner(props.overlay.owner) : undefined));

const tunnels = computed(() => tunnelOptions(networks.data.value ?? []));
const capabilities = useCapabilities();
/**
 * The kinds a new fault can be: a tunnel fault has no target, so not on a device's page; a DNS fault and a TLS case are overlays
 * only, so not while the fault is saved in the configuration, and only when the gateway has them.
 */
const families = computed(() => {
  const kinds = capabilities.data.value?.overlay_kinds;
  return (['impairment', 'mtu', 'tunnel', 'dns', 'tls'] as const)
    .filter((f) => f !== 'tunnel' || !props.fixedTarget)
    .filter((f) => !overlayOnlyFamilies.includes(f) || (layer.value === 'overlay' && (!kinds || (kinds as string[]).includes(f))))
    .map((value) => ({ value, label: familyLabels[value] }));
});
const overlayOnly = computed(() => overlayOnlyFamilies.includes(form.value.family));
/** The choice "Save as" is for the kinds that can be configured; a DNS fault or a TLS case is an overlay, as the note says. */
const showLayer = computed(() => !!props.allowConfig && !editing.value && !overlayOnly.value);
// a device the TLS case is aimed at: whether it trusts the test CA decides what a certificate case shows
const trustedDevice = computed(() => {
  const id = form.value.target.startsWith('device:') ? form.value.target.slice('device:'.length) : undefined;
  const d = id ? devices.data.value?.find((x) => x.id === id) : undefined;
  return d?.config ? { trust: d.config.trusts_test_ca === true, name: d.name } : undefined;
});
const mtuHint = computed(() => mtuModes.find((m) => m.value === form.value.mtuMode)?.hint);

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
  // the client networks of the WireGuard clients, the links' prefixes and a prefix inside them (P3-M13-08)
  out.push(...remoteScopeOptions(networks.data.value ?? []));
  return out;
});

const groupList = computed(() => (groups.data.value ?? []).map((g) => ({ id: g.id, name: g.config.name })));
const scopeOfTarget = computed(() => formToOverlay(form.value).target);
const targetName = computed(() =>
  form.value.family === 'tunnel' ? describeTunnel(formToOverlay(form.value).fault?.tunnel, names.value) : describeScope(scopeOfTarget.value, names.value),
);

const now = useNow();
const clientErrors = computed<Partial<Record<FaultField, string>>>(() => {
  const e = faultFormErrors(form.value, layer.value);
  const expired = layer.value === 'overlay' ? keepError(form.value, now.value) : undefined;
  return expired ? { ...e, duration: expired } : e;
});
const errors = computed<Partial<Record<FaultField, string>>>(() => ({ ...serverErrors.value, ...clientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const preview = computed(() => previewSentence(form.value, targetName.value, layer.value, names.value));
const durationOptions = computed(() => durationChoices(form.value, props.overlay?.ttl, now.value));
// the request as it would be sent now (a kept remaining time shrinks with the clock)
const request = computed<OverlayRequest>(() => formToOverlay(form.value, now.value));

// what the dialog would send, for the `</> API` panel (a configured fault: the merge patch against the base revision's fault)
const configId = computed(() => props.config?.id ?? '<new uuid>');
watch(
  [open, request, layer],
  () => {
    if (!open.value) return;
    apiPanel.draft =
      layer.value === 'overlay'
        ? { title: title.value, calls: [{ method: 'POST', path: '/overlays', body: request.value }] }
        : { title: `${title.value} (configuration)`, calls: [{ method: 'POST', path: '/revisions', headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base}"` }, body: { faults: { [configId.value]: draft.objectPatch('fault', configId.value, formToConfigFault(form.value, props.config?.fault)) } } }] };
  },
  { deep: true, immediate: true },
);

/** The form as it was when the gateway refused it; an edit after that takes the messages away. */
let refusedForm = '';
watch(
  form,
  (f) => {
    // an edited field is no longer what the gateway refused
    if (JSON.stringify(f) !== refusedForm) serverErrors.value = {};
  },
  { deep: true },
);

/** The gateway's problems under the fields they name; the others below the form. */
function showRefusal(errors: { path?: string; message?: string }[]) {
  const out: Partial<Record<FaultField, string>> = {};
  const rest: string[] = [];
  for (const x of errors) {
    const f = fieldOf(x.path, form.value.direction === 'each');
    if (f) out[f] = x.message ?? 'not valid';
    else rest.push(x.message ?? '');
  }
  serverErrors.value = out;
  refusedForm = JSON.stringify(form.value);
  error.value = rest.length ? rest.join(' ') : 'The gateway refused the fault; see the marked fields.';
}

async function submit() {
  if (!valid.value || busy.value) return;
  error.value = '';
  if (layer.value === 'config') {
    // an unchanged configured fault is no change: writing the form's defaults back would only rewrite it
    if (props.config && JSON.stringify(form.value) === openedForm) {
      open.value = false;
      return;
    }
    const id = props.config?.id ?? newId();
    draft.editFrom(origin, () => draft.setFault(id, formToConfigFault(form.value, props.config?.fault)));
    open.value = false;
    return;
  }
  busy.value = true;
  const req = formToOverlay(form.value, gatewayNow());
  try {
    const ov = await createOverlay(req);
    const calls: ApiCall[] = [{ method: 'POST', path: '/overlays', body: req }];
    const old = props.overlay;
    // another key (target, traffic, or the owner: one of a token or a run is written as the
    // admin's) is another overlay: the edit replaces the old one
    // the new overlay is active from here on: a failed removal of the old one must not offer to
    // write it again (a third overlay), so the dialog closes and says what is left to remove
    let leftOver = '';
    if (old && old.id !== ov.id) {
      calls.push({ method: 'DELETE', path: `/overlays/${old.id}`, note: owner.value ? `the fault of ${owner.value} before the edit` : 'the fault before the edit' });
      // one that expired meanwhile is gone already
      await deleteOverlay(old.id).catch((e: unknown) => {
        if (!(e instanceof ApiError && e.status === 404)) leftOver = errorMessage(e);
      });
    }
    apiPanel.record(editing.value ? 'Edit fault' : 'Add fault', calls);
    const ttl = durationTtl(form.value.duration);
    const kept = form.value.duration === 'keep' && form.value.keepUntil;
    if (leftOver) {
      toasts.push(
        'error',
        'The fault before the edit is still active',
        `The edited fault is active on ${targetName.value}, but removing the old one failed (${leftOver}). Remove it on the faults screen.`,
      );
    } else {
      toasts.push(
        'success',
        `Fault active on ${targetName.value}`,
        kept
          ? `Expires at ${formatHM(form.value.keepUntil)} as before.`
          : ttl
            ? `Expires after ${durations.find((d) => d.value === form.value.duration)?.label ?? ttlText(ttl)}.`
            : 'Until removed.',
      );
    }
    open.value = false;
    await Promise.all(['overlays', 'devices', 'faults', 'profiles', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem?.errors?.length) {
      showRefusal(e.problem.errors);
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
      <div v-if="showLayer">
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

      <div v-if="!editing && families.length > 1">
        <span class="label">Kind</span>
        <SegmentedControl v-model="form.family" label="Kind" :options="families" />
        <p v-if="overlayOnly" class="m-0 mt-1.5 text-[13px] text-text-2" data-testid="overlay-only-note">
          {{ form.family === 'dns' ? 'A DNS fault' : 'A TLS case' }} is an overlay: it applies now and goes with its duration; it cannot be saved in the configuration.
        </p>
        <p v-else-if="allowConfig && layer === 'config'" class="m-0 mt-1.5 text-[13px] text-text-2">DNS faults and TLS cases are overlays: choose Overlay under Save as to make one.</p>
      </div>

      <div v-if="form.family === 'tunnel'">
        <label class="label" :for="fieldId('tunnel')">Tunnel</label>
        <select
          :id="fieldId('tunnel')"
          v-model="form.tunnel"
          class="input"
          :disabled="!!overlay"
          :aria-invalid="!!errors.tunnel"
          :aria-describedby="errors.tunnel ? errId('tunnel') : undefined"
        >
          <option value="" disabled>Choose a WireGuard client or link</option>
          <option v-for="t in tunnels" :key="t.value" :value="t.value">{{ t.label }}</option>
          <option v-if="form.tunnel && !tunnels.some((t) => t.value === form.tunnel)" :value="form.tunnel">{{ targetName }}</option>
        </select>
        <p v-if="errors.tunnel" :id="errId('tunnel')" class="field-error">{{ errors.tunnel }}</p>
        <p v-else class="m-0 mt-1 text-[13px] text-text-2">Impairs the encrypted traffic of the tunnel: everything inside it, and nothing else.</p>
      </div>
      <div v-else class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fieldId('target')">Target</label>
          <ScopeSelect
            :id="fieldId('target')"
            v-model="form.target"
            :options="targets"
            :current-label="targetName"
            :disabled="!!fixedTarget || !!overlay"
            :invalid="!!errors.target"
            :describedby="errors.target ? errId('target') : undefined"
          />
          <p v-if="errors.target" :id="errId('target')" class="field-error">{{ errors.target }}</p>
        </div>
        <div v-if="!overlayOnly">
          <label class="label" :for="fieldId('traffic')">Traffic</label>
          <select :id="fieldId('traffic')" v-model="form.traffic" class="input">
            <option v-for="(t, k) in trafficPresets" :key="k" :value="k">{{ t.label }}</option>
            <option value="custom">Other…</option>
          </select>
        </div>
      </div>

      <TrafficFields v-if="form.family !== 'tunnel' && !overlayOnly && form.traffic === 'custom'" v-model="form" :errors="errors" />

      <template v-if="form.family === 'mtu'">
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="label" :for="fieldId('mtuSize')">Packet size (bytes)</label>
            <input
              :id="fieldId('mtuSize')"
              v-model="form.mtuSize"
              class="input mono"
              inputmode="numeric"
              :aria-invalid="!!errors.mtuSize"
              :aria-describedby="errors.mtuSize ? errId('mtuSize') : undefined"
            />
            <p v-if="errors.mtuSize" :id="errId('mtuSize')" class="field-error">{{ errors.mtuSize }}</p>
          </div>
          <div class="col-span-2">
            <label class="label" :for="fieldId('mtuMode')">Mode</label>
            <select
              :id="fieldId('mtuMode')"
              v-model="form.mtuMode"
              class="input"
              :aria-invalid="!!errors.mtuMode"
              :aria-describedby="errors.mtuMode ? errId('mtuMode') : undefined"
            >
              <option v-for="m in mtuModes" :key="m.value" :value="m.value">{{ m.label }}</option>
            </select>
            <p v-if="errors.mtuMode" :id="errId('mtuMode')" class="field-error">{{ errors.mtuMode }}</p>
          </div>
        </div>
        <p class="m-0 text-[13px] text-text-2" data-testid="mtu-hint">{{ mtuHint }}</p>
      </template>
      <DnsFields v-else-if="form.family === 'dns'" v-model="form.dns" :errors="errors" />
      <TlsFields v-else-if="form.family === 'tls'" v-model="form.tls" :errors="errors" :trust="trustedDevice?.trust" :device-name="trustedDevice?.name" />
      <ImpairmentFields v-else v-model="form" :errors="errors" :tunnel="form.family === 'tunnel'" />

      <div class="grid grid-cols-3 gap-3">
        <div v-if="layer === 'overlay'">
          <label class="label" :for="fieldId('duration')">Duration</label>
          <select
            :id="fieldId('duration')"
            v-model="form.duration"
            class="input"
            :aria-invalid="!!errors.duration"
            :aria-describedby="errors.duration ? `${fieldId('duration')}-error` : undefined"
          >
            <option v-for="d in durationOptions" :key="d.value" :value="d.value">{{ d.label }}</option>
          </select>
          <p v-if="errors.duration" :id="`${fieldId('duration')}-error`" class="field-error">{{ errors.duration }}</p>
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

      <p v-if="owner" class="m-0 text-[13px] text-fault-text" data-testid="fault-owner">
        Set by {{ owner }}. Applying removes it and puts a fault of yours in its place.
      </p>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" aria-live="polite" data-testid="fault-preview">
        <div class="mb-1 text-xs text-text-3">Preview</div>
        {{ preview }}
      </div>

      <div v-if="apiPanel.draft" class="flex flex-col" data-testid="fault-api">
        <DisclosureButton v-model:open="apiOpen" :controls="`${uid}-api`">&lt;/&gt; API · what {{ layer === 'config' ? 'the preview' : 'Apply' }} sends</DisclosureButton>
        <div v-if="apiOpen" :id="`${uid}-api`">
          <pre
            v-for="(c, i) in apiPanel.draft.calls"
            :key="i"
            class="mono m-0 mt-2 max-h-48 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]"
            data-testid="api-call"
            >{{ httpOf(c) }}</pre
          >
        </div>
      </div>

      <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>
      <p v-if="blocked" class="m-0 text-[13px] text-fault-text">
        {{ connection.offline ? 'The gateway does not answer.' : 'A revision waits for confirmation; apply the fault after it is resolved.' }}
      </p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || blocked || !valid">
          {{ busy ? 'Applying…' : layer === 'config' ? 'Add to the changes' : 'Apply fault' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
