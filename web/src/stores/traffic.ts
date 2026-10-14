import { defineStore } from 'pinia';
import { ref } from 'vue';

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
 * The uplink's traffic since the page was opened, from the byte counters of `GET /uplink`. It is
 * a store, not component state, so the chart keeps its history when the user leaves the overview.
 */
export const useTrafficStore = defineStore('traffic', () => {
  const points = ref<RatePoint[]>([]);
  let last: { t: number; rx: number; tx: number } | undefined;

  /** Adds a reading of the counters (bytes received and sent) taken at `at`. */
  function record(rx: number, tx: number, at: Date = new Date()) {
    const t = at.getTime() / 1000;
    if (last && t > last.t) {
      const dt = t - last.t;
      // a counter that went down was reset (interface re-created): no rate for that interval
      if (rx >= last.rx && tx >= last.tx) {
        points.value = [
          ...points.value.filter((p) => p.t > t - windowSeconds),
          { t, downBps: ((rx - last.rx) * 8) / dt, upBps: ((tx - last.tx) * 8) / dt },
        ];
      }
    }
    if (!last || t > last.t) last = { t, rx, tx };
  }

  return { points, record };
});
