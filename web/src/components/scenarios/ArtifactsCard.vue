<script setup lang="ts">
// The artifacts of a run (plan §2.10, §2.11, §2.17 "Scenarios"): the capture of the target's traffic
// (pcapng, from a scenario with `capture: true` or its capture steps), the JUnit and JSON reports, the events
// of the run as JSON Lines, and the TLS key log of a capture the proxy was involved in. They appear as the run
// produces them: a capture while it runs, the reports when it has ended. Each is a download from the API.
import { computed } from 'vue';
import type { Run } from '../../api/generated/model';
import { formatBytes } from '../../lib/format';
import { artifactLabels, isActive, sortedArtifacts } from '../../lib/runs';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ run?: Run }>();
const list = computed(() => sortedArtifacts(props.run?.artifacts));
const hint = computed(() => {
  if (!props.run) return 'A run leaves a JUnit and a JSON report, its events and, when the scenario records them, a capture.';
  if (isActive(props.run)) return 'The reports appear when the run has ended.';
  return 'This run left no artifacts.';
});
</script>

<template>
  <SectionCard title="Run artifacts">
    <ul v-if="list.length" class="m-0 flex list-none flex-col gap-2 p-0 text-[13px]" data-testid="artifacts">
      <li v-for="a in list" :key="a.url" class="flex items-baseline justify-between gap-3" data-testid="artifact" :data-kind="a.kind">
        <span class="mono min-w-0 break-words">
          {{ artifactLabels[a.kind].title }}<span v-if="artifactLabels[a.kind].note" class="font-sans text-text-2"> · {{ artifactLabels[a.kind].note }}</span>
        </span>
        <a :href="a.url" download :aria-label="`Download ${artifactLabels[a.kind].title}`">{{ a.size_bytes !== undefined ? formatBytes(a.size_bytes) : 'Download' }}</a>
      </li>
    </ul>
    <p v-else class="m-0 text-[13px] leading-relaxed text-text-2" data-testid="artifacts-empty">{{ hint }}</p>
  </SectionCard>
</template>
