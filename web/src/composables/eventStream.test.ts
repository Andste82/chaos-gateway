import { QueryClient } from '@tanstack/vue-query';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useConnectionStore } from '../stores/connection';
import { useEventStore } from '../stores/events';
import { useToastStore } from '../stores/toasts';
import { invalidates, reopenDelayMs, startEventStream, type EventSourceLike } from './eventStream';

class FakeSource implements EventSourceLike {
  listeners = new Map<string, ((e: MessageEvent) => void)[]>();
  onopen: EventSourceLike['onopen'] = null;
  onerror: EventSourceLike['onerror'] = null;
  closed = false;
  readyState = 0;
  constructor(readonly url: string) {}
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  send(type: string, ev: object) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify({ id: '1', time: '', type, ...ev }) }));
  }
  open() {
    this.readyState = 1;
    this.onopen?.call(this as unknown as EventSource, new Event('open'));
  }
  /** A network error: the browser reconnects by itself. */
  fail() {
    this.readyState = 0;
    this.onerror?.call(this as unknown as EventSource, new Event('error'));
  }
  /** An answer that is not a stream (503, 502): the browser gives the stream up for good. */
  refuse() {
    this.readyState = 2;
    this.onerror?.call(this as unknown as EventSource, new Event('error'));
  }
}

function setup() {
  const qc = new QueryClient();
  const invalidate = vi.spyOn(qc, 'invalidateQueries').mockResolvedValue();
  const refetch = vi.spyOn(qc, 'refetchQueries').mockResolvedValue();
  const all: FakeSource[] = [];
  const stop = startEventStream(qc, (url) => {
    const s = new FakeSource(url);
    all.push(s);
    return s;
  });
  return { qc, invalidate, refetch, es: all[0]!, all, stop };
}

beforeEach(() => setActivePinia(createPinia()));

/** The query keys a spy of invalidateQueries was called with. */
function keys(spy: { mock: { calls: unknown[][] } }): unknown[] {
  return spy.mock.calls.map((c) => (c[0] as { queryKey?: unknown } | undefined)?.queryKey);
}

describe('the event stream', () => {
  it('listens to the gateway events and closes again', () => {
    const { es, stop } = setup();
    expect(es.url).toBe('/api/v1/events');
    for (const t of ['applied', 'device_discovered', 'overlay_expired', 'events_lost']) expect(es.listeners.has(t)).toBe(true);
    stop();
    expect(es.closed).toBe(true);
  });

  it('invalidates the data an event changes, and nothing else', () => {
    const { es, invalidate } = setup();
    es.send('device_discovered', { subject: { kind: 'device', id: 'd', name: 'dev-1' }, message: 'device dev-1 discovered' });
    expect(keys(invalidate)).toEqual(invalidates.device_discovered);
    expect(useEventStore().recent[0]?.message).toBe('device dev-1 discovered');
    invalidate.mockClear();
    es.send('overlay_created', {});
    expect(keys(invalidate)).toContainEqual(['overlays']);
    expect(keys(invalidate)).not.toContainEqual(['networks']);
  });

  it('reads the groups again when a revision changes the configuration, so their names stay current', () => {
    const { es, invalidate } = setup();
    for (const t of ['applied', 'revision_applied', 'revision_confirmed', 'revision_rolled_back']) {
      es.send(t, {});
      expect(keys(invalidate)).toContainEqual(['groups']);
      invalidate.mockClear();
    }
    es.send('device_online', {});
    expect(keys(invalidate)).not.toContainEqual(['groups']);
  });

  it('reads the kernel state again only for events that change what the gateway applied', () => {
    const { es, invalidate } = setup();
    for (const t of ['counter_epoch', 'confirm_pending', 'network_degraded', 'safe_mode']) {
      es.send(t, {});
      expect(keys(invalidate)).toContainEqual(['state']);
      expect(keys(invalidate)).not.toContainEqual(['linux-state']);
      invalidate.mockClear();
    }
    for (const t of ['applied', 'revision_rolled_back', 'overlay_created']) {
      es.send(t, {});
      expect(keys(invalidate)).toContainEqual(['linux-state']);
      invalidate.mockClear();
    }
  });

  it('refetches everything when the gateway cannot replay what was missed', () => {
    const { es, invalidate } = setup();
    es.send('events_lost', { data: { reason: 'restart' } });
    expect(invalidate).toHaveBeenCalledWith();
  });

  it('toasts an expired overlay and a rollback', () => {
    const { es } = setup();
    es.send('overlay_expired', { subject: { kind: 'overlay', id: 'o', name: 'ESP32-42 latency' } });
    es.send('revision_rolled_back', { data: { revision: 43, reason: 'confirmation timeout' } });
    const t = useToastStore().toasts;
    expect(t.map((x) => [x.kind, x.title])).toEqual([
      ['fault', 'ESP32-42 latency expired'],
      ['error', 'Revision 43 rolled back'],
    ]);
    expect(t[1]?.detail).toMatch(/not confirmed in time/);
  });

  it('reads the captures and their storage again when a capture starts or ends and when the disk runs low', () => {
    const { es, invalidate } = setup();
    for (const t of ['capture_started', 'capture_stopped', 'disk_low']) {
      es.send(t, { subject: { kind: 'capture', id: 'c1', name: 'bug-12' } });
      expect(keys(invalidate)).toContainEqual(['captures']);
      invalidate.mockClear();
    }
    es.send('device_online', {});
    expect(keys(invalidate)).not.toContainEqual(['captures']);
  });

  it('toasts a capture the quota stopped, one that failed and a low disk, but not an end the user asked for', () => {
    const { es } = setup();
    const subject = { kind: 'capture', id: 'c1', name: 'bug-12' };
    es.send('capture_stopped', { subject, data: { reason: 'user' } });
    es.send('capture_stopped', { subject, data: { reason: 'duration' } });
    es.send('capture_stopped', { subject, data: { reason: 'size' } }); // the size limit the user chose has no message
    expect(useToastStore().toasts).toEqual([]);
    es.send('capture_stopped', { subject, data: { reason: 'size', message: 'the captures take 5.1 GiB, more than their quota of 5 GiB' } });
    es.send('capture_stopped', { subject, data: { reason: 'error', message: 'the service restarted' } });
    es.send('disk_low', { data: { free_percent: 4 } });
    expect(useToastStore().toasts.map((x) => [x.kind, x.title])).toEqual([
      ['fault', 'bug-12 stopped: quota reached'],
      ['error', 'bug-12 failed'],
      ['error', 'The disk of the captures is low'],
    ]);
  });

  it('names the revision a rollback restored, at the deadline and on request', () => {
    const { es } = setup();
    es.send('revision_rolled_back', { data: { revision: 43, reason: 'confirmation timeout', restored: 42 } });
    es.send('revision_rolled_back', { data: { revision: 44, reason: 'requested', restored: 42 } });
    expect(useToastStore().toasts.map((x) => x.detail)).toEqual([
      'It was not confirmed in time. Rolled back to revision 42.',
      'Rolled back on request. Rolled back to revision 42.',
    ]);
  });

  it('says the setup is open again when revision 1, the setup, was rolled back', () => {
    const { es } = setup();
    es.send('revision_rolled_back', { data: { revision: 1, reason: 'confirmation timeout' } });
    expect(useToastStore().toasts[0]?.detail).toBe('It was not confirmed in time; the setup is open again.');
  });

  it('tells the connection store about the stream, and asks the gateway when it breaks', () => {
    const { es, refetch, invalidate } = setup();
    const c = useConnectionStore();
    es.open();
    expect(c.streamOpen).toBe(true);
    expect(invalidate).not.toHaveBeenCalled(); // the first open needs no refetch
    es.fail();
    expect(c.streamOpen).toBe(false);
    expect(refetch).toHaveBeenCalledWith({ queryKey: ['state'] });
    es.open(); // a reconnect: data fetched meanwhile may be stale
    expect(invalidate).toHaveBeenCalledWith();
  });

  it('opens a new stream with a growing pause when the browser gave one up, and refetches everything', () => {
    vi.useFakeTimers();
    try {
      const { es, all, invalidate, stop } = setup();
      const c = useConnectionStore();
      es.open();
      es.fail(); // a network error: the browser reconnects, no new stream
      vi.advanceTimersByTime(60_000);
      expect(all).toHaveLength(1);

      es.refuse(); // e.g. 503 too many event streams
      expect(es.closed).toBe(true);
      expect(c.streamOpen).toBe(false);
      vi.advanceTimersByTime(reopenDelayMs.first - 1);
      expect(all).toHaveLength(1);
      vi.advanceTimersByTime(1);
      expect(all).toHaveLength(2);
      expect(all[1]!.url).toBe('/api/v1/events');

      // refused again: the pause doubles
      all[1]!.refuse();
      vi.advanceTimersByTime(reopenDelayMs.first * 2 - 1);
      expect(all).toHaveLength(2);
      vi.advanceTimersByTime(1);
      expect(all).toHaveLength(3);

      // the new stream replays nothing: everything is refetched, and the pause starts over
      invalidate.mockClear();
      all[2]!.open();
      expect(c.streamOpen).toBe(true);
      expect(invalidate).toHaveBeenCalledWith();
      all[2]!.send('device_online', {});
      expect(keys(invalidate)).toContainEqual(['devices']);
      all[2]!.refuse();
      vi.advanceTimersByTime(reopenDelayMs.first);
      expect(all).toHaveLength(4);

      // stopping cancels a pending reopen
      all[3]!.refuse();
      stop();
      vi.advanceTimersByTime(reopenDelayMs.max);
      expect(all).toHaveLength(4);
    } finally {
      vi.useRealTimers();
    }
  });

  it('caps the pause before a new stream', () => {
    vi.useFakeTimers();
    try {
      const { all } = setup();
      for (let i = 0; i < 8; i++) {
        all[all.length - 1]!.refuse();
        vi.advanceTimersByTime(reopenDelayMs.max);
      }
      expect(all).toHaveLength(9);
      all[all.length - 1]!.refuse();
      vi.advanceTimersByTime(reopenDelayMs.max - 1);
      expect(all).toHaveLength(9);
      vi.advanceTimersByTime(1);
      expect(all).toHaveLength(10);
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores a message that is not JSON', () => {
    const { es, invalidate } = setup();
    for (const fn of es.listeners.get('applied') ?? []) fn(new MessageEvent('applied', { data: 'not json' }));
    expect(invalidate).not.toHaveBeenCalled();
  });
});
