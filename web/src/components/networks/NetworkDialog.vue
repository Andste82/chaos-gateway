<script setup lang="ts">
// Add or edit a network (plan §2.2, §2.2.1, §2.17 "Networks"; P3-M14-01): a local test network with its
// gateway address and subnet, one or several ports (the physical interfaces of its bridge), DHCP (pools,
// lease time, options) and DNS (static entries, redirect, DoT) and downstream routes; a WireGuard hub or
// link with its role, tunnel subnet, listen port, MTU, endpoint and, for a link, the remote side and its
// static routes. Each check stands under its field and "Preview & apply" waits until the form is valid
// (plan §2.17 "Validation"). The network goes into the configuration draft and the preview-and-apply drawer
// opens, like every configuration change; a WireGuard network with the role management makes the change
// lockout-relevant, so the apply waits for confirmation. A network of the draft the gateway refused opens
// again here with the gateway's messages under the fields they are about, until the form is changed.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import { getListHostInterfacesQueryKey, listHostInterfaces } from '../../api/generated/hooks/system/system';
import type { HostInterface, InterfaceRef, Network, WireGuardNetwork } from '../../api/generated/model';
import { fetchAll, useGatewayState } from '../../composables/gateway';
import { newId } from '../../lib/ids';
import { createMergePatch } from '../../lib/mergePatch';
import {
  advancedFields,
  emptyNetworkForm,
  formFromNetwork,
  hostPortKey,
  kindOf,
  networkFieldOf,
  networkFormErrors,
  networkFromForm,
  peerOf,
  portKey,
  proposeAddress,
  proposeListenPort,
  type NetworkField,
  type NetworkForm,
  type NetworkKind,
} from '../../lib/networkForm';
import { subnetText } from '../../lib/networks';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{
  /** The network to edit, by id; none: the dialog adds one. */
  network?: string;
}>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ saved: [id: string] }>();

const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();
const hosts = useQuery({
  queryKey: [...getListHostInterfacesQueryKey(), 'all'],
  queryFn: ({ signal }) => fetchAll<HostInterface>((cursor) => listHostInterfaces({ limit: 500, cursor }, { signal }), signal),
  enabled: open,
});

const form = ref<NetworkForm>(emptyNetworkForm('lan'));
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const advanced = ref(false);
/** What the gateway refused of the network being fixed, by the pointer below the network, and the form it refused. */
const refused = ref<Record<string, string>>({});
let refusedForm = '';

/** The network as the draft has it (the one edited). */
const before = computed<Network | undefined>(() => (props.network ? draft.effectiveNetworks[props.network] : undefined));
const others = computed(() =>
  Object.entries(draft.effectiveNetworks)
    .filter(([id]) => id !== props.network)
    .map(([, n]) => ({
      name: n.name,
      address: n.address,
      kind: kindOf(n),
      listenPort: n.type === 'wireguard' ? (n as WireGuardNetwork).listen_port : undefined,
      ports: n.type === 'lan' ? n.interfaces.map(portKey) : [],
    })),
);
/** The interfaces of the uplink and the management network, by name and MAC. */
const reserved = computed(() => {
  const refs: (InterfaceRef | undefined)[] = [draft.baseConfig?.uplink.interface, draft.baseConfig?.management.interface];
  const out: string[] = [];
  for (const r of refs) {
    if (r?.name) out.push(r.name);
    if (r?.mac) {
      out.push(r.mac);
      const h = hosts.data.value?.find((x) => x.mac === r.mac);
      if (h) out.push(h.name);
    }
  }
  return out;
});

function propose(kind: NetworkKind): NetworkForm {
  const taken = Object.values(draft.effectiveNetworks).map((n) => n.address);
  const ports = Object.values(draft.effectiveNetworks).flatMap((n) => (n.type === 'wireguard' ? [(n as WireGuardNetwork).listen_port] : []));
  return emptyNetworkForm(kind, proposeAddress(kind, taken), kind === 'lan' ? '' : proposeListenPort(ports));
}

watch(open, (o) => {
  if (!o) return;
  form.value = before.value ? formFromNetwork(before.value) : propose('lan');
  refused.value = (props.network && draft.serverErrors[props.network]) || {};
  refusedForm = JSON.stringify(form.value);
  tried.value = Object.keys(refused.value).length > 0;
  advanced.value = hiddenErrors.value.length > 0;
  error.value = '';
});

const kinds = [
  { value: 'lan', label: 'Test network' },
  { value: 'hub', label: 'WireGuard hub' },
  { value: 'link', label: 'WireGuard link' },
];
const kind = computed({
  get: () => form.value.kind,
  set: (k: string) => {
    const name = form.value.name;
    form.value = { ...propose(k as NetworkKind), name };
  },
});
// a link's peer follows its transfer network while it is the other address of the one before
watch(
  () => form.value.address,
  (a, was) => {
    if (form.value.kind === 'link' && (!form.value.peerAddress || form.value.peerAddress === peerOf(was ?? ''))) form.value.peerAddress = peerOf(a);
  },
);

const formErrors = computed(() => networkFormErrors(form.value, { others: others.value, reserved: reserved.value }));
const dhcpFields: NetworkField[] = ['leaseTime', 'pools', 'router', 'dhcpDns', 'domain', 'ntp', 'customOptions'];
/** The gateway's messages while the form is the one it refused: by field, and those no field shows. */
const server = computed(() => {
  const byField: Partial<Record<NetworkField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = networkFieldOf(pointer);
    // the DHCP fields are hidden while DHCP is off: their messages stand with those no field shows
    if (f && !(dhcpFields.includes(f) && !form.value.dhcpEnabled)) byField[f] = message;
    else general.push(f ? `DHCP: ${message}` : message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
// the form the gateway refused is not sent again unchanged; a change of it may be the fix
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
function shown(f: NetworkField): string | undefined {
  return tried.value || f === 'address' ? errors.value[f] : undefined;
}
/** The messages shown for fields under "Advanced": they open it, so no check stays out of sight. */
const hiddenErrors = computed(() => advancedFields(form.value.kind).filter((f) => shown(f)));
watch(
  () => hiddenErrors.value.length > 0,
  (h) => {
    if (h) advanced.value = true;
  },
);

/** The ports of the network as the draft has it, by key (a MAC-only port by its MAC). */
const had = computed(() => (before.value?.type === 'lan' ? before.value.interfaces.map(portKey) : []));
/** The ports to choose from: the host's interfaces, and those the network has that the host does not show. */
const portChoices = computed(() => {
  const hostList = hosts.data.value ?? [];
  const out = hostList.map((h) => {
    const reservedBy = reserved.value.includes(h.name) || reserved.value.includes(h.mac);
    const other = others.value.find((o) => o.ports.includes(h.name) || o.ports.includes(h.mac));
    const notes = [h.link_up === false ? 'link down' : h.link_up ? 'link up' : '', h.os_managed ? 'configured by the OS' : ''].filter(Boolean);
    return {
      key: hostPortKey(h, form.value.ports, had.value),
      label: h.name,
      detail: [h.mac, h.driver, ...notes].filter(Boolean).join(' · '),
      disabled: reservedBy || !!other,
      why: reservedBy ? 'uplink or management, owned by the OS' : other ? `port of ${other.name}` : '',
    };
  });
  for (const p of form.value.ports) {
    if (!hostList.some((h) => h.name === p || h.mac === p)) out.push({ key: p, label: p, detail: 'not present on the host', disabled: false, why: '' });
  }
  return out;
});
function togglePort(key: string, on: boolean) {
  const rest = form.value.ports.filter((p) => p !== key);
  form.value.ports = on ? [...rest, key] : rest;
}

const management = computed(() => form.value.role === 'management' || (before.value?.type === 'wireguard' && (before.value as WireGuardNetwork).role === 'management'));
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);
const built = computed(() => networkFromForm(form.value, before.value, hosts.data.value ?? []));
const title = computed(() => (props.network ? `Edit ${before.value?.name ?? 'network'}` : 'Add network'));

/** What the dialog would put into the configuration, for the `</> API` panel. */
watch(
  [open, form],
  ([o]) => {
    apiPanel.draft = o
      ? {
          title: title.value,
          calls: [
            {
              method: 'POST',
              path: '/revisions',
              headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base ?? state.data.value?.active_revision}"` },
              body: { networks: { [props.network ?? '<new uuid>']: createMergePatch(props.network ? draft.baseNetwork(props.network) : undefined, built.value) ?? {} } },
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
    const id = props.network ?? newId();
    draft.setNetwork(id, networkFromForm(form.value, props.network ? draft.effectiveNetworks[props.network] : undefined, hosts.data.value ?? []));
    open.value = false;
    emit('saved', id);
    draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog
    v-model:open="open"
    :title="title"
    :description="network ? `${subnetText(before?.address)} · the change goes through the preview and the apply.` : 'A local test network or a WireGuard network; it goes through the preview and the apply.'"
    width="680px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="network-form" @submit.prevent="submit">
      <SegmentedControl v-if="!network" v-model="kind" :options="kinds" label="Kind of network" />
      <div class="grid grid-cols-2 gap-4">
        <TextField v-model="form.name" label="Name" placeholder="cameras" :error="shown('name')" />
        <TextField
          v-model="form.address"
          :label="form.kind === 'link' ? 'Gateway address (/31 transfer network)' : 'Gateway address and subnet'"
          mono
          :error="shown('address')"
          :hint="form.address ? `Subnet ${subnetText(form.address)}` : undefined"
        />
      </div>

      <template v-if="form.kind === 'lan'">
        <fieldset class="m-0 border-0 p-0" data-testid="network-ports">
          <legend class="label">Ports (physical interfaces of the network's bridge)</legend>
          <p v-if="hosts.isLoading.value" class="m-0 text-[13px] text-text-2">Loading the host's interfaces…</p>
          <p v-else-if="hosts.error.value" class="m-0 text-[13px] text-block-text">The host's interfaces are not loaded: {{ errorMessage(hosts.error.value) }}</p>
          <label v-for="c in portChoices" :key="c.key" class="flex items-center gap-2 py-0.5 text-sm" :class="c.disabled ? 'text-text-3' : ''">
            <input
              type="checkbox"
              :checked="form.ports.includes(c.key)"
              :disabled="c.disabled && !form.ports.includes(c.key)"
              @change="togglePort(c.key, ($event.target as HTMLInputElement).checked)"
            />
            <span class="mono">{{ c.label }}</span>
            <span class="text-xs text-text-3">{{ c.detail }}<template v-if="c.why"> · {{ c.why }}</template></span>
          </label>
          <p v-if="shown('ports')" class="field-error" role="alert">{{ shown('ports') }}</p>
          <p v-else class="mt-1 mb-0 text-xs text-text-3">Several ports make one network: the devices on all of them share the subnet.</p>
        </fieldset>
        <fieldset class="m-0 flex flex-col gap-3 border-0 p-0">
          <legend class="label">DHCP</legend>
          <label class="flex items-center gap-2 text-sm"><input v-model="form.dhcpEnabled" type="checkbox" /> The gateway hands out addresses (DHCP)</label>
          <div v-if="form.dhcpEnabled" class="grid grid-cols-2 gap-4">
            <TextField v-model="form.pools" label="Pools" mono placeholder="10.30.0.100-10.30.0.199" :error="shown('pools')" hint="Empty: the second half of the subnet." />
            <TextField v-model="form.leaseTime" label="Lease time" mono placeholder="1h" :error="shown('leaseTime')" />
          </div>
        </fieldset>
        <fieldset class="m-0 flex flex-col gap-1 border-0 p-0">
          <legend class="label">DNS</legend>
          <label class="flex items-center gap-2 text-sm"><input v-model="form.redirectDns" type="checkbox" /> Redirect DNS to other resolvers to the gateway's DNS proxy</label>
          <label class="flex items-center gap-2 text-sm"><input v-model="form.blockDot" type="checkbox" /> Block DNS over TLS (TCP 853)</label>
        </fieldset>
      </template>

      <template v-else>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="label" for="network-role">Role</label>
            <select id="network-role" v-model="form.role" class="input">
              <option value="test">Test: untrusted, like a test network</option>
              <option value="management">Management: may reach the UI and the API</option>
            </select>
          </div>
          <TextField v-model="form.listenPort" label="Listen port (UDP)" mono :error="shown('listenPort')" />
        </div>
        <div v-if="form.kind === 'link'" class="grid grid-cols-2 gap-4" data-testid="link-peer-fields">
          <TextField v-model="form.peerAddress" label="Remote side's address" mono :error="shown('peerAddress')" />
          <TextField
            v-model="form.peerEndpoint"
            label="Remote endpoint"
            mono
            placeholder="203.0.113.40:51821"
            :error="shown('peerEndpoint')"
            hint="Set: the gateway connects (behind CGNAT). Empty: the remote side connects."
          />
          <TextField v-model="form.routes" label="Static routes via the link" mono placeholder="10.60.0.0/24" :error="shown('routes')" hint="Or let a routing protocol learn them." />
        </div>
      </template>

      <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="advanced" @click="advanced = !advanced">
        {{ advanced ? 'Hide advanced' : form.kind === 'lan' ? 'Advanced: DHCP options, static DNS entries, routes, masquerade' : 'Advanced: endpoint, MTU, keys, masquerade' }}
      </button>
      <div v-if="advanced" class="flex flex-col gap-4">
        <TextField v-model="form.description" label="Description" :error="shown('description')" />
        <template v-if="form.kind === 'lan'">
          <div v-if="form.dhcpEnabled" class="grid grid-cols-2 gap-4">
            <TextField v-model="form.router" label="DHCP router (option 3)" mono :error="shown('router')" hint="Empty: the gateway address." />
            <TextField v-model="form.dhcpDns" label="DHCP DNS servers (option 6)" mono :error="shown('dhcpDns')" hint="Empty: the gateway (DNS faults apply)." />
            <TextField v-model="form.domain" label="Domain (option 15)" mono :error="shown('domain')" />
            <TextField v-model="form.ntp" label="NTP servers (option 42)" mono :error="shown('ntp')" />
            <TextField v-model="form.customOptions" class="col-span-2" label="Custom options (code and value per line)" mono :rows="2" placeholder="66 tftp.lab" :error="shown('customOptions')" />
          </div>
          <TextField
            v-model="form.staticEntries"
            label="Static DNS entries (name and addresses per line)"
            mono
            :rows="2"
            placeholder="broker.lab 10.30.0.5"
            :error="shown('staticEntries')"
          />
          <TextField
            v-model="form.routes"
            label="Downstream routes (devices behind another router)"
            mono
            :rows="2"
            placeholder="10.40.0.0/24 via 10.30.0.254"
            :error="shown('routes')"
          />
        </template>
        <template v-else>
          <div class="grid grid-cols-2 gap-4">
            <TextField v-model="form.endpoint" label="Public endpoint" mono placeholder="gw.example.org:51820" :error="shown('endpoint')" hint="Empty: the uplink address and the listen port." />
            <TextField v-model="form.mtu" label="MTU" mono placeholder="1420" :error="shown('mtu')" />
          </div>
          <template v-if="form.kind === 'link'">
            <TextField v-model="form.peerKeepalive" label="Keepalive" mono placeholder="25s" :error="shown('peerKeepalive')" />
            <fieldset class="m-0 border-0 p-0">
              <legend class="label">Keys of the remote side</legend>
              <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.peerKeyMode" type="radio" value="generated" /> The gateway generates the key pair (export of the remote configuration)</label>
              <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.peerKeyMode" type="radio" value="provided" /> The remote side has its own key: give its public key</label>
              <TextField v-if="form.peerKeyMode === 'provided'" v-model="form.peerPublicKey" class="mt-2" label="Public key" mono :error="shown('peerPublicKey')" />
              <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.peerPresharedKey" type="checkbox" /> A preshared key as well</label>
              <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="form.peerEnabled" type="checkbox" /> The link is enabled</label>
            </fieldset>
          </template>
        </template>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.nat" type="checkbox" /> Masquerade towards the uplink</label>
      </div>

      <p v-if="!advanced && hiddenErrors.length" class="field-error" role="alert" data-testid="network-form-advanced-errors">Fix the fields under Advanced.</p>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="network-form-preview">
        <div class="mb-1 text-xs text-text-3">What changes</div>
        {{ network ? 'Network' : form.kind === 'lan' ? 'Test network' : `WireGuard ${form.kind}` }} <strong>{{ form.name || '…' }}</strong>
        {{ network ? 'is changed' : 'is added' }}<template v-if="form.address"> with the subnet <span class="mono">{{ subnetText(form.address) }}</span></template
        ><template v-if="form.kind === 'lan' && form.ports.length"> on {{ form.ports.length === 1 ? 'port' : 'ports' }} <span class="mono">{{ form.ports.join(', ') }}</span></template>
        in a new revision based on revision {{ draft.base ?? state.data.value?.active_revision }}.
        <span v-if="management" class="mt-1 block text-fault-text" data-testid="network-form-lockout"
          >A management network can cut you off from the gateway: the apply waits for your confirmation, and without it the gateway rolls back.</span
        >
      </div>
      <p v-if="server.general.length" class="field-error" role="alert" data-testid="network-form-refused">The gateway refused the network: {{ server.general.join(' ') }}</p>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; change the network after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
