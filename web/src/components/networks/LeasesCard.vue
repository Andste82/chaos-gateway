<script setup lang="ts">
// The DHCP leases of a local network (plan §2.7: DHCP is shown in the network and device views): address,
// MAC, host name, the device it belongs to and the remaining time on the gateway's clock.
import { computed } from 'vue';
import type { NetworkView } from '../../api/generated/model';
import { firstError, useDevices, useLeases } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { formatRemaining } from '../../lib/format';
import { parseIPv4 } from '../../lib/networks';
import EmptyState from '../ui/EmptyState.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ network: NetworkView }>();
const leases = useLeases(() => props.network.id);
const devices = useDevices();
const now = useNow(1000);

const rows = computed(() =>
  [...(leases.data.value ?? [])]
    .sort((a, b) => (parseIPv4(a.ip) ?? 0) - (parseIPv4(b.ip) ?? 0))
    .map((l) => ({ l, device: devices.data.value?.find((d) => d.id === l.device) })),
);
const dhcpOff = computed(() => (props.network.config as { dhcp?: { enabled?: boolean } }).dhcp?.enabled === false);
</script>

<template>
  <SectionCard title="DHCP leases">
    <template #actions><span class="text-xs text-text-3">{{ rows.length }} {{ rows.length === 1 ? 'lease' : 'leases' }}</span></template>
    <p v-if="leases.isLoading.value" class="m-0 text-sm text-text-2">Loading the leases…</p>
    <LoadError v-else-if="firstError(leases)" :error="firstError(leases)" what="The leases are not loaded" :retry="leases.refetch" />
    <EmptyState v-else-if="rows.length === 0" :text="dhcpOff ? 'DHCP is off in this network: devices have static addresses.' : 'No device has a lease yet.'" />
    <table v-else class="w-full border-collapse text-sm" data-testid="leases">
      <thead>
        <tr class="data-head text-left">
          <th class="py-2 font-normal">Address</th>
          <th class="py-2 font-normal">MAC</th>
          <th class="py-2 font-normal">Host name</th>
          <th class="py-2 font-normal">Device</th>
          <th class="py-2 font-normal">State</th>
          <th class="py-2 font-normal">Expires in</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="{ l, device } in rows" :key="l.ip + l.mac" class="data-row">
          <td class="mono py-2.5 text-[13px]">{{ l.ip }}</td>
          <td class="mono py-2.5 text-[13px]">{{ l.mac }}</td>
          <td class="py-2.5">{{ l.hostname || '—' }}</td>
          <td class="py-2.5">
            <RouterLink v-if="device" :to="`/devices/${device.id}`">{{ device.name }}</RouterLink>
            <span v-else class="text-text-3">—</span>
          </td>
          <td class="py-2.5">{{ l.state ?? 'active' }}</td>
          <td class="mono py-2.5 text-[13px]">{{ formatRemaining(new Date(l.expires_at).getTime() - now.getTime()) }}</td>
        </tr>
      </tbody>
    </table>
  </SectionCard>
</template>
