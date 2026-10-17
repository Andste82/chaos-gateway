<script setup lang="ts">
// Uplink traffic of the last 10 minutes (plan §2.17, Overview), drawn with uPlot (plan §3.7) from
// the rates the gateway samples from the uplink's byte counters (GET /uplink/traffic, issue #63): its
// history at once, then every interval the samples after the newest one the chart has.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import uPlot from 'uplot';
import { getGetUplinkTrafficQueryKey, getUplinkTraffic } from '../../api/generated/hooks/config/config';
import { formatBitRate, formatHM } from '../../lib/format';
import { firstError } from '../../composables/gateway';
import LoadError from '../ui/LoadError.vue';
import { chartRange, useTrafficStore } from '../../stores/traffic';
import SectionCard from '../ui/SectionCard.vue';

const traffic = useTrafficStore();
const uplink = useQuery({
  queryKey: getGetUplinkTrafficQueryKey(),
  queryFn: async ({ signal }) => {
    const r = await getUplinkTraffic(traffic.newest ? { since: traffic.newest } : undefined, { signal });
    traffic.merge(r.samples);
    return r;
  },
  // as often as the gateway reads the counters
  refetchInterval: (q) => (q.state.data?.interval_seconds ?? 5) * 1000,
});

/** The uplink could not be read and nothing was read before: the chart must not look like an idle line. */
const uplinkError = computed(() => firstError(uplink));
const hasCounters = computed(() => uplink.data.value?.counters === true);
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
      // the last 10 minutes up to the newest reading; before there is one, up to now (not the epoch)
      scales: { x: { time: true, range: (_u, _min, max) => chartRange(max) } },
      axes: [
        {
          stroke: '#848E9D',
          grid: { stroke: '#232A34' },
          ticks: { stroke: '#232A34' },
          font: '12px IBM Plex Mono',
          // the app's clock: 24-hour HH:MM, no date line (uPlot's default is en-US 12-hour with M/D/YY)
          values: (_u, vals) => vals.map((v) => formatHM(new Date(v * 1000))),
        },
        {
          stroke: '#848E9D',
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
  // the canvas draws the axes with IBM Plex Mono only once the font is loaded: draw them again then
  void document.fonts?.ready.then(() => plot?.redraw(false, true));
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
      <LoadError v-if="uplinkError" :error="uplinkError" what="The uplink counters are not loaded" :retry="uplink.refetch" />
      <div v-else class="mono text-[13px] text-text-2" data-testid="uplink-rates">
        ↓ {{ formatBitRate(latest?.downBps) }} · ↑ {{ formatBitRate(latest?.upBps) }}
      </div>
      <div class="relative">
        <div
          ref="el"
          role="img"
          :aria-label="`Uplink throughput. ${summary}`"
          :data-samples="traffic.points.length"
          class="min-h-[160px] w-full"
          data-testid="uplink-chart"
        />
        <p
          v-if="!latest && !uplinkError"
          class="absolute inset-0 m-0 flex items-center justify-center text-[13px] text-text-2"
          role="status"
          data-testid="uplink-collecting"
        >
          Collecting the first readings…
        </p>
      </div>
    </template>
  </SectionCard>
</template>
