<script setup lang="ts">
// The Captures screen (plan §2.11, §2.17 "Captures"; M23a): every capture of the gateway, newest first — name, scope,
// state, size, packets, the packets the kernel dropped, when it started and the run it belongs to — with stop,
// download (and the TLS key log of a capture that holds one), delete, and "New capture". The storage card shows what
// the captures take of the quota and of the volume. A capture the quota or a low disk ended says so in its row; the
// events of captures keep the list live, and the counters of a running capture are read every 2 s.
import { computed, ref } from 'vue';
import CaptureDialog from '../components/captures/CaptureDialog.vue';
import CaptureRow from '../components/captures/CaptureRow.vue';
import StorageCard from '../components/captures/StorageCard.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import { useCanCapture, useCaptures, useCaptureStorage, useNames } from '../composables/gateway';
import { useNow } from '../composables/useNow';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';

const canCapture = useCanCapture();
const enabled = () => canCapture.value === true;
const captures = useCaptures(enabled);
const storage = useCaptureStorage(enabled);
const { names } = useNames();
const connection = useConnectionStore();
const now = useNow();

const newOpen = ref(false);
const list = computed(() => captures.data.value ?? []);
const cols = 'grid-cols-[minmax(130px,1.1fr)_minmax(150px,1.3fr)_minmax(150px,1.2fr)_90px_90px_80px_100px_110px_minmax(190px,auto)]';

useApiScreen('the captures', () => [
  { method: 'GET', path: '/captures', note: 'newest first; ?run= lists the captures of a run' },
  { method: 'GET', path: '/captures/storage', note: 'the quota, the free space of the volume, the running captures' },
  { method: 'POST', path: '/captures', note: 'start one' },
]);
</script>

<template>
  <PageHeader title="Captures">
    <template #actions>
      <button type="button" class="btn btn-primary" :disabled="canCapture === false || connection.offline" data-testid="new-capture" @click="newOpen = true">New capture</button>
    </template>
  </PageHeader>
  <p class="-mt-3 mb-0 text-[13px] text-text-3">Packets of a device, a network, a selector or the uplink · pcapng or pcap, downloadable while they run</p>

  <EmptyState v-if="canCapture === false" text="This gateway has no capture volume, so it cannot capture packets." data-testid="captures-unsupported" />
  <template v-else>
    <LoadError v-if="storage.error.value && !storage.data.value" :error="storage.error.value" what="The storage of the captures is not loaded" :retry="storage.refetch" />
    <StorageCard v-else-if="storage.data.value" :storage="storage.data.value" />

    <SectionCard title="Captures">
      <p v-if="canCapture === undefined || captures.isLoading.value" class="m-0 text-sm text-text-2" role="status">Loading the captures…</p>
      <LoadError v-else-if="captures.error.value && !captures.data.value" :error="captures.error.value" what="The captures are not loaded" :retry="captures.refetch" />
      <EmptyState v-else-if="list.length === 0" text="No captures yet: start one to record the packets of a device, a network or the uplink.">
        <button type="button" class="btn btn-primary" :disabled="connection.offline" @click="newOpen = true">New capture</button>
      </EmptyState>
      <div v-else class="-mx-5 overflow-x-auto" data-testid="capture-list">
        <div role="table" aria-label="Captures" :aria-rowcount="list.length + 1" class="min-w-[1100px]">
          <div role="row" class="data-head grid h-10 items-center gap-3 px-4" :class="cols">
            <span role="columnheader">Name</span>
            <span role="columnheader">Scope</span>
            <span role="columnheader">State</span>
            <span role="columnheader">Size</span>
            <span role="columnheader">Packets</span>
            <span role="columnheader">Dropped</span>
            <span role="columnheader">Started</span>
            <span role="columnheader">Run</span>
            <span role="columnheader"><span class="sr-only">Actions</span></span>
          </div>
          <CaptureRow v-for="c in list" :key="c.id" :capture="c" :names="names" :now="now" :cols="cols" />
        </div>
      </div>
    </SectionCard>
    <p class="m-0 text-xs text-text-3">A capture marked SENSITIVE holds a TLS key log or decrypted traffic. The oldest stopped captures are deleted when a new one needs room in the quota.</p>
  </template>

  <CaptureDialog v-model:open="newOpen" />
</template>
