import { onScopeDispose, ref, type Ref } from 'vue';

/** The current time, updated every `intervalMs` while the calling component lives (countdowns, TTLs). */
export function useNow(intervalMs = 1000): Ref<Date> {
  const now = ref(new Date());
  const timer = setInterval(() => (now.value = new Date()), intervalMs);
  onScopeDispose(() => clearInterval(timer));
  return now;
}
