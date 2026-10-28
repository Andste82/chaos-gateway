<script setup lang="ts">
// Profiles (plan §2.17 "Profiles", §2.9): a card per profile with its parameters and where it is
// active (with the remaining time of each activation), a scope selector (everything / network /
// group / device, and the remote networks of P3-M13-08) and a duration; activate or deactivate on
// the chosen scope. One profile per scope: activating another one on the same scope switches it
// (the overlay keeps its id). Custom profiles are made, edited and deleted in the profile editor
// (P3-M13-03): a custom profile is an object of the configuration, so the change goes into the draft
// and through the unapplied-changes bar, the preview and the apply; until then the card is marked
// NEW, CHANGED or DELETED (with Undo) and cannot be activated, and a profile the gateway refused in a
// candidate is marked with its problems. "Measure with probe" arrives with the probes (M25).
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId } from 'vue';
import { useRoute } from 'vue-router';
import { errorMessage } from '../api/fetcher';
import { createOverlay, deleteOverlay } from '../api/generated/hooks/overlays/overlays';
import type { OverlayRequest, Profile, ProfileActivation, ProfileView } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import CalibrateDialog from '../components/diagnostics/CalibrateDialog.vue';
import ProfileDialog from '../components/profiles/ProfileDialog.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import FaultChip from '../components/ui/FaultChip.vue';
import LoadError from '../components/ui/LoadError.vue';
import ScopeSelect from '../components/ui/ScopeSelect.vue';
import { firstError, useActiveConfig, useCanProbe, useGatewayState, useGroups, useNames, useProbes, useProfiles } from '../composables/gateway';
import { measurableProfiles } from '../lib/probes';
import { useNow } from '../composables/useNow';
import { durations, scopeOf, targetOf } from '../lib/faultForm';
import { describeScope } from '../lib/faults';
import { formatRemaining } from '../lib/format';
import { remoteCidrError, remoteScopeOptions } from '../lib/scopes';
import { profileChips, profileTitle } from '../lib/profiles';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useDraftStore } from '../stores/draft';
import { useToastStore } from '../stores/toasts';
import { refocusAfterSwap } from '../lib/focus';

const profiles = useProfiles();
// "Measure with probe" (M25): the self-test of a profile on a probe
const canProbe = useCanProbe();
const probesQuery = useProbes(() => canProbe.value !== false);
const probes = computed(() => probesQuery.data.value ?? []);
const measurable = computed(() => measurableProfiles(profiles.data.value ?? []));
const measuring = ref<{ open: boolean; profile?: string }>({ open: false });
const { names, devices, networks } = useNames();
const groups = useGroups();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const now = useNow();
const uid = useId();
const draft = useDraftStore();
// the draft's base: a custom profile is edited against the active revision's configuration
const active = useActiveConfig();

useApiScreen('the profiles', () => [{ method: 'GET', path: '/profiles', note: 'built-in and custom profiles with their activations' }]);

const route = useRoute();
const scope = ref(typeof route.query.scope === 'string' ? route.query.scope : 'global');
const duration = ref('perm');

const scopes = computed(() => {
  const n = names.value;
  const out: { value: string; label: string }[] = [{ value: 'global', label: 'Everything' }];
  for (const net of networks.data.value ?? []) out.push({ value: `network:${net.id}`, label: `Network · ${n.network?.(net.id) ?? net.id}` });
  for (const g of groupList.value) out.push({ value: `group:${g.id}`, label: `Group · ${g.name}` });
  for (const d of devices.data.value ?? []) out.push({ value: `device:${d.id}`, label: `Device · ${d.name}` });
  // the client networks of a WireGuard client, a link's prefixes or a prefix inside them (P3-M13-08)
  out.push(...remoteScopeOptions(networks.data.value ?? []));
  return out;
});
const scopeError = computed(() => remoteCidrError(scope.value));
const groupList = computed(() => (groups.data.value ?? []).map((g) => ({ id: g.id, name: g.config.name })));
const scopeName = computed(() => describeScope(scopeOf(scope.value), names.value));

/** The activations of a profile on the chosen scope (one per owner). */
function here(p: ProfileView): ProfileActivation[] {
  return (p.activations ?? []).filter((a) => targetOf(a.target) === scope.value);
}

interface Card {
  p: ProfileView;
  title: string;
  chips: string[];
  isNew?: boolean;
  changed?: boolean;
  /** A custom profile the draft deletes: shown until the draft is applied or discarded. */
  deleted?: boolean;
  /** What the gateway refused about the profile in the last candidate. */
  problems?: string[];
}

/** The profiles as the gateway has them, with the draft's custom profiles: new, changed and deleted ones. */
const cards = computed<Card[]>(() => {
  const views = profiles.data.value ?? [];
  const out: Card[] = [];
  const card = (p: ProfileView, extra: Partial<Card> = {}): Card => ({
    p,
    title: profileTitle(p.config.name),
    chips: profileChips(p.config.parts),
    ...(draft.serverErrors[p.id] ? { problems: Object.values(draft.serverErrors[p.id]!) } : {}),
    ...extra,
  });
  for (const v of views) {
    const edit = v.builtin ? undefined : draft.profiles[v.id];
    if (edit === undefined) out.push(card(v));
    else if (edit === null) out.push(card(v, { deleted: true }));
    else out.push(card({ ...v, config: edit }, { changed: true }));
  }
  for (const [id, edit] of Object.entries(draft.profiles)) {
    if (edit && !views.some((v) => v.id === id)) out.push(card({ id, builtin: false, available: true, config: edit }, { isNew: true }));
  }
  return out.sort((a, b) => Number(b.p.builtin) - Number(a.p.builtin));
});

/** A deleted profile's note: its activations end with it, the preview lists them and asks first. */
function deletedText(c: Card): string {
  const n = c.p.activations?.length ?? 0;
  if (!n) return 'Removed when the changes are applied.';
  return `Removed when the changes are applied, and with it ${n === 1 ? 'its activation' : `its ${n} activations`}: the preview lists ${n === 1 ? 'it' : 'them'} and asks first.`;
}

/** Not on the gateway as shown: a new, changed or deleted profile is activated after the apply. */
const pending = (c: Card) => !!(c.isNew || c.changed || c.deleted);

// the profile editor: new, or a custom profile as the draft has it
const editorOpen = ref(false);
const editing = ref<{ id: string; config: Profile }>();
const allNames = computed(() => cards.value.filter((c) => !c.deleted).map((c) => c.p.config.name));
function newProfile() {
  editing.value = undefined;
  editorOpen.value = true;
}
function editProfile(c: Card) {
  editing.value = { id: c.p.id, config: c.p.config };
  editorOpen.value = true;
}

const busy = ref<string>();
async function activate(p: ProfileView) {
  busy.value = p.id;
  const ttl = durations.find((d) => d.value === duration.value)?.ttl;
  const req: OverlayRequest = { target: scopeOf(scope.value), profile: p.id, ...(ttl ? { ttl } : {}) };
  try {
    await createOverlay(req);
    apiPanel.record(`Activate ${profileTitle(p.config.name)}`, [{ method: 'POST', path: '/overlays', body: req }]);
    toasts.push('success', `${profileTitle(p.config.name)} active on ${scopeName.value}`, ttl ? `Expires after ${durations.find((d) => d.ttl === ttl)?.label}.` : 'Until removed.');
    await refresh();
  } catch (e) {
    toasts.push('error', `${profileTitle(p.config.name)} could not be activated`, errorMessage(e));
  } finally {
    busy.value = undefined;
  }
}

async function deactivate(p: ProfileView) {
  busy.value = p.id;
  const acts = here(p);
  try {
    for (const a of acts) await deleteOverlay(a.overlay);
    apiPanel.record(
      `Deactivate ${profileTitle(p.config.name)}`,
      acts.map((a) => ({ method: 'DELETE', path: `/overlays/${a.overlay}` })),
    );
    toasts.push('success', `${profileTitle(p.config.name)} deactivated on ${scopeName.value}`);
    await refresh();
  } catch (e) {
    toasts.push('error', `${profileTitle(p.config.name)} could not be deactivated`, errorMessage(e));
  } finally {
    busy.value = undefined;
  }
}

function refresh() {
  return Promise.all(['profiles', 'overlays', 'devices', 'faults', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
}

function activationText(a: ProfileActivation): string {
  const where = describeScope(a.target, names.value);
  const left = a.expires_at ? ` · ${formatRemaining(new Date(a.expires_at).getTime() - now.value.getTime())} left` : '';
  const st = a.state && a.state !== 'effective' ? ` · ${a.state.replace('_', ' ')}` : '';
  const by = a.owner.type === 'user' ? '' : ` · by ${a.owner.type === 'token' ? `token ${a.owner.name ?? a.owner.id}` : 'a run'}`;
  return `${where}${left}${st}${by}`;
}

const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);
const retry = () => Promise.all([profiles.refetch(), active.refetch()]);
</script>

<template>
  <PageHeader title="Profiles" context="Named network conditions · one active profile per target · device faults still win over a profile">
    <template #actions>
      <button type="button" class="btn btn-primary" @click="newProfile">New profile</button>
    </template>
  </PageHeader>

  <div class="card flex flex-wrap items-end gap-4">
    <div class="min-w-[240px]">
      <label class="label" :for="`${uid}-scope`">Activate on</label>
      <ScopeSelect
        :id="`${uid}-scope`"
        v-model="scope"
        :options="scopes"
        :current-label="scopeName"
        :invalid="!!scopeError"
        :describedby="scopeError ? `${uid}-scope-error` : undefined"
      />
      <p v-if="scopeError" :id="`${uid}-scope-error`" class="field-error">{{ scopeError }}</p>
    </div>
    <div class="min-w-[160px]">
      <label class="label" :for="`${uid}-ttl`">For</label>
      <select :id="`${uid}-ttl`" v-model="duration" class="input">
        <option v-for="d in durations" :key="d.value" :value="d.value">{{ d.label }}</option>
      </select>
    </div>
    <p class="m-0 flex-1 text-[13px] text-text-2">
      A profile on a wider scope reaches every device in it unless the device has a fault or a profile of its own. Activating another profile on the
      same scope replaces the active one.
    </p>
  </div>

  <p v-if="profiles.isLoading.value || active.isLoading.value" class="text-text-2">Loading the profiles…</p>
  <LoadError v-else-if="firstError(profiles, active)" :error="firstError(profiles, active)" what="The profiles are not loaded" :retry="retry" />
  <EmptyState v-else-if="cards.length === 0" text="No profiles." />
  <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3" data-testid="profile-cards">
    <section
      v-for="c in cards"
      :key="c.p.id"
      class="card flex flex-col gap-2.5"
      :class="[c.p.activations?.length ? '!border-fault-border' : '', c.problems ? 'shadow-[inset_3px_0_0_var(--color-block-border)]' : '']"
      :aria-labelledby="`${uid}-${c.p.id}`"
      data-testid="profile-card"
    >
      <div class="flex items-center gap-2" :class="{ 'opacity-60': c.deleted }">
        <h2 :id="`${uid}-${c.p.id}`" class="m-0 text-base font-semibold" :class="{ 'line-through': c.deleted }">{{ c.title }}</h2>
        <span v-if="c.p.activations?.length" class="rounded border border-fault-border bg-fault-tint px-1.5 text-xs text-fault-text">active</span>
        <span v-if="!c.p.builtin" class="rounded border border-border-strong px-1.5 text-xs text-text-2">custom</span>
        <span v-if="c.deleted" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="profile-mark">DELETED</span>
        <span v-else-if="c.isNew" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="profile-mark">NEW</span>
        <span v-else-if="c.changed" class="rounded border border-accent-strong px-1.5 text-xs text-[#cfe6ff]" data-testid="profile-mark">CHANGED</span>
        <span v-if="c.problems" class="rounded border border-block-border px-1.5 text-xs text-block-text">NEEDS FIXING</span>
      </div>
      <p v-for="m in c.problems" :key="m" class="m-0 text-xs text-block-text" data-testid="profile-problem">{{ m }}</p>
      <p v-if="c.p.config.description" class="m-0 text-[13px] text-text-2">{{ c.p.config.description }}</p>
      <div class="flex flex-wrap gap-1.5" :class="{ 'opacity-60': c.deleted }">
        <FaultChip v-for="chip in c.chips" :key="chip" :text="chip" />
      </div>
      <div class="text-[13px]" data-testid="profile-active">
        <template v-if="c.p.activations?.length">
          Active on
          <ul class="m-0 mt-1 list-none p-0">
            <li v-for="a in c.p.activations" :key="a.overlay" class="mono text-xs text-fault-text">{{ activationText(a) }}</li>
          </ul>
        </template>
        <span v-else class="text-text-3">Not active</span>
      </div>
      <p v-if="!c.p.available" class="m-0 text-[13px] text-text-2">{{ c.p.unavailable_reason ?? 'Not available in this build.' }}</p>
      <p v-if="pending(c)" class="m-0 text-[13px] text-text-2" data-testid="profile-pending">
        {{ c.deleted ? deletedText(c) : 'Not applied yet: it can be activated after the changes are applied.' }}
      </p>
      <div class="mt-auto flex flex-wrap gap-2 pt-1" data-focus-group>
        <!-- one button that changes its action, so the focus stays on it when the activation goes through -->
        <button
          type="button"
          class="btn btn-sm"
          :class="{ 'btn-primary': !here(c.p).length }"
          :disabled="here(c.p).length ? busy === c.p.id || blocked : busy === c.p.id || blocked || !c.p.available || !!scopeError || pending(c)"
          :aria-label="`${here(c.p).length ? 'Deactivate' : 'Activate'} ${c.title} on ${scopeName}`"
          @click="here(c.p).length ? deactivate(c.p) : activate(c.p)"
        >
          {{ here(c.p).length ? 'Deactivate' : 'Activate' }} on {{ scopeName }}
        </button>
        <button
          v-if="canProbe !== false && c.p.config.parts.impairment && !c.deleted"
          type="button"
          class="btn btn-sm"
          :disabled="!probes.length || blocked || pending(c)"
          :title="!probes.length ? 'No probe: add a probe to the configuration to measure a profile.' : state.data.value?.pending_confirm ? 'Another revision waits for confirmation.' : undefined"
          :aria-label="`Measure ${c.title} with a probe`"
          data-testid="profile-measure"
          @click="measuring = { open: true, profile: c.p.id }"
        >
          Measure with probe
        </button>
        <template v-if="!c.p.builtin">
          <button
            v-if="c.deleted"
            type="button"
            class="btn btn-sm"
            :aria-label="`Undo the deletion of ${c.title}`"
            data-focus-swap
            @click="
              refocusAfterSwap($event);
              draft.restoreProfile(c.p.id);
            "
          >
            Undo
          </button>
          <template v-else>
            <button type="button" class="btn btn-sm" :aria-label="`Edit ${c.title}`" @click="editProfile(c)">Edit</button>
            <button
              type="button"
              class="btn btn-sm"
              :aria-label="`Delete ${c.title}`"
              data-focus-swap
              @click="
                refocusAfterSwap($event);
                draft.setProfile(c.p.id, null);
              "
            >
              Delete
            </button>
          </template>
        </template>
      </div>
    </section>
  </div>

  <ProfileDialog v-model:open="editorOpen" :profile="editing" :names="allNames" />
  <CalibrateDialog v-model:open="measuring.open" :probes="probes" :profiles="measurable" :profile="measuring.profile" />
</template>
