<script setup lang="ts">
// The self-test "measure this profile" (plan §2.12, §2.17 "Probes"; M25): a probe sends numbered datagrams to an echo server
// for the duration, the gateway compares the round trips, the losses and the throughput with what the profile (or the faults
// that act on the probe now) should produce, within the tolerances of plan §4.3, and says for each of delay, jitter, loss and
// rate whether it matches. The profile is activated on the probe for the run and removed afterwards; the call waits for the
// whole duration, so the dialog shows that it is under way.
import { computed, ref, useId, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { calibrateProbe } from '../../api/generated/hooks/diagnostics/diagnostics';
import type { CalibrationResult, ProbeView } from '../../api/generated/model';
import {
  calibrationErrors,
  calibrationFieldOf,
  calibrationRows,
  calibrationSeconds,
  calibrationSentence,
  checkLabel,
  emptyCalibrationForm,
  formToCalibrationRequest,
  verdictTone,
  DEFAULT_DURATION,
  DEFAULT_PORT,
  type CalibrationField,
  type CalibrationForm,
} from '../../lib/calibration';
import { useApiPanelStore } from '../../stores/apiPanel';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import StatusDot from '../ui/StatusDot.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{
  probes: ProbeView[];
  /** The profiles that can be measured (with an impairment part): their id and the name shown. */
  profiles: { id: string; name: string }[];
  /** What to preselect when the dialog opens: a probe by id, a profile by id. */
  probe?: string;
  profile?: string;
}>();
const open = defineModel<boolean>('open', { required: true });

const uid = useId();
const apiPanel = useApiPanelStore();
const probeId = ref('');
const form = ref<CalibrationForm>(emptyCalibrationForm());
const busy = ref(false);
const result = ref<CalibrationResult>();
const refusal = ref('');
const serverErrors = ref<Partial<Record<CalibrationField, string>>>({});
const refusedForm = ref('');
const tried = ref(false);

watch(open, (o) => {
  if (!o) return;
  probeId.value = props.probe ?? props.probes[0]?.id ?? '';
  form.value = emptyCalibrationForm(props.profile ?? '');
  result.value = undefined;
  refusal.value = '';
  serverErrors.value = {};
  tried.value = false;
});

const clientErrors = computed(() => calibrationErrors(form.value));
const errors = computed(() => (JSON.stringify(form.value) === refusedForm.value ? { ...serverErrors.value, ...clientErrors.value } : clientErrors.value));
const valid = computed(() => Object.keys(clientErrors.value).length === 0 && probeId.value !== '');
const request = computed(() => formToCalibrationRequest(form.value));
const probe = computed(() => props.probes.find((p) => p.id === probeId.value));
const rows = computed(() => (result.value ? calibrationRows(result.value) : []));
const verdict = computed(() => (result.value ? verdictTone(result.value) : undefined));
const seconds = computed(() => calibrationSeconds(form.value));
const hasAddress = computed(() => (probe.value?.observed.addresses ?? []).length > 0);

async function run() {
  tried.value = true;
  if (!valid.value || busy.value) return;
  busy.value = true;
  result.value = undefined;
  refusal.value = '';
  serverErrors.value = {};
  const body = request.value;
  try {
    result.value = await calibrateProbe(probeId.value, body);
    apiPanel.record('Measure with a probe', [{ method: 'POST', path: `/probes/${probeId.value}/calibrate`, body }]);
  } catch (e) {
    refusal.value = errorMessage(e);
    refusedForm.value = JSON.stringify(form.value);
    const errs = (e as { problem?: { errors?: { path?: string; message?: string }[] } }).problem?.errors ?? [];
    for (const x of errs) {
      const f = calibrationFieldOf(x.path);
      if (f) serverErrors.value[f] = x.message ?? 'not valid';
    }
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog
    v-model:open="open"
    title="Measure with a probe"
    description="The probe sends datagrams to an echo server and the gateway compares what came back with the parameters."
    width="640px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="calibrate-form" @submit.prevent="run">
      <div>
        <label class="label" :for="`${uid}-probe`">Probe</label>
        <select :id="`${uid}-probe`" v-model="probeId" class="input w-full" data-testid="calibrate-probe">
          <option v-for="p in probes" :key="p.id" :value="p.id">{{ p.config.name }}{{ (p.observed.addresses ?? []).length ? ` · ${p.observed.addresses?.[0]}` : ' · no address yet' }}</option>
        </select>
        <p v-if="probe && !hasAddress" class="mt-1.5 mb-0 text-[13px] text-text-2" role="status" data-testid="calibrate-no-address">
          This probe has no address yet: it gets one by DHCP from its network, which needs DHCP switched on.
        </p>
      </div>

      <div>
        <span class="label">Measure</span>
        <SegmentedControl
          :model-value="form.what"
          label="Measure"
          :options="[
            { value: 'profile', label: 'A profile' },
            { value: 'current', label: 'The probe’s faults now' },
          ]"
          @update:model-value="(v: string) => (form.what = v as CalibrationForm['what'])"
        />
        <p class="mt-1.5 mb-0 text-[13px] text-text-2">
          {{ form.what === 'profile' ? 'The profile is activated on the probe for the run and removed afterwards.' : 'No overlay is made: the gateway measures what the faults that act on the probe now should produce.' }}
        </p>
      </div>
      <div v-if="form.what === 'profile'">
        <label class="label" :for="`${uid}-profile`">Profile</label>
        <select :id="`${uid}-profile`" v-model="form.profile" class="input w-full" :aria-invalid="!!(tried && errors.profile)" data-testid="calibrate-profile">
          <option value="">Choose a profile</option>
          <option v-for="p in profiles" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
        <p v-if="tried && errors.profile" class="mt-1 mb-0 text-xs text-block-text" role="alert">{{ errors.profile }}</p>
      </div>

      <div class="grid grid-cols-[minmax(0,1fr)_120px_120px] items-start gap-3 max-sm:grid-cols-1">
        <TextField v-model="form.host" label="Echo server" mono placeholder="203.0.113.10" :error="tried ? errors.host : undefined" hint="An IPv4 address with a UDP and a TCP echo (RFC 862)." />
        <TextField v-model="form.port" label="Port" mono :placeholder="String(DEFAULT_PORT)" :error="tried ? errors.port : undefined" />
        <TextField v-model="form.duration" label="Duration" mono :placeholder="DEFAULT_DURATION" :error="tried ? errors.duration : undefined" hint="5s to 5m." />
      </div>

      <p v-if="busy" class="m-0 text-sm text-text-2" role="status" data-testid="calibrate-running">Measuring for {{ seconds }} s…</p>
      <p v-if="refusal" class="m-0 text-sm text-block-text" role="alert" data-testid="calibrate-refused">{{ refusal }}</p>

      <section v-if="result && verdict" class="flex flex-col gap-3 border-t border-border pt-4" data-testid="calibrate-result" :data-within="result.within_tolerance">
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <StatusDot :tone="verdict.tone" :label="verdict.label" data-testid="calibrate-verdict" />
          <span v-if="result.samples" class="ml-auto text-xs text-text-3">{{ result.samples.received }} of {{ result.samples.sent }} datagrams came back</span>
        </div>
        <p class="m-0 text-sm" data-testid="calibrate-sentence">{{ calibrationSentence(result) }}</p>
        <ul v-if="result.checks?.length" class="m-0 flex list-none flex-col gap-1.5 p-0" aria-label="The checks" data-testid="calibrate-checks">
          <li v-for="c in result.checks" :key="c.name" class="text-sm" :data-check="c.name" :data-ok="c.ok">
            <span aria-hidden="true">{{ c.ok ? '✓' : '✗' }}</span>
            <span class="font-medium"> {{ checkLabel(c.name) }}</span>
            <span class="text-text-3"> {{ c.ok ? 'matches' : 'does not match' }}</span>
            <span class="block text-xs text-text-2">{{ c.detail }}</span>
          </li>
        </ul>
        <table v-if="rows.length" class="w-full border-collapse text-sm" data-testid="calibrate-rows">
          <thead>
            <tr class="text-left text-xs text-text-3"><th class="py-1 font-normal">&nbsp;</th><th class="py-1 font-normal">Expected</th><th class="py-1 font-normal">Measured</th></tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.label" class="border-t border-border">
              <th class="py-1.5 pr-3 text-left font-normal text-text-2">{{ r.label }}</th>
              <td class="mono py-1.5 pr-3">{{ r.expected }}</td>
              <td class="mono py-1.5">{{ r.measured }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <div class="dialog-actions flex justify-end gap-2">
        <button type="button" class="btn" @click="open = false">Close</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || (tried && !valid) || !hasAddress" data-testid="calibrate-run">{{ busy ? 'Measuring…' : 'Measure' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
