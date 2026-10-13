// Live updates (plan §2.17, §3.7): the gateway's Server-Sent Events invalidate the cached server
// data, so every screen refetches what an event changed. The stream sends named events (`event:
// <type>`), so each type gets its own listener. The browser reconnects by itself and sends
// Last-Event-ID, so the gateway replays what was missed; `events_lost` says it cannot, and then
// everything is refetched.
import type { QueryClient } from '@tanstack/vue-query';
import { API_BASE } from '../api/fetcher';
import { EventType, type Event as GatewayEvent } from '../api/generated/model';
import { useConnectionStore } from '../stores/connection';
import { useEventStore } from '../stores/events';
import { useToastStore } from '../stores/toasts';

/** The cached data each event type changes (query key prefixes of the generated hooks). */
export const invalidates: Partial<Record<EventType, string[][]>> = {
  applied: [['state'], ['devices'], ['overlays'], ['faults'], ['networks'], ['uplink'], ['rules'], ['profiles']],
  apply_failed: [['state']],
  confirm_pending: [['state']],
  revision_created: [['revisions']],
  revision_applied: [['state'], ['revisions'], ['devices'], ['networks'], ['uplink'], ['faults'], ['audit']],
  revision_confirmed: [['state'], ['revisions'], ['audit']],
  revision_rolled_back: [['state'], ['revisions'], ['devices'], ['networks'], ['uplink'], ['faults'], ['overlays'], ['audit']],
  overlay_created: [['overlays'], ['devices'], ['faults'], ['state']],
  overlay_updated: [['overlays'], ['devices'], ['faults'], ['state']],
  overlay_removed: [['overlays'], ['devices'], ['faults'], ['state']],
  overlay_expired: [['overlays'], ['devices'], ['faults'], ['state']],
  overlay_orphaned: [['overlays'], ['devices'], ['faults'], ['state']],
  device_discovered: [['devices'], ['networks']],
  device_online: [['devices']],
  device_offline: [['devices']],
  device_identity_changed: [['devices'], ['flows']],
  observed_changed: [['devices'], ['flows'], ['networks']],
  dhcp_lease: [['devices'], ['networks']],
  uplink_changed: [['uplink']],
  network_degraded: [['networks'], ['state']],
  network_restored: [['networks'], ['state']],
  wireguard_peer_online: [['devices'], ['networks']],
  wireguard_peer_offline: [['devices'], ['networks']],
  safe_mode: [['state']],
  counter_epoch: [['state'], ['overlays'], ['faults']],
};

export interface EventSourceLike {
  addEventListener(type: string, listener: (e: MessageEvent) => void): void;
  onopen: ((this: EventSource, ev: Event) => unknown) | null;
  onerror: ((this: EventSource, ev: Event) => unknown) | null;
  close(): void;
}

function subjectName(ev: GatewayEvent): string {
  return ev.subject?.name ?? ev.subject?.id ?? '';
}

/** Toasts for the events a user must notice even on another screen. */
function toast(ev: GatewayEvent, toasts: ReturnType<typeof useToastStore>) {
  switch (ev.type) {
    case 'overlay_expired':
      toasts.push('fault', `${subjectName(ev) || 'An overlay'} expired`, ev.message);
      break;
    case 'revision_rolled_back': {
      const rev = ev.data?.['revision'];
      const reason = ev.data?.['reason'];
      toasts.push(
        'error',
        rev !== undefined ? `Revision ${String(rev)} rolled back` : 'Revision rolled back',
        typeof reason === 'string' && reason.startsWith('confirmation') ? 'It was not confirmed in time; the previous revision is active again.' : ev.message,
      );
      break;
    }
    case 'apply_failed':
      toasts.push('error', 'Apply failed', ev.message);
      break;
    case 'safe_mode':
      toasts.push('error', 'Safe mode: forwarding is disabled', ev.message);
      break;
  }
}

/**
 * Opens the event stream and keeps it until the returned function is called. `open` creates the
 * EventSource (tests pass a fake).
 */
export function startEventStream(
  queryClient: QueryClient,
  open: (url: string) => EventSourceLike = (url) => new EventSource(url, { withCredentials: true }),
): () => void {
  const connection = useConnectionStore();
  const events = useEventStore();
  const toasts = useToastStore();
  const es = open(`${API_BASE}/events`);
  let opened = false;

  const handle = (msg: MessageEvent) => {
    let ev: GatewayEvent;
    try {
      ev = JSON.parse(String(msg.data)) as GatewayEvent;
    } catch {
      return;
    }
    connection.answered();
    if (ev.type === 'events_lost') {
      void queryClient.invalidateQueries();
      return;
    }
    events.add(ev);
    for (const key of invalidates[ev.type] ?? []) {
      void queryClient.invalidateQueries({ queryKey: key });
    }
    toast(ev, toasts);
  };
  for (const type of Object.values(EventType)) {
    es.addEventListener(type, handle);
  }
  es.onopen = () => {
    connection.streamOpen = true;
    connection.answered();
    // after a reconnect the replay covers the gap, but data fetched while offline may be stale
    if (opened) void queryClient.invalidateQueries();
    opened = true;
  };
  es.onerror = () => {
    connection.streamOpen = false;
    // the stream ending is not proof the gateway is gone: ask it (the fetch reports either way)
    void queryClient.refetchQueries({ queryKey: ['state'] });
  };
  return () => {
    es.close();
    connection.streamOpen = false;
  };
}
