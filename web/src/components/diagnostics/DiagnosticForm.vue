<script setup lang="ts">
// The form of a diagnostic (plan §2.12, §2.17 "Diagnostics"; M24): the type as a choice with a sentence on what it does,
// and the fields that type has — a host (a name to resolve for DNS, a URL for HTTP), a port where one is needed, and
// under *Advanced* the timeout, the number of echoes, the SNI, the datagram, the TLS step of a path. Each message stands
// under its field and Run stays disabled until the form is valid; the gateway's refusal is put under the field it names.
import { computed, ref, useId, watch } from 'vue';
import type { DiagnosticRequest, ProbeView } from '../../api/generated/model';
import {
  diagnosticErrors,
  diagnosticFieldOf,
  diagnosticTypes,
  emptyDiagnosticForm,
  fieldsOf,
  formToDiagnosticRequest,
  type DiagnosticField,
  type DiagnosticForm,
  type DiagnosticType,
} from '../../lib/diagnostics';
import DisclosureButton from '../ui/DisclosureButton.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{
  busy: boolean;
  /** The gateway cannot be reached or the session may not run diagnostics. */
  disabled?: boolean;
  /** The fields the gateway refused the last request for. */
  refusal?: { path?: string; message?: string }[];
  /** The probes a diagnostic can run from (M25); the choice *From* is shown when there are any. */
  probes?: ProbeView[];
}>();
const form = defineModel<DiagnosticForm>('form', { required: true });
const emit = defineEmits<{ run: [request: DiagnosticRequest] }>();

const uid = useId();
const advancedOpen = ref(false);
const advancedId = `${uid}-advanced`;

const fields = computed(() => fieldsOf(form.value.type));
const info = computed(() => diagnosticTypes.find((t) => t.value === form.value.type));
const clientErrors = computed(() => diagnosticErrors(form.value));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const request = computed(() => formToDiagnosticRequest(form.value));

/** The refusal of the gateway until the form is edited; the other messages are below the form. */
const refusedForm = ref('');
const serverErrors = computed(() => {
  const out: Partial<Record<DiagnosticField, string>> = {};
  if (JSON.stringify(form.value) !== refusedForm.value) return out;
  for (const x of props.refusal ?? []) {
    const f = diagnosticFieldOf(x.path);
    if (f) out[f] = x.message ?? 'not valid';
  }
  return out;
});
watch(
  () => props.refusal,
  () => (refusedForm.value = JSON.stringify(form.value)),
);
const errors = computed(() => ({ ...serverErrors.value, ...clientErrors.value }));

function setType(t: string) {
  const old = form.value;
  // the host and the timeout stay when the type changes; what belongs to one type does not
  form.value = { ...emptyDiagnosticForm(t as DiagnosticType), from: old.from, host: old.host, port: old.port, timeout: old.timeout };
}

function submit() {
  if (!valid.value || props.busy || props.disabled) return;
  emit('run', request.value);
}

const hostLabel = computed(() => (form.value.type === 'dns' ? 'Name to resolve' : 'Host'));
const hostPlaceholder = computed(() => (form.value.type === 'dns' ? 'broker.example.com' : 'broker.example.com or 203.0.113.10'));
</script>

<template>
  <form class="flex flex-col gap-4" novalidate data-testid="diagnostic-form" @submit.prevent="submit">
    <div>
      <span class="label">Diagnostic</span>
      <SegmentedControl :model-value="form.type" label="Diagnostic" :options="diagnosticTypes.map((t) => ({ value: t.value, label: t.label }))" @update:model-value="setType" />
      <p class="mt-1.5 mb-0 text-[13px] text-text-2" data-testid="diagnostic-hint">{{ info?.hint }}</p>
    </div>

    <div v-if="probes?.length">
      <label class="label" :for="`${uid}-from`">From</label>
      <select :id="`${uid}-from`" v-model="form.from" class="input w-full max-w-[360px]" data-testid="diagnostic-from">
        <option value="">The gateway</option>
        <option v-for="p in probes" :key="p.id" :value="p.id">Probe {{ p.config.name }}{{ (p.observed.addresses ?? []).length ? ` · ${p.observed.addresses?.[0]}` : '' }}</option>
      </select>
      <p v-if="errors.from" class="mt-1 mb-0 text-xs text-block-text" role="alert">{{ errors.from }}</p>
      <p class="mt-1.5 mb-0 text-[13px] text-text-2" data-testid="diagnostic-from-hint">
        {{ form.from ? 'The probe’s traffic takes the path of a device’s: the faults, rules and profiles that target the probe act on it.' : 'The gateway’s own address: the faults of a device do not apply.' }}
      </p>
    </div>

    <div class="grid grid-cols-[minmax(0,1fr)_140px] items-start gap-3 max-sm:grid-cols-1">
      <TextField v-if="fields.url" v-model="form.url" label="URL" mono placeholder="https://api.example.com/health" :error="errors.url" class="col-span-full" />
      <template v-else>
        <TextField v-model="form.host" :label="hostLabel" mono :placeholder="hostPlaceholder" :error="errors.host" />
        <TextField
          v-if="fields.port && (form.type !== 'dns' || form.server.trim() !== '')"
          v-model="form.port"
          label="Port"
          mono
          :placeholder="form.type === 'dns' ? '53' : form.type === 'tls' ? '443' : ''"
          :error="errors.port"
        />
      </template>
    </div>
    <TextField
      v-if="fields.server"
      v-model="form.server"
      label="Server"
      mono
      placeholder="the gateway's DNS proxy"
      hint="Empty asks the DNS proxy of the gateway, the one the devices use; an address or a name asks that server."
      :error="errors.server"
    />

    <DisclosureButton v-model:open="advancedOpen" :controls="advancedId" data-testid="diagnostic-advanced">Advanced</DisclosureButton>
    <div v-if="advancedOpen" :id="advancedId" class="grid grid-cols-2 items-start gap-3 max-sm:grid-cols-1" data-testid="diagnostic-advanced-fields">
      <TextField v-model="form.timeout" label="Timeout" mono placeholder="5s" :error="errors.timeout" hint="How long to wait for an answer, 60s at most." />
      <TextField v-if="fields.count" v-model="form.count" label="Echoes" mono placeholder="5" :error="errors.count" hint="1 to 100, 200 ms apart." />
      <TextField v-if="fields.sni" v-model="form.sni" label="SNI" mono placeholder="the host" :error="errors.sni" hint="The server name of the handshake; empty is the host, and none for an address." />
      <TextField v-if="fields.payload" v-model="form.payload" label="Datagram" mono placeholder="empty" :error="errors.payload" hint="The text sent; many services ignore an empty datagram." />
      <label v-if="fields.tls" class="flex items-center gap-2 text-sm">
        <input v-model="form.tls" type="checkbox" class="h-4 w-4 accent-[var(--color-accent)]" />
        Check TLS as the last step
      </label>
    </div>

    <div class="flex items-center gap-3">
      <button type="submit" class="btn btn-primary" :disabled="!valid || busy || disabled" data-testid="run-diagnostic">{{ busy ? 'Running…' : 'Run' }}</button>
      <span v-if="!form.from" class="text-xs text-text-3">From the gateway's own address: the faults of a device do not apply.</span>
    </div>
  </form>
</template>
