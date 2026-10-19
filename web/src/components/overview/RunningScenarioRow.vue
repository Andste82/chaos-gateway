<script setup lang="ts">
// One run of the overview's list (plan §2.17 "Overview"): the scenario, its state, the target, the steps that
// are through and the step in effect, and a progress bar. The list of runs has no timelines, so the row reads
// the run itself (every second while it is queued or running).
import { computed } from 'vue';
import type { RouteLocationRaw } from 'vue-router';
import type { Names } from '../../lib/faults';
import { useRun } from '../../composables/gateway';
import { describeScope } from '../../lib/faults';
import { progressPercent, statusLabels, stepMarks, stepsDone } from '../../lib/runs';
import StatusDot from '../ui/StatusDot.vue';

const props = defineProps<{ id: string; name: string; to: RouteLocationRaw; names: Names; now: Date }>();
const run = useRun(() => props.id);
const r = computed(() => run.data.value);
const pct = computed(() => (r.value ? progressPercent(r.value, props.now) : 0));
const marks = computed(() => (r.value ? stepMarks(r.value, props.now) : {}));
const steps = computed(() => (r.value ? stepsDone(r.value) : { done: 0, total: 0 }));
</script>

<template>
  <div class="flex flex-col gap-1.5" data-testid="running-scenario">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <RouterLink :to="to" class="mono text-sm">{{ name }}</RouterLink>
      <StatusDot v-if="r" :tone="r.status === 'running' ? 'fault' : 'unknown'" :label="statusLabels[r.status]" />
    </div>
    <div class="text-xs text-text-2">
      <template v-if="r">{{ describeScope(r.target, names) }}</template>
      <template v-if="steps.total"> · {{ steps.done }} of {{ steps.total }} steps</template>
      <template v-if="marks.current"> · step {{ marks.current }} in effect</template>
    </div>
    <div class="h-1.5 overflow-hidden rounded-[3px] bg-[#262e39]" role="progressbar" :aria-label="`Progress of ${name}`" :aria-valuenow="pct" aria-valuemin="0" aria-valuemax="100">
      <div class="h-1.5 bg-fault" :style="{ width: `${pct}%` }" />
    </div>
  </div>
</template>
