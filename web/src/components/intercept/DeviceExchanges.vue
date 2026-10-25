<script setup lang="ts">
// The exchanges of one device (plan §2.17 "Device detail"; M23c): the latest the interception proxy saw of it, in the compact form of the
// table, and a link to the Interception screen with the device as its filter. The panel is there while an interception case acts on the
// device or the log holds exchanges of it; otherwise the device has nothing to show here and the page stays as it was.
import { computed } from 'vue';
import { useCanIntercept, useInterceptFlows, useNames } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import FlowTable from './FlowTable.vue';

const props = defineProps<{ device: { id: string; name: string }; active: boolean }>();

const canIntercept = useCanIntercept();
const { names } = useNames();
const now = useNow();
const flows = useInterceptFlows(
  () => ({ device: props.device.id, limit: 8 }),
  () => canIntercept.value === true,
  // the panel is read every 3 s only while a case acts on the device; the event of a rule that acts reads it at once
  () => props.active,
);
const items = computed(() => flows.data.value?.items ?? []);
const shown = computed(() => canIntercept.value === true && (props.active || items.value.length > 0));
</script>

<template>
  <SectionCard v-if="shown" title="HTTP exchanges">
    <template #actions>
      <RouterLink :to="{ path: '/interception', query: { device: device.id } }" data-testid="device-exchanges-all">All exchanges</RouterLink>
    </template>
    <LoadError v-if="flows.error.value && !flows.data.value" :error="flows.error.value" what="The exchanges are not loaded" :retry="flows.refetch" />
    <p v-else-if="flows.isLoading.value" class="m-0 text-sm text-text-2" role="status">Loading the exchanges…</p>
    <p v-else-if="items.length === 0" class="m-0 text-sm text-text-2" data-testid="device-exchanges-empty">
      The proxy has seen no request of {{ device.name }} yet. An interception case acts on it; the exchanges appear when it talks and trusts the test CA.
    </p>
    <FlowTable v-else :items="items" :device-names="(id: string) => names.device?.(id)" :now="now" compact />
  </SectionCard>
</template>
