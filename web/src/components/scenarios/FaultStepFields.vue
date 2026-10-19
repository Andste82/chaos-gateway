<script setup lang="ts">
// The fault of a `fault` step (plan §2.10): the fields of the fault dialog without its target and its
// duration, because a step acts on the scenario's target and lasts until a later step removes it or the
// run ends — the kind (an impairment, MTU / PMTUD, or the tunnel of a WireGuard client or link), the traffic,
// and the parameters (latency, jitter, loss and, under Advanced, the rest).
import { computed, useId } from 'vue';
import { useNames } from '../../composables/gateway';
import { familyLabels, mtuModes, trafficPresets, type FaultForm } from '../../lib/faultForm';
import { tunnelOptions } from '../../lib/scopes';
import ImpairmentFields from '../faults/ImpairmentFields.vue';
import TrafficFields from '../faults/TrafficFields.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';

defineProps<{ errors: Partial<Record<string, string>> }>();
const form = defineModel<FaultForm>({ required: true });
const uid = useId();
const { networks } = useNames();
const tunnels = computed(() => tunnelOptions(networks.data.value ?? []));
const families = (['impairment', 'mtu', 'tunnel'] as const).map((value) => ({ value, label: familyLabels[value] }));
const mtuHint = computed(() => mtuModes.find((m) => m.value === form.value.mtuMode)?.hint);
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="fault-step-fields">
    <div>
      <span class="label">Kind</span>
      <SegmentedControl v-model="form.family" label="Kind" :options="families" />
    </div>

    <div v-if="form.family === 'tunnel'">
      <label class="label" :for="fid('tunnel')">Tunnel</label>
      <select
        :id="fid('tunnel')"
        v-model="form.tunnel"
        class="input"
        :aria-invalid="!!errors.tunnel"
        :aria-describedby="errors.tunnel ? eid('tunnel') : undefined"
      >
        <option value="" disabled>Choose a WireGuard client or link</option>
        <option v-for="t in tunnels" :key="t.value" :value="t.value">{{ t.label }}</option>
        <option v-if="form.tunnel && !tunnels.some((t) => t.value === form.tunnel)" :value="form.tunnel">{{ form.tunnel }}</option>
      </select>
      <p v-if="errors.tunnel" :id="eid('tunnel')" class="field-error">{{ errors.tunnel }}</p>
      <p v-else class="m-0 mt-1 text-[13px] text-text-2">Impairs the encrypted traffic of the tunnel: everything inside it, and nothing else. The tunnel must lie inside the scenario's target.</p>
    </div>
    <div v-else>
      <label class="label" :for="fid('traffic')">Traffic</label>
      <select :id="fid('traffic')" v-model="form.traffic" class="input">
        <option v-for="(t, k) in trafficPresets" :key="k" :value="k">{{ t.label }}</option>
        <option value="custom">Other…</option>
      </select>
    </div>
    <TrafficFields v-if="form.family !== 'tunnel' && form.traffic === 'custom'" v-model="form" :errors="errors" />

    <template v-if="form.family === 'mtu'">
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fid('mtuSize')">Packet size (bytes)</label>
          <input
            :id="fid('mtuSize')"
            v-model="form.mtuSize"
            class="input mono"
            inputmode="numeric"
            :aria-invalid="!!errors.mtuSize"
            :aria-describedby="errors.mtuSize ? eid('mtuSize') : undefined"
          />
          <p v-if="errors.mtuSize" :id="eid('mtuSize')" class="field-error">{{ errors.mtuSize }}</p>
        </div>
        <div class="col-span-2">
          <label class="label" :for="fid('mtuMode')">Mode</label>
          <select :id="fid('mtuMode')" v-model="form.mtuMode" class="input">
            <option v-for="m in mtuModes" :key="m.value" :value="m.value">{{ m.label }}</option>
          </select>
        </div>
      </div>
      <p class="m-0 text-[13px] text-text-2">{{ mtuHint }}</p>
    </template>
    <ImpairmentFields v-else v-model="form" :errors="errors" :tunnel="form.family === 'tunnel'" />
  </div>
</template>
