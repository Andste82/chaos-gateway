<script setup lang="ts">
// The check editor (plan §2.10, §2.17 "Scenarios"): a check observes the scenario's target inside a window that
// opens when a named step has been applied (or at the start of the run) and closes after a time or when
// another step has been applied. Reconnected, no connection, traffic seen, traffic not seen (with the
// destinations that are allowed), and a DNS query seen; the TLS checks arrive with the TLS responder (M21).
// "Add to the changes" puts the scenario with the check into the draft.
import { computed, ref, useId, watch } from 'vue';
import type { Scenario } from '../../api/generated/model';
import { useCapabilities, useNames } from '../../composables/gateway';
import { useScenarioApi } from '../../composables/draftApi';
import {
  checkFieldOf,
  checkFormErrors,
  checkToForm,
  emptyCheckForm,
  formToCheck,
  observesTraffic,
  type CheckField,
  type CheckForm,
} from '../../lib/checkForm';
import { checkTypeHints, checkTypeLabels, checkTypes, timelineOrder, withCheck, type CheckType } from '../../lib/scenarios';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import TrafficFields from '../faults/TrafficFields.vue';
import ModalDialog from '../ui/ModalDialog.vue';
import DraftApi from './DraftApi.vue';

const props = defineProps<{
  scenario: { id: string; config: Scenario };
  /** The position of the check to edit; a new one without. */
  index?: number;
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const uid = useId();
const capabilities = useCapabilities();
const { networks, names } = useNames();

const form = ref<CheckForm>(emptyCheckForm());
const serverErrors = ref<Partial<Record<CheckField, string>>>({});
const general = ref<string[]>([]);
const tried = ref(false);
let openedForm = '';
let refusedForm = '';
let origin: EditOrigin | undefined;

const title = computed(() => (props.index !== undefined ? 'Edit check' : 'Add check'));
const editing = computed(() => (props.index !== undefined ? props.scenario.config.checks?.[props.index] : undefined));

/** The check types this build evaluates (`GET /capabilities`); the one being edited stays selectable. */
const types = computed<CheckType[]>(() => {
  const known = capabilities.data.value?.check_types;
  return checkTypes.filter((t) => (!known || known.includes(t)) && (t !== 'tls_rejected' && t !== 'tls_accepted' ? true : !!known?.includes(t)));
});

watch(open, (o) => {
  if (!o) return;
  const sc = props.scenario.config;
  form.value = editing.value ? checkToForm(editing.value) : emptyCheckForm(types.value[0] ?? 'reconnected');
  if (!editing.value) form.value.until = timelineOrder(sc.steps).at(-1)?.id ?? '';
  openedForm = JSON.stringify(form.value);
  origin = draft.editOrigin();
  serverErrors.value = {};
  general.value = [];
  tried.value = false;
  refusedForm = '';
  const refused = draft.serverErrors[props.scenario.id];
  if (refused && props.index !== undefined) {
    for (const [rest, message] of Object.entries(refused)) {
      const m = /^checks\/(\d+)(?:\/(.+))?$/.exec(rest);
      if (!m || Number(m[1]) !== props.index) continue;
      const f = m[2] ? checkFieldOf(m[2]) : undefined;
      if (f) serverErrors.value[f] = message;
      else general.value.push(message);
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

const stepIds = computed(() => timelineOrder(props.scenario.config.steps).map((s) => s.id));
const clientErrors = computed(() => checkFormErrors(form.value, { steps: stepIds.value }));
const shownClientErrors = computed(() => (tried.value ? clientErrors.value : {}));
const errors = computed<Partial<Record<string, string>>>(() => ({ ...serverErrors.value, ...shownClientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const result = computed(() => withCheck(props.scenario.config, formToCheck(form.value), props.index));

useScenarioApi(open, () => title.value, () => props.scenario.id, () => result.value);

function addExcept() {
  form.value.except.push({ destKind: 'uplink', destination: '' });
}
function exceptKindChanged(i: number) {
  const r = form.value.except[i]!;
  r.destination = r.destKind === 'network' ? (networks.data.value?.[0]?.id ?? '') : '';
}

function submit() {
  tried.value = true;
  if (!valid.value) return;
  if (editing.value && JSON.stringify(form.value) === openedForm) {
    open.value = false;
    return;
  }
  const check = formToCheck(form.value);
  draft.editFrom(origin, () => {
    const current = draft.effectiveScenarios[props.scenario.id] ?? props.scenario.config;
    draft.setScenario(props.scenario.id, withCheck(current, check, props.index));
  });
  open.value = false;
}

const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="680px">
    <form class="flex flex-col gap-4" novalidate data-testid="check-dialog" @submit.prevent="submit">
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fid('type')">Check</label>
          <select :id="fid('type')" v-model="form.type" class="input">
            <option v-for="t in types" :key="t" :value="t">{{ checkTypeLabels[t] }}</option>
            <option v-if="!types.includes(form.type)" :value="form.type">{{ checkTypeLabels[form.type] }}</option>
          </select>
        </div>
        <div class="col-span-2">
          <label class="label" :for="fid('name')">Name in the report</label>
          <input
            :id="fid('name')"
            v-model="form.name"
            class="input"
            placeholder="optional"
            :aria-invalid="!!errors.name"
            :aria-describedby="errors.name ? eid('name') : undefined"
          />
          <p v-if="errors.name" :id="eid('name')" class="field-error">{{ errors.name }}</p>
        </div>
      </div>
      <p class="m-0 text-[13px] text-text-2">{{ checkTypeHints[form.type] }}</p>

      <div v-if="form.type === 'dns_query_seen'">
        <label class="label" :for="fid('dnsName')">Host name</label>
        <input
          :id="fid('dnsName')"
          v-model="form.dnsName"
          class="input mono"
          placeholder="broker.example.com"
          :aria-invalid="!!errors.dnsName"
          :aria-describedby="errors.dnsName ? eid('dnsName') : undefined"
        />
        <p v-if="errors.dnsName" :id="eid('dnsName')" class="field-error">{{ errors.dnsName }}</p>
      </div>
      <TrafficFields v-else-if="observesTraffic(form.type)" v-model="form.traffic" :errors="errors" />

      <fieldset v-if="form.type === 'traffic_not_seen'" class="m-0 flex flex-col gap-2 rounded-md border border-border p-3">
        <legend class="px-1 text-[13px]">Allowed destinations</legend>
        <div v-for="(r, i) in form.except" :key="i" class="grid grid-cols-[180px_minmax(0,1fr)_auto] items-start gap-2" data-testid="except-row">
          <select v-model="r.destKind" class="input" :aria-label="`Allowed destination ${i + 1}`" @change="exceptKindChanged(i)">
            <option value="uplink">Uplink (Internet)</option>
            <option value="network">A network</option>
            <option value="cidr">An address or prefix</option>
            <option value="hostname">A host name (DNS-derived)</option>
          </select>
          <template v-if="r.destKind === 'uplink'"><span class="self-center text-[13px] text-text-2">everything behind the uplink</span></template>
          <select v-else-if="r.destKind === 'network'" v-model="r.destination" class="input" :aria-label="`Network of allowed destination ${i + 1}`">
            <option v-for="n in networks.data.value ?? []" :key="n.id" :value="n.id">{{ names.network?.(n.id) ?? n.id }}</option>
          </select>
          <input
            v-else
            v-model="r.destination"
            class="input mono"
            :aria-label="r.destKind === 'cidr' ? `Address of allowed destination ${i + 1}` : `Host name of allowed destination ${i + 1}`"
            :placeholder="r.destKind === 'cidr' ? '203.0.113.0/24' : 'broker.example.com'"
          />
          <button type="button" class="btn btn-sm" :aria-label="`Remove allowed destination ${i + 1}`" @click="form.except.splice(i, 1)">Remove</button>
        </div>
        <p v-if="errors.except" class="field-error m-0">{{ errors.except }}</p>
        <div><button type="button" class="btn btn-sm" @click="addExcept">Allow a destination</button></div>
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-3 rounded-md border border-border p-3">
        <legend class="px-1 text-[13px]">Window</legend>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="label" :for="fid('from')">Opens when</label>
            <select
              :id="fid('from')"
              v-model="form.from"
              class="input"
              :aria-invalid="!!errors.from"
              :aria-describedby="errors.from ? eid('from') : undefined"
            >
              <option value="start">the run starts</option>
              <option v-for="s in scenario.config.steps" :key="s.id" :value="s.id">step {{ s.id }} is applied</option>
              <option v-if="form.from !== 'start' && !scenario.config.steps.some((s) => s.id === form.from)" :value="form.from">step {{ form.from }}</option>
            </select>
            <p v-if="errors.from" :id="eid('from')" class="field-error">{{ errors.from }}</p>
          </div>
          <div>
            <label class="label" :for="fid('windowKind')">Closes</label>
            <select :id="fid('windowKind')" v-model="form.windowKind" class="input">
              <option value="within">after a time</option>
              <option value="until">when a step is applied</option>
            </select>
          </div>
          <div v-if="form.windowKind === 'within'">
            <label class="label" :for="fid('within')">Within</label>
            <input
              :id="fid('within')"
              v-model="form.within"
              class="input mono"
              placeholder="30s"
              :aria-invalid="!!errors.within"
              :aria-describedby="errors.within ? eid('within') : undefined"
            />
            <p v-if="errors.within" :id="eid('within')" class="field-error">{{ errors.within }}</p>
          </div>
          <div v-else>
            <label class="label" :for="fid('until')">Step</label>
            <select
              :id="fid('until')"
              v-model="form.until"
              class="input"
              :aria-invalid="!!errors.until"
              :aria-describedby="errors.until ? eid('until') : undefined"
            >
              <option value="" disabled>Choose a step</option>
              <option v-for="s in scenario.config.steps" :key="s.id" :value="s.id">{{ s.id }}</option>
              <option v-if="form.until && !scenario.config.steps.some((s) => s.id === form.until)" :value="form.until">{{ form.until }}</option>
            </select>
            <p v-if="errors.until" :id="eid('until')" class="field-error">{{ errors.until }}</p>
          </div>
        </div>
        <p v-if="errors.window" class="field-error m-0">{{ errors.window }}</p>
      </fieldset>

      <DraftApi />

      <p v-for="g in general" :key="g" class="field-error m-0" role="alert">{{ g }}</p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="tried && !valid">{{ editing ? 'Save to the changes' : 'Add to the changes' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
