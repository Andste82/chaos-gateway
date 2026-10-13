import { QueryClient } from '@tanstack/vue-query';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useConnectionStore } from '../stores/connection';
import { useEventStore } from '../stores/events';
import { useToastStore } from '../stores/toasts';
import { invalidates, startEventStream, type EventSourceLike } from './eventStream';

class FakeSource implements EventSourceLike {
  listeners = new Map<string, ((e: MessageEvent) => void)[]>();
  onopen: EventSourceLike['onopen'] = null;
  onerror: EventSourceLike['onerror'] = null;
  closed = false;
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
    this.onopen?.call(this as unknown as EventSource, new Event('open'));
  }
  fail() {
    this.onerror?.call(this as unknown as EventSource, new Event('error'));
  }
}

function setup() {
  const qc = new QueryClient();
  const invalidate = vi.spyOn(qc, 'invalidateQueries').mockResolvedValue();
  const refetch = vi.spyOn(qc, 'refetchQueries').mockResolvedValue();
  let es!: FakeSource;
  const stop = startEventStream(qc, (url) => (es = new FakeSource(url)));
  return { qc, invalidate, refetch, es, stop };
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

  it('ignores a message that is not JSON', () => {
    const { es, invalidate } = setup();
    for (const fn of es.listeners.get('applied') ?? []) fn(new MessageEvent('applied', { data: 'not json' }));
    expect(invalidate).not.toHaveBeenCalled();
  });
});
