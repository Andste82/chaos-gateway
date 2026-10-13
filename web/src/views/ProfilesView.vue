<script setup lang="ts">
// Profiles (plan §2.17 "Profiles", §2.9): a card per profile with its parameters and where it is
// active (with the remaining time of each activation), a scope selector (everything / network /
// group / device) and a duration; activate or deactivate on the chosen scope. One profile per scope:
// activating another one on the same scope switches it (the overlay keeps its id). "Measure with
// probe" arrives with the probes (M25).
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId } from 'vue';
import { useRoute } from 'vue-router';
import { errorMessage } from '../api/fetcher';
import { createOverlay, deleteOverlay } from '../api/generated/hooks/overlays/overlays';
import type { OverlayRequest, ProfileActivation, ProfileView } from '../api/generated/model';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import FaultChip from '../components/ui/FaultChip.vue';
import LoadError from '../components/ui/LoadError.vue';
import { firstError, useGatewayState, useGroups, useNames, useProfiles } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { durations, scopeOf, targetOf } from '../lib/faultForm';
import { describeScope } from '../lib/faults';
import { formatRemaining } from '../lib/format';
import { profileChips, profileTitle } from '../lib/profiles';
import { useApiPanelStore, useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';
import { useToastStore } from '../stores/toasts';

const profiles = useProfiles();
const { names, devices, networks } = useNames();
const groups = useGroups();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const now = useNow();
const uid = useId();

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
  return out;
});
const groupList = computed(() => (groups.data.value ?? []).map((g) => ({ id: g.id, name: g.config.name })));
const scopeName = computed(() => describeScope(scopeOf(scope.value), names.value));

/** The activations of a profile on the chosen scope (one per owner). */
function here(p: ProfileView): ProfileActivation[] {
  return (p.activations ?? []).filter((a) => targetOf(a.target) === scope.value);
}

const cards = computed(() =>
  [...(profiles.data.value ?? [])].sort((a, b) => Number(b.builtin) - Number(a.builtin)).map((p) => ({ p, title: profileTitle(p.config.name), chips: profileChips(p.config.parts) })),
);

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
</script>

<template>
  <PageHeader title="Profiles" context="Named network conditions · one active profile per target · device faults still win over a profile" />

  <div class="card flex flex-wrap items-end gap-4">
    <div class="min-w-[240px]">
      <label class="label" :for="`${uid}-scope`">Activate on</label>
      <select :id="`${uid}-scope`" v-model="scope" class="input">
        <option v-for="s in scopes" :key="s.value" :value="s.value">{{ s.label }}</option>
      </select>
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

  <p v-if="profiles.isLoading.value" class="text-text-2">Loading the profiles…</p>
  <LoadError v-else-if="firstError(profiles)" :error="firstError(profiles)" what="The profiles are not loaded" :retry="profiles.refetch" />
  <EmptyState v-else-if="cards.length === 0" text="No profiles." />
  <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3" data-testid="profile-cards">
    <section
      v-for="{ p, title, chips } in cards"
      :key="p.id"
      class="card flex flex-col gap-2.5"
      :class="p.activations?.length ? '!border-fault-border' : ''"
      :aria-labelledby="`${uid}-${p.id}`"
      data-testid="profile-card"
    >
      <div class="flex items-center gap-2">
        <h2 :id="`${uid}-${p.id}`" class="m-0 text-base font-semibold">{{ title }}</h2>
        <span v-if="p.activations?.length" class="rounded border border-fault-border bg-fault-tint px-1.5 text-xs text-fault-text">active</span>
        <span v-if="!p.builtin" class="rounded border border-border-strong px-1.5 text-xs text-text-2">custom</span>
      </div>
      <p v-if="p.config.description" class="m-0 text-[13px] text-text-2">{{ p.config.description }}</p>
      <div class="flex flex-wrap gap-1.5">
        <FaultChip v-for="c in chips" :key="c" :text="c" />
      </div>
      <div class="text-[13px]" data-testid="profile-active">
        <template v-if="p.activations?.length">
          Active on
          <ul class="m-0 mt-1 list-none p-0">
            <li v-for="a in p.activations" :key="a.overlay" class="mono text-xs text-fault-text">{{ activationText(a) }}</li>
          </ul>
        </template>
        <span v-else class="text-text-3">Not active</span>
      </div>
      <p v-if="!p.available" class="m-0 text-[13px] text-text-2">{{ p.unavailable_reason ?? 'Not available in this build.' }}</p>
      <div class="mt-auto flex gap-2 pt-1">
        <button
          v-if="here(p).length"
          type="button"
          class="btn btn-sm"
          :disabled="busy === p.id || blocked"
          @click="deactivate(p)"
        >
          Deactivate on {{ scopeName }}
        </button>
        <button v-else type="button" class="btn btn-primary btn-sm" :disabled="busy === p.id || blocked || !p.available" @click="activate(p)">
          Activate on {{ scopeName }}
        </button>
      </div>
    </section>
  </div>
</template>
