<script setup lang="ts">
// The YAML view of a scenario (plan §2.10, §2.17 "Scenarios", tab YAML): the scenario as the file that
// `chaosctl run -f` takes and that can be kept in git, with the names of the objects it refers to. The text can
// be edited; it is checked while it is typed (the syntax, the fields the format has, the schema, the steps and
// checks), and "Apply to the changes" puts it into the draft like the other editors do. That is also the way to
// edit what the forms leave out (the DNS, TLS and DHCP steps of later milestones). "Download" saves the text.
import { computed, ref, watch } from 'vue';
import type { Scenario } from '../../api/generated/model';
import { useTopology } from '../../composables/scenarioTargets';
import { jsonEqual } from '../../lib/mergePatch';
import { parseScenarioYaml, scenarioToYaml, type RefIndex } from '../../lib/scenarioYaml';
import { useDraftStore } from '../../stores/draft';

const props = defineProps<{
  scenario: { id: string; config: Scenario };
  index: RefIndex;
  /** The names of the other scenarios. */
  names: string[];
}>();
const draft = useDraftStore();
const topology = useTopology();

/** The text the scenario has as YAML now. */
const current = computed(() => scenarioToYaml(props.scenario.config, props.index));
const text = ref(current.value);
/** The text was edited since it was rendered from the scenario. */
const edited = ref(false);
// the scenario changes under the view (a step added on the timeline, a revision applied): the text follows unless it is being edited
watch(current, (c) => {
  if (!edited.value) text.value = c;
});
watch(
  () => props.scenario.id,
  () => {
    edited.value = false;
    text.value = current.value;
  },
);

const result = computed(() => parseScenarioYaml(text.value, props.index, props.names, topology.value));
const unchanged = computed(() => !!result.value.scenario && jsonEqual(result.value.scenario, props.scenario.config));

function onInput() {
  edited.value = true;
}
function reset() {
  edited.value = false;
  text.value = current.value;
}
function apply() {
  const sc = result.value.scenario;
  if (!sc) return;
  draft.setScenario(props.scenario.id, sc);
  edited.value = false;
  text.value = scenarioToYaml(sc, props.index);
}
function download() {
  const url = URL.createObjectURL(new Blob([text.value], { type: 'application/yaml' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `${props.scenario.config.name}.yaml`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}
</script>

<template>
  <section class="card flex flex-col gap-3" aria-label="YAML" data-testid="yaml-view">
    <p class="m-0 text-[13px] text-text-2">
      The scenario as a file for <span class="mono">chaosctl run -f</span> or for git. References are names here; the configuration stores their UUIDs.
    </p>
    <textarea
      v-model="text"
      class="input mono !h-auto min-h-[320px] py-2 text-[13px] leading-relaxed"
      rows="22"
      spellcheck="false"
      aria-label="Scenario YAML"
      :aria-invalid="result.problems.length > 0"
      aria-describedby="yaml-problems"
      data-testid="yaml-text"
      @input="onInput"
    />
    <div id="yaml-problems" aria-live="polite" data-testid="yaml-status">
      <ul v-if="result.problems.length" class="m-0 flex list-none flex-col gap-1 p-0 text-[13px] text-block-text" data-testid="yaml-problems">
        <li v-for="(p, i) in result.problems" :key="i" data-testid="yaml-problem">
          <span v-if="p.line" class="mono text-xs">line {{ p.line }}</span
          ><span v-else-if="p.path" class="mono text-xs">{{ p.path }}</span> {{ p.message }}
        </li>
      </ul>
      <p v-else-if="unchanged" class="m-0 text-[13px] text-text-2">The text is the scenario as it is.</p>
      <p v-else class="m-0 text-[13px] text-text-2" data-testid="yaml-valid">A valid scenario. Apply it to put it into the changes.</p>
    </div>
    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-primary" :disabled="!result.scenario || unchanged" data-testid="yaml-apply" @click="apply">Apply to the changes</button>
      <button type="button" class="btn" :disabled="!edited" @click="reset">Reset</button>
      <button type="button" class="btn" @click="download">Download</button>
    </div>
  </section>
</template>
