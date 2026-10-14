<script setup lang="ts">
// The clients of a WireGuard hub (plan §2.2.1 "Status per client", M14): name, tunnel address, client
// networks, online state (a handshake younger than 3 min) with the last handshake, the endpoint it is
// seen at and its bytes; a client of the configuration draft is marked NEW until it is applied. Add client
// opens the add-client dialog, Configuration the QR code and the download of a client's configuration.
// A new client the gateway refused says why in its row, and Fix opens it again in the dialog with the
// messages under its fields (plan §2.17 "Validation"). A client of the gateway can be disabled or
// enabled, get new keys and be deleted (plan §2.2.1; P3-M14-01): each action goes into the
// configuration draft (marked CHANGED or DELETED, Undo takes it back) and through the preview and the
// apply, with commit-confirm where the gateway says the change could lock the admin out (a client of
// a management hub).
import { computed, ref } from 'vue';
import type { NetworkView, WireGuardClient, WireGuardClientView, WireGuardNetwork } from '../../api/generated/model';
import { firstError, useNetworks, useWireGuardClients } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { formatAgo, formatBytes } from '../../lib/format';
import { keysText, parseIPv4, withEnabled } from '../../lib/networks';
import { useDraftStore } from '../../stores/draft';
import EmptyState from '../ui/EmptyState.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import StatusDot from '../ui/StatusDot.vue';
import AddClientDialog from './AddClientDialog.vue';
import ClientConfigDialog from './ClientConfigDialog.vue';
import RotateKeysDialog from './RotateKeysDialog.vue';

const props = defineProps<{ network: NetworkView }>();
const clients = useWireGuardClients(() => props.network.id);
const networks = useNetworks();
const draft = useDraftStore();
const now = useNow(1000);

const wg = computed(() => props.network.config as WireGuardNetwork);

interface Row {
  id: string;
  name: string;
  address: string;
  nets: string[];
  /** The client as the draft has it. */
  client: WireGuardClient;
  enabled: boolean;
  /** The draft gives it new keys. */
  rotated?: boolean;
  view?: WireGuardClientView;
  mark?: 'NEW' | 'CHANGED' | 'DELETED';
  errors?: Record<string, string>;
}

const rows = computed<Row[]>(() => {
  const out: Row[] = [];
  const server = clients.data.value ?? [];
  const effective = draft.effectiveClients(props.network.id);
  for (const v of server) {
    const edit = draft.clients[v.id];
    // the client as the draft has it (its base revision with the edits); the gateway's view for one the base does not know yet
    const c = effective[v.id] ?? draft.baseClient(props.network.id, v.id) ?? v.config;
    out.push({
      id: v.id,
      name: c.name,
      address: c.address,
      nets: c.client_networks ?? [],
      client: c,
      enabled: c.enabled !== false,
      rotated: !!edit?.client && keysText(edit.client) !== keysText(draft.baseClient(props.network.id, v.id) ?? v.config),
      view: v,
      mark: edit ? (edit.client === null ? 'DELETED' : 'CHANGED') : undefined,
    });
  }
  for (const [id, c] of Object.entries(draft.effectiveClients(props.network.id))) {
    if (server.some((v) => v.id === id)) continue;
    out.push({
      id,
      name: c.name,
      address: c.address,
      nets: c.client_networks ?? [],
      client: c,
      enabled: c.enabled !== false,
      mark: id in draft.clients ? 'NEW' : undefined,
      errors: draft.serverErrors[id],
    });
  }
  return out.sort((a, b) => (parseIPv4(a.address) ?? 0) - (parseIPv4(b.address) ?? 0));
});

const adding = ref(false);
/** The client of the draft the dialog fixes; none: the dialog adds a client. */
const fixing = ref<string>();
function add() {
  fixing.value = undefined;
  adding.value = true;
}
function fix(id: string) {
  fixing.value = id;
  adding.value = true;
}
const showing = ref<WireGuardClientView>();
const configOpen = computed({
  get: () => !!showing.value,
  set: (v) => {
    if (!v) showing.value = undefined;
  },
});

// the client actions: each one an edit of the draft against the client as its base revision has it
function setEnabled(r: Row, enabled: boolean) {
  draft.setClient(props.network.id, r.id, withEnabled(r.client, enabled, draft.baseClient(props.network.id, r.id)));
}
function remove(r: Row) {
  draft.setClient(props.network.id, r.id, null);
}
function undo(r: Row) {
  const base = draft.baseClient(props.network.id, r.id);
  if (base) draft.setClient(props.network.id, r.id, base);
}
const rotating = ref<Row>();
const rotateOpen = computed({
  get: () => !!rotating.value,
  set: (v) => {
    if (!v) rotating.value = undefined;
  },
});
function rotate(c: WireGuardClient) {
  if (rotating.value) draft.setClient(props.network.id, rotating.value.id, c);
}

function handshake(v: WireGuardClientView | undefined): string {
  const at = v?.status.last_handshake;
  return at ? `handshake ${formatAgo(at, now.value)}` : 'no handshake yet';
}
</script>

<template>
  <SectionCard :title="`Clients of ${wg.name}`">
    <template #actions>
      <span class="text-xs text-text-3">online: a handshake in the last 3 min</span>
      <button type="button" class="btn btn-primary btn-sm" @click="add">Add client</button>
    </template>
    <p v-if="clients.isLoading.value" class="m-0 text-sm text-text-2">Loading the clients…</p>
    <LoadError v-else-if="firstError(clients)" :error="firstError(clients)" what="The clients are not loaded" :retry="clients.refetch" />
    <EmptyState v-else-if="rows.length === 0" text="No client yet: add one, then scan its QR code or download its configuration.">
      <button type="button" class="btn btn-primary btn-sm" @click="add">Add client</button>
    </EmptyState>
    <table v-else class="w-full border-collapse text-sm" data-testid="clients">
      <thead>
        <tr class="data-head text-left">
          <th class="py-2 font-normal">Name</th>
          <th class="py-2 font-normal">Tunnel address</th>
          <th class="py-2 font-normal">Client networks</th>
          <th class="py-2 font-normal">Status</th>
          <th class="py-2 font-normal">Endpoint</th>
          <th class="py-2 font-normal">Traffic</th>
          <th class="py-2 font-normal"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.id" class="data-row" :class="r.mark === 'DELETED' ? 'line-through opacity-60' : ''" data-testid="client-row">
          <td class="py-2.5">
            <span class="font-medium">{{ r.name }}</span>
            <span
              v-if="r.mark"
              class="ml-2 rounded border px-1.5 text-[11px]"
              :class="r.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
              >{{ r.mark }}</span
            >
            <span v-if="r.errors" class="ml-2 rounded border border-block-border px-1.5 text-[11px] text-block-text">NEEDS FIXING</span>
            <span v-if="!r.enabled" class="ml-2 text-xs text-text-3" data-testid="client-disabled">disabled</span>
            <span v-if="r.rotated" class="mt-1 block text-[13px] text-fault-text" data-testid="client-rotated">
              New keys once applied: give the client its new configuration then.
            </span>
            <span v-if="r.errors" class="mt-1 block text-[13px] text-block-text" data-testid="client-refused">The gateway refused it: {{ Object.values(r.errors).join(' ') }}</span>
          </td>
          <td class="mono py-2.5 text-[13px]">{{ r.address }}</td>
          <td class="mono py-2.5 text-[13px]">{{ r.nets.join(', ') || '—' }}</td>
          <td class="py-2.5">
            <template v-if="r.view">
              <StatusDot :tone="r.view.status.online ? 'normal' : 'offline'" :label="r.view.status.online ? 'Online' : 'Offline'" />
              <span class="block text-xs text-text-3">{{ handshake(r.view) }}</span>
            </template>
            <span v-else class="text-[13px] text-text-3">not applied yet</span>
          </td>
          <td class="mono py-2.5 text-[13px]">{{ r.view?.status.endpoint || '—' }}</td>
          <td class="mono py-2.5 text-[13px]">
            <template v-if="r.view?.status.rx_bytes !== undefined">↓ {{ formatBytes(r.view.status.rx_bytes) }} ↑ {{ formatBytes(r.view.status.tx_bytes) }}</template>
            <template v-else>—</template>
          </td>
          <td class="py-2.5 text-right">
            <div class="flex flex-wrap justify-end gap-1.5">
              <button v-if="r.errors" type="button" class="btn btn-sm" :aria-label="`Fix ${r.name}`" @click="fix(r.id)">Fix</button>
              <template v-else-if="r.view && r.mark === 'DELETED'">
                <button type="button" class="btn btn-sm" :aria-label="`Undo the deletion of ${r.name}`" @click="undo(r)">Undo</button>
              </template>
              <template v-else-if="r.view && r.mark !== 'NEW'">
                <button type="button" class="btn btn-sm" :aria-label="`Configuration and QR code of ${r.name}`" @click="showing = r.view">Configuration</button>
                <button type="button" class="btn btn-sm" :aria-label="`${r.enabled ? 'Disable' : 'Enable'} ${r.name}`" @click="setEnabled(r, !r.enabled)">
                  {{ r.enabled ? 'Disable' : 'Enable' }}
                </button>
                <button type="button" class="btn btn-sm" :aria-label="`Rotate the keys of ${r.name}`" @click="rotating = r">Rotate keys</button>
                <button type="button" class="btn btn-sm" :aria-label="`Delete ${r.name}`" @click="remove(r)">Delete</button>
                <button v-if="r.mark === 'CHANGED'" type="button" class="btn btn-sm" :aria-label="`Undo the changes of ${r.name}`" @click="undo(r)">Undo</button>
              </template>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </SectionCard>
  <AddClientDialog v-model:open="adding" :fix="fixing" :hub="network" :networks="networks.data.value ?? []" :clients="clients.data.value ?? []" />
  <ClientConfigDialog v-model:open="configOpen" :network="network.id" :client="showing" />
  <RotateKeysDialog v-model:open="rotateOpen" :client="rotating?.client" @rotate="rotate" />
</template>
