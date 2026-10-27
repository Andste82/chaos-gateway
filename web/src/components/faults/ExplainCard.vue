<script setup lang="ts">
// "Explain" on the faults screen (plan §2.17, §2.4): for a device (or a source address) and a
// destination, which access rule decides and which fault wins in each family, with the faults that
// lost and why (`GET /explain`). The source is a device, or (M26, `docs/open-items.md` P31-M35a-08) an
// address behind the uplink or the management network: its destination defaults to the entry side's address,
// so a connection that a port forward (M35a) carries is explained from the peer's address. A connection picked
// in the flow view arrives in the address of the screen (`?explain=1&device=|src=&dst=&protocol=&port=`) and
// is explained at once.
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue';
import { useRoute } from 'vue-router';
import { errorMessage } from '../../api/fetcher';
import { useGetUplink } from '../../api/generated/hooks/config/config';
import { explain } from '../../api/generated/hooks/observability/observability';
import { useListHostInterfaces } from '../../api/generated/hooks/system/system';
import type { Explanation, ExplainParams, FaultRef, Protocol } from '../../api/generated/model';
import { useNames } from '../../composables/gateway';
import { isIPv4 } from '../../lib/diagnostics';
import { tunnelOf } from '../../lib/faultForm';
import { describeTunnel } from '../../lib/faults';
import { explainFromQuery } from '../../lib/flows';
import { profileTitle } from '../../lib/profiles';
import { useApiPanelStore } from '../../stores/apiPanel';
import AccessChip from '../ui/AccessChip.vue';
import SectionCard from '../ui/SectionCard.vue';

const { devices, names } = useNames();
const apiPanel = useApiPanelStore();
const route = useRoute();
const uid = useId();

type Source = 'device' | 'uplink' | 'management' | 'address';
const sourceLabels: Record<Source, string> = {
  device: 'A device',
  uplink: 'An address behind the uplink',
  management: 'An address behind the management network',
  address: 'Another address',
};

const source = ref<Source>('device');
const device = ref('');
const src = ref('');
const dst = ref('');
const protocol = ref<Protocol>('any');
const port = ref('');
const result = ref<Explanation>();
const error = ref('');
const busy = ref(false);
const card = ref<{ $el: HTMLElement }>();

// ---- the entry sides: where a peer behind the uplink or the management network connects to
const uplink = useGetUplink();
const hostInterfaces = useListHostInterfaces({ limit: 500 });
/** The address of the uplink interface, the entry side of the forwards from the uplink; empty while it has none. */
const uplinkAddress = computed(() => uplink.data.value?.status.address?.split('/')[0] ?? '');
const managementInterface = computed(() => hostInterfaces.data.value?.items.find((i) => i.assigned?.role === 'management'));
/**
 * The address of the management interface, the entry side of the forwards from the management network. A gateway whose
 * management interface is the uplink interface (two ports) has no management side of its own: it is not offered.
 */
const managementAddress = computed(() => managementInterface.value?.addresses?.[0]?.split('/')[0] ?? '');
const sources = computed<Source[]>(() => (managementInterface.value || source.value === 'management' ? ['device', 'uplink', 'management', 'address'] : ['device', 'uplink', 'address']));
const entryAddress = computed(() => (source.value === 'uplink' ? uplinkAddress.value : source.value === 'management' ? managementAddress.value : ''));

// the destination of an address behind an entry side defaults to the entry side's address, until it is edited
let lastDefault = '';
watch(
  [source, entryAddress],
  () => {
    if (source.value !== 'uplink' && source.value !== 'management') return;
    if (!dst.value.trim() || dst.value === lastDefault) dst.value = entryAddress.value;
    lastDefault = entryAddress.value;
  },
  { immediate: true },
);

const srcError = computed(() => {
  if (source.value === 'device' || !src.value.trim()) return '';
  return isIPv4(src.value.trim()) ? '' : 'An IPv4 address, for example 198.51.100.7.';
});
const portError = computed(() => {
  if (!port.value.trim()) return '';
  const n = Number(port.value);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return 'A port from 1 to 65535.';
  return protocol.value === 'tcp' || protocol.value === 'udp' ? '' : 'A port needs the protocol TCP or UDP.';
});
const valid = computed(() => (source.value === 'device' ? !!device.value : !!src.value.trim() && !srcError.value) && !!dst.value.trim() && !portError.value);

const layers: Record<string, string> = {
  gateway_protection: "the gateway's protection of its own ports",
  overlay_rule: 'an overlay rule',
  config_rule: 'a configured rule',
  access_matrix: 'the access matrix',
  port_forward: 'the port forward (no rule decided)',
};

const families: Record<string, string> = { impairment: 'Impairment', mtu: 'MTU', dns: 'DNS', tls: 'TLS', dhcp: 'DHCP', tunnel: 'Tunnel' };

/**
 * The family's heading: a tunnel family names its tunnel as every other place does ("Tunnel of rA",
 * "Tunnel of link site-b"), not by the API's key `client:<id>` / `link:<id>`.
 */
function familyText(f: { family: string; tunnel?: string }): string {
  if (f.tunnel) return describeTunnel(tunnelOf(f.tunnel), names.value);
  return families[f.family] ?? f.family;
}

function refText(r: FaultRef): string {
  const what = r.profile?.name ? `profile ${profileTitle(r.profile.name)}` : r.name ? `fault ${r.name}` : r.layer === 'overlay' ? 'an overlay' : 'a configured fault';
  return `${what}${r.summary ? ` (${r.summary})` : ''}`;
}

// ---- a connection picked in the flow view: the address of the screen fills the form and runs it
const prefill = explainFromQuery(route.query);
if (prefill) {
  source.value = prefill.source === 'device' ? 'device' : 'address';
  device.value = prefill.device;
  src.value = prefill.src;
  dst.value = prefill.dst;
  protocol.value = prefill.protocol as Protocol;
  port.value = prefill.port;
}
// a peer's address that connects to an entry side's address is an address behind that side, once the sides' addresses are known;
// the destination stays what was asked for
let settled = !prefill || prefill.source === 'device';
watch(
  [uplinkAddress, managementAddress, () => uplink.isPending.value || hostInterfaces.isPending.value],
  ([up, mgmt, loading]) => {
    if (settled || !prefill) return;
    if (up && prefill.dst === up) source.value = 'uplink';
    else if (mgmt && prefill.dst === mgmt) source.value = 'management';
    else if (loading) return;
    settled = true;
  },
  { immediate: true },
);
onMounted(async () => {
  if (!prefill) return;
  await run();
  await nextTick();
  card.value?.$el?.scrollIntoView?.({ block: 'start' });
});

async function run() {
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  const params: ExplainParams = source.value === 'device' ? { device: device.value, dst: dst.value.trim() } : { src: src.value.trim(), dst: dst.value.trim() };
  if (protocol.value !== 'any') params.protocol = protocol.value;
  if (port.value.trim()) params.port = Number(port.value);
  try {
    result.value = await explain(params);
    const q = new URLSearchParams(Object.entries(params).map(([k, v]) => [k, String(v)]));
    apiPanel.record('Explain', [{ method: 'GET', path: `/explain?${q}` }]);
  } catch (e) {
    result.value = undefined;
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <SectionCard ref="card" title="Explain">
    <form class="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_110px_90px_auto] items-end gap-3" novalidate @submit.prevent="run">
      <div>
        <label class="label" :for="`${uid}-source`">Source</label>
        <select :id="`${uid}-source`" v-model="source" class="input" data-testid="explain-source">
          <option v-for="s in sources" :key="s" :value="s">{{ sourceLabels[s] }}</option>
        </select>
      </div>
      <div v-if="source === 'device'">
        <label class="label" :for="`${uid}-dev`">Device</label>
        <select :id="`${uid}-dev`" v-model="device" class="input">
          <option value="" disabled>Choose a device</option>
          <option v-for="d in devices.data.value ?? []" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
      </div>
      <div v-else>
        <label class="label" :for="`${uid}-src`">Source address</label>
        <input
          :id="`${uid}-src`"
          v-model="src"
          class="input mono"
          :placeholder="source === 'address' ? '10.10.0.99' : '198.51.100.7'"
          :aria-invalid="!!srcError"
          :aria-describedby="srcError ? `${uid}-src-error` : undefined"
        />
      </div>
      <div>
        <label class="label" :for="`${uid}-dst`">Destination</label>
        <input :id="`${uid}-dst`" v-model="dst" class="input mono" placeholder="203.0.113.10 or broker.example.com" />
      </div>
      <div>
        <label class="label" :for="`${uid}-proto`">Protocol</label>
        <select :id="`${uid}-proto`" v-model="protocol" class="input">
          <option value="any">Any</option>
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
          <option value="icmp">ICMP</option>
        </select>
      </div>
      <div>
        <label class="label" :for="`${uid}-port`">Port</label>
        <input
          :id="`${uid}-port`"
          v-model="port"
          class="input mono"
          inputmode="numeric"
          :aria-invalid="!!portError"
          :aria-describedby="portError ? `${uid}-port-error` : undefined"
        />
      </div>
      <button type="submit" class="btn btn-primary" :disabled="!valid || busy">{{ busy ? 'Explaining…' : 'Explain' }}</button>
    </form>
    <p v-if="source === 'uplink' || source === 'management'" class="m-0 text-[13px] text-text-3" data-testid="explain-entry-hint">
      A connection that a port forward carries comes from a peer behind the {{ source === 'uplink' ? 'uplink' : 'management network' }} to the gateway's address there
      <template v-if="entryAddress"
        >(<span class="mono">{{ entryAddress }}</span
        >)</template
      ><template v-else>(the gateway has no address there now: type the destination)</template>, on the external port of a forward: choose TCP or UDP and that port.
    </p>
    <p v-if="srcError" :id="`${uid}-src-error`" class="field-error m-0">{{ srcError }}</p>
    <p v-if="portError" :id="`${uid}-port-error`" class="field-error m-0">{{ portError }}</p>
    <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>

    <div v-if="result" class="flex flex-col gap-2 text-sm" data-testid="explain-result" aria-live="polite">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-text-2">Access</span>
        <AccessChip :action="result.access.verdict" />
        <span>by {{ layers[result.access.layer] ?? result.access.layer }}<template v-if="result.access.reason"> — {{ result.access.reason }}</template></span>
      </div>
      <p v-if="result.forward" class="m-0 rounded-md border border-border bg-surface-2 px-3 py-2 text-[13px]" data-testid="explain-forward">
        A forwarded connection: port forward {{ result.forward.name ?? result.forward.id }} from the
        {{ result.forward.entry === 'management' ? 'management network' : 'uplink' }} to
        <span class="mono">{{ result.forward.device?.name ?? result.forward.target_ip ?? 'its device' }}:{{ result.forward.target_port }}</span>.
        {{ result.forward.note }}
      </p>
      <p v-if="result.access.verdict !== 'allow'" class="m-0 text-[13px] text-text-2">The faults below take effect only for traffic that is allowed.</p>
      <p v-if="result.faults.length === 0" class="m-0 text-text-2">No fault applies: the traffic passes unimpaired.</p>
      <div v-for="(f, i) in result.faults" :key="i" class="rounded-md border border-border bg-surface-2 px-3 py-2" data-testid="explain-family">
        <div>
          <span class="font-medium">{{ familyText(f) }}:</span>
          {{ ' ' + (f.winner ? `${refText(f.winner)} wins` : 'nothing wins') }}
          <span v-if="f.winner" class="text-text-2">{{ ` (${f.winner.layer === 'overlay' ? 'overlay' : 'configuration'}${f.winner.level ? `, level ${f.winner.level}` : ''})` }}</span>
        </div>
        <ul v-if="f.overridden?.length" class="mt-1 mb-0 pl-5 text-[13px] text-text-2">
          <li v-for="(o, j) in f.overridden" :key="j">{{ refText(o) }} lost<template v-if="o.reason">: {{ o.reason }}</template></li>
        </ul>
      </div>
    </div>
  </SectionCard>
</template>
