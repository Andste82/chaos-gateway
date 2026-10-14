<script setup lang="ts">
// Add or edit a routing protocol (plan §2.2.2; P3-M14-01): BGP, OSPF or Babel on a WireGuard link, what
// it announces (networks, clients, further prefixes), the import filter (allowed prefixes, the default
// route, the most prefixes it takes), the settings of its type (the BGP neighbor and its AS and timers,
// the OSPF area, timers and cost, the Babel hello interval and cost) and a custom BIRD snippet, which the
// UI marks as unmanaged. Each check stands under its field; the protocol goes into the configuration draft
// and the preview-and-apply drawer opens. A protocol the gateway refused opens again here with the
// gateway's messages under their fields, until the form is changed.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { WireGuardNetwork } from '../../api/generated/model';
import { useGatewayState } from '../../composables/gateway';
import { newId } from '../../lib/ids';
import { subnetText } from '../../lib/networks';
import {
  emptyProtocolForm,
  formFromProtocol,
  protocolFieldOf,
  protocolFormErrors,
  protocolFromForm,
  type ProtocolField,
  type ProtocolForm,
} from '../../lib/routingForm';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import TextField from '../ui/TextField.vue';

const props = defineProps<{ protocol?: string }>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();

const form = ref<ProtocolForm>(emptyProtocolForm());
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const refused = ref<Record<string, string>>({});
let refusedForm = '';

const before = computed(() => (props.protocol ? draft.effectiveRouting.protocols?.[props.protocol] : undefined));
const links = computed(() =>
  Object.entries(draft.effectiveNetworks)
    .filter(([, n]) => n.type === 'wireguard' && (n as WireGuardNetwork).kind === 'link')
    .map(([id, n]) => ({ id, name: n.name, address: n.address })),
);
const others = computed(() =>
  Object.entries(draft.effectiveRouting.protocols ?? {})
    .filter(([id]) => id !== props.protocol)
    .map(([, p]) => ({ name: p.name, type: p.type, link: p.link })),
);

/** What a protocol can announce: the networks and the clients of the hubs. */
const announceChoices = computed(() => {
  const out: { key: string; label: string }[] = [];
  for (const [id, n] of Object.entries(draft.effectiveNetworks)) {
    if (n.type === 'wireguard' && (n as WireGuardNetwork).kind === 'link') continue;
    out.push({ key: `network:${id}`, label: `${n.name} (${subnetText(n.address)})` });
  }
  for (const [id, n] of Object.entries(draft.effectiveNetworks)) {
    if (n.type !== 'wireguard' || (n as WireGuardNetwork).kind !== 'hub') continue;
    for (const [cid, c] of Object.entries(draft.effectiveClients(id))) {
      if (c.client_networks?.length) out.push({ key: `client:${cid}`, label: `client ${c.name} of ${n.name} (${c.client_networks.join(', ')})` });
    }
  }
  return out;
});

watch(open, (o) => {
  if (!o) return;
  form.value = before.value ? formFromProtocol(before.value) : emptyProtocolForm(links.value[0]?.id ?? '');
  if (!before.value) form.value.name = links.value[0] ? `${links.value[0].name}-bgp` : '';
  refused.value = (props.protocol && draft.serverErrors[props.protocol]) || {};
  refusedForm = JSON.stringify(form.value);
  tried.value = Object.keys(refused.value).length > 0;
  error.value = '';
});

const types = [
  { value: 'bgp', label: 'BGP' },
  { value: 'ospf', label: 'OSPF' },
  { value: 'babel', label: 'Babel' },
];
const type = computed({
  get: () => form.value.type,
  set: (t: string) => {
    // a proposed name follows the type
    const link = links.value.find((l) => l.id === form.value.link);
    if (!before.value && link && form.value.name === `${link.name}-${form.value.type}`) form.value.name = `${link.name}-${t}`;
    form.value.type = t as ProtocolForm['type'];
  },
});

const formErrors = computed(() => protocolFormErrors(form.value, { links: links.value, others: others.value }));
const server = computed(() => {
  const byField: Partial<Record<ProtocolField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = protocolFieldOf(pointer);
    if (f) byField[f] = message;
    else general.push(message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
function shown(f: ProtocolField): string | undefined {
  return tried.value ? errors.value[f] : undefined;
}
const needsAsn = computed(() => form.value.type === 'bgp' && draft.effectiveRouting.asn === undefined);

function toggleAnnounce(key: string, on: boolean) {
  const rest = form.value.announce.filter((k) => k !== key);
  form.value.announce = on ? [...rest, key] : rest;
}

const title = computed(() => (props.protocol ? `Edit ${before.value?.name ?? 'protocol'}` : 'Add routing protocol'));
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
              body: { routing: { protocols: { [props.protocol ?? '<new uuid>']: protocolFromForm(form.value, before.value) } } },
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
  if (!valid.value || needsAsn.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty) draft.track(await getActiveRevision());
    draft.setProtocol(props.protocol ?? newId(), protocolFromForm(form.value, props.protocol ? draft.effectiveRouting.protocols?.[props.protocol] : undefined));
    open.value = false;
    draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" description="Dynamic routing with BIRD on a WireGuard link; learned routes only enter Chaos Gateway's tables." width="680px">
    <form class="flex flex-col gap-4" novalidate data-testid="protocol-form" @submit.prevent="submit">
      <SegmentedControl v-model="type" :options="types" label="Protocol" />
      <p v-if="shown('type')" class="field-error" role="alert">{{ shown('type') }}</p>
      <div class="grid grid-cols-2 gap-4">
        <TextField v-model="form.name" label="Name" :error="shown('name')" />
        <div>
          <label class="label" for="protocol-link">Link</label>
          <select id="protocol-link" v-model="form.link" class="input" :aria-invalid="!!shown('link')">
            <option v-if="!links.length" value="">No WireGuard link yet</option>
            <option v-for="l in links" :key="l.id" :value="l.id">{{ l.name }} ({{ subnetText(l.address) }})</option>
          </select>
          <p v-if="shown('link')" class="field-error">{{ shown('link') }}</p>
        </div>
      </div>

      <div v-if="form.type === 'bgp'" class="grid grid-cols-2 gap-4" data-testid="bgp-fields">
        <TextField v-model="form.neighborAsn" label="Neighbor AS" mono placeholder="65002" :error="shown('neighborAsn')" />
        <TextField v-model="form.neighborAddress" label="Neighbor address" mono :error="shown('neighborAddress')" hint="Empty: the peer of the link." />
        <TextField v-model="form.holdTime" label="Hold time" mono placeholder="90s" :error="shown('holdTime')" />
        <TextField v-model="form.keepaliveTime" label="Keepalive time" mono placeholder="30s" :error="shown('keepaliveTime')" />
        <label class="col-span-2 flex items-center gap-2 text-sm"><input v-model="form.passive" type="checkbox" /> Passive: wait for the neighbor to connect</label>
        <p v-if="tried && needsAsn" class="field-error col-span-2" role="alert" data-testid="protocol-needs-asn">BGP needs the local AS number: set it in the routing settings first.</p>
      </div>
      <div v-if="form.type === 'ospf'" class="grid grid-cols-2 gap-4" data-testid="ospf-fields">
        <TextField v-model="form.area" label="Area" mono placeholder="0.0.0.0" :error="shown('area')" />
        <TextField v-model="form.cost" label="Cost" mono placeholder="10" :error="shown('cost')" />
        <TextField v-model="form.helloInterval" label="Hello interval" mono placeholder="10s" :error="shown('helloInterval')" />
        <TextField v-model="form.deadInterval" label="Dead interval" mono placeholder="40s" :error="shown('deadInterval')" />
      </div>
      <div v-if="form.type === 'babel'" class="grid grid-cols-2 gap-4" data-testid="babel-fields">
        <TextField v-model="form.babelHello" label="Hello interval" mono placeholder="4s" :error="shown('babelHello')" />
        <TextField v-model="form.rxcost" label="Receive cost" mono placeholder="96" :error="shown('rxcost')" />
      </div>

      <fieldset class="m-0 border-0 p-0">
        <legend class="label">Announced to the neighbor</legend>
        <label v-for="c in announceChoices" :key="c.key" class="flex items-center gap-2 py-0.5 text-sm">
          <input type="checkbox" :checked="form.announce.includes(c.key)" @change="toggleAnnounce(c.key, ($event.target as HTMLInputElement).checked)" />
          {{ c.label }}
        </label>
        <TextField v-model="form.announceCidrs" class="mt-2" label="Further prefixes" mono placeholder="10.80.0.0/24" :error="shown('announceCidrs')" />
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-3 border-0 p-0">
        <legend class="label">Import filter (the management, uplink and gateway prefixes are always rejected)</legend>
        <TextField
          v-model="form.prefixes"
          label="Allowed prefixes (one per line; le 24 also takes longer ones up to /24)"
          mono
          :rows="2"
          placeholder="10.70.0.0/16 le 24"
          :error="shown('prefixes')"
          hint="Empty: any prefix that is not forbidden."
        />
        <div class="grid grid-cols-2 gap-4">
          <TextField v-model="form.maxPrefixes" label="Most prefixes taken" mono placeholder="100" :error="shown('maxPrefixes')" />
          <label class="mt-6 flex items-center gap-2 text-sm"><input v-model="form.allowDefault" type="checkbox" /> Accept the default route</label>
        </div>
      </fieldset>

      <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" /> The protocol runs</label>
      <TextField
        v-model="form.customSnippet"
        label="Custom BIRD snippet (unmanaged)"
        mono
        :rows="3"
        :error="shown('customSnippet')"
        hint="Raw BIRD configuration inside the protocol block, for what the model does not cover; checked with bird -p."
      />

      <p v-if="server.general.length" class="field-error" role="alert" data-testid="protocol-form-refused">The gateway refused the protocol: {{ server.general.join(' ') }}</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || (tried && (!valid || needsAsn))">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
