<script setup lang="ts">
// The probes (plan §2.12, §2.17 "Probes"; M25): the virtual clients of the gateway, each a port of the bridge of a test network
// in a namespace of its own with an address from the network's DHCP. The list shows where each is and whether it has its
// address; a probe is a device, so the faults, rules and profiles that target it act on its traffic. From a probe a
// diagnostic is run, and a profile is measured.
import type { ProbeView } from '../../api/generated/model';
import { probeTone } from '../../lib/probes';
import StatusDot from '../ui/StatusDot.vue';

defineProps<{ probes: ProbeView[]; networks: Record<string, string>; disabled?: boolean }>();
defineEmits<{ diagnose: [probe: ProbeView]; measure: [probe: ProbeView] }>();
</script>

<template>
  <table class="w-full border-collapse text-sm" data-testid="probes">
    <thead>
      <tr class="text-left text-xs text-text-3">
        <th class="py-1.5 pr-3 font-normal">Probe</th>
        <th class="py-1.5 pr-3 font-normal">Network</th>
        <th class="py-1.5 pr-3 font-normal">Address</th>
        <th class="py-1.5 pr-3 font-normal">MAC</th>
        <th class="py-1.5 pr-3 font-normal">Status</th>
        <th class="py-1.5 font-normal"><span class="sr-only">Actions</span></th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="p in probes" :key="p.id" class="border-t border-border" :data-probe="p.config.name">
        <th class="py-2 pr-3 text-left font-medium">
          <RouterLink :to="`/devices/${p.id}`" class="text-text hover:underline">{{ p.config.name }}</RouterLink>
          <span v-if="p.config.trusts_test_ca" class="ml-2 text-xs font-normal text-text-3">trusts the test CA</span>
        </th>
        <td class="py-2 pr-3 text-text-2">{{ networks[p.config.network] ?? p.config.network }}</td>
        <td class="mono py-2 pr-3">{{ (p.observed.addresses ?? []).join(', ') || '—' }}</td>
        <td class="mono py-2 pr-3 text-text-2">{{ (p.observed.macs ?? [])[0] ?? '—' }}</td>
        <td class="py-2 pr-3"><StatusDot v-bind="probeTone(p)" /></td>
        <td class="py-2 text-right">
          <span class="inline-flex gap-2">
            <button type="button" class="btn btn-sm" :disabled="disabled" :aria-label="`Diagnose from ${p.config.name}`" @click="$emit('diagnose', p)">Diagnose from here</button>
            <button type="button" class="btn btn-sm" :disabled="disabled" :aria-label="`Measure with ${p.config.name}`" @click="$emit('measure', p)">Measure…</button>
          </span>
        </td>
      </tr>
    </tbody>
  </table>
</template>
