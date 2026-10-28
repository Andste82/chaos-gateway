<script setup lang="ts">
// The Diagnostics screen (plan §2.12, §2.17 "Diagnostics"; M24): ping, a TCP or UDP check, a DNS lookup, an HTTP(S)
// request, the details of a TLS handshake, and the path that strings DNS, route, TCP and TLS together, each run from the
// gateway's own address — so the faults of a device do not apply to it — with a structured result. The form is filled in
// from the address (`?type=&host=&port=`: the link of a device's page); the runs of the session stay below the last result.
// Since M25 a diagnostic runs from a probe as well (plan §2.12): a virtual client whose traffic takes the path of a device's and
// meets the faults, rules and profiles that target it. The probes are listed under the form, with the self-test "measure with
// a probe" (a profile is activated on the probe for the run and compared with what it measured). Since M26a a probe is added,
// edited and removed here as well, through the configuration draft and the preview-and-apply drawer, like a port forward.
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import DiagnosticForm from '../components/diagnostics/DiagnosticForm.vue';
import CalibrateDialog from '../components/diagnostics/CalibrateDialog.vue';
import DiagnosticResultCard from '../components/diagnostics/DiagnosticResultCard.vue';
import ProbeDialog from '../components/diagnostics/ProbeDialog.vue';
import ProbesCard from '../components/diagnostics/ProbesCard.vue';
import RemoveProbeDialog from '../components/diagnostics/RemoveProbeDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import { firstError, useActiveConfig, useCanDiagnose, useCanProbe, useNames, useProbes, useProfiles } from '../composables/gateway';
import { findProbe, measurableProfiles, probeRows } from '../lib/probes';
import type { ProbeView } from '../api/generated/model';
import { describeTarget, failureTone, formFromQuery, formatMs, formToDiagnosticRequest, resultTone, runAnnouncement, type DiagnosticForm as Form } from '../lib/diagnostics';
import { formatClock } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDiagnosticsStore, type DiagnosticRun } from '../stores/diagnostics';
import { useDraftStore } from '../stores/draft';
import type { DiagnosticRequest } from '../api/generated/model';

const route = useRoute();
const canDiagnose = useCanDiagnose();
const connection = useConnectionStore();
const store = useDiagnosticsStore();
const draft = useDraftStore();
// the base of the draft the probe dialogs edit
const active = useActiveConfig();

const canProbe = useCanProbe();
const probesQuery = useProbes(() => canProbe.value !== false);
const probes = computed(() => probesQuery.data.value ?? []);
const { networks } = useNames();
const profiles = useProfiles();
const networkNames = computed(() => Object.fromEntries((networks.data.value ?? []).map((n) => [n.id, (n.config as { name?: string }).name ?? n.id])) as Record<string, string>);
const measurable = computed(() => measurableProfiles(profiles.data.value ?? []));

const form = ref<Form>(formFromQuery(route.query));
// the probe the address names (by name or id) is chosen by its id, which the choice holds
watch(
  probes,
  (list) => {
    const p = form.value.from ? findProbe(list, form.value.from) : undefined;
    if (p && p.id !== form.value.from) form.value.from = p.id;
  },
  { immediate: true },
);
// the probes as the draft has them (M26a), each with what the gateway observes of the applied probe
const rows = computed(() => probeRows(draft.baseProbes, draft.probes, probes.value));
const probeOpen = ref(false);
const probeEditing = ref<string>();
const removing = ref<{ open: boolean; probe: string }>({ open: false, probe: '' });
function addProbe() {
  probeEditing.value = undefined;
  probeOpen.value = true;
}
function editProbe(id: string) {
  probeEditing.value = id;
  probeOpen.value = true;
}
const measuring = ref<{ open: boolean; probe?: string }>({ open: false });
const refusal = ref<{ path?: string; message?: string }[]>();
const shown = ref<number>();
// what a screen reader hears when a run ends: the result card appears where the "Running…" line was, which a live region
// that is inserted with its text does not announce, so this one stays (P6-AUDIT-49)
const announcement = ref('');

/** The run shown: the one picked below, else the newest. */
const current = computed<DiagnosticRun | undefined>(() => store.runs.find((r) => r.id === shown.value) ?? store.runs[0]);

async function run(request: DiagnosticRequest) {
  refusal.value = undefined;
  announcement.value = '';
  // the run keeps the probe's name as it was when the run started: the request holds the id
  const from = request.from?.probe ? (findProbe(probes.value, request.from.probe)?.config.name ?? request.from.probe) : undefined;
  const r = await store.run(request, from);
  shown.value = r.id;
  if (r.fieldErrors) refusal.value = r.fieldErrors;
  announcement.value = runAnnouncement(r);
}

// the request the form would send, until a run records the one that was sent
useApiScreen('the diagnostics', () => [
  { method: 'POST', path: '/diagnostics', body: formToDiagnosticRequest(form.value), note: 'runs one diagnostic from the gateway and answers when it is done (up to a minute)' },
]);
</script>

<template>
  <PageHeader title="Diagnostics" />
  <p class="-mt-3 mb-0 text-[13px] text-text-3">Ping, TCP, UDP, DNS, HTTP(S) and TLS from the gateway's own address (the faults of a device do not apply) or from a probe</p>

  <EmptyState v-if="canDiagnose === false" text="This gateway has no diagnostics." data-testid="diagnostics-unsupported" />
  <template v-else>
    <SectionCard title="Run a diagnostic">
      <DiagnosticForm v-model:form="form" :busy="store.running > 0" :disabled="connection.offline" :refusal="refusal" :probes="probes" @run="run" />
    </SectionCard>

    <SectionCard v-if="canProbe !== false" title="Probes">
      <template v-if="rows.length || probesQuery.error.value" #actions>
        <button type="button" class="btn btn-sm" :disabled="!draft.baseConfig" data-testid="probe-add" @click="addProbe">Add probe</button>
      </template>
      <!-- the list is read every 5 s: a failed read says so also when older rows are kept, and never reads as "no probes" -->
      <LoadError v-if="probesQuery.error.value" :error="probesQuery.error.value" what="The probes are not loaded" :retry="probesQuery.refetch" />
      <ProbesCard
        v-if="rows.length"
        :rows="rows"
        :networks="networkNames"
        :disabled="connection.offline"
        @diagnose="(p: ProbeView) => (form.from = p.id)"
        @measure="(p: ProbeView) => (measuring = { open: true, probe: p.id })"
        @edit="editProbe"
        @remove="(id: string) => (removing = { open: true, probe: id })"
      />
      <EmptyState
        v-else-if="!probesQuery.error.value"
        text="No probes: a probe is a virtual client of a test network. Add one to diagnose and measure from the network's side before a device is connected."
        data-testid="probes-empty"
      >
        <button type="button" class="btn btn-primary" :disabled="!draft.baseConfig" data-testid="probe-add" @click="addProbe">Add probe</button>
      </EmptyState>
    </SectionCard>
    <LoadError v-if="canProbe !== false && firstError(active)" :error="firstError(active)" what="The configuration is not loaded" :retry="active.refetch" />
    <ProbeDialog v-model:open="probeOpen" :probe="probeEditing" />
    <RemoveProbeDialog v-if="removing.probe" v-model:open="removing.open" :probe="removing.probe" />
    <CalibrateDialog v-model:open="measuring.open" :probes="probes" :profiles="measurable" :probe="measuring.probe" />

    <SectionCard title="Result">
      <p class="sr-only" role="status" data-testid="diagnostic-announcement">{{ announcement }}</p>
      <template v-if="current">
        <p v-if="current.error" class="m-0 text-sm text-block-text" role="alert" data-testid="diagnostic-refused">{{ current.error }}</p>
        <DiagnosticResultCard v-else-if="current.result" :request="current.request" :result="current.result" :at="current.at" :probe="current.from" />
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
            <StatusDot v-else v-bind="failureTone(r.failure)" />
            <span class="uppercase">{{ r.request.type }}</span>
            <span class="mono text-text-2">{{ describeTarget(r.request) }}</span>
            <span class="text-xs text-text-3" data-testid="diagnostic-history-from">from {{ r.from ?? 'the gateway' }}</span>
            <span v-if="r.result" class="ml-auto text-xs text-text-3">{{ formatMs(r.result.duration_ms) }}</span>
          </button>
        </li>
      </ul>
    </SectionCard>
  </template>
</template>
