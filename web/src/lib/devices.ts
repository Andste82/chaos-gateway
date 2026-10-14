// How the device list and the device detail present a device (plan §2.3, §2.17).
import type { DeviceView, FaultBody, LanNetwork, Network, Overlay, WireGuardNetwork } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { faultChips, overlayChips } from './faults';
import { profileTitle } from './profileNames';

export type DeviceFilter = 'all' | 'online' | 'faulted' | 'unadopted';

export function isAdopted(d: DeviceView): boolean {
  return d.origin !== 'discovered';
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

/** Sort order of the list: online first, then by name. */
export function compareDevices(a: DeviceView, b: DeviceView): number {
  if (a.observed.online !== b.observed.online) return a.observed.online ? -1 : 1;
  return a.name.localeCompare(b.name, undefined, { numeric: true });
}
