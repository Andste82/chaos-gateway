<script setup lang="ts">
// The preview-and-apply drawer (plan §2.17 "Preview & apply", "Applying / applied", "Apply failed"):
// the draft becomes a candidate revision (a merge patch against its base, If-Match the base), the
// gateway's dry run says what changes in domain terms (rule, field, old → new) and, expandable, in
// Linux terms (nftables, tc, routes, … and the verify step); nothing changes until "Apply revision
// N". Back to editing discards the candidate. A base that is not active any more opens the
// concurrent-change dialog; a refused candidate (422) lists the problems and marks the objects; a
// failed apply shows the failing step and the revision the gateway restored.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { applyRevision, createRevision, discardRevision, previewRevision } from '../../api/generated/hooks/revisions/revisions';
import { getState } from '../../api/generated/hooks/system/system';
import type { ApplyResult, Preview } from '../../api/generated/model';
import { useGatewayState, useNames } from '../../composables/gateway';
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
  set: (v) => {
    if (!v) void back();
  },
});

watch(
  () => draft.drawerOpen,
  (o) => {
    if (o) void prepare();
  },
);

function objectName(kind: string, id: string): string {
  const r = kind === 'access_rules' ? (draft.effectiveRules[id] ?? draft.baseConfig?.access_rules?.[id]) : (draft.effectiveFaults[id] ?? draft.baseConfig?.faults?.[id]);
  return `${kind === 'access_rules' ? 'Rule' : 'Fault'} ${r?.name ?? id}`;
}

function where(path: string | undefined): string {
  const m = /^\/(access_rules|faults)\/([^/]+)(?:\/(.*))?$/.exec(path ?? '');
  if (!m) return path || 'configuration';
  return `${objectName(m[1]!, m[2]!)}${m[3] ? ` · ${m[3].replace(/\//g, ' ')}` : ''}`;
}

async function prepare() {
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
    candidate.value = rev.id;
    calls.push({ method: 'POST', path: `/revisions/${rev.id}/preview`, note: 'the dry run' });
    preview.value = await previewRevision(rev.id);
    phase.value = 'ready';
  } catch (e) {
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
      problems.value = errs.length ? errs.map((x) => ({ where: where(x.path), message: x.message ?? '' })) : [{ where: 'configuration', message: e.message }];
      phase.value = 'invalid';
      return;
    }
    error.value = errorMessage(e);
    phase.value = 'error';
  }
}

async function back() {
  const id = candidate.value;
  candidate.value = undefined;
  draft.drawerOpen = false;
  if (id !== undefined && phase.value !== 'applying') await discardRevision(id).catch(() => undefined);
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
  try {
    const res = await applyRevision(id, params);
    apiPanel.record(`Preview & apply revision ${res.revision}`, [
      ...calls,
      { method: 'POST', path: `/revisions/${id}/apply${force.value ? '?force=true' : ''}`, note: 'apply: validate → apply → verify' },
    ]);
    toasts.push(
      'success',
      appliedTitle(res),
      res.status === 'pending_confirm' ? 'Confirm it in the banner before the time runs out, or the gateway rolls back.' : draft.summary(names.value) + '.',
    );
    candidate.value = undefined;
    draft.discard();
    draft.drawerOpen = false;
    await Promise.all(['state', 'revisions', 'rules', 'faults', 'overlays', 'devices', 'profiles'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
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

        <p v-if="preview.requires_confirm" class="m-0 rounded-md border border-fault-border bg-fault-tint px-3 py-2 text-[13px] text-fault-text">
          This change can cut you off from the gateway: after the apply it waits for your confirmation, and without it the gateway rolls back.
        </p>

        <div v-if="references.length" class="rounded-md border border-block-border bg-block-tint p-3 text-[13px] text-block-text" data-testid="preview-references">
          Applying removes what refers to a deleted object:
          <ul class="my-1.5 pl-5">
            <li v-for="r in references" :key="r.id">{{ r.kind }} <span class="mono">{{ r.id }}</span> ({{ r.object }})</li>
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
          <pre class="mono m-0 rounded-lg border border-border bg-bg p-3 text-xs text-[#d9dee5]">read the nftables and tc state back and compare it with the target</pre>
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
      <button type="button" class="btn" @click="back">Back to editing</button>
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
