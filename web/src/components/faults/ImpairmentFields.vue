<script setup lang="ts">
// The impairment parameters of a fault or a profile (plan §2.5, §2.17 "Add fault"; P3-M13-03,
// P3-M13-04): the direction (both, upload, download, or each its own parameters — then a switch
// chooses which direction the fields show), presets to start from, latency, jitter and loss; under
// Advanced the delay distribution, keep packet order, the loss model (random with its correlation,
// or Gilbert-Elliott burst loss), rate, queue limit, reorder, duplicate, corrupt and flapping. A
// tunnel fault shows only what it takes (latency, jitter, loss, burst loss, blackout, flapping).
// Each message stands under its field; `errors` holds the download set's as `download.<field>`.
import { computed, ref, useId, watch } from 'vue';
import { advancedFields, hasAdvanced, presets, tunnelFields, type ImpairmentField, type ImpairmentForm, type NetemField, type NetemForm } from '../../lib/netemForm';
import SegmentedControl from '../ui/SegmentedControl.vue';

const props = defineProps<{
  errors: Partial<Record<string, string>>;
  /** A tunnel fault: only the parameters a tunnel takes, and the directions are the peer's. */
  tunnel?: boolean;
}>();
const form = defineModel<ImpairmentForm>({ required: true });
const uid = useId();

/** Which direction's fields are shown when each has its own. */
const side = ref<'upload' | 'download'>('upload');
const each = computed(() => form.value.direction === 'each');
const cur = computed<NetemForm>(() => (each.value && side.value === 'download' ? form.value.download : form.value));
const key = (f: NetemField): ImpairmentField => (each.value && side.value === 'download' ? `download.${f}` : f);
const err = (f: NetemField) => props.errors[key(f)];
const shown = (f: NetemField) => !props.tunnel || tunnelFields.has(f);

const advanced = ref(false);
// opened for a fault that has advanced parameters, and for a problem under an advanced field
watch(
  () => [hasAdvanced(form.value) || (each.value && hasAdvanced(form.value.download))] as const,
  ([a]) => {
    if (a) advanced.value = true;
  },
  { immediate: true },
);
watch(
  () => Object.keys(props.errors),
  (keys) => {
    if (keys.some((k) => advancedFields.has(k.replace(/^download\./, '') as NetemField))) advanced.value = true;
  },
);

/** The other direction has problems (with `each`): the switch says so. */
const otherInvalid = computed(
  () => each.value && Object.entries(props.errors).some(([k, v]) => v && (side.value === 'upload' ? k.startsWith('download.') : k in labels)),
);

const shownPresets = computed(() => (props.tunnel ? presets.filter((p) => !p.values.rate) : presets));
function pick(values: Partial<NetemForm>) {
  Object.assign(cur.value, values);
  if (values.rate) advanced.value = true;
}

const directionOptions = computed(() => [
  { value: 'both', label: 'Both' },
  { value: 'upload', label: props.tunnel ? 'Upload · from the peer' : 'Upload' },
  { value: 'download', label: props.tunnel ? 'Download · to the peer' : 'Download' },
  { value: 'each', label: 'Each its own' },
]);
const labels: Record<NetemField, string> = {
  latency: 'Latency (ms)',
  jitter: 'Jitter (ms)',
  loss: 'Loss (%)',
  lossCorrelation: 'Loss correlation (%)',
  burstP: 'Good → bad (%)',
  burstR: 'Bad → good (%)',
  burstH: 'Delivered when bad (%)',
  burstK: 'Delivered when good (%)',
  rate: 'Rate limit',
  queueLimit: 'Queue limit (packets)',
  reorder: 'Reorder (%)',
  duplicate: 'Duplicate (%)',
  corrupt: 'Corrupt (%)',
  flapUp: 'Up (s)',
  flapDown: 'Down (s)',
};
const placeholders: Partial<Record<NetemField, string>> = { rate: 'e.g. 2 Mbit/s', queueLimit: 'computed', burstH: '0', burstK: '100' };
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
const advancedLabel = computed(() =>
  props.tunnel ? 'Advanced: burst loss, flapping' : 'Advanced: rate, reorder, duplicate, corrupt, burst loss, flapping, …',
);
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="impairment-fields">
    <div class="flex flex-wrap items-center gap-4">
      <div>
        <span class="label">Direction</span>
        <SegmentedControl v-model="form.direction" label="Direction" :options="directionOptions" />
      </div>
      <div role="group" aria-label="Start from" class="flex flex-wrap items-center gap-1.5">
        <span class="mr-1 text-[13px] text-text-2">Start from</span>
        <button v-for="p in shownPresets" :key="p.label" type="button" class="btn btn-sm" @click="pick(p.values)">{{ p.label }}</button>
      </div>
    </div>
    <p v-if="tunnel" class="m-0 text-[13px] text-text-2">
      The directions are the remote side's: upload is what the client or the remote site sends, download what it receives.
    </p>

    <div v-if="each" class="flex flex-wrap items-center gap-3">
      <span class="label m-0">Parameters of</span>
      <SegmentedControl
        v-model="side"
        label="Parameters of"
        :options="[
          { value: 'upload', label: 'Upload' },
          { value: 'download', label: 'Download' },
        ]"
      />
      <span v-if="otherInvalid" class="text-[13px] text-block-text">The {{ side === 'upload' ? 'download' : 'upload' }} parameters need fixing.</span>
    </div>

    <div class="grid grid-cols-3 gap-3">
      <div v-for="f in ['latency', 'jitter'] as const" :key="f">
        <label class="label" :for="fid(f)">{{ labels[f] }}</label>
        <input
          :id="fid(f)"
          v-model="cur[f]"
          class="input mono"
          inputmode="decimal"
          :aria-invalid="!!err(f)"
          :aria-describedby="err(f) ? eid(f) : undefined"
        />
        <p v-if="err(f)" :id="eid(f)" class="field-error">{{ err(f) }}</p>
      </div>
      <div v-if="cur.lossModel === 'random'">
        <label class="label" :for="fid('loss')">{{ labels.loss }}</label>
        <input
          :id="fid('loss')"
          v-model="cur.loss"
          class="input mono"
          inputmode="decimal"
          :aria-invalid="!!err('loss')"
          :aria-describedby="err('loss') ? eid('loss') : undefined"
        />
        <p v-if="err('loss')" :id="eid('loss')" class="field-error">{{ err('loss') }}</p>
      </div>
      <p v-else class="m-0 self-end pb-2 text-[13px] text-text-2">Loss: burst model (Advanced)</p>
    </div>

    <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="advanced" @click="advanced = !advanced">
      {{ advanced ? 'Hide advanced' : advancedLabel }}
    </button>
    <template v-if="advanced">
      <div v-if="!tunnel" class="grid grid-cols-5 gap-3">
        <div v-for="f in ['rate', 'queueLimit', 'reorder', 'duplicate', 'corrupt'] as const" :key="f">
          <label class="label" :for="fid(f)">{{ labels[f] }}</label>
          <input
            :id="fid(f)"
            v-model="cur[f]"
            class="input mono"
            :placeholder="placeholders[f] ?? ''"
            :aria-invalid="!!err(f)"
            :aria-describedby="err(f) ? eid(f) : undefined"
          />
          <p v-if="err(f)" :id="eid(f)" class="field-error">{{ err(f) }}</p>
        </div>
      </div>
      <div v-if="!tunnel" class="flex flex-wrap items-end gap-4">
        <div class="w-48">
          <label class="label" :for="fid('distribution')">Delay distribution</label>
          <select :id="fid('distribution')" v-model="cur.distribution" class="input">
            <option value="">Uniform (default)</option>
            <option value="normal">Normal</option>
            <option value="pareto">Pareto</option>
            <option value="paretonormal">Pareto-normal</option>
          </select>
        </div>
        <label class="flex items-center gap-2 pb-2 text-[13px]">
          <input v-model="cur.keepOrder" type="checkbox" />
          Keep packet order <span class="text-text-2">— jitter reorders packets unless this is on; it narrows the delay spread</span>
        </label>
      </div>

      <div class="flex flex-col gap-2">
        <div class="flex flex-wrap items-center gap-3">
          <span class="label m-0">Loss model</span>
          <SegmentedControl
            v-model="cur.lossModel"
            label="Loss model"
            :options="[
              { value: 'random', label: 'Random' },
              { value: 'burst', label: 'Burst (Gilbert-Elliott)' },
            ]"
          />
        </div>
        <div v-if="cur.lossModel === 'random' && shown('lossCorrelation')" class="grid grid-cols-4 gap-3">
          <div>
            <label class="label" :for="fid('lossCorrelation')">{{ labels.lossCorrelation }}</label>
            <input
              :id="fid('lossCorrelation')"
              v-model="cur.lossCorrelation"
              class="input mono"
              :aria-invalid="!!err('lossCorrelation')"
              :aria-describedby="err('lossCorrelation') ? eid('lossCorrelation') : undefined"
            />
            <p v-if="err('lossCorrelation')" :id="eid('lossCorrelation')" class="field-error">{{ err('lossCorrelation') }}</p>
          </div>
        </div>
        <div v-if="cur.lossModel === 'burst'" class="grid grid-cols-4 gap-3">
          <div v-for="f in ['burstP', 'burstR', 'burstH', 'burstK'] as const" :key="f">
            <label class="label" :for="fid(f)">{{ labels[f] }}</label>
            <input
              :id="fid(f)"
              v-model="cur[f]"
              class="input mono"
              :placeholder="placeholders[f] ?? ''"
              :aria-invalid="!!err(f)"
              :aria-describedby="err(f) ? eid(f) : undefined"
            />
            <p v-if="err(f)" :id="eid(f)" class="field-error">{{ err(f) }}</p>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-start gap-3">
        <label class="flex items-center gap-2 pt-7 text-[13px]">
          <input v-model="cur.flapping" type="checkbox" />
          Flapping <span class="text-text-2">— a timed blackout, starting with up</span>
        </label>
        <template v-if="cur.flapping">
          <div v-for="f in ['flapUp', 'flapDown'] as const" :key="f" class="w-28">
            <label class="label" :for="fid(f)">{{ labels[f] }}</label>
            <input
              :id="fid(f)"
              v-model="cur[f]"
              class="input mono"
              inputmode="decimal"
              :aria-invalid="!!err(f)"
              :aria-describedby="err(f) ? eid(f) : undefined"
            />
            <p v-if="err(f)" :id="eid(f)" class="field-error">{{ err(f) }}</p>
          </div>
        </template>
      </div>
    </template>
  </div>
</template>
