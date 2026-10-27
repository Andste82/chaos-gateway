<script setup lang="ts">
// A device's connections (plan §2.17 "Device detail"; M26, `docs/open-items.md` P3-M12-02): the newest the gateway tracks of it, a
// connection that came in through a port forward to it included, in the compact form of the table, and a link to the Flows
// screen with the device as its filter.
import { computed } from 'vue';
import { useFlows, useNames } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { sortFlows } from '../../lib/flows';
import { formatCount } from '../../lib/format';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import ConnectionTable from './ConnectionTable.vue';

const props = defineProps<{ device: { id: string; name: string } }>();

const shownMax = 8;
const { names } = useNames();
const now = useNow();
const flows = useFlows(() => ({ device: props.device.id, limit: 500 }));
const all = computed(() => sortFlows(flows.data.value ?? []));
const items = computed(() => all.value.slice(0, shownMax));
</script>

<template>
  <SectionCard title="Flows">
    <template #actions>
      <RouterLink :to="{ path: '/flows', query: { device: device.id } }" data-testid="device-flows-all">All flows</RouterLink>
    </template>
    <LoadError v-if="flows.error.value && !flows.data.value" :error="flows.error.value" what="The connections are not loaded" :retry="flows.refetch" />
    <p v-else-if="flows.isPending.value && !flows.data.value" class="m-0 text-sm text-text-2" role="status">Loading the connections…</p>
    <p v-else-if="items.length === 0" class="m-0 text-sm text-text-2" data-testid="device-flows-empty">The gateway tracks no connection of {{ device.name }} right now.</p>
    <template v-else>
      <ConnectionTable :items="items" :device-name="(id: string) => names.device?.(id)" :now="now" compact />
      <p v-if="all.length > items.length" class="m-0 text-[13px] text-text-3" data-testid="device-flows-more">
        {{ formatCount(all.length - items.length) }} more: <RouterLink :to="{ path: '/flows', query: { device: device.id } }">all flows of {{ device.name }}</RouterLink>
      </p>
    </template>
  </SectionCard>
</template>
