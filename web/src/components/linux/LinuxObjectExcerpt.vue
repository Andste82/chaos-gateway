<script setup lang="ts">
// "Show Linux view" on one device, access rule or fault (plan §2.17 "Technical view", P3-M14-03): what the
// kernel holds for it, as the gateway finds it by the names and keys the compiler gave it
// (`GET /state/linux/{kind}/{id}`): a device's identity map element and the classification of its
// addresses, a rule's rule in both hooks with its counter and last match, a fault's mark chain, counters,
// classification elements, tc classes and netem queues. The counters are live: the view reads again every
// 5 s while it is open.
import { useQuery } from '@tanstack/vue-query';
import { computed } from 'vue';
import { getLinuxObject } from '../../api/generated/hooks/system/system';
import LoadError from '../ui/LoadError.vue';

const props = defineProps<{ kind: 'device' | 'rule' | 'fault'; id: string }>();

const linux = useQuery({
  queryKey: ['linux-object', computed(() => props.kind), computed(() => props.id)],
  queryFn: ({ signal }) => getLinuxObject(props.kind, props.id, { signal }),
  refetchInterval: 5_000,
});

const parts = computed(() => {
  const v = linux.data.value;
  if (!v) return [];
  return [
    { name: 'nftables', text: v.nftables },
    { name: 'tc', text: v.tc },
  ].filter((p) => p.text.trim());
});
</script>

<template>
  <div class="flex flex-col gap-2" data-testid="linux-object">
    <p v-if="linux.isLoading.value" class="m-0 text-[13px] text-text-2">Reading the kernel state…</p>
    <LoadError v-else-if="linux.error.value && !linux.data.value" :error="linux.error.value" what="The kernel state is not read" :retry="linux.refetch" />
    <template v-else>
      <template v-for="p in parts" :key="p.name">
        <div class="text-xs text-text-3">{{ p.name }}</div>
        <pre
          class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre text-[#d9dee5]"
          :data-testid="`linux-object-${p.name}`"
          >{{ p.text }}</pre
        >
      </template>
      <p v-if="parts.length === 0" class="m-0 text-[13px] text-text-2">
        The kernel holds nothing of Chaos Gateway's for this.<template v-if="kind === 'fault'">
          Only impairment, MTU and tunnel faults are put into the kernel; DNS, TLS, DHCP and WireGuard parts are carried out
          by the gateway's services.</template
        >
      </p>
      <RouterLink to="/linux" class="text-[13px]">Open the whole Linux view →</RouterLink>
    </template>
  </div>
</template>
