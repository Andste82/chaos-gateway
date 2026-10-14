<script setup lang="ts">
// A scope selector (plan §2.17: everything / network / group / device, and the remote networks of
// P3-M13-08): a select of targets (`faultForm.targetOf` texts) and, for "a prefix inside a remote
// network", a field for the prefix beside it. A current target that is not among the options (a
// device that is gone, a prefix the gateway knows no more) stays selectable under `currentLabel`.
import { computed } from 'vue';
import { CIDR_CHOICE, scopeChoice, type ScopeOption } from '../../lib/scopes';

const props = defineProps<{
  id: string;
  options: ScopeOption[];
  /** The label of the current target when no option has it. */
  currentLabel?: string;
  disabled?: boolean;
  invalid?: boolean;
  describedby?: string;
}>();
const model = defineModel<string>({ required: true });

const choice = computed({
  get: () => scopeChoice(model.value),
  set: (v: string) => {
    model.value = v === CIDR_CHOICE ? CIDR_CHOICE + (model.value.startsWith(CIDR_CHOICE) ? model.value.slice(CIDR_CHOICE.length) : '') : v;
  },
});
const cidr = computed({
  get: () => (model.value.startsWith(CIDR_CHOICE) ? model.value.slice(CIDR_CHOICE.length) : ''),
  set: (v: string) => (model.value = CIDR_CHOICE + v.trim()),
});
const all = computed<ScopeOption[]>(() =>
  props.options.some((o) => o.value === choice.value) ? props.options : [...props.options, { value: choice.value, label: props.currentLabel ?? choice.value }],
);
</script>

<template>
  <select
    :id="id"
    v-model="choice"
    class="input"
    :disabled="disabled"
    :aria-invalid="invalid"
    :aria-describedby="describedby"
  >
    <option v-for="o in all" :key="o.value" :value="o.value">{{ o.label }}</option>
  </select>
  <input
    v-if="choice === CIDR_CHOICE"
    v-model="cidr"
    class="input mono mt-1.5"
    aria-label="Prefix inside a remote network"
    placeholder="10.50.0.0/25"
    :disabled="disabled"
    :aria-invalid="invalid"
    :aria-describedby="describedby"
    data-testid="scope-cidr"
  />
</template>
