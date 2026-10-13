<script setup lang="ts">
// Add a client to a WireGuard hub (plan §2.2.1 "Managing networks and clients", M14): a name, the tunnel
// address — the next free address of the hub, proposed here — and under Advanced the client networks
// behind it, what it may reach, its DNS server, the keepalive and its keys. Each check stands under its
// field and "Preview & apply" waits until the form is valid (plan §2.17 "Validation"). The client goes
// into the configuration draft and the preview-and-apply drawer opens, like every configuration change;
// a hub with the role management makes the change lockout-relevant, so the apply waits for confirmation.
import { computed, ref, useId, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { NetworkView, WireGuardClientView, WireGuardNetwork } from '../../api/generated/model';
import { useDevices, useGatewayState } from '../../composables/gateway';
import { newId } from '../../lib/ids';
import { clientFormErrors, clientFromForm, emptyClientForm, nextFreeAddress, subnetText, type ClientField, type ClientForm } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ hub: NetworkView; networks: NetworkView[]; clients: WireGuardClientView[] }>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const devices = useDevices();
const apiPanel = useApiPanelStore();
const uid = useId();

const wg = computed(() => props.hub.config as WireGuardNetwork);
const form = ref<ClientForm>(emptyClientForm());
const advanced = ref(false);
const tried = ref(false);
const error = ref('');
const busy = ref(false);

/** The tunnel addresses taken: the hub's clients as the gateway has them and as the draft has them. */
const used = computed(() => [...props.clients.map((c) => c.config.address), ...Object.values(draft.effectiveClients(props.hub.id)).map((c) => c.address)]);

watch(open, (o) => {
  if (!o) return;
  form.value = emptyClientForm(nextFreeAddress(wg.value.address, used.value) ?? '');
  advanced.value = false;
  tried.value = false;
  error.value = '';
});

const takenNames = computed(() => {
  const out = (devices.data.value ?? []).map((d) => d.name.toLowerCase());
  for (const n of props.networks) {
    if (n.config.type !== 'wireguard') continue;
    for (const c of Object.values(draft.effectiveClients(n.id))) out.push(c.name.toLowerCase());
  }
  for (const c of props.clients) out.push(c.config.name.toLowerCase());
  return out;
});
const errors = computed(() => clientFormErrors(form.value, { hubAddress: wg.value.address, usedAddresses: used.value, takenNames: takenNames.value }));
const valid = computed(() => Object.keys(errors.value).length === 0);
// the messages appear once Preview & apply was pressed, then follow every change; the address is
// checked from the start, since it was proposed and a change of it should say at once what is wrong
function shown(f: ClientField): string | undefined {
  return tried.value || f === 'address' ? errors.value[f] : undefined;
}

const reachChoices = computed(() => [
  { key: 'uplink', label: 'Internet through the uplink (full tunnel)' },
  ...props.networks.filter((n) => n.id !== props.hub.id).map((n) => ({ key: `network:${n.id}`, label: `${(n.config as { name: string }).name} (${subnetText((n.config as { address: string }).address)})` })),
]);

const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);
const management = computed(() => wg.value.role === 'management');

/** What the dialog would put into the configuration, for the `</> API` panel. */
watch(
  [open, form],
  ([o]) => {
    apiPanel.draft = o
      ? {
          title: 'Add WireGuard client',
          calls: [
            {
              method: 'POST',
              path: '/revisions',
              headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base ?? state.data.value?.active_revision}"` },
              body: { networks: { [props.hub.id]: { clients: { '<new uuid>': clientFromForm(form.value) } } } },
              note: 'the candidate; then …/preview and …/apply',
            },
          ],
        }
      : undefined;
  },
  { deep: true },
);

async function submit() {
  tried.value = true;
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty) draft.track(await getActiveRevision());
    draft.setClient(props.hub.id, newId(), clientFromForm(form.value));
    open.value = false;
    draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}

function toggleReach(key: string, on: boolean) {
  const r = form.value.reachable.filter((k) => k !== key);
  form.value.reachable = on ? [...r, key] : r;
}
</script>

<template>
  <ModalDialog v-model:open="open" :title="`Add client to ${wg.name}`" :description="`A WireGuard peer of the hub, with a tunnel address in ${subnetText(wg.address)}.`" width="600px">
    <form class="flex flex-col gap-4" novalidate data-testid="add-client" @submit.prevent="submit">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="label" :for="`${uid}-name`">Name</label>
          <input :id="`${uid}-name`" v-model="form.name" class="input" placeholder="laptop-anna" :aria-invalid="!!shown('name')" :aria-describedby="shown('name') ? `${uid}-name-error` : undefined" />
          <p v-if="shown('name')" :id="`${uid}-name-error`" class="field-error">{{ shown('name') }}</p>
        </div>
        <div>
          <label class="label" :for="`${uid}-address`">Tunnel address</label>
          <input
            :id="`${uid}-address`"
            v-model="form.address"
            class="input mono"
            :aria-invalid="!!shown('address')"
            :aria-describedby="shown('address') ? `${uid}-address-error` : `${uid}-address-hint`"
          />
          <p v-if="shown('address')" :id="`${uid}-address-error`" class="field-error">{{ shown('address') }}</p>
          <p v-else :id="`${uid}-address-hint`" class="mt-1.5 mb-0 text-xs text-text-3">The next free address of the hub.</p>
        </div>
      </div>

      <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="advanced" @click="advanced = !advanced">
        {{ advanced ? 'Hide advanced' : 'Advanced: client networks, access, DNS, keys' }}
      </button>
      <div v-if="advanced" class="flex flex-col gap-4">
        <div>
          <label class="label" :for="`${uid}-nets`">Client networks (subnets behind the client)</label>
          <input
            :id="`${uid}-nets`"
            v-model="form.clientNetworks"
            class="input mono"
            placeholder="10.50.0.0/24"
            :aria-invalid="!!shown('clientNetworks')"
            :aria-describedby="shown('clientNetworks') ? `${uid}-nets-error` : undefined"
          />
          <p v-if="shown('clientNetworks')" :id="`${uid}-nets-error`" class="field-error">{{ shown('clientNetworks') }}</p>
        </div>
        <fieldset class="m-0 border-0 p-0">
          <legend class="label">The client may reach</legend>
          <label v-for="c in reachChoices" :key="c.key" class="flex items-center gap-2 py-0.5 text-sm">
            <input type="checkbox" :checked="form.reachable.includes(c.key)" @change="toggleReach(c.key, ($event.target as HTMLInputElement).checked)" />
            {{ c.label }}
          </label>
          <p class="mt-1 mb-0 text-xs text-text-3">These become the routes of its configuration and allow entries in the access matrix.</p>
        </fieldset>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="label" :for="`${uid}-dns`">DNS server of the client</label>
            <select :id="`${uid}-dns`" v-model="form.dns" class="input">
              <option value="gateway">The gateway (DNS faults apply)</option>
              <option value="none">None</option>
              <option value="custom">Other servers</option>
            </select>
          </div>
          <div v-if="form.dns === 'custom'">
            <label class="label" :for="`${uid}-dns-servers`">Servers</label>
            <input
              :id="`${uid}-dns-servers`"
              v-model="form.dnsServers"
              class="input mono"
              :aria-invalid="!!shown('dnsServers')"
              :aria-describedby="shown('dnsServers') ? `${uid}-dns-error` : undefined"
            />
            <p v-if="shown('dnsServers')" :id="`${uid}-dns-error`" class="field-error">{{ shown('dnsServers') }}</p>
          </div>
          <div>
            <label class="label" :for="`${uid}-keepalive`">Keepalive</label>
            <input
              :id="`${uid}-keepalive`"
              v-model="form.keepalive"
              class="input mono"
              :aria-invalid="!!shown('keepalive')"
              :aria-describedby="shown('keepalive') ? `${uid}-keepalive-error` : undefined"
            />
            <p v-if="shown('keepalive')" :id="`${uid}-keepalive-error`" class="field-error">{{ shown('keepalive') }}</p>
          </div>
        </div>
        <fieldset class="m-0 border-0 p-0">
          <legend class="label">Keys</legend>
          <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.keyMode" type="radio" value="generated" /> The gateway generates the key pair (complete export)</label>
          <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.keyMode" type="radio" value="provided" /> The client has its own key: give its public key</label>
          <div v-if="form.keyMode === 'provided'" class="mt-2">
            <label class="label" :for="`${uid}-pub`">Public key</label>
            <input
              :id="`${uid}-pub`"
              v-model="form.publicKey"
              class="input mono"
              :aria-invalid="!!shown('publicKey')"
              :aria-describedby="shown('publicKey') ? `${uid}-pub-error` : undefined"
            />
            <p v-if="shown('publicKey')" :id="`${uid}-pub-error`" class="field-error">{{ shown('publicKey') }}</p>
          </div>
          <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.presharedKey" type="checkbox" /> A preshared key as well</label>
          <label v-if="form.keyMode === 'generated'" class="flex items-center gap-2 py-0.5 text-sm">
            <input v-model="form.exportOnce" type="checkbox" /> Export once: the gateway deletes the private key after the first download
          </label>
        </fieldset>
      </div>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="add-client-preview">
        <div class="mb-1 text-xs text-text-3">What changes</div>
        Client <strong>{{ form.name || '…' }}</strong> is added to {{ wg.name }} with the tunnel address
        <span class="mono">{{ form.address || '…' }}</span> in a new revision based on revision {{ draft.base ?? state.data.value?.active_revision }}. After the apply,
        its configuration and QR code are in the client list.
        <span v-if="management" class="mt-1 block text-fault-text">{{ wg.name }} is a management network: the apply waits for your confirmation, and the client is listed once it is confirmed.</span>
      </div>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; add the client after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
