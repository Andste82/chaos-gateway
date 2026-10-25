<script setup lang="ts">
// What can be done with a capture (plan §2.17 "Captures"; M23a): stop a running one, download its file (also while
// it runs: the complete records written so far), download its TLS key log (a capture that holds one; the key log is a
// secret and needs the scope `full`, which the admin's session has), and delete a stopped one with its files, after a
// question. The Captures screen and the capture panel of the device detail use the same actions. A download is a
// plain link, not a fetch into memory: a capture can be a gigabyte, the browser streams it to disk, and the file
// name comes from the gateway's Content-Disposition (an `attachment`, so the page stays where it is).
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { deleteCapture, stopCapture } from '../../api/generated/hooks/captures/captures';
import type { Capture } from '../../api/generated/model';
import { downloadUrl, formatSize, isRunning } from '../../lib/captures';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useSessionStore } from '../../stores/session';
import { useToastStore } from '../../stores/toasts';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ capture: Capture }>();

const connection = useConnectionStore();
const session = useSessionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();

const label = computed(() => props.capture.name || props.capture.id.slice(0, 8));
const busy = ref(false);
const confirmOpen = ref(false);
/** The key log and a capture that holds secrets are for the scope `full`. */
const full = computed(() => session.session?.scope === 'full');
const hasKeylog = computed(() => props.capture.keylog === true && full.value);
const sensitive = computed(() => props.capture.sensitive === true);
const downloadable = computed(() => !sensitive.value || full.value);

async function refresh() {
  await Promise.all(['captures', 'runs'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
}

async function stop() {
  if (busy.value) return;
  busy.value = true;
  try {
    const done = await stopCapture(props.capture.id);
    apiPanel.record('Stop capture', [{ method: 'POST', path: `/captures/${props.capture.id}/stop` }]);
    toasts.push('success', `${label.value} stopped`, `${formatSize(done.size_bytes ?? 0)} recorded.`);
    await refresh();
  } catch (e) {
    toasts.push('error', `${label.value} could not be stopped`, errorMessage(e));
  } finally {
    busy.value = false;
  }
}

async function remove() {
  if (busy.value) return;
  busy.value = true;
  try {
    await deleteCapture(props.capture.id);
    apiPanel.record('Delete capture', [{ method: 'DELETE', path: `/captures/${props.capture.id}` }]);
    toasts.push('success', `${label.value} deleted`);
    confirmOpen.value = false;
    await refresh();
  } catch (e) {
    confirmOpen.value = false;
    toasts.push('error', `${label.value} could not be deleted`, errorMessage(e));
    await refresh();
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5" data-testid="capture-actions">
    <button v-if="isRunning(capture)" type="button" class="btn btn-sm" :disabled="busy || connection.offline" :aria-label="`Stop ${label}`" data-testid="capture-stop" @click="stop">
      {{ busy ? 'Stopping…' : 'Stop' }}
    </button>
    <a v-if="downloadable" class="text-[13px]" :href="downloadUrl(capture.id)" :aria-label="`Download ${label}`" data-testid="capture-download">Download</a>
    <span v-else class="text-[13px] text-text-3" title="A capture that holds secrets needs the scope full" data-testid="capture-download-denied">Needs scope full</span>
    <a v-if="hasKeylog" class="text-[13px]" :href="downloadUrl(capture.id, 'keylog')" :aria-label="`Download the TLS key log of ${label}`" data-testid="capture-keylog">Key log</a>
    <button
      v-if="!isRunning(capture)"
      type="button"
      class="btn btn-sm"
      :disabled="busy || connection.offline"
      :aria-label="`Delete ${label}`"
      data-testid="capture-delete"
      @click="confirmOpen = true"
    >
      Delete
    </button>

    <ModalDialog v-model:open="confirmOpen" title="Delete capture" width="460px">
      <p class="m-0 text-sm" data-testid="capture-delete-what">
        Delete <span class="mono">{{ label }}</span> and its files ({{ formatSize(capture.size_bytes ?? 0) }})? This cannot be undone.
      </p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="confirmOpen = false">Cancel</button>
        <button type="button" class="btn !border-block-border !bg-block-tint !text-block-text" :disabled="busy || connection.offline" data-testid="capture-delete-confirm" @click="remove">
          {{ busy ? 'Deleting…' : 'Delete' }}
        </button>
      </div>
    </ModalDialog>
  </div>
</template>
