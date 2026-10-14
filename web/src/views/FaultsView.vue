<script setup lang="ts">
// Faults (plan §2.17 "Faults", D25): every fault grouped by family and sorted by scope (device →
// group → network → everything), overlays and configured faults marked; per fault its selector,
// parameters, affected packets, TTL and "overridden by X" where another fault wins. Faults are
// added and edited in the fault dialog: an overlay applies at once, a configured fault goes into the
// draft (unapplied-changes bar, preview and apply): new and changed ones are marked, a deleted one
// stays struck through with an undo until the draft is applied or discarded, and one the gateway
// refused in a candidate is marked with its problems. "Explain" names the winner per family for a
// device and a destination.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { errorMessage } from '../api/fetcher';
import { deleteOverlay } from '../api/generated/hooks/overlays/overlays';
import type { ConfigFault, FaultRef, FaultView, Overlay, Scope } from '../api/generated/model';
import ExplainCard from '../components/faults/ExplainCard.vue';
import FaultDialog from '../components/faults/FaultDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import FaultChip from '../components/ui/FaultChip.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import { firstError, useActiveConfig, useConfigFaults, useGatewayState, useNames, useOverlays } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { describeMatch, describeScope, describeTunnel, faultChips, overlayChips } from '../lib/faults';
import { formatCount, formatRemaining } from '../lib/format';
import { profileTitle } from '../lib/profiles';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDraftStore } from '../stores/draft';
import { useToastStore } from '../stores/toasts';

const active = useActiveConfig();
const overlays = useOverlays();
const configFaults = useConfigFaults();
const draft = useDraftStore();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const { names } = useNames();
const now = useNow();

useApiScreen('the faults', () => [
  { method: 'GET', path: '/overlays', note: 'the overlays (faults, profiles, …) with state, counters and what overrides them' },
  { method: 'GET', path: '/faults', note: 'the configured faults with state, counters and what overrides them' },
]);

interface Row {
  id: string;
  layer: 'overlay' | 'config';
  family: string;
  level: number;
  target: string;
  /** The name of a configured fault. */
  name?: string;
  device?: string;
  destination: string;
  chips: string[];
  packets?: number;
  expiresAt?: string;
  state?: string;
  overriddenBy: string[];
  isNew?: boolean;
  changed?: boolean;
  /** A configured fault the draft deletes: shown until the draft is applied or discarded. */
  deleted?: boolean;
  /** What the gateway refused about the fault in the last candidate. */
  problems?: string[];
  overlay?: Overlay;
  config?: { id: string; fault: ConfigFault };
}

const familyTitles: Record<string, string> = {
  impairment: 'Impairment',
  mtu: 'MTU',
  tunnel: 'Tunnel',
  profile: 'Profiles',
  dns: 'DNS',
  tls: 'TLS',
  dhcp: 'DHCP',
  wireguard: 'WireGuard actions',
};
const familyOrder = Object.keys(familyTitles);

/** device → group → network → remote network → everything → tunnel (plan §2.4 levels). */
function levelOf(s: Scope | undefined): number {
  if (!s) return 5;
  if (s.device) return 0;
  if (s.group) return 1;
  if (s.network) return 2;
  if (s.remote_network) return 3;
  return 4;
}

const overlayList = computed(() => overlays.data.value ?? []);
const viewById = computed(() => new Map<string, FaultView>((configFaults.data.value ?? []).map((f) => [f.id, f])));

function refText(r: FaultRef): string {
  const n = names.value;
  if (r.layer === 'overlay') {
    const o = overlayList.value.find((x) => x.id === r.id);
    if (o?.kind === 'profile') return `profile ${profileTitle(n.profile(o.profile ?? '') ?? o.profile ?? '')} on ${describeScope(o.target, n)} (overlay)`;
    if (o) return `${describeScope(o.target, n)} ${overlayChips(o, n.profile).join(', ')} (overlay)`;
    return r.profile?.name ? `profile ${profileTitle(r.profile.name)} (overlay)` : `an overlay${r.summary ? ` (${r.summary})` : ''}`;
  }
  const f = draft.baseConfig?.faults?.[r.id] ?? viewById.value.get(r.id)?.config;
  return `fault ${r.name ?? f?.name ?? r.id}${f ? ` on ${describeScope(f.source, n)}` : ''} (configuration)`;
}

const rows = computed<Row[]>(() => {
  const n = names.value;
  const out: Row[] = [];
  for (const o of overlayList.value) {
    if (o.kind === 'rule') continue;
    const family = o.kind === 'fault' ? (o.fault?.family ?? 'impairment') : o.kind;
    out.push({
      id: o.id,
      layer: 'overlay',
      family,
      level: o.target ? levelOf(o.target) : 5,
      target: o.target ? describeScope(o.target, n) : o.fault?.tunnel ? describeTunnel(o.fault.tunnel, n) : 'Everything',
      device: o.target?.device,
      destination: describeMatch(o.fault ?? o.rule, n),
      chips: o.kind === 'profile' ? [`profile ${profileTitle(n.profile(o.profile ?? '') ?? o.profile ?? '')}`] : overlayChips(o, n.profile),
      packets: o.counters?.packets,
      expiresAt: o.expires_at,
      state: o.state,
      overriddenBy: (o.overridden_by ?? []).map(refText),
      overlay: o,
    });
  }
  const changed = new Set(draft.changes(n).filter((c) => c.kind === 'fault').map((c) => c.id));
  for (const [id, f] of Object.entries(draft.effectiveFaults)) {
    const v = viewById.value.get(id);
    out.push({
      id,
      layer: 'config',
      family: f.family ?? 'impairment',
      level: f.source ? levelOf(f.source) : 5,
      target: f.source ? describeScope(f.source, n) : describeTunnel(f.tunnel, n),
      name: f.name,
      device: f.source?.device,
      destination: describeMatch(f, n),
      chips: faultChips(f),
      packets: v?.counters?.packets,
      state: f.enabled === false ? 'disabled' : changed.has(id) ? undefined : v?.state,
      overriddenBy: changed.has(id) ? [] : (v?.overridden_by ?? []).map(refText),
      isNew: !(id in (draft.baseConfig?.faults ?? {})),
      changed: changed.has(id),
      problems: draft.serverErrors[id] ? Object.values(draft.serverErrors[id]) : undefined,
      config: { id, fault: f },
    });
  }
  for (const [id, f] of Object.entries(draft.faults)) {
    const before = draft.baseConfig?.faults?.[id];
    if (f !== null || !before) continue;
    out.push({
      id,
      layer: 'config',
      family: before.family ?? 'impairment',
      level: before.source ? levelOf(before.source) : 5,
      target: before.source ? describeScope(before.source, n) : describeTunnel(before.tunnel, n),
      name: before.name,
      device: before.source?.device,
      destination: describeMatch(before, n),
      chips: faultChips(before),
      overriddenBy: [],
      deleted: true,
    });
  }
  return out.sort((a, b) => a.level - b.level || (a.layer === b.layer ? a.target.localeCompare(b.target) : a.layer === 'overlay' ? -1 : 1));
});

const groups = computed(() =>
  familyOrder.map((family) => ({ family, title: familyTitles[family]!, rows: rows.value.filter((r) => r.family === family) })).filter((g) => g.rows.length > 0),
);

const stateLabels: Record<string, string> = {
  effective: 'effective',
  partially_overridden: 'partially overridden',
  overridden: 'overridden',
  disabled: 'disabled',
};

// the dialog: add, edit an overlay, edit a configured fault
const dialogOpen = ref(false);
const editOverlay = ref<Overlay>();
const editConfig = ref<{ id: string; fault: ConfigFault }>();
function add() {
  editOverlay.value = undefined;
  editConfig.value = undefined;
  dialogOpen.value = true;
}
function edit(r: Row) {
  editOverlay.value = r.overlay;
  editConfig.value = r.config;
  dialogOpen.value = true;
}
/** Every fault the dialog makes is edited in it (impairment, MTU, tunnel; P3-M13-04); a profile activation is not a fault. */
const editable = (r: Row) => r.overlay?.kind === 'fault' || !!r.config;

const apiPanel = useApiPanelStore();
const removing = ref<string>();
async function remove(r: Row) {
  if (r.config) {
    draft.setFault(r.config.id, null);
    return;
  }
  removing.value = r.id;
  try {
    await deleteOverlay(r.id);
    apiPanel.record('Remove fault', [{ method: 'DELETE', path: `/overlays/${r.id}` }]);
    toasts.push('success', 'Fault removed', `${r.chips.join(', ')} on ${r.target}`);
    await Promise.all(['overlays', 'devices', 'faults', 'profiles'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    toasts.push('error', 'The fault could not be removed', errorMessage(e));
  } finally {
    removing.value = undefined;
  }
}

const loadError = computed(() => firstError(overlays, configFaults, active));
const retry = () => Promise.all([overlays.refetch(), configFaults.refetch(), active.refetch()]);
const removeBlocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);
const context = computed(() => `The most specific fault wins · an overlay beats the configuration · revision ${draft.base ?? state.data.value?.active_revision ?? '…'}`);
</script>

<template>
  <PageHeader title="Faults" :context="context">
    <template #actions>
      <button type="button" class="btn btn-primary" @click="add">Add fault</button>
    </template>
  </PageHeader>

  <ExplainCard />

  <p v-if="overlays.isLoading.value || configFaults.isLoading.value || active.isLoading.value" class="text-text-2">Loading the faults…</p>
  <LoadError
    v-else-if="loadError"
    :error="loadError"
    what="The faults are not loaded"
    :retry="retry"
  />
  <EmptyState v-else-if="groups.length === 0" text="No faults: all traffic passes unimpaired.">
    <button type="button" class="btn btn-primary" @click="add">Add fault</button>
  </EmptyState>
  <template v-else>
  <SectionCard v-for="g in groups" :key="g.family" :title="g.title" :data-testid="`fault-family-${g.family}`">
    <div role="table" :aria-label="`${g.title} faults`" class="text-sm">
      <div role="row" class="data-head grid grid-cols-[minmax(180px,1.2fr)_minmax(0,2fr)_100px_80px_150px] gap-3 px-2.5 pb-2">
        <span role="columnheader">Selector</span>
        <span role="columnheader">Parameters</span>
        <span role="columnheader">Affected</span>
        <span role="columnheader">Expires</span>
        <span role="columnheader"><span class="sr-only">Actions</span></span>
      </div>
      <div
        v-for="r in g.rows"
        :key="`${r.layer}:${r.id}`"
        role="row"
        class="data-row grid grid-cols-[minmax(180px,1.2fr)_minmax(0,2fr)_100px_80px_150px] items-center gap-3 px-2.5 py-2"
        :class="{ 'shadow-[inset_3px_0_0_var(--color-block-border)]': r.problems }"
        data-testid="fault-row"
      >
        <span role="cell" class="min-w-0">
          <span :class="{ 'text-text-3 line-through': r.deleted }">
            <span v-if="r.deleted">{{ r.target }}</span>
            <RouterLink v-else-if="r.device" :to="`/devices/${r.device}`" class="text-text no-underline hover:underline">{{ r.target }}</RouterLink>
            <span v-else>{{ r.target }}</span>
            <span v-if="r.destination" class="ml-1 text-text-3">{{ r.destination }}</span>
          </span>
          <span class="mt-0.5 flex flex-wrap gap-1.5 text-xs">
            <span
              class="rounded border px-1.5"
              :class="r.layer === 'overlay' ? 'border-fault-border text-fault-text' : 'border-border-strong text-text-2'"
              data-testid="fault-layer"
              >{{ r.layer === 'overlay' ? 'OVERLAY' : 'CONFIGURATION' }}</span
            >
            <span v-if="r.name" class="mono text-text-2">{{ r.name }}</span>
            <span v-if="r.deleted" class="rounded border border-accent-strong px-1.5 text-[#cfe6ff]">DELETED</span>
            <span v-else-if="r.isNew" class="rounded border border-accent-strong px-1.5 text-[#cfe6ff]">NEW</span>
            <span v-else-if="r.changed" class="rounded border border-accent-strong px-1.5 text-[#cfe6ff]">CHANGED</span>
            <span v-if="r.problems" class="rounded border border-block-border px-1.5 text-block-text">NEEDS FIXING</span>
          </span>
        </span>
        <span role="cell" class="flex min-w-0 flex-col gap-1">
          <span class="flex flex-wrap items-center gap-1.5" :class="{ 'opacity-60': r.deleted }">
            <FaultChip v-for="c in r.chips" :key="c" :text="c" />
            <span v-if="r.deleted" class="text-xs text-text-3">removed when applied</span>
            <span v-else-if="r.state" class="text-xs" :class="r.state === 'effective' ? 'text-text-3' : 'text-fault-text'" data-testid="fault-state">{{
              stateLabels[r.state] ?? r.state
            }}</span>
            <span v-else-if="r.changed || r.isNew" class="text-xs text-text-3">not applied yet</span>
          </span>
          <span v-if="r.overriddenBy.length" class="text-xs text-fault-text" data-testid="overridden-by">overridden by {{ r.overriddenBy.join('; ') }}</span>
          <span v-for="p in r.problems" :key="p" class="text-xs text-block-text" data-testid="fault-problem">{{ p }}</span>
        </span>
        <span role="cell" class="mono text-[13px] text-text-2">{{ r.packets !== undefined ? `${formatCount(r.packets)} pkts` : '—' }}</span>
        <span role="cell" class="mono text-[13px]" :class="r.expiresAt ? 'text-fault-text' : 'text-text-2'" data-testid="fault-expiry">{{
          r.expiresAt ? formatRemaining(new Date(r.expiresAt).getTime() - now.getTime()) : r.layer === 'config' ? 'configured' : 'no TTL'
        }}</span>
        <span role="cell" class="flex justify-end gap-1.5">
          <button
            v-if="r.deleted"
            type="button"
            class="btn btn-sm"
            :aria-label="`Undo the removal of ${r.chips.join(', ')} on ${r.target}`"
            @click="draft.restoreFault(r.id)"
          >
            Undo
          </button>
          <button v-if="editable(r)" type="button" class="btn btn-sm" :aria-label="`Edit ${r.chips.join(', ')} on ${r.target}`" @click="edit(r)">Edit</button>
          <button
            v-if="!r.deleted"
            type="button"
            class="btn btn-sm"
            :aria-label="`Remove ${r.chips.join(', ')} on ${r.target}`"
            :disabled="r.layer === 'overlay' && (removeBlocked || removing === r.id)"
            @click="remove(r)"
          >
            Remove
          </button>
        </span>
      </div>
    </div>
  </SectionCard>
  </template>

  <FaultDialog v-model:open="dialogOpen" :overlay="editOverlay" :config="editConfig" allow-config />
</template>
