// The words and chips for faults, overlays and their targets (plan §2.17: fault chips are mono,
// orange, and say what happens to the traffic: "↑↓ 200 ms ±50", "5 % loss", "↓ 64 kbit/s").
import type { FaultBody, NetemParams, Overlay, Owner, Scope, TrafficMatch } from '../api/generated/model';
import { formatDuration } from './format';

function pct(p: string | undefined): string {
  return p ? p.replace('%', ' %') : '';
}

function rate(r: string): string {
  const m = /^([0-9.]+)(bit|kbit|Mbit|Gbit)$/.exec(r);
  return m ? `${m[1]} ${m[2]}/s` : r;
}

/** The chips of one direction's (or both directions') netem parameters. */
export function netemChips(p: NetemParams | undefined, arrow: string): string[] {
  if (!p) return [];
  const out: string[] = [];
  if (p.blackout) out.push(`${arrow} blackout`);
  if (p.latency) {
    const jitter = p.jitter ? ` ±${formatDuration(p.jitter).replace(/ ms$/, '')}` : '';
    out.push(`${arrow} ${formatDuration(p.latency)}${jitter}`);
  }
  if (p.loss) out.push(`${pct(p.loss)} loss`);
  if (p.burst_loss) out.push('burst loss');
  if (p.rate) out.push(`${arrow} ${rate(p.rate)}`);
  if (p.duplicate) out.push(`${pct(p.duplicate)} duplicate`);
  if (p.reorder) out.push(`${pct(p.reorder)} reorder`);
  if (p.corrupt) out.push(`${pct(p.corrupt)} corrupt`);
  if (p.flapping) out.push(`flapping ${p.flapping.up} up / ${p.flapping.down} down`);
  return out;
}

const netemKeys: (keyof NetemParams)[] = [
  'latency',
  'jitter',
  'loss',
  'burst_loss',
  'rate',
  'duplicate',
  'reorder',
  'corrupt',
  'blackout',
  'flapping',
];

/** The chips of a fault body: flat parameters for both directions, or upload and download apart. */
export function faultChips(f: FaultBody | undefined): string[] {
  if (!f) return [];
  if (f.family === 'mtu' && f.mtu) return [`MTU ${f.mtu.size}${f.mtu.mode && f.mtu.mode !== 'icmp' ? ` ${f.mtu.mode}` : ''}`];
  const out: string[] = [];
  if (netemKeys.some((k) => f[k] !== undefined)) out.push(...netemChips(f, '↑↓'));
  out.push(...netemChips(f.upload, '↑'), ...netemChips(f.download, '↓'));
  if (f.family === 'tunnel') return out.map((c) => `tunnel ${c}`);
  return out.length > 0 ? out : ['no impairment'];
}

/** The chips of an overlay, whatever its kind. */
export function overlayChips(o: Overlay, profileName?: (ref: string) => string | undefined): string[] {
  switch (o.kind) {
    case 'fault':
      return faultChips(o.fault);
    case 'profile':
      return [`profile ${o.profile ? (profileName?.(o.profile) ?? o.profile) : ''}`.trim()];
    case 'rule':
      return [`access: ${o.rule?.action ?? 'rule'}`];
    case 'dns':
      return ['DNS fault'];
    case 'tls':
      return ['TLS case'];
    case 'dhcp':
      return [`DHCP ${o.dhcp?.action ?? ''}`.trim()];
    case 'wireguard':
      return [`WireGuard ${o.wireguard?.action ?? ''}`.trim()];
    default:
      return [o.kind];
  }
}

/** Names of the objects a scope or destination may refer to by UUID. */
export interface Names {
  device?: (id: string) => string | undefined;
  group?: (id: string) => string | undefined;
  network?: (id: string) => string | undefined;
}

/** The source of a selector in words: "ESP32-42", "Group Sensors", "Network IoT", "Everything". */
export function describeScope(s: Scope | undefined, names: Names = {}): string {
  if (!s) return 'Everything';
  if (s.device) return names.device?.(s.device) ?? s.device;
  if (s.group) return `Group ${names.group?.(s.group) ?? s.group}`;
  if (s.network) return `Network ${names.network?.(s.network) ?? s.network}`;
  if (s.remote_network) {
    const r = s.remote_network;
    if (r.cidr) return `Remote network ${r.cidr}`;
    if (r.link) return `Remote networks of link ${names.network?.(r.link) ?? r.link}`;
    // a WireGuard client is a device of the same id and name
    if (r.client) return `Client networks of ${names.device?.(r.client) ?? r.client}`;
    return 'Remote network';
  }
  if (s.global) return 'Everything';
  return 'Everything';
}

/** The destination part of a selector: "→ TCP 8883", "→ 203.0.113.0/24", "" for any. */
export function describeMatch(m: TrafficMatch | undefined, names: Names = {}): string {
  if (!m) return '';
  const parts: string[] = [];
  const d = m.destination;
  if (d?.uplink) parts.push('uplink');
  if (d?.network) parts.push(`network ${names.network?.(d.network) ?? d.network}`);
  if (d?.cidr) parts.push(d.cidr);
  if (d?.hostname) parts.push(d.hostname);
  if (m.protocol && m.protocol !== 'any') {
    const ports = [...(m.ports ?? []).map(String), ...(m.port_ranges ?? []).map((r) => `${r.from}-${r.to}`)];
    parts.push(`${m.protocol.toUpperCase()}${ports.length ? ' ' + ports.join(',') : ''}`);
  }
  return parts.length ? `→ ${parts.join(' ')}` : '';
}

/** Who set an overlay, in words: "admin", "the token ci-runner", "a scenario run". */
export function describeOwner(o: Owner): string {
  if (o.type === 'run') return 'a scenario run';
  if (o.type === 'token') return `the token ${o.name ?? o.id}`;
  return o.name ?? 'the admin';
}
