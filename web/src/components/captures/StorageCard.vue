<script setup lang="ts">
// What the captures take (plan §2.11, §3.6, §2.17 "Captures"): their total size against the quota (the oldest stopped
// captures are deleted first when a new one needs room), the free space of the volume against the limit below which
// running captures stop and new ones are refused, and how many run of the most that run at once.
import { computed } from 'vue';
import type { CaptureStorage } from '../../api/generated/model';
import { storageView } from '../../lib/captures';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ storage: CaptureStorage }>();
const view = computed(() => storageView(props.storage));
</script>

<template>
  <SectionCard title="Storage">
    <div class="grid grid-cols-1 gap-x-8 gap-y-3 md:grid-cols-3" data-testid="capture-storage">
      <div>
        <div class="text-[13px] text-text-2">Quota</div>
        <div class="mono mt-0.5 text-sm" data-testid="capture-quota">{{ view.usedText }} of {{ view.quotaText }}</div>
        <div
          class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-surface-2"
          role="progressbar"
          aria-label="Quota used by the captures"
          :aria-valuenow="view.usedPercent"
          aria-valuemin="0"
          aria-valuemax="100"
        >
          <div class="h-full rounded-full" :class="view.full ? 'bg-block-text' : 'bg-accent'" :style="{ width: `${view.usedPercent}%` }" />
        </div>
      </div>
      <div>
        <div class="text-[13px] text-text-2">Volume</div>
        <div class="mono mt-0.5 text-sm" :class="view.low ? 'text-block-text' : ''" data-testid="capture-free">{{ view.freeText ?? 'free space unknown' }}</div>
        <div class="mt-1 text-xs text-text-3">Captures stop and new ones are refused below {{ view.limitText }} free.</div>
      </div>
      <div>
        <div class="text-[13px] text-text-2">Running</div>
        <div class="mono mt-0.5 text-sm" data-testid="capture-running">{{ view.runningText }}</div>
      </div>
    </div>
    <p v-if="view.low" class="m-0 rounded-md border border-block-border bg-block-tint px-3 py-2 text-[13px] text-block-text" role="alert" data-testid="capture-disk-low">
      The volume of the captures has less than {{ view.limitText }} free space: new captures are refused and running ones stop. Delete captures, or free the disk.
    </p>
    <p v-else-if="view.full" class="m-0 text-[13px] text-text-2" data-testid="capture-quota-full">
      The captures use the whole quota: a new capture deletes the oldest stopped ones first, and is refused when running captures use all of it.
    </p>
  </SectionCard>
</template>
