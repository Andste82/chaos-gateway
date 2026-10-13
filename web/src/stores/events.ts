import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { Event as GatewayEvent } from '../api/generated/model';

/** The events the stream delivered since the page was opened, newest first (overview, recent events). */
export const useEventStore = defineStore('events', () => {
  const recent = ref<GatewayEvent[]>([]);

  function add(ev: GatewayEvent) {
    if (recent.value.some((e) => e.id === ev.id)) return; // a replay after a reconnect
    recent.value = [ev, ...recent.value].slice(0, 50);
  }

  return { recent, add };
});
