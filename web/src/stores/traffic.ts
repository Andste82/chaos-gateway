import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { UplinkTrafficSample } from '../api/generated/model';

/** One point of the uplink chart: bit rates between two readings of the interface counters. */
export interface RatePoint {
  /** seconds since the epoch */
  t: number;
  downBps: number;
  upBps: number;
}

/** How long the chart looks back (plan §2.17: "uplink traffic · last 10 min"). */
export const windowSeconds = 600;

/** The chart's time range: the window up to the newest point, or up to now while there is none (not the epoch). */
export function chartRange(max: number | null | undefined, now = Date.now()): [number, number] {
  const end = typeof max === 'number' && Number.isFinite(max) ? max : Math.floor(now / 1000);
  return [end - windowSeconds, end];
}

/**
 * The uplink's traffic as the gateway sampled it (`GET /uplink/traffic`, issue #63): the history it
 * keeps, loaded at once, then the samples taken after the newest one here. It is a store, not
 * component state, so the chart keeps its points when the user leaves the overview and fills the
 * gap from the gateway's history when they come back.
 */
export const useTrafficStore = defineStore('traffic', () => {
  const points = ref<RatePoint[]>([]);
  /** The newest sample's time as the gateway wrote it: the `since` of the next request. */
  const newest = ref<string>();

  /** Appends the samples of an answer that are newer than the newest point (none twice). */
  function merge(samples: UplinkTrafficSample[]) {
    const last = points.value.at(-1)?.t;
    const fresh = samples
      .map((s) => ({ point: { t: Date.parse(s.time) / 1000, downBps: s.down_bps, upBps: s.up_bps }, time: s.time }))
      .filter((s) => Number.isFinite(s.point.t) && (last === undefined || s.point.t > last));
    if (fresh.length === 0) return;
    newest.value = fresh[fresh.length - 1]!.time;
    const end = fresh[fresh.length - 1]!.point.t;
    points.value = [...points.value, ...fresh.map((s) => s.point)].filter((p) => p.t > end - windowSeconds);
  }

  return { points, newest, merge };
});
