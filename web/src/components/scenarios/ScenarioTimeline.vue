<script setup lang="ts">
// The timeline of a scenario (plan §2.10, §2.17 "Scenarios"): the steps in the order the engine runs them, each
// with its time, a chip for its type (orange for what impairs traffic, red for a rule, blue for the rest) and
// what it does in words. With a run, the timeline shows how the run is going: a step that was applied, is still
// pending, was skipped or failed (always with a label, never by color alone), the time it was applied at and
// how far that is from its schedule, the time it waited behind an apply, the step in effect ("now") and the
// next one with the time left. Without a run, and when it is editable, a step can be edited, deleted, or
// added after the last one.
import { computed } from 'vue';
import type { Run, RunStep, Step } from '../../api/generated/model';
import { formatDuration } from '../../lib/format';
import { lateness, offsetText, signedMs, stepMarks } from '../../lib/runs';
import { narrowText, stepText, stepType, stepTypeLabels, stepTypeTone, timelineOrder, type ScenarioNames } from '../../lib/scenarios';
import { useNow } from '../../composables/useNow';

const props = defineProps<{
  steps: Step[];
  names: ScenarioNames;
  /** The run whose progress the timeline shows. */
  run?: Run;
  /** Steps can be edited, deleted and added (no run is shown). */
  editable?: boolean;
  /** Steps something refers to cannot be deleted: the reason by step id. */
  protectedSteps?: Record<string, string>;
}>();
const emit = defineEmits<{ add: []; edit: [step: Step]; remove: [step: Step] }>();

const now = useNow();
const ordered = computed(() => timelineOrder(props.steps));
const runSteps = computed(() => new Map((props.run?.steps ?? []).map((s) => [s.id, s])));
const marks = computed(() => (props.run ? stepMarks(props.run, now.value) : {}));

interface Row {
  step: Step;
  type: string;
  tone: 'fault' | 'block' | 'normal';
  text: string;
  narrow?: string;
  run?: RunStep;
  late?: number;
  current: boolean;
  next?: { due: boolean; inMs: number };
}

const rows = computed<Row[]>(() =>
  ordered.value.map((step, i) => {
    const t = stepType(step);
    const run = runSteps.value.get(step.id);
    const earlier = ordered.value.slice(0, i).map((s) => runSteps.value.get(s.id)).filter((s): s is RunStep => !!s);
    return {
      step,
      type: t ? stepTypeLabels[t] : 'Step',
      tone: t ? stepTypeTone[t] : 'normal',
      text: stepText(step, props.names),
      narrow: narrowText(step, props.names),
      ...(run ? { run, late: lateness(run, earlier) } : {}),
      current: marks.value.current === step.id,
      ...(marks.value.next?.id === step.id ? { next: marks.value.next } : {}),
    };
  }),
);

const chipClass = {
  fault: 'border-fault-border bg-fault-tint text-fault-text',
  block: 'border-block-border bg-block-tint text-block-text',
  normal: 'border-accent-strong bg-accent-tint text-[#cfe6ff]',
};

function dotClass(r: Row): string {
  switch (r.run?.status) {
    case 'applied':
      return r.tone === 'normal' ? 'border-accent bg-accent' : r.tone === 'block' ? 'border-block-text bg-block-text' : 'border-fault bg-fault';
    case 'error':
      return 'border-block-text bg-block-tint';
    case 'skipped':
      return 'border-dashed border-text-3 bg-transparent';
    default:
      return 'border-text-3 bg-surface';
  }
}

const statusWord: Record<RunStep['status'], string> = { pending: 'pending', applied: 'applied', skipped: 'skipped', error: 'error' };

function inText(ms: number): string {
  return `in ${offsetText(Math.ceil(ms / 100) * 100)}`;
}
</script>

<template>
  <section class="card flex flex-col" aria-label="Timeline" data-testid="timeline">
    <ol class="m-0 flex list-none flex-col p-0">
      <li
        v-for="(r, i) in rows"
        :key="r.step.id"
        class="grid min-h-14 grid-cols-[64px_24px_minmax(0,1fr)] items-start"
        data-testid="timeline-step"
        :data-step="r.step.id"
        :data-status="r.run?.status"
        :aria-current="r.current ? 'step' : undefined"
      >
        <span class="mono pt-0.5 text-[13px] text-text-2">{{ r.step.at }}</span>
        <span class="flex h-full flex-col items-center" aria-hidden="true">
          <span class="mt-1 box-border h-3 w-3 rounded-full border-2" :class="dotClass(r)" />
          <span v-if="i < rows.length - 1" class="w-0.5 grow bg-[#262e39]" />
        </span>
        <div class="flex min-w-0 flex-col gap-1 pb-3.5">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span class="inline-block rounded border px-2 text-xs" :class="chipClass[r.tone]" data-testid="step-type">{{ r.type }}</span>
            <span class="text-sm" :class="r.run?.status === 'skipped' ? 'text-text-3' : ''" data-testid="step-text">{{ r.text }}</span>
            <span v-if="r.current" class="text-xs text-fault-text" data-testid="step-now">now</span>
            <span v-if="r.next" class="text-xs text-text-2" data-testid="step-next">{{ r.next.due ? 'applying…' : `next ${inText(r.next.inMs)}` }}</span>
          </div>
          <div class="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-text-2">
            <span class="mono text-text-3">{{ r.step.id }}</span>
            <span v-if="r.narrow">on {{ r.narrow }}</span>
            <template v-if="r.run">
              <span class="rounded border border-border-strong px-1.5" data-testid="step-status">{{ statusWord[r.run.status] }}</span>
              <span v-if="r.run.status === 'applied'" class="mono" data-testid="step-timing">
                at {{ offsetText(r.run.offset_ms) }}<template v-if="r.late !== undefined"> ({{ signedMs(r.late) }})</template
                ><template v-if="r.run.generation !== undefined"> · generation {{ r.run.generation }}</template>
              </span>
              <span v-if="r.run.queue_wait_ms" class="mono" data-testid="step-queue-wait">waited {{ r.run.queue_wait_ms }} ms</span>
            </template>
            <span v-if="r.step.at && r.step.wait" class="mono">timeout {{ formatDuration(r.step.wait.timeout) }}</span>
          </div>
          <p v-if="r.run?.error" class="m-0 text-xs text-block-text" data-testid="step-error">{{ r.run.error }}</p>
          <div v-if="editable && !run" class="flex gap-2 pt-0.5">
            <button type="button" class="btn btn-sm" :aria-label="`Edit step ${r.step.id}`" @click="emit('edit', r.step)">Edit</button>
            <button
              type="button"
              class="btn btn-sm"
              :aria-label="`Delete step ${r.step.id}`"
              :disabled="!!protectedSteps?.[r.step.id]"
              :title="protectedSteps?.[r.step.id]"
              @click="emit('remove', r.step)"
            >
              Delete
            </button>
          </div>
        </div>
      </li>
    </ol>
    <p v-if="rows.length === 0" class="m-0 text-sm text-text-2" data-testid="timeline-empty">No steps yet: a scenario needs at least one.</p>
    <div v-if="editable && !run" class="pt-1">
      <button type="button" class="btn" data-testid="add-step" @click="emit('add')">Add step</button>
    </div>
    <p v-if="run && !run.snapshot && steps.length === 0" class="m-0 text-sm text-text-2">The timeline appears when the run starts.</p>
  </section>
</template>
