import { onScopeDispose, ref, type Ref } from 'vue';
import { gatewayNow } from '../lib/clock';

/**
 * The current time on the gateway's clock, updated every `intervalMs` while the calling component
 * lives. Countdowns, TTLs, leases and "ago" compare it with the gateway's time stamps.
 */
export function useNow(intervalMs = 1000): Ref<Date> {
  const now = ref(gatewayNow());
  const timer = setInterval(() => (now.value = gatewayNow()), intervalMs);
  onScopeDispose(() => clearInterval(timer));
  return now;
}
