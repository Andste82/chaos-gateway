<script setup lang="ts">
// Access rules (plan §2.17 "Access rules", §2.4, D25): the ordered list — the locked system rule
// "Control plane access" at the top, the overlay rules above the configured ones (newest first),
// then the configured rules in their order — with position, name, selector, access chip, hit
// counter and enable switch; a selected rule opens in the editor next to it (IF / THEN) with its
// matches: hits, bytes, last match and the effective result in words. Edits, new rules, deletions
// and moves go into the configuration draft and reach the gateway through Preview & apply; a deleted
// rule stays at its place, struck through with an undo, until the draft is applied or discarded.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../api/fetcher';
import { deleteOverlay } from '../api/generated/hooks/overlays/overlays';
import type { AccessRule, AccessRuleView, Overlay } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import LinuxObjectExcerpt from '../components/linux/LinuxObjectExcerpt.vue';
import RuleEditor from '../components/rules/RuleEditor.vue';
import AccessChip from '../components/ui/AccessChip.vue';
import AppIcon from '../components/ui/AppIcon.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SwitchToggle from '../components/ui/SwitchToggle.vue';
import { firstError, useAccessRules, useActiveConfig, useGatewayState, useNames, useOverlays } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { describeOwner, describeScope } from '../lib/faults';
import { formatAgo, formatBytes, formatCount, formatRemaining } from '../lib/format';
import { newId } from '../lib/ids';
import { newRule, ruleEffectInWords, ruleSummary } from '../lib/rules';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDraftStore } from '../stores/draft';
import { useToastStore } from '../stores/toasts';
import { focusAfterUpdate } from '../lib/focus';

const active = useActiveConfig();
const rules = useAccessRules();
const overlays = useOverlays();
const draft = useDraftStore();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const { names, networks } = useNames();
const now = useNow();

useApiScreen('the access rules', () => [
  { method: 'GET', path: '/rules', note: 'the configured rules in their order, with counters, behind the system rules' },
  { method: 'GET', path: '/overlays?kind=rule', note: 'the overlay rules, evaluated first' },
]);

const views = computed(() => new Map<string, AccessRuleView>((rules.data.value?.items ?? []).map((r) => [r.id, r])));
const overlayRules = computed<Overlay[]>(() =>
  (overlays.data.value ?? []).filter((o) => o.kind === 'rule').sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
);
const system = computed(() => rules.data.value?.system ?? []);
const changedIds = computed(() => new Set(draft.changes(names.value).map((c) => c.id)));
const configRules = computed(() =>
  draft.effectiveOrder.map((id, i) => ({
    id,
    pos: i + 1,
    rule: draft.effectiveRules[id]!,
    view: views.value.get(id),
    isNew: !(id in (draft.baseConfig?.access_rules ?? {})),
    changed: changedIds.value.has(id),
    invalid: id in draft.invalid || id in draft.serverErrors,
  })),
);

type ListRow = ((typeof configRules.value)[number] & { deleted: false }) | { deleted: true; id: string; rule: AccessRule };
/** The configured rules, with the ones the draft deletes struck through at their place until it is applied or discarded. */
const listRows = computed<ListRow[]>(() => {
  const out: ListRow[] = configRules.value.map((row) => ({ ...row, deleted: false as const }));
  const baseOrder = draft.baseConfig?.access_rule_order ?? [];
  for (const [id, r] of Object.entries(draft.rules)) {
    const before = draft.baseConfig?.access_rules?.[id];
    if (r !== null || !before) continue;
    const prev = baseOrder.slice(0, baseOrder.indexOf(id)).reverse().find((x) => out.some((o) => o.id === x));
    out.splice(prev === undefined ? 0 : out.findIndex((o) => o.id === prev) + 1, 0, { deleted: true, id, rule: before });
  }
  return out;
});

const selected = ref<string>();
watch(
  [configRules, overlayRules, system],
  () => {
    const ok =
      selected.value &&
      ((selected.value.startsWith('config:') && configRules.value.some((r) => `config:${r.id}` === selected.value)) ||
        (selected.value.startsWith('overlay:') && overlayRules.value.some((o) => `overlay:${o.id}` === selected.value)) ||
        selected.value.startsWith('system:'));
    if (!ok) selected.value = configRules.value[0] ? `config:${configRules.value[0].id}` : system.value[0] ? system.value[0].key : undefined;
  },
  { immediate: true },
);

const selConfig = computed(() => configRules.value.find((r) => `config:${r.id}` === selected.value));
const selOverlay = computed(() => overlayRules.value.find((o) => `overlay:${o.id}` === selected.value));
const selSystem = computed(() => system.value.find((s) => s.key === selected.value));

function lastMatch(at: string | undefined, applied: boolean): string {
  if (!applied) return 'not applied yet';
  return at ? formatAgo(at, now.value) : 'never since the rule was applied';
}

// "Show Linux view" (plan §2.17 "Technical view", P3-M14-03): the selected rule's compiled rule in both
// hooks with its counter and last match; closed again when another rule is selected
const linuxOpen = ref(false);
watch(selected, () => (linuxOpen.value = false));

function setEnabled(id: string, rule: AccessRule, on: boolean) {
  const next = { ...rule };
  if (on) delete next.enabled;
  else next.enabled = false;
  draft.setRule(id, next);
}

function addRule() {
  const taken = new Set(Object.values(draft.effectiveRules).map((r) => r.name));
  let n = Object.keys(draft.effectiveRules).length + 1;
  while (taken.has(`rule-${n}`)) n++;
  const net = networks.data.value?.[0];
  const id = newId();
  draft.setRule(id, newRule(`rule-${n}`, net ? { network: net.id } : { global: true }));
  selected.value = `config:${id}`;
}

function removeRule(id: string) {
  const i = configRules.value.findIndex((r) => r.id === id);
  draft.setRule(id, null);
  const next = configRules.value[Math.min(i, configRules.value.length - 1)];
  selected.value = next ? `config:${next.id}` : undefined;
  // the focus goes to the deleted row's Undo, not to <body> or the next rule's Delete
  focusAfterUpdate(`[data-focus-key="undo-rule-${id}"]`);
}

function restoreRule(id: string) {
  draft.restoreRule(id);
  focusAfterUpdate(`[data-focus-key="rule-${id}"]`);
}

const apiPanel = useApiPanelStore();
const removing = ref(false);
async function removeOverlayRule(o: Overlay) {
  removing.value = true;
  try {
    await deleteOverlay(o.id);
    apiPanel.record('Remove overlay rule', [{ method: 'DELETE', path: `/overlays/${o.id}` }]);
    toasts.push('success', 'Overlay rule removed', ruleSummary({ ...o.rule!, source: o.target }, names.value));
    await queryClient.invalidateQueries({ queryKey: ['overlays'] });
  } catch (e) {
    toasts.push('error', 'The overlay rule could not be removed', errorMessage(e));
  } finally {
    removing.value = false;
  }
}

const context = computed(() => {
  const rev = draft.base ?? state.data.value?.active_revision;
  return `First matching rule wins · overlay rules before configured rules · revision ${rev ?? '…'}`;
});
// the overlay rules are evaluated first: without them the list and the effect in words would be wrong, not just short
const loadError = computed(() => firstError(active, rules, overlays));
const retry = () => Promise.all([active.refetch(), rules.refetch(), overlays.refetch()]);
const rowClass = (on: boolean) =>
  on ? 'border-accent-strong bg-surface-2' : 'border-border bg-surface hover:border-[#3a4552]';
</script>

<template>
  <PageHeader title="Access rules" :context="context">
    <template #actions>
      <button type="button" class="btn btn-primary" :disabled="!draft.baseConfig" @click="addRule">Add rule</button>
    </template>
  </PageHeader>

  <p v-if="active.isLoading.value || rules.isLoading.value || overlays.isLoading.value" class="text-text-2">Loading the rules…</p>
  <LoadError v-else-if="loadError" :error="loadError" what="The access rules are not loaded" :retry="retry" />
  <div v-else class="grid min-h-0 grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_470px]">
    <section aria-label="Rules in order" class="flex min-w-0 flex-col gap-2" data-testid="rule-list">
      <!-- the system rule: locked at the top -->
      <div
        v-for="s in system"
        :key="s.key"
        class="flex items-center gap-3.5 rounded-lg border px-3.5 py-3"
        :class="rowClass(selected === s.key)"
        data-testid="rule-row"
      >
        <span class="mono w-6 text-xs text-text-3" aria-hidden="true">—</span>
        <button type="button" class="flex min-w-0 flex-1 cursor-pointer flex-col items-start gap-1.5 border-0 bg-transparent p-0 text-left text-text" :aria-label="`Show rule ${s.name}`" :aria-current="selected === s.key ? 'true' : undefined" @click="selected = s.key">
          <span class="flex items-center gap-2 text-base font-medium">
            {{ s.name }}
            <span class="rounded border border-border-strong px-1.5 text-xs text-text-2">SYSTEM</span>
          </span>
          <span class="mono text-xs text-text-2">Management → gateway · UI, API and SSH</span>
          <AccessChip action="allow" />
        </button>
        <div class="w-28 text-right">
          <div class="mono text-[13px]">{{ formatCount(s.counters?.packets) }}</div>
          <div class="text-xs text-text-3">hits</div>
        </div>
        <SwitchToggle :model-value="true" disabled hide-label :label="`Rule ${s.name} is always on`" />
      </div>

      <!-- overlay rules: evaluated before the configured ones, newest first -->
      <div
        v-for="o in overlayRules"
        :key="o.id"
        class="flex items-center gap-3.5 rounded-lg border px-3.5 py-3"
        :class="rowClass(selected === `overlay:${o.id}`)"
        data-testid="rule-row"
      >
        <span class="mono w-6 text-xs text-text-3" aria-hidden="true">—</span>
        <button
          type="button"
          class="flex min-w-0 flex-1 cursor-pointer flex-col items-start gap-1.5 border-0 bg-transparent p-0 text-left text-text"
          :aria-label="`Show overlay rule ${ruleSummary({ ...o.rule!, source: o.target }, names)}`"
          :aria-current="selected === `overlay:${o.id}` ? 'true' : undefined"
          @click="selected = `overlay:${o.id}`"
        >
          <span class="flex items-center gap-2 text-base font-medium">
            Overlay rule
            <span class="rounded border border-fault-border px-1.5 text-xs text-fault-text">OVERLAY</span>
            <span v-if="o.expires_at" class="mono text-xs text-fault-text">{{ formatRemaining(new Date(o.expires_at).getTime() - now.getTime()) }} left</span>
          </span>
          <span class="mono text-xs text-text-2">{{ ruleSummary({ ...o.rule!, source: o.target }, names) }}</span>
          <AccessChip :action="o.rule!.action" />
        </button>
        <div class="w-28 text-right">
          <div class="mono text-[13px]">{{ formatCount(o.counters?.packets) }}</div>
          <div class="text-xs text-text-3">hits</div>
        </div>
        <span class="w-9" />
      </div>

      <!-- the configured rules in their order; a deleted one struck through until apply -->
      <template v-for="l in listRows" :key="l.id">
      <div
        v-if="l.deleted"
        class="flex items-center gap-3.5 rounded-lg border border-dashed border-border px-3.5 py-3"
        data-testid="rule-row"
      >
        <span class="mono w-6 text-xs text-text-3" aria-hidden="true">—</span>
        <span class="flex min-w-0 flex-1 flex-col items-start gap-1.5">
          <span class="flex items-center gap-2 text-base font-medium text-text-3">
            <span class="line-through">{{ l.rule.name }}</span>
            <span class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="rule-deleted">DELETED</span>
          </span>
          <span class="mono text-xs text-text-3 line-through">{{ ruleSummary(l.rule, names) }}</span>
          <span class="text-xs text-text-3">removed when applied</span>
        </span>
        <button
          type="button"
          class="btn btn-sm"
          :aria-label="`Undo the deletion of rule ${l.rule.name}`"
          :data-focus-key="`undo-rule-${l.id}`"
          @click="restoreRule(l.id)"
        >
          Undo
        </button>
      </div>
      <div
        v-else
        class="flex items-center gap-3.5 rounded-lg border px-3.5 py-3"
        :class="[rowClass(selected === `config:${l.id}`), l.invalid ? '!border-block-border' : '']"
        data-testid="rule-row"
      >
        <span class="mono w-6 text-xs text-text-3">{{ l.pos }}</span>
        <button
          type="button"
          class="flex min-w-0 flex-1 cursor-pointer flex-col items-start gap-1.5 border-0 bg-transparent p-0 text-left text-text"
          :aria-label="`Edit rule ${l.rule.name ?? l.pos}`"
          :aria-current="selected === `config:${l.id}` ? 'true' : undefined"
          :data-focus-key="`rule-${l.id}`"
          @click="selected = `config:${l.id}`"
        >
          <span class="flex items-center gap-2 text-base font-medium">
            {{ l.rule.name ?? `Rule ${l.pos}` }}
            <span v-if="l.isNew" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]">NEW</span>
            <span v-else-if="l.changed" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="rule-changed">CHANGED</span>
            <span v-if="l.invalid" class="rounded border border-block-border px-1.5 text-xs text-block-text">NEEDS FIXING</span>
          </span>
          <span class="mono text-xs text-text-2">{{ ruleSummary(l.rule, names) }}</span>
          <span class="flex items-center gap-1.5">
            <AccessChip :action="l.rule.action" />
            <span v-if="l.rule.cut_existing" class="text-xs text-text-2">cut existing</span>
            <span v-if="l.rule.enabled === false" class="text-xs text-text-3">disabled</span>
          </span>
        </button>
        <div class="w-28 text-right">
          <div class="mono text-[13px]">{{ l.view?.counters ? formatCount(l.view.counters.packets) : '—' }}</div>
          <div class="text-xs text-text-3">hits</div>
        </div>
        <SwitchToggle
          :model-value="l.rule.enabled !== false"
          hide-label
          :label="`Enable rule ${l.rule.name ?? l.pos}`"
          @update:model-value="(v: boolean) => setEnabled(l.id, l.rule, v)"
        />
      </div>
      </template>

      <EmptyState v-if="configRules.length === 0 && overlayRules.length === 0" text="No access rules yet: traffic between the networks follows the access matrix.">
        <button type="button" class="btn btn-primary" :disabled="!draft.baseConfig" @click="addRule">Add rule</button>
      </EmptyState>
    </section>

    <section class="card flex min-w-0 flex-col gap-4 self-start" aria-labelledby="rule-editor-title">
      <template v-if="selConfig">
        <div class="flex items-start justify-between gap-3">
          <div>
            <div class="text-xs tracking-[0.04em] text-text-3">RULE {{ selConfig.pos }}</div>
            <h2 id="rule-editor-title" class="m-0 mt-1 text-xl font-semibold">{{ selConfig.rule.name ?? `Rule ${selConfig.pos}` }}</h2>
          </div>
          <div class="flex gap-1.5">
            <button type="button" class="btn btn-sm px-2" aria-label="Move up" :disabled="selConfig.pos === 1" @click="draft.move(selConfig.id, -1)">↑</button>
            <button type="button" class="btn btn-sm px-2" aria-label="Move down" :disabled="selConfig.pos === configRules.length" @click="draft.move(selConfig.id, 1)">↓</button>
            <button type="button" class="btn btn-sm" @click="removeRule(selConfig.id)">Delete</button>
          </div>
        </div>
        <RuleEditor :id="selConfig.id" :rule="selConfig.rule" />
        <div class="flex flex-col gap-1.5 rounded-lg border border-border bg-[#10151b] px-3.5 py-3" data-testid="rule-matches">
          <div class="text-xs font-semibold tracking-[0.06em] text-text-2">MATCHES</div>
          <div class="mono text-[13px]">
            {{ formatCount(selConfig.view?.counters?.packets ?? 0) }} hits · {{ formatBytes(selConfig.view?.counters?.bytes ?? 0) }} · last
            {{ lastMatch(selConfig.view?.counters?.last_match, !!selConfig.view?.counters) }}
          </div>
          <div class="text-[13px] leading-normal text-[#d9dee5]">
            {{ ruleEffectInWords(selConfig.rule, { state: selConfig.view?.state, overlayRules: overlayRules.length, applied: !selConfig.isNew && !selConfig.changed }) }}
          </div>
        </div>
        <template v-if="!selConfig.isNew">
          <button
            type="button"
            class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text"
            :aria-expanded="linuxOpen"
            @click="linuxOpen = !linuxOpen"
          >
            {{ linuxOpen ? 'Hide Linux view' : 'Show Linux view (compiled rule and counter)' }}
          </button>
          <LinuxObjectExcerpt v-if="linuxOpen" kind="rule" :id="selConfig.id" />
        </template>
      </template>

      <template v-else-if="selOverlay">
        <div>
          <div class="text-xs tracking-[0.04em] text-text-3">OVERLAY RULE · BEFORE THE CONFIGURED RULES</div>
          <h2 id="rule-editor-title" class="m-0 mt-1 text-xl font-semibold">{{ ruleSummary({ ...selOverlay.rule!, source: selOverlay.target }, names) }}</h2>
        </div>
        <p class="m-0 text-sm leading-relaxed text-[#d9dee5]">
          Set by {{ describeOwner(selOverlay.owner) }}
          for {{ describeScope(selOverlay.target, names) }}<template v-if="selOverlay.expires_at">; it ends in
            <span class="mono text-fault-text">{{ formatRemaining(new Date(selOverlay.expires_at).getTime() - now.getTime()) }}</span></template
          >. An overlay is not part of the configuration: it is changed through the API and removed here.
        </p>
        <div class="flex flex-col gap-1.5 rounded-lg border border-border bg-[#10151b] px-3.5 py-3" data-testid="rule-matches">
          <div class="text-xs font-semibold tracking-[0.06em] text-text-2">MATCHES</div>
          <div class="mono text-[13px]">
            {{ formatCount(selOverlay.counters?.packets ?? 0) }} hits · {{ formatBytes(selOverlay.counters?.bytes ?? 0) }} · last
            {{ lastMatch(selOverlay.counters?.last_match, !!selOverlay.counters) }}
          </div>
          <div class="text-[13px] leading-normal text-[#d9dee5]">
            {{ ruleEffectInWords({ ...selOverlay.rule!, source: selOverlay.target ?? { global: true } }, { state: selOverlay.state, applied: true }) }}
          </div>
        </div>
        <button
          type="button"
          class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text"
          :aria-expanded="linuxOpen"
          @click="linuxOpen = !linuxOpen"
        >
          {{ linuxOpen ? 'Hide Linux view' : 'Show Linux view (compiled rule and counter)' }}
        </button>
        <LinuxObjectExcerpt v-if="linuxOpen" kind="rule" :id="selOverlay.id" />
        <button type="button" class="btn self-start" :disabled="removing || !!state.data.value?.pending_confirm || connection.offline" @click="removeOverlayRule(selOverlay)">
          <AppIcon name="close" />Remove overlay rule
        </button>
      </template>

      <template v-else-if="selSystem">
        <div>
          <div class="text-xs tracking-[0.04em] text-text-3">SYSTEM RULE</div>
          <h2 id="rule-editor-title" class="m-0 mt-1 text-xl font-semibold">{{ selSystem.name }}</h2>
        </div>
        <div class="rounded-lg border border-border-strong bg-[#10151b] p-3.5 text-sm leading-relaxed text-[#d9dee5]">
          Always allowed. This rule keeps the gateway's UI, API and SSH reachable from the management network, so no other rule can lock you out. It
          covers nothing else: management traffic to devices or the Internet follows the normal rules.
          <span class="mt-2 block text-text-2">{{ selSystem.description }}</span>
        </div>
        <div class="flex flex-col gap-1.5 rounded-lg border border-border bg-[#10151b] px-3.5 py-3" data-testid="rule-matches">
          <div class="text-xs font-semibold tracking-[0.06em] text-text-2">MATCHES</div>
          <div class="mono text-[13px]">
            {{ formatCount(selSystem.counters?.packets ?? 0) }} hits · {{ formatBytes(selSystem.counters?.bytes ?? 0) }} · last
            {{ lastMatch(selSystem.counters?.last_match, !!selSystem.counters) }}
          </div>
          <div class="text-[13px] text-[#d9dee5]">Evaluated before every other rule.</div>
        </div>
        <button
          type="button"
          class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text"
          :aria-expanded="linuxOpen"
          @click="linuxOpen = !linuxOpen"
        >
          {{ linuxOpen ? 'Hide Linux view' : 'Show Linux view (compiled rule and counter)' }}
        </button>
        <LinuxObjectExcerpt v-if="linuxOpen" kind="rule" :id="selSystem.key" />
      </template>
      <p v-else id="rule-editor-title" class="m-0 text-sm text-text-2">Select a rule to see it here.</p>
    </section>
  </div>
</template>
