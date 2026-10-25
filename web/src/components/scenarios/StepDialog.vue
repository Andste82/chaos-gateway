<script setup lang="ts">
// The step editor of the timeline (plan §2.10, §2.17 "Scenarios"): a step's id, its time on the timeline, whether
// it narrows the scenario's target (a device of the target group, never wider), and the fields of its type — a
// profile, a fault with the fault dialog's fields, a DNS fault, a TLS case and a DHCP action (M23b: the fields of
// the fault dialog and of the DHCP dialog), an access rule, a WireGuard action, a capture start or
// stop, a wait for the device to be online, a connection or a DNS query (it stops the timeline until the
// condition holds; the later steps move by the waiting time), the removal of the overlay of an earlier step,
// or the restore of all. "Add to the changes" puts the scenario with the step into the draft. A type the
// gateway does not run is not offered; such a step is edited in the YAML view.
import { computed, ref, useId, watch } from 'vue';
import type { Scenario, Scope, Step } from '../../api/generated/model';
import { useCapabilities, useProfiles } from '../../composables/gateway';
import { useScenarioApi } from '../../composables/draftApi';
import { useScenarioTargets, useTopology } from '../../composables/scenarioTargets';
import { durationMs } from '../../lib/format';
import { accessLabels, rejectLabels } from '../../lib/rules';
import { describeScope } from '../../lib/faults';
import { dhcpScopeOf } from '../../lib/dhcpForm';
import { scopeOf, tunnelOf } from '../../lib/faultForm';
import { createsOverlay, scopeWithin, tunnelOutsideTarget } from '../../lib/scenarioRules';
import { freeStepId, stepsBefore, stepTypeLabels, withStep, type StepType, stepTypes } from '../../lib/scenarios';
import { profileTitle } from '../../lib/profileNames';
import {
  editableTypes,
  emptyStepForm,
  formToStep,
  stepFieldOf,
  stepFormErrors,
  stepToForm,
  type StepField,
  type StepForm,
} from '../../lib/stepForm';
import { tunnelOptions } from '../../lib/scopes';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import { useNames } from '../../composables/gateway';
import DhcpFields from '../dhcp/DhcpFields.vue';
import DnsFields from '../faults/DnsFields.vue';
import TlsFields from '../faults/TlsFields.vue';
import TrafficFields from '../faults/TrafficFields.vue';
import ModalDialog from '../ui/ModalDialog.vue';
import ScopeSelect from '../ui/ScopeSelect.vue';
import DraftApi from './DraftApi.vue';
import FaultStepFields from './FaultStepFields.vue';

const props = defineProps<{
  /** The scenario as the draft has it. */
  scenario: { id: string; config: Scenario };
  /** The step to edit; a new one without. */
  step?: Step;
  /** The time a new step starts with. */
  at?: string;
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const uid = useId();
const targets = useScenarioTargets();
const topology = useTopology();
const profiles = useProfiles();
const capabilities = useCapabilities();
const { networks, devices } = useNames();

const form = ref<StepForm>(emptyStepForm('profile', 'step-1'));
const serverErrors = ref<Partial<Record<StepField, string>>>({});
const general = ref<string[]>([]);
const tried = ref(false);
let openedForm = '';
let refusedForm = '';
let origin: EditOrigin | undefined;
/** The id the edited step had when the dialog opened (a changed id takes the references to it along). */
let was: string | undefined;

const title = computed(() => (props.step ? 'Edit step' : 'Add step'));

/** The types this build runs and the editor makes, in the order of the editor. */
const types = computed<StepType[]>(() => {
  const known = capabilities.data.value?.step_types;
  return stepTypes.filter((t) => editableTypes.has(t) && (!known || known.includes(t)));
});

watch(open, (o) => {
  if (!o) return;
  const sc = props.scenario.config;
  was = props.step?.id;
  form.value = props.step ? stepToForm(props.step) : emptyStepForm(types.value[0] ?? 'profile', freeStepId(sc), props.at ?? lastAt(sc));
  openedForm = JSON.stringify(form.value);
  origin = draft.editOrigin();
  serverErrors.value = {};
  general.value = [];
  tried.value = false;
  refusedForm = '';
  // what the gateway refused in the last candidate, until the step is edited
  const refused = draft.serverErrors[props.scenario.id];
  const i = was === undefined ? -1 : sc.steps.findIndex((s) => s.id === was);
  if (refused && i >= 0) {
    for (const [rest, message] of Object.entries(refused)) {
      const m = /^steps\/(\d+)(?:\/(.+))?$/.exec(rest);
      if (!m || Number(m[1]) !== i) continue;
      const f = m[2] ? stepFieldOf(m[2], form.value.fault.direction === 'each') : undefined;
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

/** A new step starts at the time of the last one (the user moves it). */
function lastAt(sc: Scenario): string {
  let best = '0s';
  let latest = -1;
  for (const s of sc.steps) {
    const t = durationMs(s.at) ?? 0;
    if (t >= latest) {
      latest = t;
      best = s.at;
    }
  }
  return best;
}

const others = computed(() => props.scenario.config.steps.filter((s) => s.id !== was).map((s) => s.id));
const earlier = computed(() => stepsBefore(props.scenario.config, form.value.at.trim(), was));
const clientErrors = computed(() =>
  stepFormErrors(form.value, {
    others: others.value,
    earlier: earlier.value,
    steps: props.scenario.config.steps,
    target: props.scenario.config.target,
    topology: topology.value,
  }),
);
function touched(f: string): boolean {
  const was0 = JSON.parse(openedForm || '{}') as Record<string, unknown>;
  const differs = (k: string) => JSON.stringify((form.value as unknown as Record<string, unknown>)[k]) !== JSON.stringify(was0[k]);
  // the target of a DHCP action is the step's narrowing, but what is wrong with it is shown once the action or the type is chosen
  if (f === 'narrow') return differs('narrow') || (form.value.type === 'dhcp' && (differs('type') || differs('dhcp')));
  if (f === 'id' || f === 'at') return differs(f);
  if (f === 'profile') return differs('profile');
  return differs('fault') || differs('dns') || differs('tls') || differs('dhcp') || differs('traffic') || differs('action') || differs('cutExisting') || differs('who') || differs('timeout') || differs('name') || differs('remove') || differs('capture') || differs('type');
}
const shownClientErrors = computed(() =>
  tried.value ? clientErrors.value : (Object.fromEntries(Object.entries(clientErrors.value).filter(([f]) => touched(f))) as typeof clientErrors.value),
);
const errors = computed<Partial<Record<string, string>>>(() => ({ ...serverErrors.value, ...shownClientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const result = computed(() => withStep(props.scenario.config, formToStep(form.value), was));

useScenarioApi(open, () => title.value, () => props.scenario.id, () => result.value);

const narrowable = computed(() => form.value.type !== 'wireguard' && !(form.value.type === 'fault' && form.value.fault.family === 'tunnel') && !form.value.kept);
const narrowOptions = computed(() => [
  { value: '', label: `The scenario's target (${describeScope(props.scenario.config.target, targets.names.value)})` },
  // a step narrows the scenario's target, never widens it
  ...targets.options.value.filter((o) => scopeWithin(scopeOf(o.value), props.scenario.config.target, topology.value)),
]);
/** What the step acts on: its own narrowing, else the scenario's target (a DHCP action takes a device or a network). */
const actsOn = computed<Scope>(() => (form.value.narrow ? scopeOf(form.value.narrow) : props.scenario.config.target));
const dhcpScope = computed(() => dhcpScopeOf(actsOn.value));
/** The device a TLS step acts on, when it acts on one: whether it trusts the test CA decides what a certificate case shows. */
const trustedDevice = computed(() => {
  const d = actsOn.value.device ? devices.data.value?.find((x) => x.id === actsOn.value.device) : undefined;
  return d?.config ? { trust: d.config.trusts_test_ca === true, name: d.name } : undefined;
});
const narrowName = computed(() => (form.value.narrow ? describeScope(scopeOf(form.value.narrow), targets.names.value) : ''));

const profileOptions = computed(() => {
  const out = (profiles.data.value ?? []).filter((p) => p.available).map((p) => ({ value: p.id, label: profileTitle(p.config.name) }));
  // a custom profile the draft adds can be used in the same revision
  for (const [id, p] of Object.entries(draft.effectiveProfiles)) if (!out.some((o) => o.value === id)) out.push({ value: id, label: `${profileTitle(p.name)} (not applied yet)` });
  return out;
});
// the tunnels inside the scenario's target, and the earlier steps that made an overlay
const tunnels = computed(() => tunnelOptions(networks.data.value ?? []).filter((t) => !tunnelOutsideTarget(tunnelOf(t.value), props.scenario.config.target, topology.value)));
const removable = computed(() => props.scenario.config.steps.filter((s) => earlier.value.includes(s.id) && createsOverlay(s)));

function submit() {
  tried.value = true;
  if (!valid.value) return;
  if (props.step && JSON.stringify(form.value) === openedForm) {
    open.value = false;
    return;
  }
  const step = formToStep(form.value);
  draft.editFrom(origin, () => {
    const current = draft.effectiveScenarios[props.scenario.id] ?? props.scenario.config;
    draft.setScenario(props.scenario.id, withStep(current, step, was));
  });
  open.value = false;
}

const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
const typeHints: Record<string, string> = {
  profile: 'Activates a named profile on the target; it replaces the profile or fault of an earlier step with the same key.',
  fault: 'A fault with complete parameters: it replaces the fault of an earlier step on the same target and traffic, it is never merged with it.',
  dns: 'A DNS fault of the gateway\'s DNS proxy on the target; it replaces the DNS fault of an earlier step for the same names.',
  tls: 'A TLS case on the target: a certificate the device must reject, or a handshake that fails; it replaces the case of an earlier step with the same selection.',
  dhcp: 'A DHCP action for a device or a network; it replaces the action of an earlier step with the same action on the same target.',
  rule: 'An access rule that acts on the target until a later step removes it or the run ends.',
  wireguard: 'Takes a WireGuard client or link down, breaks its key or blocks its endpoint; the client or link must lie inside the target.',
  capture: 'Starts or stops a capture of the target, attached to the run.',
  wait: 'Stops the timeline until the condition holds; the later steps move by the time it waited. A timeout ends the run as an error.',
  remove: 'Removes the overlay an earlier step made.',
  restore: 'Removes all overlays of the run.',
};
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="680px">
    <form class="flex flex-col gap-4" novalidate data-testid="step-dialog" @submit.prevent="submit">
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fid('type')">Type</label>
          <select :id="fid('type')" v-model="form.type" class="input" :disabled="!!form.kept">
            <option v-for="t in types" :key="t" :value="t">{{ stepTypeLabels[t] }}</option>
            <option v-if="!types.includes(form.type)" :value="form.type">{{ stepTypeLabels[form.type] }}</option>
          </select>
        </div>
        <div>
          <label class="label" :for="fid('id')">Id</label>
          <input
            :id="fid('id')"
            v-model="form.id"
            class="input mono"
            :aria-invalid="!!errors.id"
            :aria-describedby="errors.id ? eid('id') : undefined"
          />
          <p v-if="errors.id" :id="eid('id')" class="field-error">{{ errors.id }}</p>
        </div>
        <div>
          <label class="label" :for="fid('at')">At</label>
          <input
            :id="fid('at')"
            v-model="form.at"
            class="input mono"
            placeholder="10s"
            :aria-invalid="!!errors.at"
            :aria-describedby="errors.at ? eid('at') : undefined"
          />
          <p v-if="errors.at" :id="eid('at')" class="field-error">{{ errors.at }}</p>
        </div>
      </div>
      <p class="m-0 text-[13px] text-text-2">{{ form.kept ? 'This step is edited in the YAML view.' : typeHints[form.type] }}</p>

      <div v-if="narrowable && !form.kept">
        <label class="label" :for="fid('narrow')">Acts on</label>
        <ScopeSelect
          :id="fid('narrow')"
          v-model="form.narrow"
          :options="narrowOptions"
          :current-label="narrowName"
          :invalid="!!errors.narrow"
          :describedby="errors.narrow ? eid('narrow') : undefined"
        />
        <p v-if="errors.narrow" :id="eid('narrow')" class="field-error">{{ errors.narrow }}</p>
        <p v-else class="m-0 mt-1 text-[13px] text-text-2">A step may narrow the scenario's target (a device of the target group), never widen it.</p>
      </div>

      <template v-if="!form.kept">
        <div v-if="form.type === 'profile'">
          <label class="label" :for="fid('profile')">Profile</label>
          <select
            :id="fid('profile')"
            v-model="form.profile"
            class="input"
            :aria-invalid="!!errors.profile"
            :aria-describedby="errors.profile ? eid('profile') : undefined"
          >
            <option value="" disabled>Choose a profile</option>
            <option v-for="p in profileOptions" :key="p.value" :value="p.value">{{ p.label }}</option>
            <option v-if="form.profile && !profileOptions.some((p) => p.value === form.profile)" :value="form.profile">{{ form.profile }}</option>
          </select>
          <p v-if="errors.profile" :id="eid('profile')" class="field-error">{{ errors.profile }}</p>
        </div>

        <FaultStepFields v-else-if="form.type === 'fault'" v-model="form.fault" :errors="errors" :tunnels="tunnels" />

        <DnsFields v-else-if="form.type === 'dns'" v-model="form.dns" :errors="errors" />

        <TlsFields v-else-if="form.type === 'tls'" v-model="form.tls" :errors="errors" :trust="trustedDevice?.trust" :device-name="trustedDevice?.name" />

        <DhcpFields v-else-if="form.type === 'dhcp'" v-model="form.dhcp" :errors="errors" :scope="dhcpScope" />

        <template v-else-if="form.type === 'rule'">
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="label" :for="fid('action')">Access</label>
              <select
                :id="fid('action')"
                v-model="form.action"
                class="input"
                :aria-invalid="!!errors.action"
                :aria-describedby="errors.action ? eid('action') : undefined"
              >
                <option v-for="(label, a) in accessLabels" :key="a" :value="a">{{ label }}</option>
              </select>
              <p v-if="errors.action" :id="eid('action')" class="field-error">{{ errors.action }}</p>
            </div>
            <div v-if="form.action === 'reject'" class="col-span-2">
              <label class="label" :for="fid('rejectWith')">Reject with</label>
              <select :id="fid('rejectWith')" v-model="form.rejectWith" class="input">
                <option value="">Port unreachable (default)</option>
                <option v-for="(label, r) in rejectLabels" :key="r" :value="r">{{ label }}</option>
              </select>
            </div>
          </div>
          <TrafficFields v-model="form.traffic" :errors="errors" />
          <label class="flex items-start gap-2 text-sm">
            <input v-model="form.cutExisting" type="checkbox" class="mt-1" :aria-invalid="!!errors.cutExisting" />
            <span>
              Also cut existing connections
              <span class="block text-[13px] text-text-2">Established TCP connections the rule decides get a reset; they can reconnect.</span>
            </span>
          </label>
          <p v-if="errors.cutExisting" class="field-error m-0">{{ errors.cutExisting }}</p>
        </template>

        <div v-else-if="form.type === 'wireguard'" class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" :for="fid('who')">Client or link</label>
            <select
              :id="fid('who')"
              v-model="form.who"
              class="input"
              :aria-invalid="!!errors.who"
              :aria-describedby="errors.who ? eid('who') : undefined"
            >
              <option value="" disabled>Choose a WireGuard client or link</option>
              <option v-for="t in tunnels" :key="t.value" :value="t.value">{{ t.label }}</option>
              <option v-if="form.who && !tunnels.some((t) => t.value === form.who)" :value="form.who">{{ form.who }}</option>
            </select>
            <p v-if="errors.who" :id="eid('who')" class="field-error">{{ errors.who }}</p>
          </div>
          <div>
            <label class="label" :for="fid('wgAction')">Action</label>
            <select :id="fid('wgAction')" v-model="form.wgAction" class="input">
              <option value="disable">Disable · peer offline, link down</option>
              <option value="key_mismatch">Key mismatch · a rotated key not deployed</option>
              <option value="block_endpoint">Block the endpoint · drop its encrypted UDP</option>
            </select>
          </div>
        </div>

        <div v-else-if="form.type === 'capture'">
          <span class="label">Capture</span>
          <div class="flex gap-4 text-sm">
            <label class="flex items-center gap-2"><input v-model="form.capture" type="radio" value="start" />Start</label>
            <label class="flex items-center gap-2"><input v-model="form.capture" type="radio" value="stop" />Stop</label>
          </div>
          <p v-if="errors.capture" class="field-error">{{ errors.capture }}</p>
        </div>

        <template v-else-if="form.type === 'wait'">
          <div class="grid grid-cols-3 gap-3">
            <div class="col-span-2">
              <label class="label" :for="fid('waitFor')">Wait until</label>
              <select :id="fid('waitFor')" v-model="form.waitFor" class="input">
                <option value="online">The target is online</option>
                <option value="connected">A new connection is established</option>
                <option value="dns_query">A DNS query for a name is seen</option>
              </select>
            </div>
            <div>
              <label class="label" :for="fid('timeout')">Timeout</label>
              <input
                :id="fid('timeout')"
                v-model="form.timeout"
                class="input mono"
                placeholder="60s"
                :aria-invalid="!!errors.timeout"
                :aria-describedby="errors.timeout ? eid('timeout') : undefined"
              />
              <p v-if="errors.timeout" :id="eid('timeout')" class="field-error">{{ errors.timeout }}</p>
            </div>
          </div>
          <TrafficFields v-if="form.waitFor === 'connected'" v-model="form.traffic" :errors="errors" />
          <div v-if="form.waitFor === 'dns_query'">
            <label class="label" :for="fid('name')">Host name</label>
            <input
              :id="fid('name')"
              v-model="form.name"
              class="input mono"
              placeholder="broker.example.com"
              :aria-invalid="!!errors.name"
              :aria-describedby="errors.name ? eid('name') : undefined"
            />
            <p v-if="errors.name" :id="eid('name')" class="field-error">{{ errors.name }}</p>
          </div>
        </template>

        <div v-else-if="form.type === 'remove'">
          <label class="label" :for="fid('remove')">Remove the overlay of step</label>
          <select
            :id="fid('remove')"
            v-model="form.remove"
            class="input"
            :aria-invalid="!!errors.remove"
            :aria-describedby="errors.remove ? eid('remove') : undefined"
          >
            <option value="" disabled>Choose a step</option>
            <option v-for="s in removable" :key="s.id" :value="s.id">{{ s.id }} · at {{ s.at }}</option>
            <option v-if="form.remove && !removable.some((s) => s.id === form.remove)" :value="form.remove">{{ form.remove }}</option>
          </select>
          <p v-if="errors.remove" :id="eid('remove')" class="field-error">{{ errors.remove }}</p>
        </div>
      </template>

      <DraftApi />

      <p v-for="g in general" :key="g" class="field-error m-0" role="alert">{{ g }}</p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="tried && !valid">{{ step ? 'Save to the changes' : 'Add to the changes' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
