<script setup lang="ts">
// The running scenario on the overview (plan §2.17 "Overview": "running scenario with progress"): each run that
// is queued or running with its scenario, its target, the step in effect and a progress bar; every run links to
// its page (the scenario's, with the run shown, or the page of the run for an inline scenario).
import { computed } from 'vue';
import { useActiveRuns, useNames, useScenarios } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import RunningScenarioRow from './RunningScenarioRow.vue';

// the runs that are queued or running, asked for by status: not the newest runs, among which they may not be
const runs = useActiveRuns();
const { names } = useNames();
const now = useNow();
const activeRuns = computed(() => runs.data.value ?? []);
// a run links to its scenario's page only while the scenario exists: the run of a deleted scenario, or of an inline one, to its own page
const scenarios = useScenarios();
const stored = computed(() => new Set((scenarios.data.value ?? []).map((s) => s.id)));
</script>

<template>
  <SectionCard title="Running scenarios">
    <template #actions><RouterLink to="/scenarios">Scenarios</RouterLink></template>
    <p v-if="runs.isLoading.value" class="m-0 text-sm text-text-2">Loading…</p>
    <LoadError v-else-if="runs.error.value && !runs.data.value" :error="runs.error.value" what="The runs are not loaded" :retry="runs.refetch" />
    <p v-else-if="activeRuns.length === 0" class="m-0 text-sm text-text-2" data-testid="no-running-scenario">No scenario is running.</p>
    <ul v-else class="m-0 flex list-none flex-col gap-3 p-0" data-testid="running-scenarios">
      <li v-for="r in activeRuns" :key="r.id">
        <RunningScenarioRow
          :id="r.id"
          :name="r.scenario.name"
          :to="r.scenario.id && stored.has(r.scenario.id) ? { path: `/scenarios/${r.scenario.id}`, query: { run: r.id } } : `/runs/${r.id}`"
          :names="names"
          :now="now"
        />
      </li>
    </ul>
  </SectionCard>
</template>
