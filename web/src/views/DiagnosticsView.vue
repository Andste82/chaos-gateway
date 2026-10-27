<script setup lang="ts">
// The Diagnostics screen (plan §2.12, §2.17 "Diagnostics"; M24): ping, a TCP or UDP check, a DNS lookup, an HTTP(S)
// request, the details of a TLS handshake, and the path that strings DNS, route, TCP and TLS together, each run from the
// gateway's own address — so the faults of a device do not apply to it — with a structured result. The form is filled in
// from the address (`?type=&host=&port=`: the link of a device's page); the runs of the session stay below the last result.
// Diagnostics from a probe arrive with the probes (M25).
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import DiagnosticForm from '../components/diagnostics/DiagnosticForm.vue';
import DiagnosticResultCard from '../components/diagnostics/DiagnosticResultCard.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { useCanDiagnose } from '../composables/gateway';
import { describeTarget, formFromQuery, formatMs, formToDiagnosticRequest, resultTone, type DiagnosticForm as Form } from '../lib/diagnostics';
import { formatClock } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDiagnosticsStore, type DiagnosticRun } from '../stores/diagnostics';
import type { DiagnosticRequest } from '../api/generated/model';

const route = useRoute();
const canDiagnose = useCanDiagnose();
const connection = useConnectionStore();
const store = useDiagnosticsStore();

const form = ref<Form>(formFromQuery(route.query));
const refusal = ref<{ path?: string; message?: string }[]>();
const shown = ref<number>();

/** The run shown: the one picked below, else the newest. */
const current = computed<DiagnosticRun | undefined>(() => store.runs.find((r) => r.id === shown.value) ?? store.runs[0]);

async function run(request: DiagnosticRequest) {
  refusal.value = undefined;
  const r = await store.run(request);
  shown.value = r.id;
  if (r.fieldErrors) refusal.value = r.fieldErrors;
}

// the request the form would send, until a run records the one that was sent
useApiScreen('the diagnostics', () => [
  { method: 'POST', path: '/diagnostics', body: formToDiagnosticRequest(form.value), note: 'runs one diagnostic from the gateway and answers when it is done (up to a minute)' },
]);
</script>

<template>
  <PageHeader title="Diagnostics" />
  <p class="-mt-3 mb-0 text-[13px] text-text-3">Ping, TCP, UDP, DNS, HTTP(S) and TLS from the gateway's own address · the faults of a device do not apply</p>

  <EmptyState v-if="canDiagnose === false" text="This gateway has no diagnostics." data-testid="diagnostics-unsupported" />
  <template v-else>
    <SectionCard title="Run a diagnostic">
      <DiagnosticForm v-model:form="form" :busy="store.running > 0" :disabled="connection.offline" :refusal="refusal" @run="run" />
    </SectionCard>

    <SectionCard title="Result">
      <template v-if="current">
        <p v-if="current.error" class="m-0 text-sm text-block-text" role="alert" data-testid="diagnostic-refused">{{ current.error }}</p>
        <DiagnosticResultCard v-else-if="current.result" :request="current.request" :result="current.result" :at="current.at" />
      </template>
      <p v-else-if="store.running > 0" class="m-0 text-sm text-text-2" role="status">Running…</p>
      <EmptyState v-else text="Nothing run yet: choose a diagnostic and press Run." />
      <p v-if="current && store.running > 0" class="m-0 text-sm text-text-2" role="status">Running another diagnostic…</p>
    </SectionCard>

    <SectionCard v-if="store.runs.length > 1" title="Earlier in this session">
      <template #actions><button type="button" class="btn btn-sm" @click="store.clear()">Clear</button></template>
      <ul class="m-0 flex list-none flex-col p-0" data-testid="diagnostic-history">
        <li v-for="r in store.runs" :key="r.id" class="border-t border-border first:border-t-0">
          <button
            type="button"
            class="flex w-full cursor-pointer flex-wrap items-center gap-x-4 gap-y-1 border-0 bg-transparent px-1 py-2 text-left text-sm text-text hover:bg-surface-2"
            :aria-pressed="r.id === current?.id"
            @click="shown = r.id"
          >
            <span class="mono w-[70px] text-text-3">{{ formatClock(r.at) }}</span>
            <StatusDot v-if="r.result" v-bind="resultTone(r.result)" />
            <StatusDot v-else tone="fault" label="Refused" />
            <span class="uppercase">{{ r.request.type }}</span>
            <span class="mono text-text-2">{{ describeTarget(r.request) }}</span>
            <span v-if="r.result" class="ml-auto text-xs text-text-3">{{ formatMs(r.result.duration_ms) }}</span>
          </button>
        </li>
      </ul>
    </SectionCard>
  </template>
</template>
