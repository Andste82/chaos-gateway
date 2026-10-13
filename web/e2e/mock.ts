// A mocked gateway API for the Playwright tests (plan M12: "Playwright against a mocked API for
// every state"). It answers /api/v1 from data a test sets up, records the requests, and replaces
// the browser's EventSource with one the test drives (`emit`), so live updates are deterministic.
// The data use the generated types, so a change of the spec breaks the fixtures at compile time.
import type { Page, Request, Route } from '@playwright/test';
import type {
  ApplyResult,
  AuditEntry,
  DeviceView,
  Event as GatewayEvent,
  FaultView,
  GatewayState,
  GroupView,
  Health,
  HostInterface,
  NetworkView,
  Overlay,
  PreflightReport,
  ProfileView,
  Revision,
  Session,
  SetupStatus,
  SystemInfo,
  UplinkView,
} from '../src/api/generated/model';

export const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
export const TEST = '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7';

/** The toast with this title (the visible one, not the screen reader's copy). */
export function toast(page: Page, title: string) {
  return page.locator('[data-kind]').filter({ hasText: title });
}

export function device(over: Partial<DeviceView> & { id: string; name: string }): DeviceView {
  return {
    origin: 'configured',
    network: IOT,
    observed: { online: true, macs: ['02:00:00:00:00:2a'], addresses: ['10.10.0.42'], sources: ['dhcp', 'neighbor'] },
    ...over,
  };
}

/** The sample lab of the prototype: two networks, configured and discovered devices, faults. */
export function sampleLab(now = Date.now()) {
  const devices: DeviceView[] = [
    device({
      id: 'a1b2c3d4-0000-4000-8000-000000000042',
      name: 'ESP32-42',
      config: { name: 'ESP32-42', identifiers: { macs: ['02:00:00:00:00:2a'] }, trusts_test_ca: false },
      observed: {
        online: true,
        macs: ['02:00:00:00:00:2a'],
        addresses: ['10.10.0.42'],
        sources: ['dhcp', 'neighbor', 'conntrack'],
        upload_bps: 3100,
        download_bps: 2400,
        flows_active: 3,
        last_seen: new Date(now - 2000).toISOString(),
        lease: {
          ip: '10.10.0.42',
          mac: '02:00:00:00:00:2a',
          hostname: 'esp32-42',
          network: IOT,
          expires_at: new Date(now + 58 * 60_000).toISOString(),
          state: 'active',
        },
      },
      effective: [{ family: 'impairment', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000001', summary: 'upload 200 ms ± 50 ms, download 200 ms ± 50 ms' }],
      overlays: ['f0000000-0000-4000-8000-000000000001'],
    }),
    device({
      id: 'a1b2c3d4-0000-4000-8000-000000000057',
      name: 'Sensor-07',
      observed: { online: true, macs: ['02:00:00:00:00:39'], addresses: ['10.10.0.57'], upload_bps: 800, download_bps: 8000 },
    }),
    device({
      id: 'a1b2c3d4-0000-4000-8000-000000000059',
      name: 'Sensor-09',
      observed: { online: false, macs: ['02:00:00:00:00:3b'], addresses: [] },
    }),
    device({
      id: 'd15c0000-0000-4000-8000-3af19c2207b4',
      name: 'dev-3a-f1-9c-22-07-b4',
      origin: 'discovered',
      observed: { online: true, macs: ['3a:f1:9c:22:07:b4'], addresses: ['10.10.0.133'], sources: ['neighbor'], upload_bps: 1200, download_bps: 900 },
    }),
    device({
      id: 'a1b2c3d4-0000-4000-8000-000000000015',
      name: 'Test-Phone',
      network: TEST,
      observed: { online: true, macs: ['02:00:00:00:01:0f'], addresses: ['10.20.0.15'], upload_bps: 22000, download_bps: 840000 },
    }),
  ];
  const networks: NetworkView[] = [
    {
      id: IOT,
      config: { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24', dhcp: { enabled: true } },
      status: { state: 'ok', interface: 'br-iot', dhcp: { enabled: true, leases_active: 3 } },
    },
    {
      id: TEST,
      config: { type: 'lan', name: 'Test', interfaces: [{ name: 'eth2' }], address: '10.20.0.1/24' },
      status: { state: 'ok', interface: 'br-test' },
    },
  ];
  const overlays: Overlay[] = [
    {
      id: 'f0000000-0000-4000-8000-000000000001',
      kind: 'fault',
      owner: { type: 'user', id: 'admin' },
      target: { device: devices[0]!.id },
      fault: { latency: '200ms', jitter: '50ms', loss: '5%', protocol: 'tcp', ports: [8883] },
      ttl: '5m',
      expires_at: new Date(now + 4 * 60_000 + 12_000).toISOString(),
      created_at: new Date(now - 48_000).toISOString(),
      updated_at: new Date(now - 48_000).toISOString(),
      generation: 41,
      counters: { packets: 18342, bytes: 2_400_000, epoch: 1 },
      state: 'effective',
    },
    {
      id: 'f0000000-0000-4000-8000-000000000002',
      kind: 'profile',
      owner: { type: 'token', id: 'ci' },
      target: { network: IOT },
      profile: 'lte',
      created_at: new Date(now - 600_000).toISOString(),
      updated_at: new Date(now - 600_000).toISOString(),
      generation: 40,
      counters: { packets: 2104, bytes: 120_000, epoch: 1 },
      state: 'partially_overridden',
    },
  ];
  const faults: FaultView[] = [
    {
      id: 'c0f00000-0000-4000-8000-000000000007',
      config: { name: 'sensor7-slow', source: { device: devices[1]!.id }, download: { rate: '64kbit' }, enabled: true },
      state: 'effective',
      counters: { packets: 2104, bytes: 300_000, epoch: 1 },
    },
  ];
  return { devices, networks, overlays, faults };
}

export function manyDevices(n: number): DeviceView[] {
  return Array.from({ length: n }, (_, i) => {
    const hex = i.toString(16).padStart(4, '0');
    const mac = `02:00:00:00:${hex.slice(0, 2)}:${hex.slice(2)}`;
    const discovered = i % 10 === 9;
    return device({
      id: `00000000-0000-4000-8000-00000000${hex}`,
      name: discovered ? `dev-${mac.replace(/:/g, '-')}` : `sensor-${String(i).padStart(3, '0')}`,
      origin: discovered ? 'discovered' : 'configured',
      network: i % 2 ? TEST : IOT,
      observed: {
        online: i % 3 !== 0,
        macs: [mac],
        addresses: [`10.${i % 2 ? 20 : 10}.${Math.floor(i / 250)}.${(i % 250) + 2}`],
        upload_bps: i * 10,
        download_bps: i * 20,
      },
      effective: i % 7 === 0 ? [{ family: 'impairment', layer: 'overlay', id: `f${hex}0000-0000-4000-8000-000000000000`, summary: '↓ 64 kbit/s' }] : [],
    });
  });
}

const interfaces: HostInterface[] = [
  { name: 'eth0', mac: '52:54:00:00:00:01', driver: 'virtio_net', bus: 'pci', link_up: true, addresses: ['192.168.1.57/24'], os_managed: true },
  { name: 'eth1', mac: '52:54:00:00:00:02', driver: 'virtio_net', bus: 'pci', link_up: true, addresses: ['192.168.56.10/24'], os_managed: true },
  { name: 'eth2', mac: '52:54:00:00:00:03', driver: 'igb', bus: 'pci', link_up: true, os_managed: false },
  { name: 'eth3', mac: '52:54:00:00:00:04', driver: 'igb', bus: 'pci', link_up: false, os_managed: true },
];

const preflightWarn: PreflightReport = {
  status: 'warn',
  checks: [
    { id: 'kernel.version', status: 'ok', message: 'kernel 6.8 is supported' },
    { id: 'module.sch_netem', status: 'warn', message: 'sch_netem is not loaded yet', fix: 'echo sch_netem | sudo tee /etc/modules-load.d/chaos-gateway.conf' },
  ],
};

export interface Recorded {
  method: string;
  path: string;
  headers: Record<string, string>;
  body: unknown;
}

export class MockGateway {
  setupToken = 'setup-token-1234';
  password = 'correct horse battery staple';
  setup: SetupStatus = { completed: true };
  loggedIn = true;
  unreachable = false;
  state: GatewayState = {
    generation: 42,
    active_revision: 42,
    safe_mode: false,
    last_apply: { revision: 42, generation: 42, at: new Date().toISOString(), result: 'ok', duration_ms: 87 },
    overlays_active: 2,
  };
  health: Health = { status: 'healthy', components: [{ name: 'executor', status: 'healthy' }] };
  info: SystemInfo = { hostname: 'gw-lab-01', os: 'Ubuntu 24.04', kernel: '6.8.0-142-generic', arch: 'amd64', version: 'test' };
  devices: DeviceView[] = [];
  networks: NetworkView[] = [];
  groups: GroupView[] = [];
  profiles: ProfileView[] = [{ id: 'b0000000-0000-5000-8000-0000000000e1', builtin: true, available: true, config: { name: 'lte', parts: { impairment: { latency: '50ms' } } } }];
  overlays: Overlay[] = [];
  faults: FaultView[] = [];
  audit: AuditEntry[] = [];
  uplink: UplinkView = {
    config: { interface: { name: 'eth0' } },
    status: { interface: 'eth0', address: '192.168.1.57/24', gateway: '192.168.1.1', link_up: true, rx_bytes: 1_000_000, tx_bytes: 200_000 },
  };
  /** Bytes the uplink counters grow by per reading. */
  uplinkStep = { rx: 750_000, tx: 125_000 };
  requests: Recorded[] = [];
  /** The next POST /revisions is refused with 409 revision_conflict (someone else applied one). */
  conflictOnce = false;
  private nextRevision = 43;

  static sample(): MockGateway {
    const m = new MockGateway();
    Object.assign(m, sampleLab());
    m.audit = [
      { id: '1', time: new Date(Date.now() - 60_000).toISOString(), actor: { type: 'user', id: 'admin', name: 'admin' }, via: 'ui', action: 'revision.apply', revision: 42 },
    ];
    return m;
  }

  /** A gateway at first start: the setup is open. */
  static firstStart(): MockGateway {
    const m = new MockGateway();
    m.loggedIn = false;
    m.setup = { completed: false, interfaces, preflight: preflightWarn, suggested_uplink: { name: 'eth0', mac: '52:54:00:00:00:01' } };
    m.state = { generation: 0, active_revision: 0, safe_mode: false };
    m.uplink = { config: { interface: { name: 'eth0' } }, status: {} };
    return m;
  }

  session(): Session {
    return { kind: 'session', scope: 'full', user: 'admin', csrf_token: 'csrf-1', expires_at: new Date(Date.now() + 3_600_000).toISOString() };
  }

  async install(page: Page): Promise<void> {
    await page.addInitScript(() => {
      type Listener = (e: { data: string }) => void;
      class FakeEventSource {
        url: string;
        listeners: Record<string, Listener[]> = {};
        onopen: ((e: unknown) => void) | null = null;
        onerror: ((e: unknown) => void) | null = null;
        closed = false;
        constructor(url: string) {
          this.url = url;
          w.__sse.instances.push(this);
          setTimeout(() => this.onopen?.({}), 20);
        }
        addEventListener(type: string, fn: Listener) {
          (this.listeners[type] ??= []).push(fn);
        }
        close() {
          this.closed = true;
        }
      }
      const w = window as unknown as {
        __sse: { instances: FakeEventSource[]; emit: (type: string, ev: unknown) => void; fail: () => void; reopen: () => void };
        EventSource: unknown;
      };
      w.__sse = {
        instances: [],
        emit(type, ev) {
          for (const es of this.instances) if (!es.closed) for (const fn of es.listeners[type] ?? []) fn({ data: JSON.stringify(ev) });
        },
        fail() {
          for (const es of this.instances) if (!es.closed) es.onerror?.({});
        },
        reopen() {
          for (const es of this.instances) if (!es.closed) es.onopen?.({});
        },
      };
      w.EventSource = FakeEventSource;
    });
    await page.route('**/api/v1/**', (route, request) => this.handle(route, request));
  }

  /** Sends an event over the (fake) event stream of every open page. */
  async emit(page: Page, ev: Omit<GatewayEvent, 'id' | 'time'> & Partial<Pick<GatewayEvent, 'id' | 'time'>>): Promise<void> {
    const full = { id: `boot-${Math.random().toString(36).slice(2)}`, time: new Date().toISOString(), ...ev };
    await page.evaluate(([t, e]) => (window as unknown as { __sse: { emit: (a: string, b: unknown) => void } }).__sse.emit(t, e), [
      full.type,
      full,
    ] as const);
  }

  requestsTo(method: string, path: string): Recorded[] {
    return this.requests.filter((r) => r.method === method && r.path === path);
  }

  private page<T>(items: T[], url: URL) {
    const limit = Number(url.searchParams.get('limit') ?? 100);
    const start = Number(url.searchParams.get('cursor') ?? 0);
    const slice = items.slice(start, start + limit);
    return start + limit < items.length ? { items: slice, next_cursor: String(start + limit) } : { items: slice };
  }

  private async handle(route: Route, request: Request): Promise<void> {
    if (this.unreachable) return route.abort('connectionrefused');
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, '');
    const method = request.method();
    let body: unknown;
    try {
      body = request.postDataJSON();
    } catch {
      body = request.postData();
    }
    this.requests.push({ method, path, headers: request.headers(), body });
    const json = (status: number, data: unknown) =>
      route.fulfill({ status, contentType: status >= 400 ? 'application/problem+json' : 'application/json', body: JSON.stringify(data) });
    const problem = (status: number, code: string, detail: string) => json(status, { type: 'about:blank', title: code, status, code, detail });

    if (path === '/setup' && method === 'GET') return json(200, this.setup);
    if (path === '/setup' && method === 'POST') {
      if (this.setup.completed) return problem(409, 'setup_completed', 'the setup is completed');
      if (request.headers()['x-setup-token'] !== this.setupToken) return problem(401, 'unauthorized', 'a valid setup token is required');
      this.setup = { completed: true };
      this.password = (body as { admin_password: string }).admin_password;
      const deadline = new Date(Date.now() + 60_000).toISOString();
      this.state = { ...this.state, active_revision: 1, generation: 1, pending_confirm: { revision: 1, deadline } };
      const res: ApplyResult = { revision: 1, status: 'pending_confirm', generation: 1, confirm_deadline: deadline, duration_ms: 312 };
      return json(200, res);
    }
    if (path === '/system/health') return json(200, this.health);
    if (!this.setup.completed) return problem(503, 'unavailable', 'the first-start setup is not finished');
    if (path === '/auth/login') {
      if ((body as { password?: string }).password !== this.password) return problem(401, 'unauthorized', 'wrong user name or password');
      this.loggedIn = true;
      return json(200, this.session());
    }
    if (!this.loggedIn) return problem(401, 'unauthorized', 'log in or send an API token');
    if (path === '/auth/session') return json(200, this.session());
    if (path === '/auth/logout') {
      this.loggedIn = false;
      return route.fulfill({ status: 204 });
    }
    if (method !== 'GET' && request.headers()['x-csrf-token'] !== 'csrf-1') return problem(403, 'csrf_failed', 'CSRF');

    if (path === '/state') return json(200, this.state);
    if (path === '/system/info') return json(200, this.info);
    if (path === '/uplink') {
      const st = this.uplink.status;
      if (st.rx_bytes !== undefined && st.tx_bytes !== undefined) {
        this.uplink = { ...this.uplink, status: { ...st, rx_bytes: st.rx_bytes + this.uplinkStep.rx, tx_bytes: st.tx_bytes + this.uplinkStep.tx } };
      }
      return json(200, this.uplink);
    }
    if (path === '/devices') return json(200, this.page(this.devices, url));
    const dev = /^\/devices\/([^/]+)$/.exec(path);
    if (dev) {
      const d = this.devices.find((x) => x.id === dev[1] || x.name === decodeURIComponent(dev[1]!));
      return d ? json(200, d) : problem(404, 'not_found', 'no such device');
    }
    if (path === '/networks') return json(200, this.page(this.networks, url));
    if (path === '/groups') return json(200, this.page(this.groups, url));
    if (path === '/profiles') return json(200, this.page(this.profiles, url));
    if (path === '/faults') return json(200, this.page(this.faults, url));
    if (path === '/audit') return json(200, this.page(this.audit, url));
    if (path === '/overlays' && method === 'GET') {
      const devId = url.searchParams.get('device');
      const d = this.devices.find((x) => x.id === devId);
      const items = devId
        ? this.overlays.filter((o) => o.target?.device === devId || (d && o.target?.network === d.network))
        : this.overlays;
      return json(200, this.page(items, url));
    }
    const ov = /^\/overlays\/([^/]+)$/.exec(path);
    if (ov && method === 'DELETE') {
      this.overlays = this.overlays.filter((o) => o.id !== ov[1]);
      this.devices = this.devices.map((d) => ({ ...d, effective: (d.effective ?? []).filter((f) => f.id !== ov[1]) }));
      return route.fulfill({ status: 204 });
    }
    if (path === '/revisions' && method === 'POST') {
      const base = /"(\d+)"/.exec(request.headers()['if-match'] ?? '')?.[1];
      if (this.conflictOnce || Number(base) !== this.state.active_revision) {
        this.conflictOnce = false;
        return problem(409, 'revision_conflict', `revision ${base} is not the active revision any more`);
      }
      const rev: Revision = {
        id: this.nextRevision++,
        status: 'candidate',
        base: this.state.active_revision,
        schema_version: 1,
        created_at: new Date().toISOString(),
        created_by: { type: 'user', id: 'admin' },
      };
      this.candidates.set(rev.id, body);
      return json(201, rev);
    }
    const apply = /^\/revisions\/(\d+)\/apply$/.exec(path);
    if (apply) {
      const id = Number(apply[1]);
      const patch = this.candidates.get(id) as { devices?: Record<string, { name: string }> } | undefined;
      for (const [devId, cfg] of Object.entries(patch?.devices ?? {})) {
        this.devices = this.devices.map((d) => (d.id === devId ? { ...d, name: cfg.name, origin: 'configured', config: cfg } : d));
      }
      this.state = { ...this.state, active_revision: id, generation: this.state.generation + 1 };
      const res: ApplyResult = { revision: id, status: 'active', generation: this.state.generation, duration_ms: 87 };
      return json(200, res);
    }
    const discard = /^\/revisions\/(\d+)$/.exec(path);
    if (discard && method === 'DELETE') return route.fulfill({ status: 204 });
    const confirm = /^\/revisions\/(\d+)\/confirm$/.exec(path);
    if (confirm) {
      const { pending_confirm: _done, ...rest } = this.state;
      this.state = { ...rest, last_known_good: Number(confirm[1]) };
      return json(200, { id: Number(confirm[1]), status: 'active', schema_version: 1, created_at: new Date().toISOString(), created_by: { type: 'user', id: 'admin' } });
    }
    return problem(404, 'not_found', `the mock has no ${method} ${path}`);
  }

  private candidates = new Map<number, unknown>();
}
