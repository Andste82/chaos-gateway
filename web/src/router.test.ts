import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAppRouter } from './router';

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

/** The gateway as the router asks it: setup status, then the session. */
function gateway(setupDone: boolean, session: boolean) {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation((url: string) => {
      if (url === '/api/v1/setup') return Promise.resolve(respond(200, { completed: setupDone }));
      if (url === '/api/v1/auth/session') {
        return Promise.resolve(session ? respond(200, { kind: 'session', scope: 'full', csrf_token: 't' }) : respond(401, { code: 'unauthorized' }));
      }
      return Promise.resolve(respond(404, {}));
    }),
  );
}

beforeEach(() => {
  setActivePinia(createPinia());
  // uPlot (the overview's chart) asks for it when it loads; jsdom has none
  window.matchMedia ??= ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
  window.history.replaceState({}, '', '/');
});
afterEach(() => vi.unstubAllGlobals());

describe('the router', () => {
  it('leads every page to the setup while it is open', async () => {
    gateway(false, false);
    const r = createAppRouter();
    await r.push('/devices');
    expect(r.currentRoute.value.name).toBe('setup');
  });

  it('leads to the login without a session, and remembers the page', async () => {
    gateway(true, false);
    const r = createAppRouter();
    await r.push('/devices/esp32-42');
    expect(r.currentRoute.value.name).toBe('login');
    expect(r.currentRoute.value.query.next).toBe('/devices/esp32-42');
    await r.push('/setup');
    expect(r.currentRoute.value.name).toBe('login');
  });

  it('with a session, the login and the setup lead to the overview', { timeout: 20_000 }, async () => {
    gateway(true, true);
    const r = createAppRouter();
    await r.push('/login');
    expect(r.currentRoute.value.name).toBe('overview');
    await r.push('/setup');
    expect(r.currentRoute.value.name).toBe('overview');
    await r.push('/devices/esp32-42');
    expect(r.currentRoute.value.name).toBe('device');
    expect(r.currentRoute.value.params.id).toBe('esp32-42');
    await r.push('/networks');
    expect(r.currentRoute.value.name).toBe('networks');
    await r.push('/networks/routing');
    expect(r.currentRoute.value.name).toBe('routing');
    await r.push('/networks/IoT');
    expect(r.currentRoute.value.name).toBe('network');
    expect(r.currentRoute.value.params.id).toBe('IoT');
    await r.push('/linux');
    expect(r.currentRoute.value.name).toBe('linux');
    await r.push('/scenarios');
    expect(r.currentRoute.value.name).toBe('not-found');
  });

  it('lets the app show the unreachable gateway', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const r = createAppRouter();
    await r.push('/devices');
    expect(r.currentRoute.value.name).toBe('devices');
  });
});
