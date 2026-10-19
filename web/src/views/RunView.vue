<script setup lang="ts">
// A run (plan §2.10, §2.17 "Run states"; M19): the page of one run, also of an inline run that has no scenario
// of its own — its state, progress and warnings, the timeline as executed (what each step did and when, against
// its schedule), the checks, the artifacts, and after its end the counters of the overlays it made. While it is queued
// or running it is read every second and by its events; Abort removes its overlays.
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { errorMessage } from '../api/fetcher';
import { abortRun, getRunReportJson } from '../api/generated/hooks/runs/runs';
import ArtifactsCard from '../components/scenarios/ArtifactsCard.vue';
import ChecksCard from '../components/scenarios/ChecksCard.vue';
import RunProgress from '../components/scenarios/RunProgress.vue';
import ScenarioTimeline from '../components/scenarios/ScenarioTimeline.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import { ApiError } from '../api/fetcher';
import { useNames, useRun } from '../composables/gateway';
import { formatCount } from '../lib/format';
import { isActive } from '../lib/runs';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useToastStore } from '../stores/toasts';

const props = defineProps<{ id: string }>();

const { names } = useNames();
const run = useRun(() => props.id);
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();

useApiScreen('the run', () => [
  { method: 'GET', path: `/runs/${props.id}`, note: 'the run with its timeline, checks and artifacts' },
  { method: 'POST', path: `/runs/${props.id}/abort`, note: 'abort it; its overlays are removed' },
  { method: 'GET', path: `/runs/${props.id}/report.json`, note: 'the final report with the counters of its overlays' },
]);

const data = computed(() => run.data.value);
const notFound = computed(() => run.error.value instanceof ApiError && run.error.value.status === 404);

// the final report has the counters of every overlay the run made (zero hits show there); it exists once the run has ended
const report = useQuery({
  queryKey: computed(() => ['runs', 'report', props.id]),
  queryFn: ({ signal }) => getRunReportJson(props.id, { signal }),
  enabled: computed(() => !!data.value && !isActive(data.value)),
  retry: false,
});
const counters = computed(() => report.data.value?.counters ?? []);

const aborting = ref(false);
async function abort() {
  aborting.value = true;
  try {
    const done = await abortRun(props.id);
    apiPanel.record('Abort run', [{ method: 'POST', path: `/runs/${props.id}/abort` }]);
    toasts.push('success', `${done.scenario.name} aborted`, 'The overlays of the run are removed.');
    await Promise.all(['runs', 'overlays', 'devices', 'faults', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    toasts.push('error', 'The run could not be aborted', errorMessage(e));
  } finally {
    aborting.value = false;
  }
}
</script>

<template>
  <PageHeader title="Run" :context="data ? `${data.scenario.name} · ${data.id}` : id">
    <template #breadcrumb>
      <RouterLink to="/scenarios" class="text-[13px]">← Scenarios</RouterLink>
    </template>
    <template #actions>
      <RouterLink v-if="data?.scenario.id" :to="{ path: `/scenarios/${data.scenario.id}`, query: { run: data.id } }" class="btn" data-testid="run-scenario-link">Open the scenario</RouterLink>
      <button v-if="data && isActive(data)" type="button" class="btn !border-block-border !bg-block-tint !text-block-text" :disabled="aborting || connection.offline" data-testid="abort-run" @click="abort">
        {{ aborting ? 'Aborting…' : 'Abort run' }}
      </button>
    </template>
  </PageHeader>

  <p v-if="run.isLoading.value" class="text-text-2">Loading the run…</p>
  <div v-else-if="notFound" class="card text-sm text-text-2" data-testid="run-not-found">
    There is no run {{ id }}. The gateway keeps the runs of 30 days. <RouterLink to="/scenarios">Back to the scenarios</RouterLink>
  </div>
  <LoadError v-else-if="run.error.value && !data" :error="run.error.value" what="The run is not loaded" :retry="run.refetch" />
  <div v-else-if="data" class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
    <div class="flex min-w-0 flex-col gap-4">
      <RunProgress :run="data" :names="names" />
      <ScenarioTimeline :steps="data.snapshot?.steps ?? []" :names="names" :run="data" />
    </div>
    <div class="flex min-w-0 flex-col gap-4">
      <ChecksCard :checks="data.snapshot?.checks ?? []" :names="names" :results="data.checks ?? []" />
      <ArtifactsCard :run="data" />
      <SectionCard v-if="counters.length" title="Counters of the run's overlays">
        <ul class="m-0 flex list-none flex-col gap-1.5 p-0 text-[13px]" data-testid="run-counters">
          <li v-for="c in counters" :key="c.overlay ?? c.step" class="flex justify-between gap-3" data-testid="run-counter">
            <span class="mono">{{ c.step }}</span>
            <span :class="c.counters?.packets ? '' : 'text-fault-text'">{{ formatCount(c.counters?.packets ?? 0) }} packets</span>
          </li>
        </ul>
      </SectionCard>
    </div>
  </div>
</template>
