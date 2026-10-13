// How the device list and the device detail present a device (plan §2.3, §2.17).
import type { DeviceView } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';

export type DeviceFilter = 'all' | 'online' | 'faulted' | 'unadopted';

export function isAdopted(d: DeviceView): boolean {
  return d.origin !== 'discovered';
}

/** The faults that act on the device: the winners per family, without the ones that impair nothing. */
export function deviceFaults(d: DeviceView): string[] {
  return (d.effective ?? [])
    .filter((f) => !(f.profile?.name === 'normal'))
    .map((f) => {
      const what = f.profile?.name ? `profile ${f.profile.name}` : (f.summary ?? f.name ?? f.family);
      return f.summary && f.profile?.name ? `${what}: ${f.summary}` : what;
    });
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
