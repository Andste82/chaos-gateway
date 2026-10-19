<script setup lang="ts">
// The settings of a scenario (plan §2.10, §2.17 "Scenarios"): name, description, the target (a device, a group
// or a network; one run per target at a time), whether the run records the target's traffic, how a fault that
// matched no packet is reported, and the preconditions to skip. A scenario is an object of the configuration:
// "Add to the changes" puts it into the draft, and the unapplied-changes bar, the preview and the apply take it
// to the gateway, like a profile. A new scenario has no steps yet; they are added on the timeline.
import { computed, ref, useId, watch } from 'vue';
import type { PreconditionId, Scenario } from '../../api/generated/model';
import { useScenarioTargets } from '../../composables/scenarioTargets';
import { useScenarioApi } from '../../composables/draftApi';
import { describeScope } from '../../lib/faults';
import { scopeOf } from '../../lib/faultForm';
import { newId } from '../../lib/ids';
import {
  emptyScenarioForm,
  formToScenario,
  preconditionLabels,
  scenarioFieldOf,
  scenarioFormErrors,
  scenarioToForm,
  type ScenarioField,
  type ScenarioForm,
} from '../../lib/scenarioForm';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import ScopeSelect from '../ui/ScopeSelect.vue';
import DraftApi from './DraftApi.vue';

const props = defineProps<{
  /** The scenario to edit; a new one without. */
  scenario?: { id: string; config: Scenario };
  /** The names of all scenarios, for the check that a name is unique. */
  names: string[];
}>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ saved: [id: string] }>();

const draft = useDraftStore();
const targets = useScenarioTargets();
const uid = useId();

const form = ref<ScenarioForm>(emptyScenarioForm());
const serverErrors = ref<Partial<Record<ScenarioField, string>>>({});
const general = ref<string[]>([]);
let refusedForm = '';
let openedForm = '';
let origin: EditOrigin | undefined;
const tried = ref(false);
/** The id of a new scenario, chosen when the dialog opens so the API panel can show it. */
const newScenarioId = ref(newId());

const title = computed(() => (props.scenario ? 'Scenario settings' : 'New scenario'));
const id = computed(() => props.scenario?.id ?? newScenarioId.value);

watch(open, (o) => {
  if (!o) return;
  form.value = props.scenario ? scenarioToForm(props.scenario.config) : emptyScenarioForm(targets.options.value[0]?.value ?? '');
  openedForm = JSON.stringify(form.value);
  origin = props.scenario ? draft.editOrigin() : undefined;
  newScenarioId.value = newId();
  serverErrors.value = {};
  general.value = [];
  tried.value = false;
  refusedForm = '';
  // what the gateway refused in the last candidate, until the scenario is edited
  const refused = props.scenario ? draft.serverErrors[props.scenario.id] : undefined;
  if (refused) {
    for (const [rest, message] of Object.entries(refused)) {
      const f = scenarioFieldOf(rest);
      if (f) serverErrors.value[f] = message;
      // the problems of the steps and checks are shown on the timeline
    }
    refusedForm = JSON.stringify(form.value);
  }
});
watch(
  form,
  (f) => {
    if (refusedForm && JSON.stringify(f) !== refusedForm) {
      serverErrors.value = {};
      general.value = [];
      refusedForm = '';
    }
  },
  { deep: true },
);

const others = computed(() => props.names.filter((n) => n.toLowerCase() !== props.scenario?.config.name.toLowerCase()));
const clientErrors = computed(() => scenarioFormErrors(form.value, others.value));
function touched(f: ScenarioField): boolean {
  const was = JSON.parse(openedForm || '{}') as Partial<ScenarioForm>;
  const key = f === 'skip' ? 'skip' : f;
  return JSON.stringify(form.value[key]) !== JSON.stringify(was[key]);
}
const shownClientErrors = computed(() =>
  tried.value ? clientErrors.value : (Object.fromEntries(Object.entries(clientErrors.value).filter(([f]) => touched(f as ScenarioField))) as typeof clientErrors.value),
);
const errors = computed<Partial<Record<ScenarioField, string>>>(() => ({ ...serverErrors.value, ...shownClientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const result = computed(() => formToScenario(form.value, props.scenario?.config));
const targetName = computed(() => (form.value.target ? describeScope(scopeOf(form.value.target), targets.names.value) : ''));

useScenarioApi(open, () => title.value, () => id.value, () => result.value);

function toggleSkip(p: PreconditionId, on: boolean) {
  form.value.skip = on ? [...form.value.skip, p] : form.value.skip.filter((x) => x !== p);
}

function submit() {
  tried.value = true;
  if (!valid.value) return;
  if (props.scenario && JSON.stringify(form.value) === openedForm) {
    open.value = false;
    return;
  }
  const value = result.value;
  draft.editFrom(origin, () => draft.setScenario(id.value, value));
  open.value = false;
  emit('saved', id.value);
}

const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="640px">
    <form class="flex flex-col gap-4" novalidate data-testid="scenario-dialog" @submit.prevent="submit">
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fid('name')">Name</label>
          <input
            :id="fid('name')"
            v-model="form.name"
            class="input mono"
            placeholder="e.g. mqtt-outage"
            :aria-invalid="!!errors.name"
            :aria-describedby="errors.name ? eid('name') : undefined"
          />
          <p v-if="errors.name" :id="eid('name')" class="field-error">{{ errors.name }}</p>
        </div>
        <div class="col-span-2">
          <label class="label" :for="fid('description')">Description</label>
          <input
            :id="fid('description')"
            v-model="form.description"
            class="input"
            placeholder="optional"
            :aria-invalid="!!errors.description"
            :aria-describedby="errors.description ? eid('description') : undefined"
          />
          <p v-if="errors.description" :id="eid('description')" class="field-error">{{ errors.description }}</p>
        </div>
      </div>

      <div>
        <label class="label" :for="fid('target')">Target</label>
        <ScopeSelect
          :id="fid('target')"
          v-model="form.target"
          :options="targets.options.value"
          :current-label="targetName"
          :invalid="!!errors.target"
          :describedby="errors.target ? eid('target') : undefined"
        />
        <p v-if="errors.target" :id="eid('target')" class="field-error">{{ errors.target }}</p>
        <p v-else class="m-0 mt-1 text-[13px] text-text-2">
          The device, group or network the steps act on. Only one run at a time can have a target; a second one waits in the queue.
        </p>
      </div>

      <fieldset class="m-0 flex flex-col gap-3 rounded-md border border-border p-3">
        <legend class="px-1 text-[13px]">Run</legend>
        <label class="flex items-start gap-2 text-sm">
          <input v-model="form.capture" type="checkbox" class="mt-1" />
          <span>
            Capture the target's traffic for the whole run
            <span class="block text-[13px] text-text-2">The capture is attached to the run as an artifact (pcapng).</span>
          </span>
        </label>
        <div>
          <label class="label" :for="fid('zero')">A fault, rule or profile that matched no packet</label>
          <select :id="fid('zero')" v-model="form.zeroHit" class="input">
            <option value="warning">is a warning in the report</option>
            <option value="error">fails the run</option>
          </select>
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="label m-0">Preconditions to skip</span>
          <label v-for="(p, key) in preconditionLabels" :key="key" class="flex items-start gap-2 text-sm">
            <input type="checkbox" class="mt-1" :checked="form.skip.includes(key)" @change="toggleSkip(key, ($event.target as HTMLInputElement).checked)" />
            <span>
              {{ p.label }}
              <span class="block text-[13px] text-text-2">{{ p.hint }}</span>
            </span>
          </label>
        </div>
      </fieldset>

      <DraftApi />

      <p v-for="g in general" :key="g" class="field-error m-0" role="alert">{{ g }}</p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="tried && !valid">{{ scenario ? 'Save to the changes' : 'Add to the changes' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
