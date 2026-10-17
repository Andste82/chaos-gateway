<script setup lang="ts">
// The export of a WireGuard link for its remote side (plan §2.2.1 "Keys and export": "for links: the
// configuration of the remote side plus, if dynamic routing is used, a matching BIRD configuration
// snippet"): `GET /networks/{id}/export?format=conf` gives the remote side's wg-quick configuration,
// `format=bird` the BIRD snippet of the protocol that runs on the link. The configuration holds the
// remote side's private key when the gateway generated it: the dialog says so and shows the text only
// on request. Each opening exports once; an answer for an opening that is over is dropped.
import { computed, ref, useId, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { exportWireGuardNetwork } from '../../api/generated/hooks/wireguard/wireguard';
import type { NetworkView, WireGuardNetwork } from '../../api/generated/model';
import { useApiPanelStore } from '../../stores/apiPanel';
import DisclosureButton from '../ui/DisclosureButton.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ network: NetworkView; format: 'conf' | 'bird' }>();
const open = defineModel<boolean>('open', { required: true });
const apiPanel = useApiPanelStore();

const text = ref<string>();
const error = ref('');
const loading = ref(false);
const showText = ref(false);
const textId = `${useId()}-text`;
let opening = 0;

const wg = computed(() => props.network.config as WireGuardNetwork);
const conf = computed(() => props.format === 'conf');
const title = computed(() => (conf.value ? `Remote configuration of ${wg.value.name}` : `BIRD snippet of ${wg.value.name}`));
const fileName = computed(() => (conf.value ? `${wg.value.name}-remote.conf` : `${wg.value.name}-bird.conf`));
const placeholder = computed(() => !!text.value?.includes('<PRIVATE_KEY>'));

watch(open, async (o) => {
  const mine = ++opening;
  text.value = undefined;
  error.value = '';
  showText.value = !conf.value;
  loading.value = false;
  if (!o) return;
  loading.value = true;
  try {
    const body = await exportWireGuardNetwork(props.network.id, { format: props.format });
    const t = typeof body === 'string' ? body : await body.text();
    if (mine !== opening || !open.value) return;
    text.value = t;
    apiPanel.record(title.value, [
      {
        method: 'GET',
        path: `/networks/${props.network.id}/export?format=${props.format}`,
        note: conf.value ? "the remote side's wg-quick configuration (secret)" : 'the BIRD snippet of the protocol on the link',
      },
    ]);
  } catch (e) {
    if (mine === opening) error.value = errorMessage(e);
  } finally {
    if (mine === opening) loading.value = false;
  }
});

function download() {
  if (!text.value) return;
  const url = URL.createObjectURL(new Blob([text.value], { type: 'text/plain' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName.value;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}
</script>

<template>
  <ModalDialog
    v-model:open="open"
    :title="title"
    :description="conf ? 'The wg-quick configuration for the router at the other end of the link.' : 'The BIRD configuration for the other end of the link, matching the protocol that runs on it.'"
    width="560px"
  >
    <div class="flex flex-col gap-4" data-testid="link-export">
      <p v-if="loading" class="m-0 text-sm text-text-2" role="status">Exporting…</p>
      <p v-if="error" class="field-error m-0" role="alert">The gateway did not export it: {{ error }}</p>
      <template v-if="text">
        <div v-if="conf" class="rounded-md border border-fault-border bg-fault-tint px-3 py-2 text-[13px] text-fault-text" role="note" data-testid="link-export-secret">
          <template v-if="placeholder">
            The gateway holds no private key for the remote side: the configuration has the placeholder &lt;PRIVATE_KEY&gt;, to be replaced by the key
            the remote side keeps.
          </template>
          <template v-else>This configuration contains the remote side's private key: whoever has the file can join the tunnel as {{ wg.name }}'s remote side.</template>
        </div>
        <DisclosureButton v-if="conf" v-model:open="showText" :controls="textId">
          {{ showText ? 'Hide the configuration text' : 'Show the configuration text' }}
        </DisclosureButton>
        <pre v-if="showText" :id="textId" class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed text-[#d9dee5]" data-testid="link-export-text">{{ text }}</pre>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn" @click="open = false">Close</button>
      <button type="button" class="btn btn-primary" :disabled="!text" @click="download">Download {{ fileName }}</button>
    </template>
  </ModalDialog>
</template>
