// A mocked gateway API for the Playwright tests (plan M12: "Playwright against a mocked API for
// every state"). It answers /api/v1 from data a test sets up, records the requests, and replaces
// the browser's EventSource with one the test drives (`emit`), so live updates are deterministic.
// The data use the generated types, so a change of the spec breaks the fixtures at compile time.
import type { Page, Request, Route } from '@playwright/test';
import { applyMergePatch } from '../src/lib/mergePatch';
import type {
  AccessRuleView,
  Actor,
  ApplyResult,
  AuditEntry,
  BlockingReference,
  Configuration,
  DhcpLease,
  DomainChange,
  Explanation,
  DeviceView,
  Event as GatewayEvent,
  FaultView,
  GatewayState,
  GroupView,
  Health,
  HostInterface,
  LinuxObjectState,
  LinuxState,
  NetworkView,
  Overlay,
  OverlayRequest,
  PreflightReport,
  Preview,
  ValidationError,
  ProfileView,
  Revision,
  RouteEntry,
  Routing,
  RoutingStatus,
  Session,
  SetupStatus,
  SystemInfo,
  UplinkView,
  WireGuardClient,
  WireGuardClientView,
  WireGuardNetwork,
  WireGuardPeerStatus,
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
      // the configured fault sensor7-slow wins for it (the gateway's summary, as GET /devices sends it)
      effective: [{ family: 'impairment', layer: 'config', id: 'c0f00000-0000-4000-8000-000000000007', name: 'sensor7-slow', summary: 'download rate 64kbit' }],
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

export const RULE_DNS = 'a1000000-0000-4000-8000-000000000001';
export const RULE_DOT = 'a1000000-0000-4000-8000-000000000002';
export const RULE_WEB = 'a1000000-0000-4000-8000-000000000003';
export const RULE_OVERLAY = 'f0000000-0000-4000-8000-0000000000a1';

/** Access rules of the sample lab: three configured rules and one overlay rule in front of them. */
export function withRules(m: MockGateway, now = Date.now()): MockGateway {
  m.config = {
    ...m.config,
    access_rules: {
      [RULE_DNS]: { name: 'no-dns', source: { network: IOT }, protocol: 'udp', ports: [53], action: 'drop' },
      [RULE_DOT]: { name: 'no-dot', source: { network: IOT }, protocol: 'tcp', ports: [853], action: 'reject', cut_existing: true },
      [RULE_WEB]: { name: 'test-web', source: { network: TEST }, destination: { uplink: true }, protocol: 'tcp', ports: [80, 443], action: 'allow' },
    },
    access_rule_order: [RULE_DOT, RULE_DNS, RULE_WEB],
  };
  m.ruleViews = {
    [RULE_DNS]: { counters: { packets: 311, bytes: 24_000, epoch: 1, last_match: new Date(now - 180_000).toISOString() } },
    [RULE_DOT]: { counters: { packets: 18342, bytes: 12_400_000, epoch: 1, last_match: new Date(now - 2_000).toISOString() } },
    [RULE_WEB]: { counters: { packets: 90211, bytes: 310_000_000, epoch: 1 } },
  };
  m.overlays = [
    ...m.overlays,
    {
      id: RULE_OVERLAY,
      kind: 'rule',
      owner: { type: 'token', id: 't1', name: 'ci-runner' },
      target: { network: IOT },
      rule: { protocol: 'tcp', ports: [8883], action: 'reset' },
      ttl: '10m',
      expires_at: new Date(now + 9 * 60_000).toISOString(),
      created_at: new Date(now - 60_000).toISOString(),
      updated_at: new Date(now - 60_000).toISOString(),
      generation: 42,
      counters: { packets: 57, bytes: 4_000, epoch: 1 },
      state: 'effective',
    },
  ];
  return m;
}

export const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';
export const ADMIN_HUB = '7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d';
export const LINK = 'c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f';
export const CLIENT_RA = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
export const BGP = 'b9000000-0000-4000-8000-000000000001';

/**
 * The networks of the sample lab as the testbed has them (plan §4.2): IoT and Test, the test hub lab-hub
 * with the client rA (online) and the network behind it, the management hub admin, the link site-b with
 * a BGP session; an access matrix entry, DHCP leases of IoT, routes, and the kernel state of the Linux view.
 */
export function withNetworks(m: MockGateway, now = Date.now()): MockGateway {
  const lan = (id: string) => m.networks.find((n) => n.id === id)!.config;
  m.config = {
    ...m.config,
    // the management network has an interface of its own, which no test network may take
    management: { interface: { name: 'eth5' } },
    networks: {
      [IOT]: lan(IOT),
      [TEST]: lan(TEST),
      [HUB]: {
        type: 'wireguard',
        kind: 'hub',
        name: 'lab-hub',
        role: 'test',
        address: '10.99.0.1/24',
        listen_port: 51820,
        clients: { [CLIENT_RA]: { name: 'rA', address: '10.99.0.2', client_networks: ['10.50.0.0/24'], keepalive: '1s', key: { mode: 'generated', preshared_key: true } } },
      },
      [ADMIN_HUB]: { type: 'wireguard', kind: 'hub', name: 'admin', role: 'management', address: '10.98.0.1/24', listen_port: 51822 },
      [LINK]: {
        type: 'wireguard',
        kind: 'link',
        name: 'site-b',
        address: '10.255.0.0/31',
        listen_port: 51821,
        routes: ['10.60.0.0/24'],
        peer: { address: '10.255.0.1', endpoint: '203.0.113.40:51821', key: { mode: 'generated' } },
      },
    },
    access_matrix: { entries: [{ from: { network: 'IoT' }, to: { network: 'lab-hub' }, policy: 'allow' }] },
    routing: { asn: 65001, protocols: { [BGP]: { name: 'site-b-bgp', type: 'bgp', link: LINK, bgp: { neighbor_asn: 65002 }, announce: [{ network: IOT }] } } },
  };
  m.networks = [
    {
      ...m.networks.find((n) => n.id === IOT)!,
      status: {
        state: 'ok',
        interface: 'br-iot',
        ports: [{ name: 'eth1', present: true, link_up: true }],
        dhcp: { enabled: true, leases_active: 2, pool_free: 125 },
      },
    },
    m.networks.find((n) => n.id === TEST)!,
    { id: HUB, config: m.config.networks![HUB]!, status: { state: 'ok', interface: 'wg-lab-hub', public_key: 'qU1BFDFDgsH5I7uGhzuhQjkMSw6bP8nXN6CN4AKxEF8=' } },
    { id: ADMIN_HUB, config: m.config.networks![ADMIN_HUB]!, status: { state: 'ok', interface: 'wg-admin' } },
    {
      id: LINK,
      config: m.config.networks![LINK]!,
      status: {
        state: 'ok',
        interface: 'wg-site-b',
        link_peer: { online: true, last_handshake: new Date(now - 20_000).toISOString(), endpoint: '203.0.113.40:51821', rx_bytes: 52_000, tx_bytes: 48_000, learned_routes: ['10.70.0.0/24'] },
      },
    },
  ];
  m.clientStatus = {
    [CLIENT_RA]: { online: true, last_handshake: new Date(now - 12_000).toISOString(), endpoint: '198.51.100.7:40312', rx_bytes: 1_800_000, tx_bytes: 240_000 },
  };
  m.leases = {
    [IOT]: [
      { ip: '10.10.0.42', mac: '02:00:00:00:00:2a', hostname: 'esp32-42', network: IOT, device: 'a1b2c3d4-0000-4000-8000-000000000042', expires_at: new Date(now + 58 * 60_000).toISOString(), state: 'active' },
      { ip: '10.10.0.133', mac: '3a:f1:9c:22:07:b4', network: IOT, expires_at: new Date(now + 5 * 60_000).toISOString(), state: 'active' },
    ],
  };
  m.routingStatus = {
    bird: { running: true, version: '2.15.1', router_id: '10.10.0.1', last_reconfigure: new Date(now - 300_000).toISOString() },
    protocols: [
      {
        id: BGP,
        name: 'site-b-bgp',
        type: 'bgp',
        state: 'up',
        info: 'Established',
        since: new Date(now - 240_000).toISOString(),
        routes: { imported: 1, filtered: 1, exported: 1 },
        announced: ['10.10.0.0/24'],
        received: ['10.70.0.0/24', '0.0.0.0/0'],
      },
    ],
  };
  m.routing = m.config.routing!;
  m.routes = [
    { destination: '10.10.0.0/24', table: 100, origin: 'connected', network: IOT },
    { destination: '10.50.0.0/24', table: 100, origin: 'client', network: HUB },
    { destination: '10.60.0.0/24', table: 100, origin: 'static', network: LINK },
    { destination: '10.70.0.0/24', gateway: '10.255.0.1', table: 100, origin: 'learned', network: LINK, protocol: { id: BGP, name: 'site-b-bgp' } },
  ];
  m.linux = {
    ...m.linux,
    wireguard: 'wireguard wg-lab-hub up public key qU1BFDFDgsH5I7uGhzuhQjkMSw6bP8nXN6CN4AKxEF8=\n  address 10.99.0.1/24 mtu 1420 port 51820\n  peer FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo= allowed 10.50.0.0/24,10.99.0.2/32 preshared handshake 2026-10-13T12:00:00Z rx 1800000 tx 240000\n',
  };
  return m;
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
    // the OS-configuration check of every interface but the uplink and the management interface (P3-M12-07)
    {
      id: 'interface.eth0.os_config',
      interface: 'eth0',
      status: 'warn',
      message: 'eth0 is configured by netplan (/etc/netplan/50-cloud-init.yaml, "eth0" under "ethernets"); a test interface must not be',
      fix: 'delete the entry "eth0:" under "ethernets" in /etc/netplan/50-cloud-init.yaml, then run "sudo netplan apply"',
    },
    { id: 'interface.eth2.os_config', interface: 'eth2', status: 'ok', message: 'eth2 carries no network configuration of the operating system' },
    {
      id: 'interface.eth3.os_config',
      interface: 'eth3',
      status: 'warn',
      message: 'eth3 is configured by netplan (/etc/netplan/60-lab.yaml, "lab" under "ethernets") and by NetworkManager (connection "Wired connection 2", /etc/NetworkManager/system-connections/Wired connection 2.nmconnection); a test interface must not be',
      fix: 'delete the entry "lab:" under "ethernets" in /etc/netplan/60-lab.yaml, then run "sudo netplan apply"; delete the NetworkManager connection "Wired connection 2" (sudo nmcli connection delete "Wired connection 2")',
    },
  ],
};

export interface Recorded {
  method: string;
  path: string;
  /** The query parameters. */
  query: Record<string, string>;
  headers: Record<string, string>;
  body: unknown;
}

export class MockGateway {
  setupToken = 'setup-token-1234';
  password = 'correct horse battery staple';
  setup: SetupStatus = { completed: true };
  loggedIn = true;
  /** Seconds for which POST /auth/login answers 429 rate_limited with Retry-After (the lockout after failed logins). */
  loginLockedFor = 0;
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
  /**
   * How far the gateway's clock is ahead of the browser's (negative: behind). The answers carry the
   * gateway's time in their Date header, and its deadlines are on its clock.
   */
  clockSkewMs = 0;
  /** GET paths (without /api/v1) the gateway answers with this error status instead of the data. */
  failing: Record<string, number> = {};
  /** GET paths (without /api/v1) the gateway answers only after the promise settles (a slow read: the loading states). */
  holding: Record<string, Promise<void>> = {};
  /** The next POST /revisions is refused with 409 revision_conflict (someone else applied one). */
  conflictOnce = false;
  private nextRevision = 43;
  /** The configuration of the active revision (rules, faults, groups, …). */
  config: Configuration = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } } };
  /** Who made the active revision. */
  activeBy: Actor = { type: 'user', id: 'admin', name: 'admin' };
  /** The revisions applied since the mock started (GET /revisions lists them, newest first). */
  applied: Revision[] = [];
  /** The domain diff of a revision against an earlier one (GET /revisions/{id}/diff), by revision. */
  diffs = new Map<number, DomainChange[]>();
  /** Counters, state and what overrides a configured rule or fault, by id. */
  ruleViews: Record<string, Partial<AccessRuleView>> = {};
  faultViews: Record<string, Partial<FaultView>> = {};
  systemRuleCounters = { packets: 4812, bytes: 38_000_000, epoch: 1, last_match: new Date().toISOString() };
  /** The next POST /revisions is refused with 422 and these errors (or those made from its merge patch). */
  rejectRevision?: ValidationError[] | ((patch: unknown) => ValidationError[]);
  /** POST /revisions answers only after this settles (a candidate that takes its time). */
  revisionGate?: Promise<void>;
  /** POST /revisions/{id}/apply answers only after this settles (an apply that takes its time). */
  applyGate?: Promise<void>;
  /** The next apply fails at this step (`apply` or `verify`). */
  failApply?: 'apply' | 'verify';
  /** The next POST /overlays is refused with 422 and these errors. */
  rejectOverlay?: ValidationError[];
  /** The answer of GET /explain. */
  explanation?: Explanation;
  /** DHCP leases by network id (GET /networks/{id}/leases). */
  leases: Record<string, DhcpLease[]> = {};
  /** The status of WireGuard clients by client id; a client without one is offline, never seen. */
  clientStatus: Record<string, WireGuardPeerStatus> = {};
  /** The next client export answers only after this settles (a slow export). */
  exportGate?: Promise<void>;
  /** Clients whose private key the gateway does not hold (any more): their export has a placeholder. */
  keyGone = new Set<string>();
  /** GET /system/interfaces: the host's interfaces with their assignment. */
  hostInterfaces: HostInterface[] = [
    { name: 'eth0', mac: '52:54:00:00:00:01', driver: 'virtio_net', bus: 'pci', link_up: true, addresses: ['192.168.1.57/24'], os_managed: true, assigned: { role: 'uplink' } },
    { name: 'eth1', mac: '52:54:00:00:00:02', driver: 'igb', bus: 'pci', link_up: true, os_managed: false, assigned: { role: 'network', network: IOT } },
    { name: 'eth2', mac: '52:54:00:00:00:03', driver: 'igb', bus: 'pci', link_up: true, os_managed: false, assigned: { role: 'network', network: TEST } },
    { name: 'eth3', mac: '52:54:00:00:00:04', driver: 'igb', bus: 'pci', link_up: true, os_managed: false },
    { name: 'eth4', mac: '52:54:00:00:00:05', driver: 'r8152', bus: 'usb', link_up: false, os_managed: false },
    { name: 'eth5', mac: '52:54:00:00:00:06', driver: 'igb', bus: 'pci', link_up: true, addresses: ['192.168.56.10/24'], os_managed: true, assigned: { role: 'management' } },
  ];
  /** GET /routing, /routing/status and /routing/routes. */
  routing: Routing = {};
  routingStatus: RoutingStatus = { bird: { running: false }, protocols: [] };
  routes: RouteEntry[] = [];
  /** GET /state/linux. */
  linux: LinuxState = {
    revision: 42,
    generation: 42,
    read_at: new Date().toISOString(),
    nftables: 'table inet chaosgw {\n  chain forward {\n    type filter hook forward priority 0; policy drop;\n    ct state { established, related } accept\n    iifname "br-iot" oifname "eth0" counter packets 12 bytes 840 accept\n    iifname "br-test" oifname "eth0" counter packets 3 bytes 180 accept\n  }\n}\n',
    tc: 'br-iot: qdisc 1: parent root htb default=0x1\nbr-iot:   sent 840 bytes 12 pkt (dropped 0, overlimits 0, requeues 0) backlog 0b 0p\n',
    routes: 'bridge br-iot up\n  address 10.10.0.1/24\n  port eth1\nbridge br-test up\n  address 10.20.0.1/24\n  port eth2\nroute table 100 10.10.0.0/24 dev br-iot\nroute table 100 10.20.0.0/24 dev br-test\n',
    wireguard: '',
  };
  /** GET /state/linux/{kind}/{id} by `kind/id`; an object without an entry holds nothing in the kernel. */
  linuxObjects: Record<string, Pick<LinuxObjectState, 'keys' | 'nftables' | 'tc'>> = {};
  /** How long a lockout-relevant apply waits for confirmation. */
  confirmWindowMs = 60_000;
  /**
   * The revision that waits for confirmation with its configuration: like the gateway's store, the active revision
   * (and with it every resource view) stays the one before until it is confirmed.
   */
  private pending?: { config: Configuration; revision: number };
  /** The Linux part of every preview. */
  previewLinux: Preview['linux'] = { nftables: '+ rule inet chaosgw access_rules ip saddr @net_iot udp dport 53 counter drop' };

  static sample(): MockGateway {
    const m = new MockGateway();
    Object.assign(m, sampleLab());
    // the configured faults are the active revision's; /faults adds their state and counters
    m.config = { ...m.config, faults: Object.fromEntries(m.faults.map((f) => [f.id, f.config])) };
    m.faultViews = Object.fromEntries(m.faults.map((f) => [f.id, { state: f.state, counters: f.counters }]));
    m.faults = [];
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
    this.moveCustomProfiles();
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
    this.requests.push({ method, path, query: Object.fromEntries(url.searchParams), headers: request.headers(), body });
    const headers: Record<string, string> = this.clockSkewMs ? { Date: new Date(this.now()).toUTCString() } : {};
    const json = (status: number, data: unknown) =>
      route.fulfill({ status, headers, contentType: status >= 400 ? 'application/problem+json' : 'application/json', body: JSON.stringify(data) });
    const problem = (status: number, code: string, detail: string) => json(status, { type: 'about:blank', title: code, status, code, detail });
    const failure = method === 'GET' ? this.failing[path] : undefined;
    if (failure) return problem(failure, 'internal', `the gateway failed to read ${path}`);
    const hold = method === 'GET' ? this.holding[path] : undefined;
    if (hold) await hold;

    if (path === '/setup' && method === 'GET') return json(200, this.setup);
    if (path === '/setup' && method === 'POST') {
      if (this.setup.completed) return problem(409, 'setup_completed', 'the setup is completed');
      if (request.headers()['x-setup-token'] !== this.setupToken) return problem(401, 'unauthorized', 'a valid setup token is required');
      this.setup = { completed: true };
      this.password = (body as { admin_password: string }).admin_password;
      const deadline = new Date(this.now() + 60_000).toISOString();
      this.state = { ...this.state, active_revision: 1, generation: 1, pending_confirm: { revision: 1, deadline } };
      const res: ApplyResult = { revision: 1, status: 'pending_confirm', generation: 1, confirm_deadline: deadline, duration_ms: 312 };
      return json(200, res);
    }
    if (path === '/system/health') return json(200, this.health);
    if (!this.setup.completed) return problem(503, 'unavailable', 'the first-start setup is not finished');
    if (path === '/auth/login') {
      if (this.loginLockedFor > 0) {
        const s = this.loginLockedFor;
        const p = { type: 'about:blank', title: 'rate_limited', status: 429, code: 'rate_limited', detail: `too many failed logins; try again in ${s} seconds` };
        return route.fulfill({ status: 429, headers: { ...headers, 'Retry-After': String(s) }, contentType: 'application/problem+json', body: JSON.stringify(p) });
      }
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
    if (path === '/networks') return json(200, this.page(this.networkViews(), url));
    const net = /^\/networks\/([^/]+)$/.exec(path);
    if (net) {
      const n = this.networkViews().find((x) => x.id === net[1]);
      return n ? json(200, n) : problem(404, 'not_found', 'no such network');
    }
    const leases = /^\/networks\/([^/]+)\/leases$/.exec(path);
    if (leases) return json(200, this.page(this.leases[leases[1]!] ?? [], url));
    const clients = /^\/networks\/([^/]+)\/clients$/.exec(path);
    if (clients) return json(200, this.page(this.clientViews(clients[1]!), url));
    const exp = /^\/networks\/([^/]+)\/clients\/([^/]+)\/export$/.exec(path);
    if (exp) {
      const c = this.clientViews(exp[1]!).find((x) => x.id === exp[2] || x.config.name === exp[2]);
      if (!c) return problem(404, 'not_found', 'no such client');
      const conf = this.confOf(exp[1]!, c);
      if (c.config.key?.export_once) this.keyGone.add(c.id);
      const gate = this.exportGate;
      this.exportGate = undefined;
      if (gate) await gate;
      return route.fulfill({ status: 200, headers: { ...headers, 'Content-Disposition': `attachment; filename="${c.config.name}.conf"` }, contentType: 'text/plain; charset=utf-8', body: conf });
    }
    const netExp = /^\/networks\/([^/]+)\/export$/.exec(path);
    if (netExp) {
      // a link: the remote side's wg-quick configuration, or the BIRD snippet of the protocol on it
      const n = this.config.networks?.[netExp[1]!] as WireGuardNetwork | undefined;
      if (n?.type !== 'wireguard' || n.kind !== 'link') return problem(422, 'validation_failed', 'only a link exports conf or bird here');
      const format = url.searchParams.get('format') ?? 'conf';
      const key = (seed: string) => btoa(seed.padEnd(32, '#').slice(0, 32));
      let body: string;
      if (format === 'bird') {
        const p = Object.values(this.config.routing?.protocols ?? {}).find((x) => x.link === netExp[1] || x.link === n.name);
        if (!p) return problem(422, 'validation_failed', 'no routing protocol runs on the link');
        body = `protocol bgp ${p.name} {\n  local as 65002;\n  neighbor ${n.address.split('/')[0]} as ${this.config.routing?.asn ?? 65001};\n  connect retry time 5;\n  error wait time 1, 30;\n}\n`;
      } else {
        body = ['[Interface]', `PrivateKey = ${key(`remote ${netExp[1]}`)}`, `Address = ${n.peer?.address ?? ''}/31`, '', '[Peer]', `PublicKey = ${key(`link ${netExp[1]}`)}`, `AllowedIPs = ${n.address}`, `Endpoint = 192.168.1.57:${n.listen_port}`, ''].join('\n');
      }
      return route.fulfill({ status: 200, headers: { ...headers, 'Content-Disposition': `attachment; filename="${n.name}.${format === 'bird' ? 'bird' : 'conf'}"` }, contentType: 'text/plain; charset=utf-8', body });
    }
    if (path === '/routing') return json(200, this.config.routing ?? this.routing);
    if (path === '/system/interfaces') return json(200, this.page(this.hostInterfaces, url));
    if (path === '/routing/status') return json(200, this.routingStatus);
    if (path === '/routing/routes') return json(200, this.page(this.routes, url));
    if (path === '/state/linux') return json(200, this.linux);
    const lobj = /^\/state\/linux\/(device|rule|fault)\/([^/]+)$/.exec(path);
    if (lobj) {
      const kind = lobj[1] as LinuxObjectState['kind'];
      const id = decodeURIComponent(lobj[2]!);
      const o = this.linuxObjects[`${kind}/${id}`] ?? { keys: [], nftables: '', tc: '' };
      return json(200, { kind, id, revision: 42, generation: 42, read_at: new Date().toISOString(), ...o } satisfies LinuxObjectState);
    }
    if (path === '/groups') return json(200, this.page(this.groups, url));
    if (path === '/profiles') return json(200, this.page(this.profileViews(), url));
    if (path === '/faults') return json(200, this.page(this.faultList(), url));
    if (path === '/rules') return json(200, { system_rules: [this.systemRule()], ...this.page(this.ruleList(), url) });
    if (path === '/explain') return this.explanation ? json(200, this.explanation) : problem(404, 'not_found', 'no such device');
    if (path === '/revisions/active') return json(200, this.activeRevision());
    if (path === '/revisions' && method === 'GET') return json(200, this.page([...this.applied].reverse(), url));
    const diff = /^\/revisions\/(\d+)\/diff$/.exec(path);
    if (diff) return json(200, this.diffs.get(Number(diff[1])) ?? []);
    const preview = /^\/revisions\/(\d+)\/preview$/.exec(path);
    if (preview) {
      const c = this.candidates.get(Number(preview[1]));
      if (!c) return problem(404, 'not_found', 'no such revision');
      return json(200, this.previewOf(Number(preview[1]), c));
    }
    if (path === '/overlays' && method === 'POST') {
      if (this.rejectOverlay) {
        const errors = this.rejectOverlay;
        this.rejectOverlay = undefined;
        return json(422, { type: 'about:blank', title: 'validation_failed', status: 422, code: 'validation_failed', detail: 'the overlay is not valid', errors });
      }
      const { overlay, created } = this.putOverlay(body as OverlayRequest);
      return json(created ? 201 : 200, overlay);
    }
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
      await this.revisionGate;
      const base = /"(\d+)"/.exec(request.headers()['if-match'] ?? '')?.[1];
      if (this.conflictOnce || Number(base) !== this.state.active_revision) {
        this.conflictOnce = false;
        return problem(409, 'revision_conflict', `revision ${base} is not the active revision any more`);
      }
      // like the gateway: a deleted network something still refers to makes the candidate invalid
      const dangling = this.danglingReferences(body);
      if (this.rejectRevision || dangling.length) {
        const errors = this.rejectRevision ? (typeof this.rejectRevision === 'function' ? this.rejectRevision(body) : this.rejectRevision) : dangling;
        this.rejectRevision = undefined;
        return json(422, { type: 'about:blank', title: 'validation_failed', status: 422, code: 'validation_failed', detail: `the configuration is not valid (${errors.length} problems)`, errors });
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
      this.candidateBases.set(rev.id, this.state.active_revision);
      return json(201, rev);
    }
    const apply = /^\/revisions\/(\d+)\/apply$/.exec(path);
    if (apply) {
      const id = Number(apply[1]);
      await this.applyGate;
      if (this.failApply) {
        const step = this.failApply;
        this.failApply = undefined;
        return problem(500, step === 'verify' ? 'verify_failed' : 'apply_failed', `apply of revision ${id} failed: ${step === 'verify' ? 'verify: the nftables state differs from the target' : 'tc: RTNETLINK answers: Invalid argument'}`);
      }
      if (this.revisionBase(id) !== this.state.active_revision) return problem(409, 'revision_conflict', `the candidate's base is not the active revision`);
      if (this.state.pending_confirm) return problem(409, 'confirm_pending', `revision ${this.state.pending_confirm.revision} waits for confirmation`);
      const patch = this.candidates.get(id) as { devices?: Record<string, { name: string }> } | undefined;
      // a deletion that orphans active overlays needs force=true, which removes them
      const references = this.referencesOf(patch);
      if (references.length && url.searchParams.get('force') !== 'true') {
        return json(422, { type: 'about:blank', title: 'validation_failed', status: 422, code: 'validation_failed', detail: 'the revision orphans active overlays', references });
      }
      this.overlays = this.overlays.filter((o) => !references.some((r) => r.id === o.id));
      if (this.requiresConfirm(patch)) {
        this.pending = { config: applyMergePatch(this.config, patch), revision: id };
        const deadline = new Date(this.now() + this.confirmWindowMs).toISOString();
        this.state = { ...this.state, generation: this.state.generation + 1, pending_confirm: { revision: id, deadline } };
        return json(200, { revision: id, status: 'pending_confirm', generation: this.state.generation, confirm_deadline: deadline, duration_ms: 87 } satisfies ApplyResult);
      }
      this.config = applyMergePatch(this.config, patch);
      this.activeBy = { type: 'user', id: 'admin', name: 'admin' };
      this.noteApplied(id, this.activeBy);
      for (const [devId, cfg] of Object.entries(patch?.devices ?? {})) {
        this.devices = this.devices.map((d) => (d.id === devId ? { ...d, name: cfg.name, origin: 'configured', config: cfg } : d));
      }
      this.state = { ...this.state, active_revision: id, generation: this.state.generation + 1 };
      const res: ApplyResult = { revision: id, status: 'active', generation: this.state.generation, duration_ms: 87 };
      return json(200, res);
    }
    const discard = /^\/revisions\/(\d+)$/.exec(path);
    if (discard && method === 'DELETE') return route.fulfill({ status: 204 });
    const rollback = /^\/revisions\/(\d+)\/rollback$/.exec(path);
    if (rollback) {
      if (this.state.pending_confirm?.revision !== Number(rollback[1])) return problem(409, 'not_a_candidate', `revision ${rollback[1]} does not wait for confirmation`);
      this.rollBackPending();
      return json(200, { id: Number(rollback[1]), status: 'rolled_back', schema_version: 1, created_at: new Date().toISOString(), created_by: { type: 'user', id: 'admin' } });
    }
    const confirm = /^\/revisions\/(\d+)\/confirm$/.exec(path);
    if (confirm) {
      if (this.state.pending_confirm?.revision !== Number(confirm[1])) return problem(409, 'not_a_candidate', `revision ${confirm[1]} does not wait for confirmation`);
      const { pending_confirm: _done, ...rest } = this.state;
      this.state = { ...rest, last_known_good: Number(confirm[1]) };
      if (this.pending) {
        this.config = this.pending.config;
        this.activeBy = { type: 'user', id: 'admin', name: 'admin' };
        this.noteApplied(this.pending.revision, this.activeBy);
        this.state = { ...this.state, active_revision: this.pending.revision, generation: this.state.generation + 1 };
        this.pending = undefined;
      }
      return json(200, { id: Number(confirm[1]), status: 'active', schema_version: 1, created_at: new Date().toISOString(), created_by: { type: 'user', id: 'admin' } });
    }
    return problem(404, 'not_found', `the mock has no ${method} ${path}`);
  }

  private candidates = new Map<number, unknown>();
  private candidateBases = new Map<number, number>();

  private revisionBase(id: number): number | undefined {
    return this.candidateBases.get(id);
  }

  /** Someone else applies a revision through the API (a test with a token): it is active at once. */
  applyFromApi(patch: Record<string, unknown>, by: Actor, changes: DomainChange[]): number {
    const id = this.nextRevision++;
    this.diffs.set(id, changes);
    this.config = applyMergePatch(this.config, patch);
    this.activeBy = by;
    this.noteApplied(id, by);
    this.state = { ...this.state, active_revision: id, generation: this.state.generation + 1 };
    return id;
  }

  private noteApplied(id: number, by: Actor) {
    const now = new Date().toISOString();
    this.applied = this.applied.map((r) => (r.status === 'active' ? { ...r, status: 'superseded' } : r));
    this.applied.push({ id, status: 'active', base: this.state.active_revision, schema_version: 1, created_at: now, created_by: by, applied_at: now });
  }

  activeRevision() {
    return {
      id: this.state.active_revision,
      status: 'active' as const,
      schema_version: 1,
      created_at: new Date().toISOString(),
      created_by: this.activeBy,
      configuration: this.config,
    };
  }

  ruleList(): AccessRuleView[] {
    const rules = this.config.access_rules ?? {};
    return (this.config.access_rule_order ?? Object.keys(rules)).map((id, position) => ({
      id,
      position,
      config: rules[id]!,
      state: rules[id]!.enabled === false ? ('disabled' as const) : ('effective' as const),
      ...(rules[id]!.enabled === false ? {} : { counters: { packets: 0, bytes: 0, epoch: 1 } }),
      ...this.ruleViews[id],
    }));
  }

  systemRule() {
    return {
      key: 'system:anti_lockout',
      name: 'Control plane access',
      description: 'The management sources always reach SSH and the UI/API.',
      counters: this.systemRuleCounters,
    };
  }

  faultList(): FaultView[] {
    const configured = Object.entries(this.config.faults ?? {}).map(([id, f]) => ({
      id,
      config: f,
      state: f.enabled === false ? ('disabled' as const) : ('effective' as const),
      ...this.faultViews[id],
    }));
    return [...this.faults, ...configured];
  }

  /**
   * The built-in profiles of `profiles` and the custom profiles of the active configuration. A custom
   * profile a test put into `profiles` is moved into the configuration the first time (it is an object
   * of the configuration, edited and deleted through revisions).
   */
  profileViews(): ProfileView[] {
    this.moveCustomProfiles();
    const all: ProfileView[] = [
      ...this.profiles,
      ...Object.entries(this.config.profiles ?? {}).map(([id, config]) => ({ id, builtin: false, ...(this.profileExtras[id] ?? { available: true }), config })),
    ];
    return all.map((p) => ({
      ...p,
      activations: this.overlays
        .filter((o) => o.kind === 'profile' && (o.profile === p.id || o.profile === p.config.name))
        .map((o) => ({ overlay: o.id, target: o.target!, owner: o.owner, expires_at: o.expires_at, state: o.state })),
    }));
  }

  /** What the gateway says about a custom profile besides its configuration (`available`), by id. */
  private profileExtras: Record<string, Pick<ProfileView, 'available' | 'unavailable_reason'>> = {};

  private moveCustomProfiles() {
    const custom = this.profiles.filter((p) => !p.builtin);
    if (!custom.length) return;
    this.config = { ...this.config, profiles: { ...Object.fromEntries(custom.map((p) => [p.id, p.config])), ...this.config.profiles } };
    for (const p of custom) this.profileExtras[p.id] = { available: p.available, ...(p.unavailable_reason ? { unavailable_reason: p.unavailable_reason } : {}) };
    this.profiles = this.profiles.filter((p) => p.builtin);
  }

  previewOf(id: number, patch: unknown): Preview {
    const p = patch as { access_rules?: Record<string, Record<string, unknown> | null>; faults?: Record<string, Record<string, unknown> | null>; access_rule_order?: string[] };
    const domain: DomainChange[] = [];
    for (const [key, v] of Object.entries(p.access_rules ?? {})) {
      const before = this.config.access_rules?.[key] as Record<string, unknown> | undefined;
      const name = String(before?.['name'] ?? v?.['name'] ?? key);
      const op = v === null ? 'removed' : before ? 'changed' : 'added';
      const fields = v && before ? Object.entries(v).map(([f, nv]) => ({ path: f, old: before[f], new: nv })) : undefined;
      domain.push({ op, kind: 'access_rule', id: key, name, path: `/access_rules/${key}`, summary: `access rule ${name} ${op}`, ...(fields ? { fields } : {}) });
    }
    if (p.access_rule_order) domain.push({ op: 'changed', kind: 'access_rule', path: '/access_rule_order', summary: 'order of the access rules changed' });
    for (const [key, v] of Object.entries(p.faults ?? {})) {
      const before = this.config.faults?.[key];
      const name = String(before?.name ?? v?.['name'] ?? key);
      const op = v === null ? 'removed' : before ? 'changed' : 'added';
      domain.push({ op, kind: 'fault', id: key, name, path: `/faults/${key}`, summary: `fault ${name} ${op}` });
    }
    for (const [key, v] of Object.entries((patch as { profiles?: Record<string, { name?: string } | null> }).profiles ?? {})) {
      const before = this.config.profiles?.[key];
      const name = String(v?.name ?? before?.name ?? key);
      const op = v === null ? 'removed' : before ? 'changed' : 'added';
      domain.push({ op, kind: 'profile', id: key, name, path: `/profiles/${key}`, summary: `profile ${name} ${op}` });
    }
    for (const [key, v] of Object.entries((patch as { devices?: Record<string, { name?: string } | null> }).devices ?? {})) {
      const name = String(v?.name ?? key);
      domain.push({ op: v === null ? 'removed' : 'added', kind: 'device', id: key, name, path: `/devices/${key}`, summary: `device ${name} ${v === null ? 'removed' : 'added'}` });
    }
    const nets = (patch as { networks?: Record<string, { clients?: Record<string, { name?: string; address?: string } | null> } | null> }).networks ?? {};
    for (const [netId, n] of Object.entries(nets)) {
      for (const [key, c] of Object.entries(n?.clients ?? {})) {
        const before = (this.config.networks?.[netId] as WireGuardNetwork | undefined)?.clients?.[key];
        const name = String(c?.name ?? before?.name ?? key);
        const op = c === null ? 'removed' : before ? 'changed' : 'added';
        domain.push({ op, kind: 'client', id: key, name, path: `/networks/${netId}/clients/${key}`, summary: `WireGuard client ${name} ${op}${c?.address ? ` (${c.address})` : ''}` });
      }
    }
    for (const [netId, n] of Object.entries(nets)) {
      const before = this.config.networks?.[netId];
      const own = n === null ? null : Object.keys(n).filter((k) => k !== 'clients');
      if (own !== null && own.length === 0) continue;
      const name = String((n as { name?: string } | null)?.name ?? before?.name ?? netId);
      const op = n === null ? 'removed' : before ? 'changed' : 'added';
      const fields = n && before ? own!.map((f) => ({ path: f, old: (before as unknown as Record<string, unknown>)[f], new: (n as Record<string, unknown>)[f] })) : undefined;
      domain.push({ op, kind: 'network', id: netId, name, path: `/networks/${netId}`, summary: `network ${name} ${op}`, ...(fields ? { fields } : {}) });
    }
    const routing = (patch as { routing?: { protocols?: Record<string, { name?: string } | null> } & Record<string, unknown> }).routing;
    if (routing) {
      for (const [key, v] of Object.entries(routing.protocols ?? {})) {
        const before = this.config.routing?.protocols?.[key];
        const name = String(v?.name ?? before?.name ?? key);
        const op = v === null ? 'removed' : before ? 'changed' : 'added';
        domain.push({ op, kind: 'routing_protocol', id: key, name, path: `/routing/protocols/${key}`, summary: `routing protocol ${name} ${op}` });
      }
      if (Object.keys(routing).some((k) => k !== 'protocols')) domain.push({ op: 'changed', kind: 'routing', path: '/routing', summary: 'routing settings changed' });
    }
    if ((patch as { access_matrix?: unknown }).access_matrix) {
      domain.push({ op: 'changed', kind: 'access_matrix', path: '/access_matrix', summary: 'access between networks changed' });
    }
    const references = this.referencesOf(patch);
    return { revision: id, base: this.revisionBase(id), domain, linux: this.previewLinux, requires_confirm: this.requiresConfirm(patch), ...(references.length ? { references } : {}) };
  }

  /**
   * The references of the configuration to a network the candidate deletes, as the gateway reports them
   * (`unknown_reference`): access rules, faults and devices, the access matrix and the routing protocols.
   */
  danglingReferences(patch: unknown): ValidationError[] {
    const nets = (patch as { networks?: Record<string, unknown> } | undefined)?.networks ?? {};
    const deleted = Object.keys(nets).filter((id) => nets[id] === null);
    if (!deleted.length) return [];
    const cfg = applyMergePatch(this.config, patch);
    const out: ValidationError[] = [];
    const ref = (path: string, id: string | undefined) => {
      const hit = deleted.find((d) => d === id || this.config.networks?.[d]?.name === id);
      if (hit) out.push({ path, code: 'unknown_reference', message: `there is no network "${id}"` });
    };
    for (const [id, r] of Object.entries(cfg.access_rules ?? {})) {
      ref(`/access_rules/${id}/source/network`, r.source.network);
      ref(`/access_rules/${id}/destination/network`, r.destination?.network);
    }
    for (const [id, f] of Object.entries(cfg.faults ?? {})) ref(`/faults/${id}/source/network`, f.source?.network);
    for (const [id, d] of Object.entries(cfg.devices ?? {})) ref(`/devices/${id}/network`, d.network);
    (cfg.access_matrix?.entries ?? []).forEach((e, i) => {
      ref(`/access_matrix/entries/${i}/from/network`, e.from.network);
      ref(`/access_matrix/entries/${i}/to/network`, e.to.network);
    });
    for (const [id, p] of Object.entries(cfg.routing?.protocols ?? {})) {
      ref(`/routing/protocols/${id}/link`, p.link);
      (p.announce ?? []).forEach((a, i) => ref(`/routing/protocols/${id}/announce/${i}/network`, a.network));
    }
    return out;
  }

  /** The active overlays a candidate would orphan: the activations of a custom profile it deletes. */
  referencesOf(patch: unknown): BlockingReference[] {
    const deleted = Object.entries((patch as { profiles?: Record<string, unknown> } | undefined)?.profiles ?? {})
      .filter(([, v]) => v === null)
      .map(([key]) => key);
    return this.overlays
      .filter((o) => o.kind === 'profile' && deleted.some((key) => o.profile === key || o.profile === this.config.profiles?.[key]?.name))
      .map((o) => {
        const key = deleted.find((k) => o.profile === k || o.profile === this.config.profiles?.[k]?.name)!;
        return { kind: 'overlay' as const, id: o.id, owner: o.owner, object: `/profiles/${key}` };
      });
  }

  /**
   * The network views, their configuration as the active revision has it (a client added by an apply is
   * there). Once the configuration has networks, a network it does not have is gone and one it adds is
   * listed.
   */
  networkViews(): NetworkView[] {
    const cfgs = this.config.networks;
    const added: NetworkView[] = Object.entries(cfgs ?? {})
      .filter(([id]) => !this.networks.some((n) => n.id === id))
      .map(([id, config]) => ({ id, config, status: { state: 'ok', interface: `${config.type === 'lan' ? 'br' : 'wg'}-${config.name}`.slice(0, 15) } }));
    return [...this.networks.filter((n) => !cfgs || n.id in cfgs), ...added].map((n) => {
      const cfg = cfgs?.[n.id];
      if (!cfg) return n;
      const clients = cfg.type === 'wireguard' ? Object.keys((cfg as WireGuardNetwork).clients ?? {}) : [];
      // a local network's ports are those of its configuration, present and up unless the status says otherwise
      const ports = cfg.type === 'lan' ? cfg.interfaces.map((i) => n.status.ports?.find((p) => p.name === i.name) ?? { name: i.name ?? i.mac, present: true, link_up: true }) : undefined;
      const status = cfg.type === 'wireguard' && (cfg as WireGuardNetwork).kind === 'hub'
        ? { ...n.status, peers_total: clients.length, peers_online: clients.filter((id) => this.clientStatus[id]?.online).length }
        : ports && n.status.ports
          ? { ...n.status, ports }
          : n.status;
      return { ...n, config: cfg, status };
    });
  }

  /** The clients of a hub of the active configuration, with their status. */
  clientViews(network: string): WireGuardClientView[] {
    const n = this.config.networks?.[network];
    if (n?.type !== 'wireguard') return [];
    return Object.entries((n as WireGuardNetwork).clients ?? {}).map(([id, c]) => ({
      id,
      network,
      config: c,
      status: this.clientStatus[id] ?? { online: false },
      private_key_stored: (c.key?.mode ?? 'generated') === 'generated' && !this.keyGone.has(id),
    }));
  }

  /** The wg-quick configuration the gateway would export for a client (keys made up, stable per client). */
  confOf(network: string, c: WireGuardClientView): string {
    const hub = this.config.networks?.[network] as WireGuardNetwork;
    const key = (seed: string) => btoa(seed.padEnd(32, '#').slice(0, 32));
    const priv = c.private_key_stored ? key(`private ${c.id}`) : '<PRIVATE_KEY>';
    const allowed = [hub.address.replace(/\.\d+\//, '.0/'), ...(c.config.reachable ?? []).map((r: NonNullable<WireGuardClient['reachable']>[number]) => (r.uplink ? '0.0.0.0/0' : (this.config.networks?.[r.network ?? ''] as { address?: string } | undefined)?.address?.replace(/\.\d+\//, '.0/') ?? ''))];
    return [
      '[Interface]',
      `PrivateKey = ${priv}`,
      `Address = ${c.config.address}/32`,
      `DNS = ${hub.address.split('/')[0]}`,
      '',
      '[Peer]',
      `PublicKey = ${key(`hub ${network}`)}`,
      `AllowedIPs = ${allowed.filter(Boolean).join(', ')}`,
      `Endpoint = 192.168.1.57:${hub.listen_port}`,
      `PersistentKeepalive = 25`,
      '',
    ].join('\n');
  }

  /** Like the gateway (LockoutRelevant): the management settings or a WireGuard network with the role management changed. */
  requiresConfirm(patch: unknown): boolean {
    const p = patch as { management?: unknown; networks?: Record<string, unknown> } | undefined;
    if (p?.management) return true;
    return Object.keys(p?.networks ?? {}).some((id) => {
      const before = this.config.networks?.[id] as { role?: string } | undefined;
      const after = (p!.networks![id] ?? {}) as { role?: string };
      return before?.role === 'management' || after.role === 'management';
    });
  }

  /**
   * The revision that waits for confirmation is rolled back (at its deadline, or on request): the active revision
   * stays what it was. Returns the event the gateway would send.
   */
  rollBackPending(reason = 'requested'): Omit<GatewayEvent, 'id' | 'time'> {
    const rev = this.state.pending_confirm?.revision ?? this.pending?.revision ?? this.state.active_revision;
    this.pending = undefined;
    const { pending_confirm: _gone, ...rest } = this.state;
    this.state = { ...rest, generation: this.state.generation + 1 };
    return { type: 'revision_rolled_back', message: `revision ${rev} rolled back`, data: { revision: rev, reason, restored: this.state.active_revision } };
  }

  /** POST /overlays: a new overlay, or the one with the same key replaced (it keeps its id). */
  putOverlay(req: OverlayRequest): { overlay: Overlay; created: boolean } {
    const kind = req.profile ? 'profile' : req.rule ? 'rule' : 'fault';
    // a fault's key has its family and its traffic, or its tunnel (a tunnel fault has no target)
    const sel = (o: OverlayRequest) =>
      kind === 'profile' ? '' : JSON.stringify([o.fault?.family ?? 'impairment', o.fault?.tunnel, o.fault?.protocol, o.fault?.ports, o.fault?.destination, o.rule?.protocol, o.rule?.ports]);
    const key = (o: OverlayRequest) => JSON.stringify([o.target ?? {}, sel(o)]);
    const old = this.overlays.find((o) => o.kind === kind && o.owner.id === 'admin' && key(o) === key(req));
    const unit: Record<string, number> = { s: 1000, m: 60_000, h: 3_600_000 };
    // the first removal is after `expires_in` when the request has it, and the overlay keeps its `ttl`
    const first = req.expires_in ?? req.ttl;
    const m = first ? /^(\d+)(s|m|h)$/.exec(first) : null;
    const ttlMs = m ? Number(m[1]) * unit[m[2]!]! : 0;
    const { expires_in: _writeOnly, ...body } = req;
    const overlay: Overlay = {
      ...body,
      id: old?.id ?? `f0000000-0000-4000-8000-${String(this.nextOverlay++).padStart(12, '0')}`,
      kind,
      owner: { type: 'user', id: 'admin', name: 'admin' },
      created_at: old?.created_at ?? new Date().toISOString(),
      updated_at: new Date().toISOString(),
      generation: ++this.state.generation,
      state: 'effective',
      counters: { packets: 0, bytes: 0, epoch: 1 },
      ...(ttlMs ? { expires_at: new Date(this.now() + ttlMs).toISOString() } : {}),
    };
    this.overlays = old ? this.overlays.map((o) => (o.id === old.id ? overlay : o)) : [...this.overlays, overlay];
    return { overlay, created: !old };
  }
  private nextOverlay = 100;

  /** The time on the gateway's clock. */
  now(): number {
    return Date.now() + this.clockSkewMs;
  }

  /** The commit-confirm deadline of the setup passed on the gateway: it rolled back, the setup is open again. */
  rollBackSetup(): void {
    const open = MockGateway.firstStart();
    this.setup = open.setup;
    this.state = open.state;
    this.loggedIn = false;
  }
}
