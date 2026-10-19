<script setup lang="ts">
// Scenarios (plan §2.10, §2.17 "Scenarios"; M19): the list of scenarios with their target, length, steps and
// last result, and for the chosen one the tabs Timeline, YAML and Runs. The timeline shows the steps with their
// type chips, the checks and the artifacts of a run; a scenario is made and changed here — its settings, its
// steps and its checks, or the YAML — as a change of the draft that goes through the unapplied-changes bar, the
// preview and the apply like a profile (M13). Run starts a run (a scenario with changes that are not applied
// yet is run from the draft, without making a revision) and the page follows it: the current step is marked,
// the checks resolve to PASSED or FAILED when their windows close, the artifacts appear, and Abort removes
// the run's overlays. A run keeps going when the tab closes.
import { useQueryClient } from '@tanstack/vue-query';
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui';
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ApiError, errorMessage } from '../api/fetcher';
import { abortRun } from '../api/generated/hooks/runs/runs';
import type { Check, Run, Scenario, Step } from '../api/generated/model';
import ChecksCard from '../components/scenarios/ChecksCard.vue';
import ArtifactsCard from '../components/scenarios/ArtifactsCard.vue';
import CheckDialog from '../components/scenarios/CheckDialog.vue';
import ImportDialog from '../components/scenarios/ImportDialog.vue';
import RunDialog from '../components/scenarios/RunDialog.vue';
import RunProgress from '../components/scenarios/RunProgress.vue';
import ScenarioDialog from '../components/scenarios/ScenarioDialog.vue';
import ScenarioTimeline from '../components/scenarios/ScenarioTimeline.vue';
import ScenarioYaml from '../components/scenarios/ScenarioYaml.vue';
import StepDialog from '../components/scenarios/StepDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import { firstError, useActiveConfig, useActiveRuns, useGatewayState, useLastRuns, useNames, useRun, useRuns, useScenarioRuns, useScenarios } from '../composables/gateway';
import { useRefIndex, useTopology } from '../composables/scenarioTargets';
import { useNow } from '../composables/useNow';
import { describeScope } from '../lib/faults';
import { formatAgo } from '../lib/format';
import { refocusAfterSwap } from '../lib/focus';
import { isActive, isRunOf, lastRunByScenario, mergeRuns, resultText, statusLabels, statusTone } from '../lib/runs';
import { scenarioLength, scenarioMeta, scenarioProblems, stepReferences, withoutCheck, withoutStep, lengthText } from '../lib/scenarios';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDraftStore } from '../stores/draft';
import { useToastStore } from '../stores/toasts';

const props = defineProps<{ id?: string }>();

const scenarios = useScenarios();
const runs = useRuns();
const active = useActiveConfig();
const { names } = useNames();
const index = useRefIndex();
const topology = useTopology();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const draft = useDraftStore();
const route = useRoute();
const router = useRouter();
const now = useNow();

interface Item {
  id: string;
  config: Scenario;
  isNew?: boolean;
  changed?: boolean;
  /** A scenario the draft deletes: shown until the draft is applied or discarded. */
  deleted?: boolean;
}

/** The scenarios as the gateway has them, with the draft's: new, changed and deleted ones. */
const items = computed<Item[]>(() => {
  const views = scenarios.data.value ?? [];
  const out: Item[] = [];
  for (const v of views) {
    const edit = draft.scenarios[v.id];
    if (edit === undefined) out.push({ id: v.id, config: v.config });
    else if (edit === null) out.push({ id: v.id, config: v.config, deleted: true });
    else out.push({ id: v.id, config: edit, changed: true });
  }
  for (const [id, edit] of Object.entries(draft.scenarios)) {
    if (edit && !views.some((v) => v.id === id)) out.push({ id, config: edit, isNew: true });
  }
  return out.sort((a, b) => a.config.name.localeCompare(b.config.name));
});
const allNames = computed(() => items.value.filter((i) => !i.deleted).map((i) => i.config.name));

const selected = computed<Item | undefined>(() => (props.id ? items.value.find((i) => i.id === props.id || i.config.name === props.id) : items.value[0]));
const unapplied = computed(() => !!(selected.value?.isNew || selected.value?.changed));
const notFound = computed(() => !!props.id && !scenarios.isLoading.value && !selected.value && !!scenarios.data.value);

useApiScreen('the scenarios', () => [
  { method: 'GET', path: '/scenarios', note: 'the scenarios of the active revision' },
  { method: 'GET', path: '/runs', note: 'the runs, newest first (without their timelines)' },
  ...(selected.value?.id ? [{ method: 'POST' as const, path: `/scenarios/${selected.value.id}/runs`, note: 'start a run' }] : []),
]);

// ---- the tabs
type Tab = 'timeline' | 'yaml' | 'runs';
const tabs: { value: Tab; label: string }[] = [
  { value: 'timeline', label: 'Timeline' },
  { value: 'yaml', label: 'YAML' },
  { value: 'runs', label: 'Runs' },
];
const tab = computed<Tab>(() => (['timeline', 'yaml', 'runs'] as const).find((t) => t === route.query.tab) ?? 'timeline');
function go(query: Record<string, string | undefined>) {
  const q: Record<string, string> = {};
  for (const [k, v] of Object.entries({ ...route.query, ...query })) if (typeof v === 'string') q[k] = v;
  void router.replace({ path: route.path, query: q });
}
function pickTab(t: Tab) {
  go({ tab: t === 'timeline' ? undefined : t });
}

// ---- the runs of the scenario
// `runs` is a window (the newest 200): it gives the last results of the list; the runs that are going on and the
// runs of the scenario shown are asked for by the gateway, so a run behind many newer ones is not missed
const activeRuns = useActiveRuns();
const allRuns = computed(() => mergeRuns(runs.data.value?.items ?? [], activeRuns.data.value ?? []));
const last = computed(() => lastRunByScenario(allRuns.value));
const ref0 = computed(() => (selected.value ? { id: selected.value.id, name: selected.value.config.name } : undefined));
const RUNS_PAGE = 50;
const runsShown = ref(RUNS_PAGE);
watch(() => selected.value?.id, () => (runsShown.value = RUNS_PAGE));
const scenarioRuns = useScenarioRuns(() => ref0.value, () => runsShown.value, () => tab.value === 'runs');
const ofScenario = computed(() => scenarioRuns.data.value ?? []);
const tabLoading = computed(() => scenarioRuns.isLoading.value);
const tabUnknown = computed(() => !!scenarioRuns.error.value && scenarioRuns.data.value === undefined);
const activeRun = computed(() => (ref0.value ? activeRuns.data.value?.find((r) => isRunOf(r, ref0.value!)) : undefined));
/** The run the page follows: asked for in the address, else the one that is going on, and after it ended the same one. */
const followed = ref<string>();
const dismissed = ref<string>();
watch(
  () => [selected.value?.id, activeRun.value?.id] as const,
  ([sid, rid], old) => {
    if (sid !== old?.[0]) {
      followed.value = undefined;
      dismissed.value = undefined;
    }
    if (rid && rid !== dismissed.value && !route.query.run) followed.value = rid;
  },
  { immediate: true },
);
const runId = computed(() => (typeof route.query.run === 'string' ? route.query.run : followed.value));
const runQuery = useRun(() => runId.value);
const run = computed<Run | undefined>(() => (runId.value ? runQuery.data.value : undefined));
// only the gateway's 404 says the run is gone (it keeps the runs of 30 days); any other failure is a failed read to retry
const runGone = computed(() => !!runId.value && !runQuery.data.value && runQuery.error.value instanceof ApiError && runQuery.error.value.status === 404);
const runFailed = computed(() => !!runId.value && !runQuery.data.value && !!runQuery.error.value && !runGone.value);

function showRun(id: string) {
  dismissed.value = undefined;
  go({ run: id, tab: undefined });
}
function hideRun() {
  dismissed.value = runId.value;
  followed.value = undefined;
  go({ run: undefined });
}
function started(r: Run) {
  showRun(r.id);
}

// the definition shown: the run's own snapshot while a run is shown (what it executes), else the scenario as the draft has it
const shown = computed<Scenario | undefined>(() => (run.value?.snapshot ?? selected.value?.config) as Scenario | undefined);
const problems = computed(() => (selected.value && !selected.value.deleted ? scenarioProblems(selected.value.config, allNames.value.filter((n) => n !== selected.value!.config.name), topology.value) : []));
const refused = computed(() => (selected.value ? Object.entries(draft.serverErrors[selected.value.id] ?? {}) : []));

// ---- the editors
const settingsOpen = ref(false);
const settingsFor = ref<{ id: string; config: Scenario }>();
function newScenario() {
  settingsFor.value = undefined;
  settingsOpen.value = true;
}
function editSettings() {
  if (!selected.value) return;
  settingsFor.value = { id: selected.value.id, config: selected.value.config };
  settingsOpen.value = true;
}
function saved(id: string) {
  void router.push({ path: `/scenarios/${id}`, query: {} });
}
const importOpen = ref(false);

const stepOpen = ref(false);
const stepEditing = ref<Step>();
function addStep() {
  stepEditing.value = undefined;
  stepOpen.value = true;
}
function editStep(s: Step) {
  stepEditing.value = s;
  stepOpen.value = true;
}
const protectedSteps = computed(() => {
  const out: Record<string, string> = {};
  const sc = selected.value?.config;
  if (!sc) return out;
  for (const s of sc.steps) {
    const r = stepReferences(sc, s.id);
    const by = [...r.steps.map((x) => `the step ${x}`), ...r.checks.map((x) => `the check ${x}`)];
    if (by.length) out[s.id] = `Used by ${by.join(' and ')}; change that first.`;
  }
  return out;
});
function removeStep(s: Step) {
  const sel = selected.value;
  if (!sel) return;
  draft.setScenario(sel.id, withoutStep(sel.config, s.id));
}
const checkOpen = ref(false);
const checkEditing = ref<number>();
function addCheck() {
  checkEditing.value = undefined;
  checkOpen.value = true;
}
function editCheck(i: number) {
  checkEditing.value = i;
  checkOpen.value = true;
}
function removeCheck(i: number) {
  const sel = selected.value;
  if (!sel) return;
  draft.setScenario(sel.id, withoutCheck(sel.config, i));
}

function deleteScenario() {
  if (selected.value) draft.setScenario(selected.value.id, null);
}

// ---- running and aborting
const runOpen = ref(false);
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);
const aborting = ref(false);
async function abort() {
  const r = run.value;
  if (!r) return;
  aborting.value = true;
  try {
    const done = await abortRun(r.id);
    apiPanel.record('Abort run', [{ method: 'POST', path: `/runs/${r.id}/abort` }]);
    toasts.push('success', `${done.scenario.name} aborted`, 'The overlays of the run are removed.');
    await Promise.all(['runs', 'overlays', 'devices', 'faults', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    toasts.push('error', 'The run could not be aborted', errorMessage(e));
  } finally {
    aborting.value = false;
  }
}

// The last result of each scenario: the newest run of the window; a scenario with none in it, while the gateway
// has more runs than the window holds, is asked for by itself, so "Never run" is only said when it is true.
const beyond = computed(() =>
  runs.data.value?.next_cursor ? items.value.filter((i) => !i.isNew && !last.value.has(i.id) && !last.value.has(i.config.name)).map((i) => ({ id: i.id, name: i.config.name })) : [],
);
const beyondRuns = useLastRuns(() => beyond.value);

type LastResult = { state: 'run'; text: string; tone: string } | { state: 'loading' | 'unknown' | 'never' };
function lastResult(i: Item): LastResult {
  let r = last.value.get(i.id) ?? last.value.get(i.config.name);
  if (!r) {
    if (runs.isLoading.value) return { state: 'loading' };
    if (runs.error.value && runs.data.value === undefined) return { state: 'unknown' };
    const k = beyond.value.findIndex((b) => b.id === i.id);
    if (k >= 0) {
      const q = beyondRuns.value[k];
      if (!q || q.isLoading) return { state: 'loading' };
      if (q.data === undefined) return { state: 'unknown' };
      r = q.data ?? undefined;
    }
  }
  if (!r) return { state: 'never' };
  const when = r.ended_at ?? r.started_at ?? r.created_at;
  const text = isActive(r) ? statusLabels[r.status] : `${resultText(r)} · ${formatAgo(when, now.value)}`;
  const tone = r.status === 'passed' ? 'text-accent-text' : r.status === 'running' ? 'text-fault-text' : r.status === 'failed' || r.status === 'error' ? 'text-block-text' : 'text-text-2';
  return { state: 'run', text, tone };
}
const lastById = computed(() => new Map(items.value.map((i) => [i.id, lastResult(i)])));

// the last results are answers only once the runs are read: until then, or when the read failed, the list says so instead of "Never run"
const runsUnknown = computed(() => !!runs.error.value && runs.data.value === undefined);
const retry = () => Promise.all([scenarios.refetch(), active.refetch(), runs.refetch(), activeRuns.refetch()]);
const selectedLength = computed(() => (selected.value ? lengthText(scenarioLength(selected.value.config)) : ''));
const checksShown = computed<Check[]>(() => shown.value?.checks ?? []);
</script>

<template>
  <PageHeader title="Scenarios" context="Timelines of faults, checks and captures · stored in the revisions, runnable from the UI, the API or the CLI">
    <template #actions>
      <button type="button" class="btn" @click="importOpen = true">Import YAML</button>
      <button type="button" class="btn btn-primary" @click="newScenario">New scenario</button>
    </template>
  </PageHeader>

  <p v-if="scenarios.isLoading.value || active.isLoading.value" class="text-text-2">Loading the scenarios…</p>
  <LoadError v-else-if="firstError(scenarios, active)" :error="firstError(scenarios, active)" what="The scenarios are not loaded" :retry="retry" />
  <EmptyState v-else-if="items.length === 0" text="No scenarios yet. A scenario is a timeline of faults and checks that can be run again and again.">
    <button type="button" class="btn btn-primary" @click="newScenario">New scenario</button>
  </EmptyState>
  <div v-else class="grid grid-cols-1 gap-5 lg:grid-cols-[280px_minmax(0,1fr)]">
    <nav aria-label="Scenarios" class="flex flex-col gap-2.5" data-testid="scenario-list">
      <LoadError v-if="runsUnknown" :error="runs.error.value" what="The last results are not loaded" :retry="() => runs.refetch()" />
      <RouterLink
        v-for="i in items"
        :key="i.id"
        :to="`/scenarios/${i.id}`"
        class="flex flex-col items-start gap-1 rounded-lg border bg-surface px-3.5 py-3 text-left text-text no-underline hover:bg-surface-2"
        :class="selected?.id === i.id ? 'border-accent-strong !bg-surface-2' : 'border-border'"
        :aria-current="selected?.id === i.id ? 'page' : undefined"
        data-testid="scenario-item"
      >
        <span class="flex w-full flex-wrap items-center gap-2">
          <span class="mono text-sm font-medium" :class="{ 'line-through opacity-60': i.deleted }">{{ i.config.name }}</span>
          <span v-if="i.deleted" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="scenario-mark">DELETED</span>
          <span v-else-if="i.isNew" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="scenario-mark">NEW</span>
          <span v-else-if="i.changed" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="scenario-mark">CHANGED</span>
        </span>
        <span class="text-xs text-text-2" data-testid="scenario-meta">{{ describeScope(i.config.target, names) }} · {{ scenarioMeta(i.config) }}</span>
        <template v-for="l in [lastById.get(i.id)!]" :key="l.state">
          <span v-if="l.state === 'run'" class="text-xs" :class="l.tone" data-testid="scenario-last">{{ l.text }}</span>
          <span v-else-if="l.state === 'loading'" class="text-xs text-text-3" data-testid="scenario-last">Loading…</span>
          <span v-else-if="l.state === 'unknown'" class="text-xs text-text-3" data-testid="scenario-last">Last result unknown</span>
          <span v-else class="text-xs text-text-3" data-testid="scenario-last">Never run</span>
        </template>
      </RouterLink>
    </nav>

    <section v-if="selected" class="flex min-w-0 flex-col gap-4" :aria-label="`Scenario ${selected.config.name}`" data-testid="scenario-detail">
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div class="min-w-0">
          <h2 class="m-0 text-[22px] font-medium mono" :class="{ 'line-through opacity-60': selected.deleted }" data-testid="scenario-name">{{ selected.config.name }}</h2>
          <div class="mt-1 text-[13px] text-text-2" data-testid="scenario-context">
            Target {{ describeScope(selected.config.target, names) }} · {{ selectedLength }} · capture {{ selected.config.capture ? 'on' : 'off' }}
            <template v-if="selected.config.description"> · {{ selected.config.description }}</template>
          </div>
        </div>
        <div class="flex flex-wrap gap-2" data-focus-group>
          <template v-if="selected.deleted">
            <button type="button" class="btn" :aria-label="`Undo the deletion of ${selected.config.name}`" data-focus-swap @click="refocusAfterSwap($event); draft.restoreScenario(selected.id)">Undo</button>
          </template>
          <template v-else>
            <button type="button" class="btn" @click="editSettings">Settings</button>
            <button type="button" class="btn" data-focus-swap :aria-label="`Delete ${selected.config.name}`" @click="refocusAfterSwap($event); deleteScenario()">Delete</button>
            <button v-if="run && isActive(run)" type="button" class="btn !border-block-border !bg-block-tint !text-block-text" :disabled="aborting || connection.offline" data-testid="abort-run" @click="abort">
              {{ aborting ? 'Aborting…' : 'Abort run' }}
            </button>
            <button
              v-else
              type="button"
              class="btn btn-primary"
              :disabled="blocked || problems.length > 0"
              :title="problems.length ? 'Fix the problems of the scenario first' : undefined"
              data-testid="run-scenario"
              @click="runOpen = true"
            >
              Run
            </button>
          </template>
        </div>
      </header>

      <p v-if="selected.deleted" class="m-0 text-[13px] text-text-2">Removed when the changes are applied. A run of it that is queued or running is not stopped by that: it keeps going on its own copy of the scenario and stays reachable from the overview and from the page of the run.</p>

      <ul v-if="refused.length" class="m-0 flex list-none flex-col gap-1 rounded-md border border-block-border bg-block-tint p-3 text-[13px] text-block-text" data-testid="scenario-refused">
        <li v-for="[path, message] in refused" :key="path"><span class="mono text-xs">{{ path === 'scenario' ? 'scenario' : path }}</span> {{ message }}</li>
      </ul>
      <ul v-else-if="problems.length && !selected.deleted" class="m-0 flex list-none flex-col gap-1 rounded-md border border-fault-border bg-fault-tint p-3 text-[13px] text-fault-text" data-testid="scenario-problems">
        <li v-for="p in problems" :key="p.path + p.message"><span class="mono text-xs">{{ p.path }}</span> {{ p.message }}</li>
      </ul>

      <!-- Reka's tabs: the tab pattern with its tab panels, roving tabindex and arrow keys; the tab is the address's -->
      <TabsRoot :model-value="tab" class="flex min-w-0 flex-col gap-4" @update:model-value="pickTab($event as Tab)">
        <TabsList aria-label="Scenario views" class="flex gap-1 border-b border-border">
          <TabsTrigger
            v-for="t in tabs"
            :key="t.value"
            :value="t.value"
            class="h-10 cursor-pointer border-0 border-b-2 bg-transparent px-3.5 text-sm"
            :class="tab === t.value ? 'border-accent text-text' : 'border-transparent text-text-2 hover:text-text'"
          >
            {{ t.label }}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="timeline" class="flex flex-col gap-4">
          <div v-if="runGone" class="card text-sm text-text-2" data-testid="run-gone">
            This run is not there any more (the gateway keeps the runs of 30 days).
            <button type="button" class="btn btn-sm ml-2" @click="hideRun">Back to the scenario</button>
          </div>
          <div v-else-if="runFailed" class="flex flex-col items-start gap-2" data-testid="run-failed">
            <LoadError class="self-stretch" :error="runQuery.error.value" what="The run is not loaded" :retry="() => runQuery.refetch()" />
            <button type="button" class="btn btn-sm" @click="hideRun">Back to the scenario</button>
          </div>
          <p v-else-if="runId && runQuery.isLoading.value" class="m-0 text-sm text-text-2" data-testid="run-loading">Loading the run…</p>
          <div class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
            <div class="flex min-w-0 flex-col gap-4">
              <template v-if="run">
                <RunProgress :run="run" :names="names" />
                <div class="flex flex-wrap gap-2 text-[13px]">
                  <RouterLink :to="`/runs/${run.id}`" data-testid="run-page-link">Open the run page</RouterLink>
                  <button type="button" class="btn btn-sm" data-testid="hide-run" @click="hideRun">Back to the scenario</button>
                </div>
              </template>
              <ScenarioTimeline
                v-if="shown"
                :steps="shown.steps"
                :names="names"
                :run="run"
                :editable="!selected.deleted"
                :protected-steps="protectedSteps"
                @add="addStep"
                @edit="editStep"
                @remove="removeStep"
              />
            </div>
            <div class="flex min-w-0 flex-col gap-4">
              <ChecksCard
                :checks="checksShown"
                :names="names"
                :results="run?.checks"
                :editable="!selected.deleted"
                @add="addCheck"
                @edit="editCheck"
                @remove="removeCheck"
              />
              <ArtifactsCard :run="run" />
              <p class="m-0 text-xs leading-relaxed text-text-3">A run keeps going if this tab closes. It stops at its last step and its last check, or when it is aborted; either way its overlays are removed.</p>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="yaml">
          <ScenarioYaml v-if="!selected.deleted" :scenario="{ id: selected.id, config: selected.config }" :index="index" :names="allNames.filter((n) => n !== selected!.config.name)" />
        </TabsContent>

        <TabsContent value="runs">
          <section class="card flex flex-col gap-2" aria-label="Runs" data-testid="runs-tab">
            <p v-if="tabLoading" class="m-0 text-sm text-text-2" data-testid="runs-loading">Loading the runs…</p>
            <LoadError v-else-if="tabUnknown" :error="scenarioRuns.error.value" what="The runs are not loaded" :retry="() => scenarioRuns.refetch()" />
            <p v-else-if="ofScenario.length === 0" class="m-0 text-sm text-text-2" data-testid="runs-empty">This scenario has not been run yet.</p>
            <table v-else class="w-full border-collapse text-[13px]">
              <thead>
                <tr class="text-left text-text-3">
                  <th class="py-1.5 pr-3 font-normal">Result</th>
                  <th class="py-1.5 pr-3 font-normal">Started</th>
                  <th class="py-1.5 pr-3 font-normal">Reason</th>
                  <th class="py-1.5 font-normal"><span class="sr-only">Show</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in ofScenario" :key="r.id" class="border-t border-border" data-testid="run-row" :data-status="r.status">
                  <td class="py-2 pr-3">
                    <span class="inline-flex items-center gap-2">
                      <span class="inline-block h-2 w-2 rounded-full" :class="{ 'bg-accent': statusTone[r.status] === 'normal', 'bg-fault': statusTone[r.status] === 'fault', 'bg-block-text': statusTone[r.status] === 'block', 'bg-offline': statusTone[r.status] === 'offline', 'bg-unknown': statusTone[r.status] === 'unknown' }" aria-hidden="true" />
                      {{ resultText(r) }}
                    </span>
                  </td>
                  <td class="py-2 pr-3 text-text-2">{{ formatAgo(r.started_at ?? r.created_at, now) }}<span v-if="r.scenario.inline"> · inline</span></td>
                  <td class="py-2 pr-3 text-text-2">{{ r.reason ?? '' }}</td>
                  <td class="py-2 text-right"><a href="#" :aria-label="`Show the run from ${formatAgo(r.started_at ?? r.created_at, now)}`" @click.prevent="showRun(r.id)">Show</a></td>
                </tr>
              </tbody>
            </table>
            <div v-if="!tabLoading && !tabUnknown && scenarioRuns.more.value" class="flex items-center gap-3 pt-1 text-[13px] text-text-2">
              <button v-if="runsShown < 500" type="button" class="btn btn-sm" data-testid="runs-more" @click="runsShown = Math.min(runsShown + 100, 500)">Show older runs</button>
              <span v-else data-testid="runs-more-api">The newest 500 runs are shown; the API lists the older ones (<span class="mono">GET /runs?scenario=…</span>).</span>
            </div>
          </section>
        </TabsContent>
      </TabsRoot>
    </section>

    <section v-else-if="notFound" class="card text-sm text-text-2" data-testid="scenario-not-found">
      There is no scenario "{{ id }}".
      <template v-if="typeof route.query.run === 'string'"><RouterLink :to="`/runs/${route.query.run}`" data-testid="not-found-run-link">Open the run</RouterLink> · </template>
      <RouterLink to="/scenarios">Back to the scenarios</RouterLink>
    </section>
  </div>

  <ScenarioDialog v-model:open="settingsOpen" :scenario="settingsFor" :names="allNames" @saved="saved" />
  <ImportDialog v-model:open="importOpen" :index="index" :names="allNames" @saved="saved" />
  <template v-if="selected && !selected.deleted">
    <StepDialog v-model:open="stepOpen" :scenario="{ id: selected.id, config: selected.config }" :step="stepEditing" />
    <CheckDialog v-model:open="checkOpen" :scenario="{ id: selected.id, config: selected.config }" :index="checkEditing" />
    <RunDialog v-model:open="runOpen" :scenario="{ id: selected.id, config: selected.config, unapplied }" @started="started" />
  </template>
</template>
