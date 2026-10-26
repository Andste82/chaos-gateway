<script setup lang="ts">
// The captures of a device (plan §2.17 "Device detail"; M23a): the captures whose scope is the device or a selector
// with the device as its source, newest first, each with its state, size and packets and the same actions as the
// Captures screen; "Capture" starts one of the device (the scope is fixed), and a link leads to the whole screen. A client of a
// management WireGuard network is the admin's tunnel, which the gateway never records: its Capture is off and the panel says why.
import { computed, ref } from 'vue';
import { useCanCapture, useCaptures, useNames } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { isDeviceCapture, isRecordableDevice } from '../../lib/captures';
import { useConnectionStore } from '../../stores/connection';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import CaptureDialog from './CaptureDialog.vue';
import CaptureRow from './CaptureRow.vue';

const props = defineProps<{ device: { id: string; name: string; network?: string } }>();

const canCapture = useCanCapture();
const captures = useCaptures(() => canCapture.value === true);
const { names, networks } = useNames();
const connection = useConnectionStore();
const now = useNow();
const open = ref(false);

/** The device is a client of a management WireGuard network: the gateway refuses to record it. */
const management = computed(() => !isRecordableDevice(props.device, networks.data.value));
const mine = computed(() => (captures.data.value ?? []).filter((c) => isDeviceCapture(c, props.device)));
</script>

<template>
  <SectionCard title="Captures">
    <template #actions>
      <button type="button" class="btn btn-sm" :disabled="canCapture === false || connection.offline || management" :aria-label="`Capture ${device.name}`" data-testid="device-capture" @click="open = true">Capture</button>
      <RouterLink to="/captures" data-testid="device-captures-all">All captures</RouterLink>
    </template>
    <p v-if="management && canCapture !== false" class="m-0 mb-2 text-sm text-text-2" data-testid="device-captures-management">
      This device is a client of a management WireGuard network: its packets are the admin's tunnel, which the gateway does not record.
    </p>
    <p v-if="canCapture === false" class="m-0 text-sm text-text-2" data-testid="device-captures-unsupported">This gateway has no capture volume, so it cannot capture packets.</p>
    <p v-else-if="canCapture === undefined || captures.isLoading.value" class="m-0 text-sm text-text-2" role="status">Loading the captures…</p>
    <LoadError v-else-if="captures.error.value && !captures.data.value" :error="captures.error.value" what="The captures are not loaded" :retry="captures.refetch" />
    <p v-else-if="mine.length === 0" class="m-0 text-sm text-text-2" data-testid="device-captures-empty">No capture of this device yet.</p>
    <div v-else role="list" class="flex flex-col" data-testid="device-captures">
      <CaptureRow v-for="c in mine" :key="c.id" :capture="c" :names="names" :now="now" compact />
    </div>
    <CaptureDialog v-model:open="open" :fixed-device="{ id: device.id, name: device.name }" />
  </SectionCard>
</template>
