// How the device list and the device detail present a device (plan §2.3, §2.17).
import type {
  AccessRule,
  ConfigFault,
  DeviceView,
  FaultBody,
  Group,
  LanNetwork,
  Network,
  Overlay,
  PortForward,
  Scenario,
  Scope,
  WireGuardNetwork,
} from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { faultChips, overlayChips } from './faults';
import { forwardTitle } from './forwards';
import { discoveredDeviceId } from './ids';
import { profileTitle } from './profileNames';

export type DeviceFilter = 'all' | 'online' | 'faulted' | 'unadopted' | 'adopted';

export function isAdopted(d: DeviceView): boolean {
  return d.origin !== 'discovered';
}

/** A configured device: adopted in the UI or configured via YAML or the API (the API does not tell them apart). */
export function isConfigured(d: DeviceView): boolean {
  return d.origin === 'configured';
}

/**
 * The name the UI shows for a device (#78): a discovered device under the host name of its DHCP lease
 * when it announces one, else under its generated name (`dev-<mac>`); every other device under its
 * name. Only for display: the gateway keeps the generated name as the device's `name` (a host name is
 * not unique, changes with the lease and need not be a valid name), and references use that.
 */
export function displayName(d: DeviceView): string {
  const host = d.observed.lease?.hostname?.trim();
  return d.origin === 'discovered' && host ? host : d.name;
}

/**
 * The small line under the displayed name: the generated name of a discovered device listed under its
 * host name, and the host name of any other device whose host name differs from its name.
 */
export function nameDetail(d: DeviceView): string | undefined {
  const host = d.observed.lease?.hostname?.trim();
  if (!host) return undefined;
  if (d.origin === 'discovered') return d.name;
  return host.toLowerCase() === d.name.toLowerCase() ? undefined : `host name ${host}`;
}

/**
 * The MAC of a configured device whose discovered id is the device's id (#78): the device was adopted
 * from discovery with that MAC, and once it is removed from the configuration the gateway lists it as a
 * discovered device under the same id while it sees it with that MAC. Undefined for a device configured
 * with an id of its own (YAML or the API) or one whose MACs no longer include that MAC.
 */
export function idMac(id: string, macs: readonly string[] | undefined): string | undefined {
  return (macs ?? []).find((m) => discoveredDeviceId(m) === id.toLowerCase());
}

/** A configuration object that names a device; removing the device makes it an `unknown_reference`. */
export interface DeviceReference {
  kind: 'rule' | 'fault' | 'group' | 'forward' | 'scenario';
  id: string;
  /** The object's name, or a title for one without a name. */
  name: string;
  /** Where it can be changed in the UI; none for a group (configured via YAML or the API). */
  to?: string;
}

/** The configuration objects that can name a device (the draft's view of them). */
export interface DeviceReferrers {
  rules?: Record<string, AccessRule>;
  faults?: Record<string, ConfigFault>;
  groups?: Record<string, Group>;
  forwards?: Record<string, PortForward>;
  scenarios?: Record<string, Scenario>;
}

/**
 * The objects that name the device (by its UUID or its name, as the gateway resolves references; plan
 * conventions): access rules and configured faults by their source, groups by their members, port
 * forwards by their target, scenarios by their target or a step's. Sorted by kind, then by name.
 */
export function deviceReferences(device: { id: string; name: string }, cfg: DeviceReferrers): DeviceReference[] {
  const lower = device.name.toLowerCase();
  const own = device.id.toLowerCase();
  const names = (ref: string | undefined) => !!ref && (ref.toLowerCase() === own || ref.toLowerCase() === lower);
  const inScope = (s: Scope | undefined) => names(s?.device);
  const out: DeviceReference[] = [];
  for (const [id, r] of Object.entries(cfg.rules ?? {})) if (inScope(r.source)) out.push({ kind: 'rule', id, name: r.name ?? id, to: '/rules' });
  for (const [id, f] of Object.entries(cfg.faults ?? {})) if (inScope(f.source)) out.push({ kind: 'fault', id, name: f.name ?? id, to: '/faults' });
  for (const [id, g] of Object.entries(cfg.groups ?? {})) if ((g.members ?? []).some(names)) out.push({ kind: 'group', id, name: g.name });
  for (const [id, p] of Object.entries(cfg.forwards ?? {})) {
    if (names(p.target.device)) out.push({ kind: 'forward', id, name: forwardTitle(p), to: `/networks?forward=${id}` });
  }
  for (const [id, sc] of Object.entries(cfg.scenarios ?? {})) {
    if (inScope(sc.target) || sc.steps.some((st) => inScope(st.target))) out.push({ kind: 'scenario', id, name: sc.name, to: `/scenarios/${id}` });
  }
  const order = ['rule', 'fault', 'group', 'forward', 'scenario'];
  return out.sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind) || a.name.localeCompare(b.name, undefined, { numeric: true }));
}

const referenceKinds: Record<DeviceReference['kind'], string> = { rule: 'rule', fault: 'fault', group: 'group', forward: 'port forward', scenario: 'scenario' };

/** The kind of a reference in words ("port forward"). */
export function referenceKind(r: DeviceReference): string {
  return referenceKinds[r.kind];
}

/** Where the chips of a winning fault come from: the overlay or the configured fault it names. */
export interface FaultBodies {
  overlay?: (id: string) => Overlay | undefined;
  config?: (id: string) => FaultBody | undefined;
}

const chipFamilies = new Set(['impairment', 'mtu', 'tunnel']);

/**
 * The faults that act on the device as fault chips: the winners per family (DeviceView.effective),
 * without the ones that impair nothing, in the same compact form as the overview and the faults
 * screen ("↑↓ 200 ms ±50", "5 % loss", "profile LTE"). A winner whose overlay or configured fault is
 * not at hand falls back to the gateway's summary.
 */
export function deviceFaults(d: DeviceView, bodies: FaultBodies = {}): string[] {
  const out: string[] = [];
  for (const f of d.effective ?? []) {
    if (f.profile?.name === 'normal') continue;
    if (f.profile?.name) {
      out.push(`profile ${profileTitle(f.profile.name)}`);
      continue;
    }
    if (f.layer === 'overlay') {
      const o = bodies.overlay?.(f.id);
      if (o) {
        out.push(...overlayChips(o));
        continue;
      }
    } else if (chipFamilies.has(f.family)) {
      const body = bodies.config?.(f.id);
      if (body) {
        out.push(...faultChips(body));
        continue;
      }
    }
    out.push(f.summary ?? f.name ?? f.family);
  }
  return [...new Set(out)];
}

/** The router and the DNS servers a device of a network is given, as the device detail shows them. */
export interface DeviceNetworkInfo {
  /** The default router; undefined where there is none to show (a WireGuard tunnel). */
  gateway?: string;
  /** The DNS servers; empty when none is handed out. */
  dns: string[];
  /** Whether the DNS server is the gateway's own address: its DNS proxy (DNS faults apply). */
  proxy: boolean;
}

/**
 * What a device in a network is told (plan §2.7): in a local network the DHCP options router and DNS
 * servers, each defaulting to the network's gateway address; for a WireGuard client the DNS setting
 * of its exported configuration (the hub address by default, custom servers, or none).
 */
export function deviceNetworkInfo(network: Network | undefined, deviceId: string): DeviceNetworkInfo | undefined {
  if (!network?.address) return undefined;
  const own = network.address.split('/')[0]!;
  if (network.type === 'wireguard') {
    const client = (network as WireGuardNetwork).clients?.[deviceId];
    if (!client) return undefined;
    const mode = client.dns?.mode ?? 'gateway';
    const dns = mode === 'gateway' ? [own] : mode === 'custom' ? (client.dns?.servers ?? []) : [];
    return { dns, proxy: dns.includes(own) };
  }
  const opts = (network as LanNetwork).dhcp?.options;
  const dns = opts?.dns_servers?.length ? opts.dns_servers : [own];
  return { gateway: opts?.router ?? own, dns, proxy: dns.includes(own) };
}

export function matchesFilter(d: DeviceView, f: DeviceFilter): boolean {
  switch (f) {
    case 'online':
      return d.observed.online;
    case 'faulted':
      return deviceFaults(d).length > 0;
    case 'unadopted':
      return !isAdopted(d);
    case 'adopted':
      return isConfigured(d);
    default:
      return true;
  }
}

export function matchesSearch(d: DeviceView, q: string): boolean {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  const hay = [d.name, ...(d.observed.addresses ?? []), ...(d.observed.macs ?? []), d.observed.lease?.hostname ?? '']
    .join(' ')
    .toLowerCase();
  return s.split(/\s+/).every((part) => hay.includes(part));
}

/** The status dot of a device: online, offline, a probe, or not adopted yet. */
export function deviceStatus(d: DeviceView): { tone: DotTone; label: string } {
  if (d.origin === 'probe') return { tone: 'probe', label: 'Probe' };
  if (!d.observed.online) return { tone: 'offline', label: 'Offline' };
  return { tone: 'normal', label: 'Online' };
}

export function originLabel(d: DeviceView): string {
  return { configured: 'configured', discovered: 'discovered, not adopted', wireguard_client: 'WireGuard client', probe: 'probe' }[d.origin];
}

const sourceLabels: Record<string, string> = {
  config: 'configuration',
  dhcp: 'DHCP',
  neighbor: 'neighbor table',
  conntrack: 'conntrack',
  wireguard: 'WireGuard',
  probe: 'probe',
};

export function sourcesLabel(d: DeviceView): string {
  return (d.observed.sources ?? []).map((s) => sourceLabels[s] ?? s).join(', ');
}

/** Sort order of the list: online first, then by the displayed name. */
export function compareDevices(a: DeviceView, b: DeviceView): number {
  if (a.observed.online !== b.observed.online) return a.observed.online ? -1 : 1;
  return displayName(a).localeCompare(displayName(b), undefined, { numeric: true });
}
