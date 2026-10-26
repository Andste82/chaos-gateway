// The words and chips for faults, overlays and their targets (plan §2.17: fault chips are mono,
// orange, and say what happens to the traffic: "↑↓ 200 ms ±50", "5 % loss", "↓ 64 kbit/s").
import type { DhcpAction, DnsFault, FaultBody, ImpairmentParams, NetemParams, Overlay, Owner, Scope, TlsCase, TrafficMatch, TunnelRef } from '../api/generated/model';
import { durationMs, formatDuration } from './format';
import { profileTitle } from './profileNames';

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
  if (p.distribution && p.distribution !== 'uniform') out.push(`${p.distribution} delay`);
  if (p.keep_order) out.push('keep order');
  if (p.loss) out.push(`${pct(p.loss)} loss`);
  if (p.loss_correlation) out.push(`loss correlation ${pct(p.loss_correlation)}`);
  if (p.burst_loss) {
    // Gilbert-Elliott: good → bad / bad → good, and the delivery in each state when set
    const b = p.burst_loss;
    out.push(`burst loss ${pct(b.p)}/${pct(b.r)}${b.h ? ` h ${pct(b.h)}` : ''}${b.k ? ` k ${pct(b.k)}` : ''}`);
  }
  if (p.rate) out.push(`${arrow} ${rate(p.rate)}`);
  if (p.queue_limit) out.push(`queue ${p.queue_limit} pkts`);
  if (p.duplicate) out.push(`${pct(p.duplicate)} duplicate`);
  if (p.reorder) out.push(`${pct(p.reorder)} reorder`);
  if (p.corrupt) out.push(`${pct(p.corrupt)} corrupt`);
  if (p.flapping) out.push(`${arrow} flapping ${p.flapping.up} up / ${p.flapping.down} down`);
  return out;
}

const netemKeys: (keyof NetemParams)[] = [
  'latency',
  'jitter',
  'distribution',
  'keep_order',
  'loss',
  'loss_correlation',
  'burst_loss',
  'rate',
  'queue_limit',
  'duplicate',
  'reorder',
  'corrupt',
  'blackout',
  'flapping',
];

/** The members of an impairment parameter set: the flat netem parameters and the two directions. */
export const impairmentKeys: ReadonlySet<string> = new Set<string>([...netemKeys, 'upload', 'download']);

/** The impairment parameter set of a fault body (its flat netem parameters and its directions), or undefined when it has none. */
export function impairmentOf(f: FaultBody | undefined): ImpairmentParams | undefined {
  if (!f) return undefined;
  const out = Object.fromEntries(Object.entries(f).filter(([k, v]) => impairmentKeys.has(k) && v !== undefined)) as ImpairmentParams;
  return Object.keys(out).length ? out : undefined;
}

/** A fault body with its impairment parameter set replaced (the other members kept). */
export function withImpairment<T extends FaultBody>(f: T, i: ImpairmentParams | undefined): T {
  const out = Object.fromEntries(Object.entries(f).filter(([k]) => !impairmentKeys.has(k))) as T;
  return { ...out, ...i };
}

/**
 * Whether the gateway takes this combination of impairment parameters (internal/domain/validate_fault.go):
 * flat parameters or upload/download, not both (`mixed_directions`); per set not loss and burst
 * loss, not blackout and flapping (`mutually_exclusive`), no jitter larger than the latency. A
 * parameter set merged member by member out of two edits can break one of these where neither edit did.
 */
export function impairmentCombines(i: ImpairmentParams | undefined): boolean {
  if (!i) return true;
  const one = (p: NetemParams) =>
    !(p.loss !== undefined && p.burst_loss !== undefined) && !(p.blackout === true && p.flapping !== undefined) && (durationMs(p.jitter) ?? 0) <= (durationMs(p.latency) ?? 0);
  const flat = netemKeys.some((k) => i[k] !== undefined);
  if (i.upload !== undefined || i.download !== undefined) return !flat && [i.upload, i.download].every((p) => p === undefined || one(p));
  return one(i);
}

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

/** The chips of a DNS fault: "DNS nxdomain", "DNS delay 2s · broker.example.com, *.cdn.example.net", "DNS answer 10.9.0.1". */
export function dnsChips(d: DnsFault): string[] {
  let what = `DNS ${d.action.replace('_', ' ')}`;
  if (d.action === 'delay' && d.delay) what += ` ${d.delay}`;
  if (d.action === 'short_ttl' && d.ttl !== undefined) what = `DNS TTL ${d.ttl}`;
  if (d.action === 'wrong_answer') what = `DNS answer ${(d.answers ?? []).join(', ')}`.trim();
  const names = d.names ?? [];
  return [names.length ? `${what} · ${names.join(', ')}` : what];
}

/**
 * The chips of a TLS case: "TLS expired · 8883", "TLS self signed · broker.example.com", "TLS untrusted ca · 203.0.113.0/24 · cuts existing". The
 * interception adds what its settings change: "TLS intercept · 443 · 2 HTTP rules · QUIC allowed · no key log · servers not verified".
 * A network destination is named as the dialog's preview names it ("network IoT"), its UUID only when the name is not at hand
 * (P5-AUDIT-33).
 */
export function tlsChips(t: TlsCase, names: Names = {}): string[] {
  const what = `TLS ${t.case.replace(/_/g, ' ')}`;
  const parts: string[] = [];
  const ports = [...(t.ports ?? []).map(String), ...(t.port_ranges ?? []).map((r) => `${r.from}-${r.to}`)];
  if (ports.length) parts.push(ports.join(', '));
  if (t.destination?.cidr) parts.push(t.destination.cidr);
  if (t.destination?.hostname) parts.push(t.destination.hostname);
  if (t.destination?.network) parts.push(`network ${names.network?.(t.destination.network) ?? t.destination.network}`);
  if (t.destination?.uplink) parts.push('uplink');
  if (t.sni?.length) parts.push(`SNI ${t.sni.join(', ')}`);
  if (t.cut_existing) parts.push('cuts existing');
  if (t.case === 'intercept') {
    const rules = t.intercept?.http_rules?.length ?? 0;
    if (rules) parts.push(`${rules} HTTP rule${rules === 1 ? '' : 's'}`);
    if (t.intercept?.block_quic === false) parts.push('QUIC allowed');
    if (t.intercept?.keylog === false) parts.push('no key log');
    // the policy for the servers' certificates (P5-M22-03); nothing when the case leaves it to the installation
    const policy = t.intercept?.upstream;
    if (policy) parts.push({ verify: 'servers verified (bundle)', own_ca: 'servers verified (own CA)', insecure: 'servers not verified' }[policy]);
  }
  return [parts.length ? `${what} · ${parts.join(' · ')}` : what];
}

/** The chips of a DHCP action: "DHCP short lease 7s", "DHCP new IP 10.10.0.200", "DHCP options · router, DNS", "DHCP silence". */
export function dhcpChips(d: DhcpAction): string[] {
  switch (d.action) {
    case 'short_lease':
      return [d.lease_time ? `DHCP short lease ${d.lease_time}` : 'DHCP short lease'];
    case 'delete_lease':
      return ['DHCP delete lease'];
    case 'force_new_ip':
      return [d.ip ? `DHCP new IP ${d.ip}` : 'DHCP new IP'];
    case 'set_options': {
      const o = d.options;
      const named = [
        o?.router ? 'router' : '',
        o?.dns_servers?.length ? 'DNS' : '',
        o?.domain !== undefined ? 'domain' : '',
        o?.ntp_servers?.length ? 'NTP' : '',
        ...(o?.custom ?? []).map((c) => `option ${c.code}`),
      ].filter(Boolean);
      return [named.length ? `DHCP options · ${named.join(', ')}` : 'DHCP options'];
    }
    default:
      return [`DHCP ${String(d.action).replace(/_/g, ' ')}`.trim()];
  }
}

/** The chips of an overlay, whatever its kind; the names give the networks a TLS case names their names. */
export function overlayChips(o: Overlay, profileName?: (ref: string) => string | undefined, names: Names = {}): string[] {
  switch (o.kind) {
    case 'fault':
      return faultChips(o.fault);
    case 'profile':
      return [`profile ${o.profile ? profileTitle(profileName?.(o.profile) ?? o.profile) : ''}`.trim()];
    case 'rule':
      return [`access: ${o.rule?.action ?? 'rule'}`];
    case 'dns':
      return o.dns ? dnsChips(o.dns) : ['DNS fault'];
    case 'tls':
      return o.tls ? tlsChips(o.tls, names) : ['TLS case'];
    case 'dhcp':
      return o.dhcp ? dhcpChips(o.dhcp) : ['DHCP action'];
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

/** The tunnel of a tunnel fault in words: "Tunnel of rA", "Tunnel of link site-b". */
export function describeTunnel(t: TunnelRef | undefined, names: Names = {}): string {
  if (t?.client) return `Tunnel of ${names.device?.(t.client) ?? t.client}`;
  if (t?.link) return `Tunnel of link ${names.network?.(t.link) ?? t.link}`;
  return 'Tunnel';
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
