<script setup lang="ts">
// The test CA on the Interception screen (plan §2.8, §2.17 "Interception UI"; M23c): what it is for, the download of its
// certificate (`GET /tls/ca`, a plain link: the file name is the gateway's) and its replacement (`POST /tls/ca/regenerate`) after a
// question that says what it costs: every device that installed the old certificate trusts nothing the gateway shows it until it
// installs the new one. The replacement needs the scope `full`, which the admin's session has. After it the card offers the new
// certificate for download, with a reminder that the devices need it.
import { computed, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { regenerateTestCa } from '../../api/generated/hooks/config/config';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useSessionStore } from '../../stores/session';
import { useToastStore } from '../../stores/toasts';
import ModalDialog from '../ui/ModalDialog.vue';
import SectionCard from '../ui/SectionCard.vue';
import TestCaLink from '../ui/TestCaLink.vue';

const connection = useConnectionStore();
const session = useSessionStore();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();

const askOpen = ref(false);
const busy = ref(false);
const error = ref('');
/** The CA was replaced since the page was opened: the new certificate has to go to the devices. */
const replaced = ref(false);
const full = computed(() => session.session?.scope === 'full');

async function regenerate() {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    await regenerateTestCa();
    apiPanel.record('Replace the test CA', [{ method: 'POST', path: '/tls/ca/regenerate', note: 'answers with the new certificate' }]);
    replaced.value = true;
    askOpen.value = false;
    toasts.push('success', 'The test CA is replaced', 'Install the new certificate on the devices that trust it.');
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <SectionCard title="Test CA">
    <p class="m-0 text-sm text-text-2">
      The proxy shows a device a certificate that the test CA signed, for the name the device asked for. The device must trust the test CA (a development firmware with its certificate installed): any other rejects the certificate in the handshake and the proxy sees none of its requests.
    </p>
    <div class="flex flex-wrap items-center gap-2">
      <TestCaLink button label="Download the test CA" />
      <button type="button" class="btn" :disabled="connection.offline || !full" data-testid="ca-regenerate" @click="askOpen = true">Replace the test CA…</button>
    </div>
    <p v-if="!full" class="m-0 text-[13px] text-text-3" data-testid="ca-needs-full">Replacing the test CA needs the full scope; this session does not have it.</p>
    <p v-if="replaced" class="m-0 rounded-md border border-fault-border bg-fault-tint p-3 text-[13px] text-fault-text" role="status" data-testid="ca-replaced">
      The test CA was replaced. Every device that installed the old certificate trusts nothing the gateway shows it until it installs the new one: download it above.
    </p>
    <ModalDialog v-model:open="askOpen" title="Replace the test CA?" width="520px">
      <div class="flex flex-col gap-2 text-sm">
        <p class="m-0">The gateway makes a new test CA, and the old certificate stops working at once.</p>
        <ul class="m-0 flex list-disc flex-col gap-1 pl-5 text-text-2">
          <li>Every device that installed the old certificate trusts nothing the gateway shows it until it installs the new one: the TLS cases that the test CA signs (expired, not yet valid, hostname mismatch) and the interception are rejected as an unknown issuer until then.</li>
          <li>The unknown CA, which is never distributed, does not change.</li>
          <li>The request is recorded in the audit log.</li>
        </ul>
      </div>
      <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>
      <template #footer>
        <button type="button" class="btn" @click="askOpen = false">Cancel</button>
        <button type="button" class="btn !border-block-border !bg-block-tint !text-block-text" :disabled="busy || connection.offline" data-testid="ca-regenerate-confirm" @click="regenerate">
          {{ busy ? 'Replacing…' : 'Replace the test CA' }}
        </button>
      </template>
    </ModalDialog>
  </SectionCard>
</template>
