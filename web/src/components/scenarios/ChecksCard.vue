<script setup lang="ts">
// The checks of a scenario (plan §2.10, §2.17 "Scenarios"): what each observes and in which window. With a
// run they resolve to PASSED or FAILED when their windows close (PENDING until then, and ERROR for one that
// could not be decided), with the message and the observations that decided them; a run that ended before
// a check was decided leaves it PENDING. Without a run, and when it is editable, a check can be edited,
// deleted, or added.
import { computed } from 'vue';
import type { Check, CheckResult } from '../../api/generated/model';
import { checkStatusText } from '../../lib/runs';
import { checkName, checkText, checkType, checkTypeLabels, windowText, type ScenarioNames } from '../../lib/scenarios';
import { formatClock } from '../../lib/format';
import { focusAfterUpdate } from '../../lib/focus';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{
  checks: Check[];
  names: ScenarioNames;
  /** The results of a run, by the position of the check. */
  results?: CheckResult[];
  editable?: boolean;
}>();
const emit = defineEmits<{ add: []; edit: [index: number]; remove: [index: number] }>();

interface Row {
  index: number;
  check: Check;
  title: string;
  detail: string;
  result?: CheckResult;
}

const rows = computed<Row[]>(() => {
  const byIndex = new Map((props.results ?? []).map((r) => [r.index, r]));
  const out = props.checks.map((check, index) => {
    const type = checkType(check);
    const result = byIndex.get(index);
    return {
      index,
      check,
      title: result?.name ?? checkName(check, index),
      detail: `${type ? checkTypeLabels[type] : 'Check'}: ${checkText(check, props.names)} · ${windowText(check.window)}`,
      ...(result ? { result } : {}),
    };
  });
  // a run of an older version of the scenario has results for checks the scenario no longer has
  for (const r of props.results ?? []) {
    if (!props.checks[r.index]) out.push({ index: r.index, check: { window: { from: 'start' } }, title: r.name, detail: r.type.replace(/_/g, ' '), result: r });
  }
  return out;
});

const tone = {
  pending: 'border-border-strong text-text-2',
  passed: 'border-accent-strong bg-accent-tint text-[#cfe6ff]',
  failed: 'border-block-border bg-block-tint text-block-text',
  error: 'border-block-border bg-block-tint text-block-text',
};

/** Deletes a check; the focus goes to the check that took its place, else the one before, else "Add check" (the button used is gone). */
function remove(r: Row, pos: number) {
  const target = pos + 1 < rows.value.length ? pos : pos - 1;
  emit('remove', r.index);
  focusAfterUpdate(target >= 0 ? `[data-testid="checks"] [data-edit-check="${target}"]` : '[data-testid="add-check"]');
}

function evidenceText(e: Record<string, unknown>): string {
  return Object.entries(e)
    .map(([k, v]) => `${k} ${typeof v === 'object' ? JSON.stringify(v) : String(v)}`)
    .join(' · ');
}
</script>

<template>
  <SectionCard title="Checks">
    <ul v-if="rows.length" class="m-0 flex list-none flex-col gap-3 p-0" data-testid="checks">
      <li v-for="(r, pos) in rows" :key="r.index" class="flex items-start gap-2.5" data-testid="check" :data-status="r.result?.status">
        <span
          v-if="r.result"
          class="mt-0.5 shrink-0 rounded border px-1.5 text-xs font-semibold tracking-wide"
          :class="tone[r.result.status]"
          data-testid="check-state"
          >{{ checkStatusText[r.result.status] }}</span
        >
        <span v-else class="mt-0.5 shrink-0 rounded border border-border-strong px-1.5 text-xs font-semibold tracking-wide text-text-2">CHECK</span>
        <div class="min-w-0 flex-1 text-sm leading-relaxed">
          <div class="break-words" data-testid="check-name">{{ r.title }}</div>
          <div class="text-xs text-text-2">{{ r.detail }}</div>
          <div v-if="r.result?.message" class="text-xs text-text-2" data-testid="check-message">{{ r.result.message }}</div>
          <div v-if="r.result?.window_start" class="mono text-xs text-text-3">
            window {{ formatClock(r.result.window_start) }}<template v-if="r.result.window_end"> to {{ formatClock(r.result.window_end) }}</template>
          </div>
          <details v-if="r.result?.evidence?.length" class="text-xs text-text-2">
            <summary class="cursor-pointer text-accent-text">{{ r.result.evidence.length }} {{ r.result.evidence.length === 1 ? 'observation' : 'observations' }}</summary>
            <ul class="m-0 mt-1 list-none p-0">
              <li v-for="(e, i) in r.result.evidence" :key="i" class="mono break-words" data-testid="check-evidence">{{ evidenceText(e) }}</li>
            </ul>
          </details>
          <div v-if="editable && !results" class="flex gap-2 pt-1">
            <button type="button" class="btn btn-sm" :data-edit-check="pos" :aria-label="`Edit check ${r.title}`" @click="emit('edit', r.index)">Edit</button>
            <button type="button" class="btn btn-sm" :aria-label="`Delete check ${r.title}`" @click="remove(r, pos)">Delete</button>
          </div>
        </div>
      </li>
    </ul>
    <p v-else class="m-0 text-sm text-text-2" data-testid="checks-empty">
      No checks: the run passes when its steps could be executed. A check observes what the device does, e.g. that it reconnects within 30 s.
    </p>
    <div v-if="editable && !results"><button type="button" class="btn" data-testid="add-check" @click="emit('add')">Add check</button></div>
  </SectionCard>
</template>
