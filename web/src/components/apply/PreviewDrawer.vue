<script setup lang="ts">
// The preview-and-apply drawer (plan §2.17 "Preview & apply", "Applying / applied", "Apply failed"):
// the draft becomes a candidate revision (a merge patch against its base, If-Match the base), the
// gateway's dry run says what changes in domain terms (rule, field, old → new) and, expandable, in
// Linux terms (nftables, tc, routes, … and the verify step); nothing changes until "Apply revision
// N". Back to editing discards the candidate. A base that is not active any more opens the
// concurrent-change dialog; a refused candidate (422) lists the problems and marks the objects; a
// failed apply shows the failing step and the revision the gateway restored. The port forwards after
// the change are listed with the note that faults do not apply to forwarded connections (M35a, M35b).
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { applyRevision, createRevision, discardRevision, previewRevision } from '../../api/generated/hooks/revisions/revisions';
import { getState } from '../../api/generated/hooks/system/system';
import type { ApplyResult, Preview } from '../../api/generated/model';
import { useGatewayState, useNames } from '../../composables/gateway';
import { entryLabels, forwardTitle } from '../../lib/forwards';
import { useApiPanelStore, type ApiCall } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useDraftStore } from '../../stores/draft';
import { useToastStore } from '../../stores/toasts';
import SideDrawer from '../ui/SideDrawer.vue';

type Phase = 'creating' | 'ready' | 'invalid' | 'applying' | 'failed' | 'error';

const draft = useDraftStore();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const { names } = useNames();

const phase = ref<Phase>('creating');
const candidate = ref<number>();
const preview = ref<Preview>();
const problems = ref<{ where: string; message: string }[]>([]);
const error = ref('');
const failure = ref<{ step: string; detail: string; restored?: number }>();
const linux = ref(false);
const force = ref(false);
let calls: ApiCall[] = [];

const open = computed({
  get: () => draft.drawerOpen,
  // while the apply runs, the drawer stays open: its result (an apply that failed, the step and the
  // revision restored) has nowhere else to show (plan §2.17 "Apply failed")
  set: (v) => {
    if (!v && phase.value !== 'applying') void back();
  },
});

watch(
  () => draft.drawerOpen,
  (o) => {
    if (o) void prepare();
  },
);

function objectName(kind: string, id: string): string {
  if (kind === 'devices') return `Device ${draft.devices[id]?.name ?? draft.baseConfig?.devices?.[id]?.name ?? id}`;
  if (kind === 'profiles') return `Profile ${draft.profiles[id]?.name ?? draft.baseConfig?.profiles?.[id]?.name ?? id}`;
  const r = kind === 'access_rules' ? (draft.effectiveRules[id] ?? draft.baseConfig?.access_rules?.[id]) : (draft.effectiveFaults[id] ?? draft.baseConfig?.faults?.[id]);
  return `${kind === 'access_rules' ? 'Rule' : 'Fault'} ${r?.name ?? id}`;
}

function where(path: string | undefined): string {
  const c = /^\/networks\/([^/]+)\/clients\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (c) {
    const name = draft.clients[c[2]!]?.client?.name ?? draft.clientName(c[2]!) ?? c[2];
    return `WireGuard client ${name}${c[3] ? ` · ${c[3].replace(/\//g, ' ')}` : ''}`;
  }
  if (path?.startsWith('/access_matrix')) return 'Access between networks';
  const rest = (r: string | undefined) => (r ? ` · ${r.replace(/\//g, ' ')}` : '');
  const n = /^\/networks\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (n) return `Network ${networkName(n[1]!)}${rest(n[2])}`;
  const p = /^\/routing\/protocols\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (p) {
    const name = draft.effectiveRouting.protocols?.[p[1]!]?.name ?? draft.baseRouting.protocols?.[p[1]!]?.name ?? p[1];
    return `Routing protocol ${name}${rest(p[2])}`;
  }
  const f = /^\/port_forwards\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (f) {
    const p = draft.effectiveForwards[f[1]!] ?? draft.baseForwards[f[1]!];
    return `Port forward ${p ? forwardTitle(p) : f[1]}${rest(f[2])}`;
  }
  if (path === '/routing' || path?.startsWith('/routing/')) return `Routing${rest(path.slice('/routing/'.length))}`;
  const o = /^\/(groups|probes|scenarios)\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (o) {
    const kind = { groups: 'Group', probes: 'Probe', scenarios: 'Scenario' }[o[1] as 'groups'];
    const name = (draft.baseConfig?.[o[1] as 'groups']?.[o[2]!] as { name?: string } | undefined)?.name ?? o[2];
    return `${kind} ${name}${rest(o[3])}`;
  }
  const m = /^\/(access_rules|faults|profiles|devices)\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (!m) return path || 'configuration';
  return `${objectName(m[1]!, m[2]!)}${rest(m[3])}`;
}

function networkName(id: string): string {
  return draft.effectiveNetworks[id]?.name ?? draft.baseConfig?.networks?.[id]?.name ?? id;
}

/**
 * The gateway's message in the user's words: a reference to a network this change deletes names the
 * network and says so (P3-M14-01: deleting a network something still refers to — a device, a rule, a
 * fault, the access matrix, a routing protocol — shows what refers to it).
 */
function messageOf(e: { code?: string; message?: string }): string {
  const msg = e.message ?? '';
  if (e.code !== 'unknown_reference') return msg;
  for (const [id, n] of Object.entries(draft.networks)) {
    const name = draft.baseConfig?.networks?.[id]?.name;
    // the reference by UUID, or by name where the configuration has one
    if (n !== null || !(msg.includes(id) || (name && msg.includes(`"${name}"`)))) continue;
    return `refers to ${name ?? id}, which this change deletes; remove the reference first or keep the network`;
  }
  return msg;
}

/**
 * Counts the opens and closes of the drawer: a candidate that arrives for an earlier one (the
 * drawer was closed or opened again while it was being created) is discarded, not shown.
 */
let generation = 0;
const stale = (gen: number) => gen !== generation || !draft.drawerOpen;

async function prepare() {
  const gen = ++generation;
  phase.value = 'creating';
  candidate.value = undefined;
  preview.value = undefined;
  problems.value = [];
  error.value = '';
  failure.value = undefined;
  force.value = false;
  linux.value = false;
  const base = draft.base;
  const patch = draft.patch();
  const message = draft.summary(names.value).slice(0, 200);
  const headers = { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${base}"` };
  calls = [{ method: 'POST', path: `/revisions?message=${encodeURIComponent(message)}`, headers, body: patch, note: 'the candidate: a merge patch against the base revision' }];
  try {
    const rev = await createRevision(patch, { message }, { headers });
    if (stale(gen)) {
      await discardRevision(rev.id).catch(() => undefined);
      return;
    }
    candidate.value = rev.id;
    calls.push({ method: 'POST', path: `/revisions/${rev.id}/preview`, note: 'the dry run' });
    const p = await previewRevision(rev.id);
    if (stale(gen)) return; // closed during the dry run: back() discarded the candidate
    preview.value = p;
    phase.value = 'ready';
  } catch (e) {
    if (stale(gen)) return;
    // a refused candidate or dry run is an action too: the panel shows the calls that were sent
    apiPanel.record(`Preview & apply (refused: ${errorMessage(e)})`, calls);
    if (e instanceof ApiError && e.code === 'revision_conflict' && base !== undefined) {
      if (candidate.value !== undefined) await discardRevision(candidate.value).catch(() => undefined);
      candidate.value = undefined;
      draft.drawerOpen = false;
      draft.conflict = { base };
      return;
    }
    if (e instanceof ApiError && e.status === 422) {
      const errs = e.problem?.errors ?? [];
      draft.setServerErrors(errs);
      problems.value = errs.length ? errs.map((x) => ({ where: where(x.path), message: messageOf(x) })) : [{ where: 'configuration', message: e.message }];
      phase.value = 'invalid';
      return;
    }
    error.value = errorMessage(e);
    phase.value = 'error';
  }
}

async function back() {
  if (phase.value === 'applying') return;
  // a candidate still being created is discarded when it arrives (prepare)
  generation++;
  const id = candidate.value;
  candidate.value = undefined;
  draft.drawerOpen = false;
  if (id !== undefined) await discardRevision(id).catch(() => undefined);
}

function appliedTitle(res: ApplyResult): string {
  if (res.status === 'pending_confirm') return `Revision ${res.revision} applied, waiting for confirmation`;
  return `Revision ${res.revision} applied and verified${res.duration_ms !== undefined ? ` in ${res.duration_ms} ms` : ''}`;
}

async function apply() {
  const id = candidate.value;
  if (id === undefined) return;
  phase.value = 'applying';
  error.value = '';
  const params = force.value ? { force: true } : undefined;
  const applyCall: ApiCall = { method: 'POST', path: `/revisions/${id}/apply${force.value ? '?force=true' : ''}`, note: 'apply: validate → apply → verify' };
  try {
    const res = await applyRevision(id, params);
    apiPanel.record(`Preview & apply revision ${res.revision}`, [...calls, applyCall]);
    toasts.push(
      'success',
      appliedTitle(res),
      res.status === 'pending_confirm' ? 'Confirm it in the banner before the time runs out, or the gateway rolls back.' : draft.summary(names.value) + '.',
    );
    candidate.value = undefined;
    draft.discard();
    // edits made while it waits for confirmation move onto it once it is confirmed
    if (res.status === 'pending_confirm') draft.notePending(res.revision);
    draft.drawerOpen = false;
    await Promise.all(['state', 'revisions', 'rules', 'faults', 'overlays', 'devices', 'profiles', 'networks', 'routing', 'uplink', 'port-forwards'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    apiPanel.record(`Preview & apply revision ${id} (refused: ${errorMessage(e)})`, [...calls, applyCall]);
    if (e instanceof ApiError && e.code === 'revision_conflict' && draft.base !== undefined) {
      await discardRevision(id).catch(() => undefined);
      candidate.value = undefined;
      draft.drawerOpen = false;
      draft.conflict = { base: draft.base };
      return;
    }
    if (e instanceof ApiError && (e.code === 'apply_failed' || e.code === 'verify_failed')) {
      let restored: number | undefined;
      try {
        restored = (await getState()).active_revision;
      } catch {
        // the state comes with the next refetch
      }
      failure.value = { step: e.code === 'verify_failed' ? 'verify' : 'apply', detail: e.message, restored };
      phase.value = 'failed';
      await queryClient.invalidateQueries({ queryKey: ['state'] });
      return;
    }
    error.value =
      e instanceof ApiError && e.code === 'confirm_pending'
        ? 'Another revision waits for confirmation: confirm it or let it roll back first.'
        : errorMessage(e);
    phase.value = 'ready';
  }
}

const changes = computed(() => preview.value?.domain ?? []);
const linuxParts = computed(() => {
  const l = preview.value?.linux ?? {};
  return (
    [
      ['nftables', l.nftables],
      ['tc', l.tc],
      ['routes', l.routes],
      ['WireGuard', l.wireguard],
      ['BIRD', l.bird],
      ['services', l.services],
    ] as const
  ).filter(([, text]) => !!text);
});
const references = computed(() => preview.value?.references ?? []);
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

function value(v: unknown): string {
  if (v === undefined || v === null) return '—';
  return typeof v === 'string' ? v : JSON.stringify(v);
}
</script>

<template>
  <SideDrawer
    v-model:open="open"
    :title="candidate !== undefined ? `Preview revision ${candidate}` : 'Preview'"
    badge="dry run · nothing changed yet"
    :description="draft.base !== undefined ? `Based on revision ${draft.base}.` : undefined"
    :busy="phase === 'applying'"
  >
    <div class="flex flex-col gap-4" data-testid="preview-drawer">
      <p v-if="phase === 'creating'" class="m-0 text-sm text-text-2" role="status">Asking the gateway for a dry run…</p>

      <div v-if="phase === 'invalid'" class="rounded-md border border-block-border bg-block-tint p-3 text-sm text-block-text" role="alert" data-testid="preview-invalid">
        <strong class="font-semibold">The gateway refused the change.</strong> Fix it and preview again:
        <ul class="mt-2 mb-0 pl-5">
          <li v-for="(p, i) in problems" :key="i">
            <span class="font-medium">{{ p.where }}</span>: {{ p.message }}
          </li>
        </ul>
      </div>

      <p v-if="phase === 'error' || error" class="field-error m-0" role="alert">{{ error }}</p>

      <template v-if="preview">
        <div class="text-xs font-semibold tracking-[0.06em] text-text-2">WHAT CHANGES</div>
        <ul class="m-0 flex list-none flex-col gap-2 p-0" data-testid="preview-changes">
          <li v-for="(c, i) in changes" :key="i" class="flex gap-2.5 rounded-md border border-[#2a323d] bg-surface-2 px-3 py-2.5 text-sm">
            <span class="mono text-accent-text" aria-hidden="true">{{ c.op === 'added' ? '+' : c.op === 'removed' ? '−' : '~' }}</span>
            <span class="min-w-0">
              <span class="font-medium">{{ c.summary }}</span>
              <span v-for="(f, j) in c.fields ?? []" :key="j" class="mt-1 block text-[13px] text-text-2">
                {{ f.path }}: <span class="mono">{{ value(f.old) }}</span> → <span class="mono text-text">{{ value(f.new) }}</span>
              </span>
            </span>
          </li>
          <li v-if="changes.length === 0" class="text-sm text-text-2">Nothing changes in the configuration's objects.</li>
        </ul>

        <ul v-if="preview.warnings?.length" class="m-0 flex list-none flex-col gap-1.5 p-0" data-testid="preview-warnings">
          <li v-for="(w, i) in preview.warnings" :key="i" class="rounded-md border border-fault-border bg-fault-tint px-3 py-2 text-[13px] text-fault-text">
            {{ w.message }}
          </li>
        </ul>

        <div v-if="preview.port_forwards?.length" class="flex flex-col gap-1.5" data-testid="preview-forwards">
          <div class="text-xs font-semibold tracking-[0.06em] text-text-2">PORT FORWARDS</div>
          <ul class="m-0 flex list-none flex-col gap-1.5 p-0">
            <li v-for="f in preview.port_forwards" :key="f.id" class="rounded-md border border-[#2a323d] bg-surface-2 px-3 py-2 text-[13px]">
              <span class="font-medium">{{ f.name ?? `${f.protocol.toUpperCase()} ${f.external_ports}` }}</span>
              <!-- `new` is set for a changed forward too: the cards call that CHANGED -->
              <span v-if="f.new" class="ml-1.5 rounded border border-accent-strong px-1.5 text-xs text-accent-text" data-testid="preview-forward-mark">{{
                draft.baseForwards[f.id] ? 'CHANGED' : 'NEW'
              }}</span>
              <span class="mono ml-1.5 text-text-2">{{ f.protocol.toUpperCase() }} {{ f.external_ports }} from the {{ entryLabels[f.entry] }} → {{ f.target }}:{{ f.target_ports }}</span>
            </li>
          </ul>
          <p class="m-0 text-[13px] text-text-2" data-testid="preview-forwards-note">
            Faults do not apply to forwarded connections: a fault of the target device, of its network or of everything leaves them
            unimpaired. The access rules do apply, with the device as responder.
          </p>
        </div>

        <p v-if="preview.requires_confirm" class="m-0 rounded-md border border-fault-border bg-fault-tint px-3 py-2 text-[13px] text-fault-text">
          This change can cut you off from the gateway: after the apply it waits for your confirmation, and without it the gateway rolls back.
        </p>

        <div v-if="references.length" class="rounded-md border border-block-border bg-block-tint p-3 text-[13px] text-block-text" data-testid="preview-references">
          Applying removes what refers to a deleted object:
          <ul class="my-1.5 pl-5">
            <li v-for="r in references" :key="r.id">{{ r.kind }} <span class="mono">{{ r.id }}</span> ({{ where(r.object) }})</li>
          </ul>
          <label class="flex items-center gap-2 text-text"><input v-model="force" type="checkbox" /> Remove them when applying</label>
        </div>

        <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="linux" @click="linux = !linux">
          {{ linux ? 'Hide Linux changes' : 'Show Linux changes' }}
        </button>
        <div v-if="linux" class="flex flex-col gap-2" data-testid="preview-linux">
          <template v-for="[name, text] in linuxParts" :key="name">
            <div class="text-xs text-text-3">{{ name }}</div>
            <pre class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]">{{ text }}</pre>
          </template>
          <p v-if="linuxParts.length === 0" class="m-0 text-[13px] text-text-2">The kernel's state does not change.</p>
          <div class="text-xs text-text-3">verify</div>
          <pre class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]" data-testid="preview-verify">read the nftables and tc state back and compare it with the target</pre>
        </div>

        <p class="m-0 text-[13px] leading-relaxed text-text-2">
          Apply runs validate → apply → verify. If verification fails, revision {{ draft.base }} is restored automatically.
        </p>
      </template>

      <div v-if="failure" class="rounded-md border border-block-border bg-block-tint p-3 text-sm text-block-text" role="alert" data-testid="apply-failed">
        <strong class="font-semibold">Apply failed at the {{ failure.step }} step.</strong> {{ failure.detail }}
        <span class="mt-1 block">
          {{ failure.restored !== undefined ? `Revision ${failure.restored} restored` : 'The previous revision is restored' }}; nothing of revision
          {{ candidate }} is active.
        </span>
      </div>
      <p v-if="blocked && phase === 'ready'" class="m-0 text-[13px] text-fault-text">
        {{ connection.offline ? 'The gateway does not answer.' : 'Another revision waits for confirmation; apply after it is resolved.' }}
      </p>
    </div>
    <template #footer>
      <button type="button" class="btn" :disabled="phase === 'applying'" @click="back">Back to editing</button>
      <button
        v-if="phase !== 'invalid' && phase !== 'failed'"
        type="button"
        class="btn btn-primary"
        :disabled="phase !== 'ready' || blocked || (references.length > 0 && !force)"
        @click="apply"
      >
        {{ phase === 'applying' ? 'Applying…' : candidate !== undefined ? `Apply revision ${candidate}` : 'Apply' }}
      </button>
    </template>
  </SideDrawer>
</template>
