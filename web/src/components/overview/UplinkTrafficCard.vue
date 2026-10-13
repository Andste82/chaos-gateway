<script setup lang="ts">
// Uplink traffic of the last 10 minutes (plan §2.17, Overview), drawn with uPlot (plan §3.7) from
// the rates between readings of the uplink's byte counters.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import uPlot from 'uplot';
import { useGetUplink } from '../../api/generated/hooks/config/config';
import { formatBitRate } from '../../lib/format';
import { useTrafficStore, windowSeconds } from '../../stores/traffic';
import SectionCard from '../ui/SectionCard.vue';

const uplink = useGetUplink(undefined, { query: { refetchInterval: 5_000 } });
const traffic = useTrafficStore();

// every fetch is a reading, also one with the same numbers (no traffic): watch the fetch time, not
// the data, which the query cache keeps as the same object when nothing changed
watch(
  () => uplink.dataUpdatedAt.value,
  (at) => {
    const s = uplink.data.value?.status;
    if (s?.rx_bytes !== undefined && s.tx_bytes !== undefined) traffic.record(s.rx_bytes, s.tx_bytes, new Date(at || Date.now()));
  },
  { immediate: true },
);

const hasCounters = computed(() => uplink.data.value?.status.rx_bytes !== undefined);
const latest = computed(() => traffic.points[traffic.points.length - 1]);
const summary = computed(() =>
  latest.value
    ? `Download ${formatBitRate(latest.value.downBps)}, upload ${formatBitRate(latest.value.upBps)}`
    : 'Collecting the first readings',
);

const el = ref<HTMLDivElement>();
let plot: uPlot | undefined;
let observer: ResizeObserver | undefined;

function data(): uPlot.AlignedData {
  const pts = traffic.points;
  return [pts.map((p) => p.t), pts.map((p) => p.downBps), pts.map((p) => p.upBps)];
}

onMounted(() => {
  if (!el.value || typeof window === 'undefined' || !('matchMedia' in window)) return;
  const width = el.value.clientWidth || 600;
  plot = new uPlot(
    {
      width,
      height: 160,
      legend: { show: false },
      cursor: { show: true, drag: { x: false, y: false } },
      scales: { x: { time: true, range: (_u, _min, max) => [max - windowSeconds, max] } },
      axes: [
        { stroke: '#7D8796', grid: { stroke: '#232A34' }, ticks: { stroke: '#232A34' }, font: '12px IBM Plex Mono' },
        {
          stroke: '#7D8796',
          grid: { stroke: '#232A34' },
          ticks: { stroke: '#232A34' },
          font: '12px IBM Plex Mono',
          size: 96,
          values: (_u, vals) => vals.map((v) => formatBitRate(v)),
        },
      ],
      series: [
        {},
        { label: 'Download', stroke: '#6CB2FF', width: 2 },
        { label: 'Upload', stroke: '#D9DEE5', width: 2, dash: [5, 4] },
      ],
    },
    data(),
    el.value,
  );
  if ('ResizeObserver' in window) {
    observer = new ResizeObserver(() => plot?.setSize({ width: el.value?.clientWidth || width, height: 160 }));
    observer.observe(el.value);
  }
});

watch(
  () => traffic.points,
  () => plot?.setData(data()),
);

onUnmounted(() => {
  observer?.disconnect();
  plot?.destroy();
});
</script>

<template>
  <SectionCard title="Uplink traffic · last 10 min">
    <template #actions>
      <span class="inline-flex items-center gap-1.5 text-xs text-text-2"
        ><span class="inline-block h-0.5 w-4 bg-accent" aria-hidden="true" />Download</span
      >
      <span class="inline-flex items-center gap-1.5 text-xs text-text-2"
        ><span class="inline-block w-4 border-t-2 border-dashed border-[#d9dee5]" aria-hidden="true" />Upload</span
      >
    </template>
    <p v-if="uplink.data.value && !hasCounters" class="m-0 text-sm text-text-2" data-testid="no-uplink-counters">
      The uplink reports no traffic counters.
    </p>
    <template v-else>
      <div class="mono text-[13px] text-text-2" data-testid="uplink-rates">
        ↓ {{ formatBitRate(latest?.downBps) }} · ↑ {{ formatBitRate(latest?.upBps) }}
      </div>
      <div ref="el" role="img" :aria-label="`Uplink throughput. ${summary}`" class="min-h-[160px] w-full" />
    </template>
  </SectionCard>
</template>
