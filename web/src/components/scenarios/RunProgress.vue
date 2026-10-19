<script setup lang="ts">
// How a run is going (plan §2.10, §2.17 "Run states"): its state with a label (queued, running, passed,
// failed, error or aborted), who started it and when, the steps that are through, the time against the length
// of the timeline, a progress bar, the lease that keeps an unattended run alive, the reason an `error` or
// `aborted` run ended, and the warnings of the report (a fault that matched no packet, a step that was late,
// a capture that is missing).
import { computed } from 'vue';
import type { Run } from '../../api/generated/model';
import { useNow } from '../../composables/useNow';
import { describeOwner, describeScope } from '../../lib/faults';
import { formatAgo, formatClock, formatRemaining } from '../../lib/format';
import { elapsedMs, isActive, progressPercent, statusHints, statusLabels, statusTone, stepsDone } from '../../lib/runs';
import { lengthText, scenarioLength, type ScenarioNames } from '../../lib/scenarios';
import StatusDot from '../ui/StatusDot.vue';

const props = defineProps<{ run: Run; names: ScenarioNames }>();
const now = useNow();

const pct = computed(() => progressPercent(props.run, now.value));
const steps = computed(() => stepsDone(props.run));
const length = computed(() => (props.run.snapshot ? scenarioLength(props.run.snapshot) : 0));
const clock = computed(() => {
  const el = elapsedMs(props.run, now.value);
  const text = lengthText(Math.floor(el / 1000) * 1000);
  return length.value ? `${text} of ${lengthText(length.value)}` : text;
});
const barColor = computed(() => {
  switch (props.run.status) {
    case 'failed':
    case 'error':
      return 'bg-block-text';
    case 'running':
      return 'bg-fault';
    default:
      return 'bg-accent';
  }
});
const leaseLeft = computed(() => {
  if (!props.run.lease_expires_at || !isActive(props.run)) return undefined;
  return formatRemaining(new Date(props.run.lease_expires_at).getTime() - now.value.getTime());
});
const warningLabels: Record<string, string> = { zero_hit: 'matched no packet', step_late: 'late', capture_missing: 'capture missing' };
</script>

<template>
  <section class="card flex flex-col gap-3" aria-label="Run" data-testid="run-progress">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <span class="text-base font-semibold" data-testid="run-title">Run of {{ run.scenario.name }}</span>
        <StatusDot :tone="statusTone[run.status]" :label="statusLabels[run.status]" :title="statusHints[run.status]" data-testid="run-status" />
      </div>
      <span class="mono text-[13px] text-text-2" data-testid="run-clock">{{ clock }}</span>
    </div>
    <div class="text-[13px] text-text-2" data-testid="run-meta">
      {{ describeScope(run.target, names) }} · started by {{ describeOwner(run.owner) }} · revision {{ run.revision }}
      <template v-if="run.started_at"> · started {{ formatClock(run.started_at) }} ({{ formatAgo(run.started_at, now) }})</template>
      <template v-else> · created {{ formatClock(run.created_at) }}</template>
      <template v-if="run.scenario.inline"> · inline scenario, not stored</template>
      <template v-if="steps.total"> · {{ steps.done }} of {{ steps.total }} steps through</template>
      <template v-if="leaseLeft"> · lease {{ leaseLeft }}</template>
    </div>
    <div
      class="h-1.5 overflow-hidden rounded-[3px] bg-[#262e39]"
      role="progressbar"
      aria-label="Run progress"
      :aria-valuenow="pct"
      aria-valuemin="0"
      aria-valuemax="100"
      data-testid="run-bar"
    >
      <div class="h-1.5" :class="barColor" :style="{ width: `${pct}%` }" />
    </div>
    <p v-if="run.status === 'queued'" class="m-0 text-[13px] text-text-2" data-testid="run-queued">
      Waits for the run on the same target to end; it starts by itself. The target is not touched until then.
    </p>
    <p v-if="run.reason && (run.status === 'error' || run.status === 'aborted')" class="m-0 text-[13px] text-block-text" data-testid="run-reason">{{ run.reason }}</p>
    <p v-else-if="run.status === 'failed'" class="m-0 text-[13px] text-block-text" data-testid="run-reason">
      {{ run.reason ?? 'A check failed.' }}
    </p>
    <ul v-if="run.warnings?.length" class="m-0 flex list-none flex-col gap-1 p-0 text-[13px] text-fault-text" data-testid="run-warnings">
      <li v-for="(w, i) in run.warnings" :key="i" data-testid="run-warning">
        <span class="mono text-xs">{{ warningLabels[w.code] ?? w.code }}</span> · {{ w.message }}
      </li>
    </ul>
  </section>
</template>
