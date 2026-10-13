<script setup lang="ts">
// "Explain" on the faults screen (plan §2.17, §2.4): for a device (or a source address) and a
// destination, which access rule decides and which fault wins in each family, with the faults that
// lost and why (`GET /explain`).
import { computed, ref, useId } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { explain } from '../../api/generated/hooks/observability/observability';
import type { Explanation, ExplainParams, FaultRef, Protocol } from '../../api/generated/model';
import { useNames } from '../../composables/gateway';
import { profileTitle } from '../../lib/profiles';
import { useApiPanelStore } from '../../stores/apiPanel';
import AccessChip from '../ui/AccessChip.vue';
import SectionCard from '../ui/SectionCard.vue';

const { devices } = useNames();
const apiPanel = useApiPanelStore();
const uid = useId();

const device = ref('');
const dst = ref('');
const protocol = ref<Protocol>('any');
const port = ref('');
const result = ref<Explanation>();
const error = ref('');
const busy = ref(false);

const portError = computed(() => {
  if (!port.value.trim()) return '';
  const n = Number(port.value);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return 'A port from 1 to 65535.';
  return protocol.value === 'tcp' || protocol.value === 'udp' ? '' : 'A port needs the protocol TCP or UDP.';
});
const valid = computed(() => !!device.value && !!dst.value.trim() && !portError.value);

const layers: Record<string, string> = {
  gateway_protection: "the gateway's protection of its own ports",
  overlay_rule: 'an overlay rule',
  config_rule: 'a configured rule',
  access_matrix: 'the access matrix',
};

const families: Record<string, string> = { impairment: 'Impairment', mtu: 'MTU', dns: 'DNS', tls: 'TLS', dhcp: 'DHCP', tunnel: 'Tunnel' };

function refText(r: FaultRef): string {
  const what = r.profile?.name ? `profile ${profileTitle(r.profile.name)}` : r.name ? `fault ${r.name}` : r.layer === 'overlay' ? 'an overlay' : 'a configured fault';
  return `${what}${r.summary ? ` (${r.summary})` : ''}`;
}

async function run() {
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  const params: ExplainParams = { device: device.value, dst: dst.value.trim() };
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
  <SectionCard title="Explain">
    <form class="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1.3fr)_110px_90px_auto] items-end gap-3" novalidate @submit.prevent="run">
      <div>
        <label class="label" :for="`${uid}-dev`">Device</label>
        <select :id="`${uid}-dev`" v-model="device" class="input">
          <option value="" disabled>Choose a device</option>
          <option v-for="d in devices.data.value ?? []" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
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
    <p v-if="portError" :id="`${uid}-port-error`" class="field-error m-0">{{ portError }}</p>
    <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>

    <div v-if="result" class="flex flex-col gap-2 text-sm" data-testid="explain-result" aria-live="polite">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-text-2">Access</span>
        <AccessChip :action="result.access.verdict" />
        <span>by {{ layers[result.access.layer] ?? result.access.layer }}<template v-if="result.access.reason"> — {{ result.access.reason }}</template></span>
      </div>
      <p v-if="result.access.verdict !== 'allow'" class="m-0 text-[13px] text-text-2">The faults below take effect only for traffic that is allowed.</p>
      <p v-if="result.faults.length === 0" class="m-0 text-text-2">No fault applies: the traffic passes unimpaired.</p>
      <div v-for="(f, i) in result.faults" :key="i" class="rounded-md border border-border bg-surface-2 px-3 py-2" data-testid="explain-family">
        <div>
          <span class="font-medium">{{ families[f.family] ?? f.family }}{{ f.tunnel ? ` · ${f.tunnel}` : '' }}:</span>
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
