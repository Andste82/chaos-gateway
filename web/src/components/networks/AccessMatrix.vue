<script setup lang="ts">
// The access matrix between networks (plan §2.2 firewall layer 2, §2.17 "Networks", M14): from each test
// or WireGuard network to the Internet, the other networks and the management network, Allowed or
// Blocked, and whether that is the default (a local test network may reach the uplink, everything else
// is blocked) or an entry of the configuration. A click changes the cell in the configuration draft: the
// unapplied-changes bar counts it, and it goes through the preview and the apply like any other change;
// the apply waits for confirmation only when the gateway says the change could lock the admin out. Its
// entries come from the active revision: until it is loaded the matrix says so instead of showing the
// defaults as if they were the policy.
import { computed, ref } from 'vue';
import type { NetworkView, WireGuardNetwork } from '../../api/generated/model';
import { firstError, useActiveConfig, useNames } from '../../composables/gateway';
import { cellOf, describeEndpoint, entryKey } from '../../lib/networks';
import { useDraftStore } from '../../stores/draft';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import LinuxExcerpt from './LinuxExcerpt.vue';

const props = defineProps<{ networks: NetworkView[] }>();
const draft = useDraftStore();
const { names } = useNames();
const active = useActiveConfig();

const rows = computed(() => props.networks.map((n) => ({ key: `network:${n.id}`, name: (n.config as { name: string }).name })));
const cols = computed(() => [{ key: 'uplink', name: 'Internet' }, ...rows.value, { key: 'management', name: 'Management' }]);
const base = computed(() => draft.baseConfig?.access_matrix?.entries ?? []);
const entries = computed(() => draft.effectiveMatrix);
const ready = computed(() => draft.baseConfig !== undefined);

const endpointNames = computed(() => ({ network: (id: string) => names.value.network?.(id), client: (id: string) => draft.clientName(id) }));

/** Entries the matrix does not show: those of a single WireGuard client. */
const clientEntries = computed(() =>
  entries.value
    .filter((e) => e.from.client || e.to.client)
    .map((e) => `${describeEndpoint(entryKey(e.from, props.networks), endpointNames.value)} → ${describeEndpoint(entryKey(e.to, props.networks), endpointNames.value)}: ${e.policy === 'allow' ? 'allowed' : 'blocked'}`),
);
const reachable = computed(() => {
  let n = 0;
  for (const net of props.networks) {
    if (net.config.type !== 'wireguard') continue;
    for (const c of Object.values((net.config as WireGuardNetwork).clients ?? {})) n += c.reachable?.length ?? 0;
  }
  return n;
});

function cell(from: string, to: string) {
  const now = cellOf(entries.value, from, to, props.networks);
  const was = cellOf(base.value, from, to, props.networks);
  return { ...now, changed: now.policy !== was.policy };
}

function label(from: { name: string }, to: { name: string }, c: ReturnType<typeof cell>): string {
  const word = c.policy === 'allow' ? 'allowed' : 'blocked';
  return `${from.name} to ${to.name}: ${word}${c.explicit ? '' : ' (default)'}${c.changed ? ', changed, not applied yet' : ''}. Click to change`;
}

const linux = ref(false);
</script>

<template>
  <SectionCard title="Access between networks">
    <template #actions><span class="text-xs text-text-3">Click a cell to change it · applies through preview</span></template>
    <LoadError v-if="!ready && firstError(active)" :error="firstError(active)" what="The access matrix is not loaded" :retry="active.refetch" />
    <p v-else-if="!ready" class="m-0 text-sm text-text-2" role="status">Loading the access matrix…</p>
    <div
      v-else
      class="grid items-center gap-2"
      :style="{ gridTemplateColumns: `150px repeat(${cols.length}, minmax(0, 1fr))` }"
      role="group"
      aria-label="Access matrix: from the row's network to the column's"
      data-testid="access-matrix"
    >
      <span class="text-xs text-text-3">From ↓ · to →</span>
      <span v-for="c in cols" :key="c.key" class="text-center text-[13px] text-text-2">{{ c.name }}</span>
      <template v-for="r in rows" :key="r.key">
        <span class="text-sm font-medium">{{ r.name }}</span>
        <template v-for="c in cols" :key="c.key">
          <span v-if="c.key === r.key" class="flex h-11 items-center justify-center text-[#4a5461]" aria-hidden="true">—</span>
          <button
            v-else
            type="button"
            class="flex h-11 flex-col items-center justify-center rounded-md border text-[13px] font-medium"
            :class="[
              cell(r.key, c.key).policy === 'allow' ? 'border-[#2d4b6b] bg-[#16222e] text-[#b8dbff]' : 'border-block-border bg-block-tint text-block-text',
              cell(r.key, c.key).changed ? 'outline-2 outline-offset-1 outline-accent outline-dashed' : '',
            ]"
            :aria-pressed="cell(r.key, c.key).policy === 'allow'"
            :aria-label="label(r, c, cell(r.key, c.key))"
            :data-cell="`${r.name}>${c.name}`"
            @click="draft.toggleMatrix(r.key, c.key)"
          >
            {{ cell(r.key, c.key).policy === 'allow' ? 'Allowed' : 'Blocked' }}
            <span class="text-[11px] font-normal opacity-75">{{ cell(r.key, c.key).changed ? 'CHANGED' : cell(r.key, c.key).explicit ? '' : 'default' }}</span>
          </button>
        </template>
      </template>
    </div>
    <p v-if="clientEntries.length || reachable" class="m-0 text-[13px] text-text-2" data-testid="client-entries">
      <template v-if="clientEntries.length">Entries for single WireGuard clients (they win over the network's): {{ clientEntries.join('; ') }}.</template>
      <template v-if="reachable"> What a client may reach is allowed for it as well ({{ reachable }} {{ reachable === 1 ? 'entry' : 'entries' }} in its settings).</template>
    </p>
    <div class="rounded-lg border border-border bg-[#10151b] px-3.5 py-3 text-[13px] leading-relaxed text-[#d9dee5]">
      The matrix sets the default for traffic between networks. Access rules are evaluated first and can override it for single devices or ports. Access
      from Management to the gateway itself is always allowed.
    </div>
    <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="linux" @click="linux = !linux">
      {{ linux ? 'Hide Linux view' : 'Show Linux view (compiled forward chain)' }}
    </button>
    <LinuxExcerpt v-if="linux" :chains="['forward']" />
  </SectionCard>
</template>
