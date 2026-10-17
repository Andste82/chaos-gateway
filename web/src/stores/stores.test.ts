import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setCsrfToken } from '../api/fetcher';
import { useConnectionStore } from './connection';
import { useEventStore } from './events';
import { onLoggedOut, useSessionStore } from './session';
import { useToastStore } from './toasts';
import { chartRange, useTrafficStore } from './traffic';

function respond(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

beforeEach(() => setActivePinia(createPinia()));
afterEach(() => {
  vi.unstubAllGlobals();
  setCsrfToken(undefined);
});

describe('session store', () => {
  it('sends the browser to the setup while it is open', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(200, { completed: false })));
    const s = useSessionStore();
    await s.bootstrap();
    expect(s.status).toBe('setup');
  });

  it('knows a session, and its CSRF token goes on unsafe requests', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(respond(200, { completed: true }))
      .mockResolvedValueOnce(respond(200, { kind: 'session', scope: 'full', csrf_token: 'tok' }))
      .mockResolvedValueOnce(respond(204, undefined));
    vi.stubGlobal('fetch', fetchMock);
    const s = useSessionStore();
    await Promise.all([s.bootstrap(), s.bootstrap()]); // one round trip for both
    expect(s.status).toBe('authenticated');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await s.logout();
    expect(new Headers(fetchMock.mock.calls[2]?.[1].headers).get('X-CSRF-Token')).toBe('tok');
    expect(s.status).toBe('anonymous');
  });

  it('without a session the user is anonymous; a login makes the session', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(respond(200, { completed: true }))
        .mockResolvedValueOnce(respond(401, { code: 'unauthorized' }))
        .mockResolvedValueOnce(respond(200, { kind: 'session', scope: 'full', csrf_token: 'tok2' })),
    );
    const s = useSessionStore();
    await s.bootstrap();
    expect(s.status).toBe('anonymous');
    await s.login('pw');
    expect(s.status).toBe('authenticated');
    expect(s.session?.csrf_token).toBe('tok2');
  });

  it('a setup the gateway rolled back is open again and ends the session of its login', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(respond(200, { kind: 'session', scope: 'full', csrf_token: 'tok3' }))
      .mockResolvedValueOnce(respond(204, undefined));
    vi.stubGlobal('fetch', fetchMock);
    const s = useSessionStore();
    await s.login('pw');
    expect(s.status).toBe('authenticated');
    s.setupReopened();
    expect(s.status).toBe('setup');
    expect(s.session).toBeUndefined();
    // the CSRF token of the gone session is not sent any more
    await s.logout();
    expect(new Headers(fetchMock.mock.calls[1]?.[1].headers).get('X-CSRF-Token')).toBeNull();
  });

  it('a logout tells the page to drop what the session read, also when the gateway does not answer it', async () => {
    const dropped = vi.fn();
    const off = onLoggedOut(dropped);
    try {
      vi.stubGlobal(
        'fetch',
        vi
          .fn()
          .mockResolvedValueOnce(respond(200, { kind: 'session', scope: 'full', csrf_token: 'tok4' }))
          .mockResolvedValueOnce(respond(204, undefined))
          .mockRejectedValueOnce(new TypeError('Failed to fetch')),
      );
      const s = useSessionStore();
      await s.login('pw');
      expect(dropped).not.toHaveBeenCalled();
      await s.logout();
      expect(dropped).toHaveBeenCalledTimes(1);
      await s.logout().catch(() => undefined);
      expect(dropped).toHaveBeenCalledTimes(2);
      // an ended session (401 elsewhere) is no logout: main.ts clears the cache itself and keeps the draft
      s.ended();
      expect(dropped).toHaveBeenCalledTimes(2);
    } finally {
      off();
    }
  });

  it('passes other failures on, so the app can say the gateway does not answer', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const s = useSessionStore();
    await expect(s.bootstrap()).rejects.toThrow('the gateway does not answer');
    expect(s.status).toBe('unknown');
  });
});

describe('connection store', () => {
  it('is offline from a failed request to the next answered one', () => {
    const c = useConnectionStore();
    expect(c.offline).toBe(false);
    c.unreachable();
    expect(c.offline).toBe(true);
    const at = new Date(2026, 0, 1, 12, 0, 0);
    c.answered(at);
    expect(c.offline).toBe(false);
    expect(c.lastUpdated).toEqual(at);
  });
});

describe('toast store', () => {
  it('keeps the last five toasts and dismisses one by id', () => {
    const t = useToastStore();
    const ids = Array.from({ length: 7 }, (_, i) => t.push('info', `t${i}`));
    expect(t.toasts.map((x) => x.title)).toEqual(['t2', 't3', 't4', 't5', 't6']);
    t.dismiss(ids[6]!);
    expect(t.toasts.map((x) => x.title)).toEqual(['t2', 't3', 't4', 't5']);
  });
});

describe('event store', () => {
  it('keeps the newest events first, once each', () => {
    const e = useEventStore();
    e.add({ id: '1', type: 'applied', time: '' });
    e.add({ id: '2', type: 'device_online', time: '' });
    e.add({ id: '1', type: 'applied', time: '' }); // replayed after a reconnect
    expect(e.recent.map((x) => x.id)).toEqual(['2', '1']);
  });
});

describe('traffic chart range', () => {
  it('ends at the newest point, or at now while there is none, never at the epoch', () => {
    expect(chartRange(1_800_000_600)).toEqual([1_800_000_000, 1_800_000_600]);
    const now = new Date(2026, 9, 14, 19, 40, 0).getTime();
    expect(chartRange(null, now)).toEqual([now / 1000 - 600, now / 1000]);
    expect(chartRange(undefined, now)[1]).toBe(now / 1000);
  });
});

describe('traffic store', () => {
  // issue #63: the gateway's history at once, then the newer samples appended without a duplicate
  it('appends the gateway samples newer than its newest point, over the last ten minutes', () => {
    const t = useTrafficStore();
    const t0 = Date.UTC(2026, 0, 1, 12, 0, 0);
    const s = (ms: number, down: number) => ({ time: new Date(t0 + ms).toISOString(), down_bps: down, up_bps: down / 10 });
    t.merge([s(0, 1000), s(5000, 2000)]);
    expect(t.points).toEqual([
      { t: t0 / 1000, downBps: 1000, upBps: 100 },
      { t: (t0 + 5000) / 1000, downBps: 2000, upBps: 200 },
    ]);
    expect(t.newest).toBe(new Date(t0 + 5000).toISOString());
    // an answer that repeats the newest sample (or an empty one) adds nothing twice
    t.merge([s(5000, 2000), s(10_000, 3000)]);
    t.merge([]);
    expect(t.points.map((p) => p.downBps)).toEqual([1000, 2000, 3000]);
    expect(t.newest).toBe(new Date(t0 + 10_000).toISOString());
    // points older than the window drop out
    t.merge([s(604_000, 4000)]);
    expect(t.points.map((p) => p.downBps)).toEqual([2000, 3000, 4000]);
    t.merge([s(609_000, 5000)]);
    expect(t.points.map((p) => p.downBps)).toEqual([3000, 4000, 5000]);
  });
});
