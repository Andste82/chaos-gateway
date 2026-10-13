<script setup lang="ts">
// Overview (plan §2.17): topology with the fault badge on the path, the active faults, the uplink
// traffic and the recent events; every item links to its detail. The running scenario arrives with
// the scenario engine (M15, M19).
import { computed } from 'vue';
import ActiveFaultList from '../components/faults/ActiveFaultList.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import RecentEventsCard from '../components/overview/RecentEventsCard.vue';
import TopologyCard from '../components/overview/TopologyCard.vue';
import UplinkTrafficCard from '../components/overview/UplinkTrafficCard.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import { useActiveFaults } from '../composables/gateway';

const faults = useActiveFaults();

const deviceCounts = computed(() => {
  const m = new Map<string, number>();
  for (const d of faults.devices.data.value ?? []) {
    if (d.network) m.set(d.network, (m.get(d.network) ?? 0) + 1);
  }
  return m;
});
</script>

<template>
  <PageHeader title="Overview" />
  <TopologyCard :networks="faults.networks.data.value ?? []" :device-counts="deviceCounts" :faults="faults.items.value" />
  <div id="active-faults" class="scroll-mt-4">
    <SectionCard title="Active faults">
      <template #actions><RouterLink to="/devices">Devices</RouterLink></template>
      <p v-if="faults.isLoading.value" class="m-0 text-sm text-text-2">Loading…</p>
      <EmptyState v-else-if="faults.items.value.length === 0" text="No active faults: all traffic passes unimpaired.">
        <RouterLink to="/devices" class="btn btn-primary">Choose a device</RouterLink>
      </EmptyState>
      <ActiveFaultList v-else :items="faults.items.value" show-target />
    </SectionCard>
  </div>
  <div class="grid grid-cols-1 gap-5 xl:grid-cols-3">
    <div class="min-w-0 xl:col-span-2"><UplinkTrafficCard /></div>
    <RecentEventsCard />
  </div>
</template>
