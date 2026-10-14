<script setup lang="ts">
// A part of the technical view (plan §2.17): what the kernel holds for one object, read from the
// gateway (`GET /state/linux`) and cut down to the lines that name its interface or subnet (`terms`), or
// to whole chains (`chains`). The counters are live: the view reads again every 5 s while it is open.
import { useQuery } from '@tanstack/vue-query';
import { computed } from 'vue';
import { getLinuxState } from '../../api/generated/hooks/system/system';
import { groupExcerpt, nftExcerpt, sectionLabels, sectionsOf } from '../../lib/linuxView';
import StaleNotice from '../linux/StaleNotice.vue';
import LoadError from '../ui/LoadError.vue';

const props = defineProps<{ terms?: string[]; chains?: string[] }>();

const linux = useQuery({ queryKey: ['linux-state'], queryFn: ({ signal }) => getLinuxState({ signal }), refetchInterval: 5_000 });

const parts = computed(() =>
  sectionsOf(linux.data.value)
    .map(({ name, text }) => {
      if (props.chains) {
        if (name !== 'nftables') return { name, text: '' };
        return { name, text: nftExcerpt(text, [], (h) => props.chains!.some((c) => h.startsWith(`  chain ${c} {`))) };
      }
      return { name, text: name === 'nftables' ? nftExcerpt(text, props.terms ?? []) : groupExcerpt(text, props.terms ?? []) };
    })
    .filter((p) => p.text.trim()),
);
</script>

<template>
  <div class="flex flex-col gap-2" data-testid="linux-excerpt">
    <p v-if="linux.isLoading.value" class="m-0 text-[13px] text-text-2">Reading the kernel state…</p>
    <LoadError v-else-if="linux.error.value && !linux.data.value" :error="linux.error.value" what="The kernel state is not read" :retry="linux.refetch" />
    <template v-else>
      <StaleNotice v-if="linux.error.value" :error="linux.error.value" :read-at="linux.dataUpdatedAt.value" />
      <template v-for="p in parts" :key="p.name">
        <div class="text-xs text-text-3">{{ sectionLabels[p.name] }}</div>
        <pre
          class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre text-[#d9dee5]"
          :class="{ stale: linux.error.value }"
          >{{ p.text }}</pre
        >
      </template>
      <p v-if="parts.length === 0" class="m-0 text-[13px] text-text-2">The kernel holds nothing of Chaos Gateway's for this.</p>
      <RouterLink to="/linux" class="text-[13px]">Open the whole Linux view →</RouterLink>
    </template>
  </div>
</template>
