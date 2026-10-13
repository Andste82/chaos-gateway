<script setup lang="ts">
// The configuration of a WireGuard client (plan §2.2.1 "Keys and export", M14 "QR dialog and config
// download"): the dialog downloads the wg-quick configuration once (`GET …/clients/{id}/export`, audited
// by the gateway) and shows it as a QR code for phones and tablets and as a .conf file to save. Both come
// from that one text, so the code and the file are the same, and a client with "export once" is exported
// once only. The configuration holds the client's private key: the dialog says so.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { exportWireGuardClient } from '../../api/generated/hooks/wireguard/wireguard';
import type { WireGuardClientView } from '../../api/generated/model';
import { qrMatrix, qrPath } from '../../lib/qr';
import { useApiPanelStore } from '../../stores/apiPanel';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ network: string; client: WireGuardClientView | undefined }>();
const open = defineModel<boolean>('open', { required: true });
const apiPanel = useApiPanelStore();

const conf = ref<string>();
const error = ref('');
const loading = ref(false);
const showText = ref(false);

watch(open, async (o) => {
  conf.value = undefined;
  error.value = '';
  showText.value = false;
  const c = props.client;
  if (!o || !c) return;
  loading.value = true;
  try {
    const body = await exportWireGuardClient(props.network, c.id, { format: 'conf' });
    if (!open.value) return;
    conf.value = typeof body === 'string' ? body : await body.text();
    apiPanel.record(`Configuration of ${c.config.name}`, [
      { method: 'GET', path: `/networks/${props.network}/clients/${c.id}/export?format=conf`, note: 'the wg-quick configuration (secret; png and svg give the QR code)' },
    ]);
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    loading.value = false;
  }
});

const qr = computed(() => {
  if (!conf.value) return undefined;
  try {
    const m = qrMatrix(conf.value);
    return { size: m.size + 8, path: qrPath(m, 4) };
  } catch {
    return undefined;
  }
});
const placeholder = computed(() => !!conf.value?.includes('<PRIVATE_KEY>'));
const fileName = computed(() => `${props.client?.config.name ?? 'client'}.conf`);

function download() {
  if (!conf.value) return;
  const url = URL.createObjectURL(new Blob([conf.value], { type: 'text/plain' }));
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
    :title="`Configuration of ${client?.config.name ?? ''}`"
    description="Scan the code with the WireGuard app, or save the file for wg-quick."
    width="560px"
  >
    <div class="flex flex-col gap-4" data-testid="client-config">
      <p v-if="loading" class="m-0 text-sm text-text-2" role="status">Exporting the configuration…</p>
      <p v-if="error" class="field-error m-0" role="alert">The gateway did not export it: {{ error }}</p>
      <template v-if="conf">
        <div class="rounded-md border border-fault-border bg-fault-tint px-3 py-2 text-[13px] text-fault-text" role="note">
          <template v-if="placeholder">
            The gateway holds no private key for this client: the configuration has the placeholder &lt;PRIVATE_KEY&gt;, to be replaced by the
            key the client keeps.
          </template>
          <template v-else>
            This configuration contains the client's private key: whoever has the code or the file can join the tunnel as {{ client?.config.name }}.
            <template v-if="client?.config.key?.export_once"> The gateway deletes the key after this export: save the file now.</template>
          </template>
        </div>
        <div class="flex justify-center">
          <svg
            v-if="qr"
            :viewBox="`0 0 ${qr.size} ${qr.size}`"
            width="300"
            height="300"
            role="img"
            :aria-label="`QR code of the configuration of ${client?.config.name}`"
            shape-rendering="crispEdges"
            data-testid="client-qr"
          >
            <rect :width="qr.size" :height="qr.size" fill="#ffffff" />
            <path :d="qr.path" fill="#000000" />
          </svg>
          <p v-else class="m-0 text-sm text-block-text">The configuration is too long for a QR code; save the file instead.</p>
        </div>
        <button type="button" class="self-start border-0 bg-transparent p-0 text-[13px] text-accent-text" :aria-expanded="showText" @click="showText = !showText">
          {{ showText ? 'Hide the configuration text' : 'Show the configuration text' }}
        </button>
        <pre v-if="showText" class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed text-[#d9dee5]" data-testid="client-conf-text">{{ conf }}</pre>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn" @click="open = false">Close</button>
      <button type="button" class="btn btn-primary" :disabled="!conf" @click="download">Download {{ fileName }}</button>
    </template>
  </ModalDialog>
</template>
