import { defineStore } from 'pinia';
import { computed, ref } from 'vue';

/**
 * Whether the backend answers (plan §2.17, "Offline / backend unreachable"): the fetch wrapper
 * reports every request, the event stream its connection. The app is offline from the first
 * request that did not reach the gateway until the next one that did; live data then shows the
 * time it was last updated.
 */
export const useConnectionStore = defineStore('connection', () => {
  const reachable = ref(true);
  const lastUpdated = ref<Date | undefined>();
  const streamOpen = ref(false);

  function answered(at = new Date()) {
    reachable.value = true;
    lastUpdated.value = at;
  }

  function unreachable() {
    reachable.value = false;
  }

  return { reachable, lastUpdated, streamOpen, offline: computed(() => !reachable.value), answered, unreachable };
});
