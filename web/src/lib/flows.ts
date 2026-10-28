// The flow view (plan §2.17 "Flows"; M26): the connections the gateway tracks (`GET /flows`), a device's recent DNS queries
// (`GET /dns/queries?device=`) and the path view of the device detail. These functions make the text and the choices of the
// screens; the components only show them.
import type { DnsQueryLogEntry, Explanation, FaultRef, Flow } from '../api/generated/model';
import { profileTitle } from './profiles';

// ---- what a connection is

const ports: Record<string, string> = {
  'tcp/22': 'SSH',
  'tcp/53': 'DNS',
  'udp/53': 'DNS',
  'tcp/80': 'HTTP',
  'udp/123': 'NTP',
  'tcp/443': 'HTTPS',
  'udp/443': 'QUIC',
  'tcp/853': 'DoT',
  'tcp/1883': 'MQTT',
  'udp/5353': 'mDNS',
  'udp/5683': 'CoAP',
  'tcp/8883': 'MQTT/TLS',
};

/**
 * The name of what a connection carries: the well-known port's service ("MQTT/TLS", "NTP"), else the protocol ("TCP", "ICMP").
 * A connection the gateway redirected into its own service namespace is named for it.
 */
export function serviceLabel(f: Pick<Flow, 'protocol' | 'dport' | 'service'>): string {
  if (f.service === 'dns_proxy') return 'DNS';
  if (f.service === 'tls_responder') return 'TLS test';
  if (f.service === 'tls_proxy') return 'HTTP proxy';
  const named = f.dport ? ports[`${f.protocol}/${f.dport}`] : undefined;
  return named ?? f.protocol.toUpperCase();
}

/** The address a connection goes to, as the table shows it: the name the DNS proxy returned for it, else the address, with the port. */
export function destinationText(f: Pick<Flow, 'dst' | 'dst_hostname' | 'dport'>): string {
  return `${f.dst_hostname ?? f.dst}${f.dport ? `:${f.dport}` : ''}`;
}

/** The address a connection comes from, with the port. */
export function sourceText(f: Pick<Flow, 'src' | 'sport'>): string {
  return `${f.src}${f.sport ? `:${f.sport}` : ''}`;
}

/** The state of a connection in words: conntrack's ("ESTABLISHED" → "established"), "udp" for a datagram flow without one. */
export function stateText(f: Pick<Flow, 'state' | 'protocol'>): string {
  if (f.state) return f.state.toLowerCase().replace(/_/g, ' ');
  return f.protocol === 'udp' ? 'udp' : '—';
}

/** The bytes a connection moved in both directions. */
export function flowBytes(f: Pick<Flow, 'upload' | 'download'>): number {
  return (f.upload?.bytes ?? 0) + (f.download?.bytes ?? 0);
}

/**
 * The connections newest first (the start conntrack noted, else after those with one), then by id so that the order is the
 * same at every poll.
 */
export function sortFlows(flows: Flow[]): Flow[] {
  const start = (f: Flow) => (f.started_at ? Date.parse(f.started_at) : Number.NEGATIVE_INFINITY);
  return [...flows].sort((a, b) => start(b) - start(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// ---- the filters of the flow view

export interface FlowFilters {
  device: string;
  network: string;
  /** Free text: part of an address, a name, a port or a service. */
  text: string;
  /** `tcp`, `udp`, `icmp` or empty for every protocol. */
  protocol: string;
  /** Only the connections that came in through a port forward. */
  forwarded: boolean;
}

export const noFlowFilters = (): FlowFilters => ({ device: '', network: '', text: '', protocol: '', forwarded: false });

export function flowsFiltered(f: FlowFilters): boolean {
  return !!(f.device || f.network || f.text.trim() || f.protocol || f.forwarded);
}

/** What the gateway filters (`GET /flows?device=&network=`); the rest is filtered here. */
export function flowParams(f: FlowFilters, limit: number): { device?: string; network?: string; limit: number } {
  return { ...(f.device ? { device: f.device } : {}), ...(f.network ? { network: f.network } : {}), limit };
}

/** The connections the text, the protocol and the port forward filters select (the gateway has filtered device and network). */
export function filterFlows(flows: Flow[], f: FlowFilters): Flow[] {
  const text = f.text.trim().toLowerCase();
  return flows.filter((x) => {
    if (f.protocol && x.protocol !== f.protocol) return false;
    if (f.forwarded && !x.forwarded) return false;
    if (!text) return true;
    return [x.src, x.dst, x.dst_hostname, x.nat_src, x.sport, x.dport, serviceLabel(x), x.protocol]
      .filter((v) => v !== undefined && v !== '')
      .some((v) => String(v).toLowerCase().includes(text));
  });
}

/** How many connections the flow view draws at first and adds with "Show more". */
export const flowPageSize = 200;

// ---- explain: a connection picked in the flow view

export interface ExplainRequest {
  device?: string;
  src?: string;
  dst: string;
  protocol?: 'tcp' | 'udp' | 'icmp';
  port?: number;
}

/**
 * What `GET /explain` needs to explain a connection: a connection of a device is explained for the device, a forwarded one
 * (the source is a peer behind the entry side, the destination the entry side's address) and one of an unknown host for its
 * source address.
 */
export function explainRequest(f: Flow): ExplainRequest {
  const r: ExplainRequest = { dst: f.dst };
  if (f.device && !f.forwarded) r.device = f.device;
  else r.src = f.src;
  if (f.protocol === 'tcp' || f.protocol === 'udp' || f.protocol === 'icmp') r.protocol = f.protocol;
  if ((f.protocol === 'tcp' || f.protocol === 'udp') && f.dport) r.port = f.dport;
  return r;
}

/** The address of the Faults screen with the explain card filled in from a request (and run at once). */
export function explainQuery(r: ExplainRequest): Record<string, string> {
  return {
    explain: '1',
    ...(r.device ? { device: r.device } : {}),
    ...(r.src ? { src: r.src } : {}),
    dst: r.dst,
    ...(r.protocol ? { protocol: r.protocol } : {}),
    ...(r.port ? { port: String(r.port) } : {}),
  };
}

/** The request an address of the Faults screen asks for, or undefined when it asks for none. */
export function explainFromQuery(q: Record<string, unknown>): { source: 'device' | 'address'; device: string; src: string; dst: string; protocol: string; port: string } | undefined {
  const s = (k: string) => (typeof q[k] === 'string' ? (q[k] as string) : '');
  if (s('explain') !== '1' || !s('dst') || (!s('device') && !s('src'))) return undefined;
  const protocol = s('protocol');
  return {
    source: s('device') ? 'device' : 'address',
    device: s('device'),
    src: s('src'),
    dst: s('dst'),
    protocol: protocol === 'tcp' || protocol === 'udp' || protocol === 'icmp' ? protocol : 'any',
    port: s('port'),
  };
}

// ---- the path view of the device detail

export interface PathTarget {
  /** Identifies the destination in the choice: the address, the protocol and the port. */
  key: string;
  /** The address the connections to it came from: the question to the gateway is asked for this source, not for whichever address the device sorts first. */
  src: string;
  dst: string;
  hostname?: string;
  protocol: 'tcp' | 'udp' | 'icmp';
  port?: number;
  /** The label of the choice and of the last node: "MQTT/TLS broker.example.com:8883". */
  label: string;
  /** The bytes the connections to it moved, by which the destinations are ordered. */
  bytes: number;
  connections: number;
}

/**
 * The destinations of a device's connections, the one that moved the most first: what the path view can be drawn to. A
 * connection that came in through a port forward is not a path out of the device and a protocol that is neither TCP,
 * UDP nor ICMP cannot be explained, so neither is listed.
 */
export function pathTargets(flows: Flow[]): PathTarget[] {
  const by = new Map<string, PathTarget>();
  for (const f of flows) {
    if (f.forwarded || (f.protocol !== 'tcp' && f.protocol !== 'udp' && f.protocol !== 'icmp')) continue;
    const port = f.protocol === 'icmp' ? undefined : f.dport;
    const key = `${f.protocol}/${f.dst}${port ? `:${port}` : ''}`;
    const have = by.get(key);
    if (have) {
      have.bytes += flowBytes(f);
      have.connections++;
      have.hostname ??= f.dst_hostname;
      continue;
    }
    by.set(key, { key, src: f.src, dst: f.dst, hostname: f.dst_hostname, protocol: f.protocol, port, label: '', bytes: flowBytes(f), connections: 1 });
  }
  const out = [...by.values()];
  for (const t of out) t.label = `${serviceLabel({ protocol: t.protocol, dport: t.port })} ${destinationText({ dst: t.dst, dst_hostname: t.hostname, dport: t.port })}`;
  return out.sort((a, b) => b.bytes - a.bytes || (a.key < b.key ? -1 : 1));
}

const accessLayers: Record<string, string> = {
  gateway_protection: "the gateway's protection of its own ports",
  overlay_rule: 'an overlay rule',
  config_rule: 'a configured rule',
  access_matrix: 'the access matrix',
  port_forward: 'the port forward',
};

/** What a fault that wins for the path is called on the badge: the profile it belongs to, its own summary, its name, else its family. */
export function faultBadgeText(r: FaultRef): string {
  if (r.profile?.name) return `profile ${profileTitle(r.profile.name)}`;
  return r.summary ?? r.name ?? r.family;
}

export interface PathState {
  /** The access verdict stops the traffic: the path is red. */
  blocked?: string;
  /** The faults that win for the path and act on the connection, one per family. */
  faults: { family: string; text: string }[];
  /** The faults of the device that win but do not act on the path: its name resolution and its DHCP. */
  elsewhere: { family: string; text: string }[];
}

/**
 * The families whose faults act on the connection the path draws. A DNS fault changes the answer of a name query and a DHCP fault
 * the lease: neither touches the packets of a connection to an address (`GET /explain` resolves them for every destination, a
 * DHCP overlay or a DNS fault without names matches all of them), so they are listed apart and never turn the line orange.
 */
const onThePath = new Set(['impairment', 'mtu', 'tls', 'tunnel']);

const accessVerdicts: Record<string, string> = { reject: 'Rejected', reset: 'Reset', drop: 'Dropped' };

/**
 * What the badge of the path says (plan §2.17: "path view MQTT → broker with fault badge"): the faults that act on the
 * connection and win for it, one per family, and an access verdict that stops it (drop, reject or a reset). A path without
 * either is as configured.
 */
export function pathState(e: Explanation | undefined): PathState {
  const out: PathState = { faults: [], elsewhere: [] };
  if (!e) return out;
  if (e.access.verdict !== 'allow') out.blocked = `${accessVerdicts[e.access.verdict] ?? 'Stopped'} by ${accessLayers[e.access.layer] ?? e.access.layer}`;
  for (const f of e.faults) if (f.winner) (onThePath.has(f.family) ? out.faults : out.elsewhere).push({ family: f.family, text: faultBadgeText(f.winner) });
  return out;
}

// ---- recent DNS queries

/** The answer of a query in words: the addresses, else the response code. */
export function dnsAnswerText(q: Pick<DnsQueryLogEntry, 'rcode' | 'answers'>): string {
  if (q.answers?.length) return q.answers.join(', ');
  return q.rcode === 'TIMEOUT' ? 'no answer' : q.rcode === 'NOERROR' ? 'empty answer' : q.rcode;
}

/**
 * How a query reads in the list: a fault changed its answer (orange: it impairs traffic), it was not answered with an address
 * (red: NXDOMAIN, SERVFAIL, a timeout), or it was an ordinary answer.
 */
export function dnsQueryTone(q: Pick<DnsQueryLogEntry, 'rcode' | 'fault'>): 'normal' | 'fault' | 'block' {
  if (q.fault) return 'fault';
  return q.rcode === 'NOERROR' ? 'normal' : 'block';
}
