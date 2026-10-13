// Live updates (plan §2.17, §3.7): the gateway's Server-Sent Events invalidate the cached server
// data, so every screen refetches what an event changed. The stream sends named events (`event:
// <type>`), so each type gets its own listener. After a network error the browser reconnects by
// itself and sends Last-Event-ID, so the gateway replays what was missed; `events_lost` says it
// cannot, and then everything is refetched. An answer that is not a stream (a 503 "too many event
// streams", a 502 of a proxy while the gateway restarts) ends the EventSource for good: then a new
// one is opened after a growing pause, and since it sends no Last-Event-ID, everything is
// refetched once it is open.
import type { QueryClient } from '@tanstack/vue-query';
import { API_BASE } from '../api/fetcher';
import { EventType, type Event as GatewayEvent } from '../api/generated/model';
import { useConnectionStore } from '../stores/connection';
import { useEventStore } from '../stores/events';
import { useToastStore } from '../stores/toasts';

/** The cached data each event type changes (query key prefixes of the generated hooks). The kernel
 * state of the technical view (`['linux-state']`, outside the `state` prefix: reading it runs nft, tc,
 * ip and wg) is refetched only for events that change what the gateway applied, and polls otherwise. */
export const invalidates: Partial<Record<EventType, string[][]>> = {
  applied: [['state'], ['devices'], ['overlays'], ['faults'], ['networks'], ['uplink'], ['rules'], ['profiles'], ['routing'], ['linux-state']],
  apply_failed: [['state']],
  confirm_pending: [['state']],
  revision_created: [['revisions']],
  revision_applied: [['state'], ['revisions'], ['devices'], ['networks'], ['uplink'], ['faults'], ['rules'], ['profiles'], ['audit'], ['routing'], ['linux-state']],
  // a confirmed revision becomes the active one: the views of the configuration change with it
  revision_confirmed: [['state'], ['revisions'], ['devices'], ['networks'], ['uplink'], ['faults'], ['rules'], ['profiles'], ['audit'], ['routing'], ['linux-state']],
  revision_rolled_back: [['state'], ['revisions'], ['devices'], ['networks'], ['uplink'], ['faults'], ['rules'], ['profiles'], ['overlays'], ['audit'], ['routing'], ['linux-state']],
  overlay_created: [['overlays'], ['devices'], ['faults'], ['profiles'], ['state'], ['linux-state']],
  overlay_updated: [['overlays'], ['devices'], ['faults'], ['profiles'], ['state'], ['linux-state']],
  overlay_removed: [['overlays'], ['devices'], ['faults'], ['profiles'], ['state'], ['linux-state']],
  overlay_expired: [['overlays'], ['devices'], ['faults'], ['profiles'], ['state'], ['linux-state']],
  overlay_orphaned: [['overlays'], ['devices'], ['faults'], ['profiles'], ['state'], ['linux-state']],
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
  routing_session_changed: [['routing']],
  routing_routes_changed: [['routing']],
  safe_mode: [['state']],
  counter_epoch: [['state'], ['overlays'], ['faults']],
};

export interface EventSourceLike {
  addEventListener(type: string, listener: (e: MessageEvent) => void): void;
  onopen: ((this: EventSource, ev: Event) => unknown) | null;
  onerror: ((this: EventSource, ev: Event) => unknown) | null;
  /** 0 connecting, 1 open, 2 closed (the browser gave up: no reconnect of its own follows). */
  readonly readyState: number;
  close(): void;
}

const CLOSED = 2;
/** The pause before a new stream after the browser gave one up: doubling from the first to the last. */
export const reopenDelayMs = { first: 2_000, max: 30_000 };

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
      const restored = ev.data?.['restored'];
      // plan §2.17 "Commit-confirm": on timeout a notice "rolled back to revision N-1"
      const back = restored !== undefined ? `Rolled back to revision ${String(restored)}` : 'The previous revision is active again';
      toasts.push(
        'error',
        rev !== undefined ? `Revision ${String(rev)} rolled back` : 'Revision rolled back',
        typeof reason === 'string' && reason.startsWith('confirmation')
          ? // revision 1 is the first-start setup: there is no earlier one, the setup opens again
            rev === 1
            ? 'It was not confirmed in time; the setup is open again.'
            : `It was not confirmed in time. ${back}.`
          : reason === 'requested'
            ? `Rolled back on request. ${back}.`
            : ev.message,
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
  let es: EventSourceLike | undefined;
  let opened = false;
  let stopped = false;
  let delay = reopenDelayMs.first;
  let timer: ReturnType<typeof setTimeout> | undefined;

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
  const connect = () => {
    timer = undefined;
    const source = open(`${API_BASE}/events`);
    es = source;
    for (const type of Object.values(EventType)) {
      source.addEventListener(type, handle);
    }
    source.onopen = () => {
      connection.streamOpen = true;
      connection.answered();
      delay = reopenDelayMs.first;
      // after a reconnect the replay covers the gap, but data fetched while offline may be stale;
      // a new stream after one the browser gave up replays nothing
      if (opened) void queryClient.invalidateQueries();
      opened = true;
    };
    source.onerror = () => {
      connection.streamOpen = false;
      // the stream ending is not proof the gateway is gone: ask it (the fetch reports either way)
      void queryClient.refetchQueries({ queryKey: ['state'] });
      if (source.readyState === CLOSED && !stopped && timer === undefined) {
        source.close();
        timer = setTimeout(connect, delay);
        delay = Math.min(delay * 2, reopenDelayMs.max);
      }
    };
  };
  connect();
  return () => {
    stopped = true;
    if (timer !== undefined) clearTimeout(timer);
    es?.close();
    connection.streamOpen = false;
  };
}
