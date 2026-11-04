<script setup lang="ts">
// The fields of a time fault (plan §2.18, §2.17 "Add fault", M34): the presets to start from, what the NTP server answers
// (a time of your choice with an offset or an absolute instant, a drift and a leap indicator, or an answer that says the
// server is not to be trusted, no answer at all, or an invalid one) and the server the device asked (none: every server).
// The "Add fault" dialog and the `time` step of the step dialog share them. Each message stands under its field (`errors` by
// field name).
import { computed, useId } from 'vue';
import { applyTimePreset, timeAnswerGroups, timeAnswers, timeKissCodes, timePresets, type TimeForm } from '../../lib/timeForm';
import TrafficFields from './TrafficFields.vue';

defineProps<{ errors: Partial<Record<string, string>> }>();
const form = defineModel<TimeForm>({ required: true });
const uid = useId();
const hint = computed(() => timeAnswers.find((a) => a.value === form.value.answer)?.hint);
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="time-fields">
    <div>
      <span class="label">Presets</span>
      <div class="flex flex-wrap gap-2" role="group" aria-label="Presets">
        <button
          v-for="p in timePresets"
          :key="p.id"
          type="button"
          class="btn btn-sm"
          :title="p.hint"
          :data-testid="`time-preset-${p.id}`"
          @click="form = applyTimePreset(form, p.id)"
        >
          {{ p.label }}
        </button>
      </div>
    </div>

    <div>
      <label class="label" :for="fid('answer')">Answer</label>
      <select
        :id="fid('answer')"
        v-model="form.answer"
        class="input"
        :aria-invalid="!!errors.timeAnswer"
        :aria-describedby="errors.timeAnswer ? eid('answer') : fid('hint')"
      >
        <optgroup v-for="g in timeAnswerGroups" :key="g" :label="g">
          <option v-for="a in timeAnswers.filter((x) => x.group === g)" :key="a.value" :value="a.value">{{ a.label }}</option>
        </optgroup>
      </select>
      <p v-if="errors.timeAnswer" :id="eid('answer')" class="field-error">{{ errors.timeAnswer }}</p>
      <p v-else :id="fid('hint')" class="m-0 mt-1 text-[13px] text-text-2" data-testid="time-hint">{{ hint }}</p>
    </div>

    <div v-if="form.answer === 'kiss_of_death'" class="max-w-[320px]">
      <label class="label" :for="fid('kiss')">Kiss code</label>
      <select
        :id="fid('kiss')"
        v-model="form.kissCode"
        class="input"
        :aria-invalid="!!errors.timeKiss"
        :aria-describedby="errors.timeKiss ? eid('kiss') : undefined"
      >
        <option v-for="k in timeKissCodes" :key="k.value" :value="k.value">{{ k.label }}</option>
      </select>
      <p v-if="errors.timeKiss" :id="eid('kiss')" class="field-error">{{ errors.timeKiss }}</p>
    </div>

    <template v-if="form.answer === 'normal'">
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fid('shift')">Served time</label>
          <select :id="fid('shift')" v-model="form.shift" class="input">
            <option value="offset">The correct time plus an offset</option>
            <option value="absolute">A fixed instant, running on</option>
            <option value="none">The correct time (drift or leap only)</option>
          </select>
        </div>
        <div v-if="form.shift === 'offset'">
          <label class="label" :for="fid('offset')">Offset</label>
          <input
            :id="fid('offset')"
            v-model="form.offset"
            class="input mono"
            placeholder="+400d"
            :aria-invalid="!!errors.timeOffset"
            :aria-describedby="errors.timeOffset ? eid('offset') : fid('offset-hint')"
          />
          <p v-if="errors.timeOffset" :id="eid('offset')" class="field-error">{{ errors.timeOffset }}</p>
          <p v-else :id="fid('offset-hint')" class="m-0 mt-1 text-[13px] text-text-2">A signed span: <span class="mono">+400d</span>, <span class="mono">-90s</span>, <span class="mono">-1y2d</span> (ms, s, m, h, d, w, y).</p>
        </div>
        <div v-else-if="form.shift === 'absolute'">
          <label class="label" :for="fid('absolute')">Instant</label>
          <input
            :id="fid('absolute')"
            v-model="form.absolute"
            class="input mono"
            placeholder="1970-01-01T00:00:00Z"
            :aria-invalid="!!errors.timeAbsolute"
            :aria-describedby="errors.timeAbsolute ? eid('absolute') : fid('absolute-hint')"
          />
          <p v-if="errors.timeAbsolute" :id="eid('absolute')" class="field-error">{{ errors.timeAbsolute }}</p>
          <p v-else :id="fid('absolute-hint')" class="m-0 mt-1 text-[13px] text-text-2">With a time zone, not before 1900-01-01. The clock runs on from the moment the fault takes effect.</p>
        </div>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fid('skew')">Drift (ppm)</label>
          <input
            :id="fid('skew')"
            v-model="form.skew"
            class="input mono"
            inputmode="decimal"
            placeholder="none"
            :aria-invalid="!!errors.timeSkew"
            :aria-describedby="errors.timeSkew ? eid('skew') : fid('skew-hint')"
          />
          <p v-if="errors.timeSkew" :id="eid('skew')" class="field-error">{{ errors.timeSkew }}</p>
          <p v-else :id="fid('skew-hint')" class="m-0 mt-1 text-[13px] text-text-2">Positive runs fast, negative slow: 1000 ppm adds 10 ms after 10 s.</p>
        </div>
        <div>
          <label class="label" :for="fid('leap')">Leap indicator</label>
          <select
            :id="fid('leap')"
            v-model="form.leap"
            class="input"
            :aria-invalid="!!errors.timeLeap"
            :aria-describedby="errors.timeLeap ? eid('leap') : undefined"
          >
            <option value="">None</option>
            <option value="insert">Insert a second at the end of the day</option>
            <option value="delete">Delete a second at the end of the day</option>
          </select>
          <p v-if="errors.timeLeap" :id="eid('leap')" class="field-error">{{ errors.timeLeap }}</p>
        </div>
      </div>
      <p v-if="errors.timeChange" class="field-error m-0" data-testid="time-change-error">{{ errors.timeChange }}</p>
    </template>

    <div>
      <span class="label">Server asked</span>
      <TrafficFields v-model="form.traffic" :errors="errors" no-protocol server-destinations ports-placeholder="123" />
      <p class="m-0 mt-1 text-[13px] text-text-2">
        Empty: the fault applies to every NTP server the target asks, the gateway's own address included. An address, a prefix or a host name narrows it to the queries for that server (UDP port 123 unless you name ports).
      </p>
    </div>

    <p class="m-0 text-[13px] text-text-2" data-testid="time-jump-note">
      The served time jumps when the fault starts and jumps back when it ends. A device sees either only when it polls next, which can take minutes to hours.
    </p>
  </div>
</template>
