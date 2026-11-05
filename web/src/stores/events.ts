import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { Event as GatewayEvent } from '../api/generated/model';

/**
 * Events that are no news for the overview: `ntp_query_seen` and `nts_seen` come for every device that asks the NTP server, once in
 * 10 s, and serve the wait of a scenario (which does not depend on them); the Time panel of the device shows the queries.
 * `ntp_answered`, which says that a time fault shaped an answer, stays.
 */
const quietTypes = new Set<string>(['ntp_query_seen', 'nts_seen']);

/** The events the stream delivered since the page was opened, newest first (overview, recent events). */
export const useEventStore = defineStore('events', () => {
  const recent = ref<GatewayEvent[]>([]);

  function add(ev: GatewayEvent) {
    if (quietTypes.has(ev.type)) return;
    if (recent.value.some((e) => e.id === ev.id)) return; // a replay after a reconnect
    recent.value = [ev, ...recent.value].slice(0, 50);
  }

  return { recent, add };
});
