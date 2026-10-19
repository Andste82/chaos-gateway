<script setup lang="ts">
// Import a scenario from YAML (plan §2.10: YAML is the import/export format): paste the text of a file, as
// `chaosctl run -f` takes it, or choose the file. References may be names or UUIDs. The scenario goes into the
// draft as a new one, like one made with the forms; the preview and the apply take it to the gateway.
import { computed, ref, useId, watch } from 'vue';
import { useTopology } from '../../composables/scenarioTargets';
import { newId } from '../../lib/ids';
import { parseScenarioYaml, type RefIndex } from '../../lib/scenarioYaml';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ index: RefIndex; names: string[] }>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ saved: [id: string] }>();

const draft = useDraftStore();
const topology = useTopology();
const uid = useId();
const text = ref('');
const tried = ref(false);

watch(open, (o) => {
  if (o) {
    text.value = '';
    tried.value = false;
  }
});

const result = computed(() => parseScenarioYaml(text.value, props.index, props.names, topology.value));

async function pick(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (file) text.value = await file.text();
}

function submit() {
  tried.value = true;
  const sc = result.value.scenario;
  if (!sc) return;
  const id = newId();
  draft.setScenario(id, sc);
  open.value = false;
  emit('saved', id);
}
</script>

<template>
  <ModalDialog v-model:open="open" title="Import a scenario" description="Paste the YAML of a scenario, or choose a file." width="680px">
    <form class="flex flex-col gap-4" novalidate data-testid="import-dialog" @submit.prevent="submit">
      <div>
        <label class="label" :for="`${uid}-file`">File</label>
        <input :id="`${uid}-file`" type="file" accept=".yaml,.yml,text/yaml" class="text-sm" @change="pick" />
      </div>
      <div>
        <label class="label" :for="`${uid}-text`">YAML</label>
        <textarea
          :id="`${uid}-text`"
          v-model="text"
          class="input mono !h-auto min-h-[240px] py-2 text-[13px]"
          rows="14"
          spellcheck="false"
          placeholder="name: mqtt-outage&#10;target: { device: esp32-42 }&#10;steps:&#10;  - { id: slow, at: 0s, fault: { latency: 200ms } }"
          :aria-invalid="tried && result.problems.length > 0"
          :aria-describedby="`${uid}-problems`"
        />
      </div>
      <div :id="`${uid}-problems`" aria-live="polite">
        <ul v-if="text.trim() && result.problems.length" class="m-0 flex list-none flex-col gap-1 p-0 text-[13px] text-block-text" data-testid="import-problems">
          <li v-for="(p, i) in result.problems" :key="i">
            <span v-if="p.line" class="mono text-xs">line {{ p.line }}</span
            ><span v-else-if="p.path" class="mono text-xs">{{ p.path }}</span> {{ p.message }}
          </li>
        </ul>
        <p v-else-if="tried && !text.trim()" class="field-error m-0">Paste a scenario or choose a file.</p>
      </div>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="!!text.trim() && !result.scenario">Add to the changes</button>
      </div>
    </form>
  </ModalDialog>
</template>
