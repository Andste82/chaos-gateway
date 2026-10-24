import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAppRouter } from './router';

// The router loads a page's component (a lazy chunk) before a navigation ends. The real ones pull
// in the whole app (the overview alone brings uPlot), which took about 8 s to transform on an idle
// machine and ran past the timeout while Playwright ran at the same time. These tests are about
// where the guards lead, so every page is a stub here; the e2e tests load the real pages.
vi.mock('./views/SetupWizard.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/LoginView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./components/layout/AppShell.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/OverviewView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/DevicesView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/DeviceDetailView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/RulesView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/FaultsView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/ProfilesView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/ScenariosView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/RunView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/NetworksView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/RoutingView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/LinuxStateView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));
vi.mock('./views/NotFoundView.vue', () => ({ default: { name: 'PageStub', render: () => null } }));

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

  it('with a session, the login and the setup lead to the overview', async () => {
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
    expect(r.currentRoute.value.name).toBe('scenarios');
    await r.push('/scenarios/mqtt-outage?tab=yaml');
    expect(r.currentRoute.value.name).toBe('scenario');
    expect(r.currentRoute.value.params.id).toBe('mqtt-outage');
    await r.push('/runs/0a1b2c3d');
    expect(r.currentRoute.value.name).toBe('run');
    expect(r.currentRoute.value.params.id).toBe('0a1b2c3d');
    await r.push('/captures');
    expect(r.currentRoute.value.name).toBe('not-found');
  });

  it('stubs every page (a new page needs its vi.mock above, or these tests load it for real)', async () => {
    const r = createAppRouter();
    for (const route of r.getRoutes()) {
      for (const load of Object.values(route.components ?? {})) {
        const page = await (load as () => Promise<{ default: { name?: string } }>)();
        expect(page.default.name, route.path).toBe('PageStub');
      }
    }
  });

  it('lets the app show the unreachable gateway', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const r = createAppRouter();
    await r.push('/devices');
    expect(r.currentRoute.value.name).toBe('devices');
  });
});
