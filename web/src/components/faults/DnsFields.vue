<script setup lang="ts">
// The fields of a DNS fault (plan §2.6, §2.17 "Add fault", M23b): the action the DNS proxy takes, the names it applies to
// (none: every name) and what the action needs, the delay, the addresses or the TTL. The "Add fault" dialog and the `dns`
// step of the step dialog share them. Each message stands under its field (`errors` by field name).
import { computed, useId } from 'vue';
import { dnsActions, type DnsForm } from '../../lib/dnsForm';

defineProps<{ errors: Partial<Record<string, string>> }>();
const form = defineModel<DnsForm>({ required: true });
const uid = useId();
const hint = computed(() => dnsActions.find((a) => a.value === form.value.action)?.hint);
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="dns-fields">
    <div>
      <label class="label" :for="fid('action')">Action</label>
      <select
        :id="fid('action')"
        v-model="form.action"
        class="input"
        :aria-invalid="!!errors.dnsAction"
        :aria-describedby="errors.dnsAction ? eid('action') : fid('hint')"
      >
        <option v-for="a in dnsActions" :key="a.value" :value="a.value">{{ a.label }}</option>
      </select>
      <p v-if="errors.dnsAction" :id="eid('action')" class="field-error">{{ errors.dnsAction }}</p>
      <p v-else :id="fid('hint')" class="m-0 mt-1 text-[13px] text-text-2" data-testid="dns-hint">{{ hint }}</p>
    </div>

    <div>
      <label class="label" :for="fid('names')">Host names</label>
      <input
        :id="fid('names')"
        v-model="form.names"
        class="input mono"
        placeholder="every name"
        :aria-invalid="!!errors.dnsNames"
        :aria-describedby="errors.dnsNames ? eid('names') : fid('names-hint')"
      />
      <p v-if="errors.dnsNames" :id="eid('names')" class="field-error">{{ errors.dnsNames }}</p>
      <p v-else :id="fid('names-hint')" class="m-0 mt-1 text-[13px] text-text-2">
        Exact names or <span class="mono">*.suffix</span>, separated by commas; empty: every name the target asks for.
      </p>
    </div>

    <div v-if="form.action === 'delay'" class="max-w-[200px]">
      <label class="label" :for="fid('delay')">Delay</label>
      <input
        :id="fid('delay')"
        v-model="form.delay"
        class="input mono"
        placeholder="2s"
        :aria-invalid="!!errors.dnsDelay"
        :aria-describedby="errors.dnsDelay ? eid('delay') : undefined"
      />
      <p v-if="errors.dnsDelay" :id="eid('delay')" class="field-error">{{ errors.dnsDelay }}</p>
    </div>

    <div v-if="form.action === 'wrong_answer'">
      <label class="label" :for="fid('answers')">Answer with</label>
      <input
        :id="fid('answers')"
        v-model="form.answers"
        class="input mono"
        placeholder="10.9.0.1"
        :aria-invalid="!!errors.dnsAnswers"
        :aria-describedby="errors.dnsAnswers ? eid('answers') : undefined"
      />
      <p v-if="errors.dnsAnswers" :id="eid('answers')" class="field-error">{{ errors.dnsAnswers }}</p>
      <p v-else class="m-0 mt-1 text-[13px] text-text-2">IPv4 addresses, separated by commas; the name resolves to these.</p>
    </div>

    <div v-if="form.action === 'short_ttl'" class="max-w-[200px]">
      <label class="label" :for="fid('ttl')">TTL of the answers</label>
      <input
        :id="fid('ttl')"
        v-model="form.ttl"
        class="input mono"
        placeholder="5s"
        :aria-invalid="!!errors.dnsTtl"
        :aria-describedby="errors.dnsTtl ? eid('ttl') : undefined"
      />
      <p v-if="errors.dnsTtl" :id="eid('ttl')" class="field-error">{{ errors.dnsTtl }}</p>
    </div>
  </div>
</template>
