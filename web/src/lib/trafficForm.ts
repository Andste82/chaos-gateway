// The selector of traffic in a form (plan §2.4 `TrafficMatch`): a protocol, ports and a destination. The
// fault dialog (custom traffic), the steps of a scenario that select traffic (a fault, a rule, a wait) and
// its checks use the same fields, the same checks under them and the same selector they become.
import type { Destination, Protocol, TrafficMatch } from '../api/generated/model';
import {
  createOverlayBodyFaultOneDestinationCidrRegExp as cidrRegExp,
  createOverlayBodyFaultOneDestinationHostnameOneRegExp as hostnameRegExp,
} from '../api/generated/zod/overlays/overlays';
import { formatPorts, parsePorts } from './rules';

export type DestKind = 'any' | 'uplink' | 'network' | 'cidr' | 'hostname';

export interface TrafficForm {
  protocol: Protocol;
  /** "80, 443, 1000-2000"; empty is any port. */
  ports: string;
  /** The kind of destination; `destination` holds its network, address or host name. */
  destKind: DestKind;
  destination: string;
}

export type TrafficField = 'ports' | 'destination';

export function emptyTraffic(): TrafficForm {
  return { protocol: 'any', ports: '', destKind: 'any', destination: '' };
}

/** The problems of the traffic fields, by field; empty when the selector can be used. */
export function trafficErrors(t: TrafficForm): Partial<Record<TrafficField, string>> {
  const e: Partial<Record<TrafficField, string>> = {};
  const p = parsePorts(t.ports);
  if (p.error) e.ports = p.error;
  else if ((p.ports || p.port_ranges) && t.protocol !== 'tcp' && t.protocol !== 'udp') e.ports = 'Ports need the protocol TCP or UDP.';
  const d = t.destination.trim();
  if (t.destKind === 'cidr' && !cidrRegExp.test(d)) e.destination = 'An IPv4 address or a prefix like 203.0.113.0/24.';
  if (t.destKind === 'hostname' && !hostnameRegExp.test(d)) e.destination = 'A host name like broker.example.com or *.example.com.';
  if (t.destKind === 'network' && !d) e.destination = 'Choose a network.';
  return e;
}

/** The destination of the form; undefined for "anywhere". */
export function destinationOf(t: Pick<TrafficForm, 'destKind' | 'destination'>): Destination | undefined {
  const d = t.destination.trim();
  switch (t.destKind) {
    case 'uplink':
      return { uplink: true };
    case 'network':
      return { network: d };
    case 'cidr':
      return { cidr: d };
    case 'hostname':
      return { hostname: d };
    default:
      return undefined;
  }
}

/** The selector of the form: only what is set (protocol `any`, no ports and no destination are the empty selector). */
export function trafficMatch(t: TrafficForm): TrafficMatch {
  const out: TrafficMatch = {};
  if (t.protocol !== 'any') out.protocol = t.protocol;
  const p = parsePorts(t.ports);
  if (p.ports) out.ports = p.ports;
  if (p.port_ranges) out.port_ranges = p.port_ranges;
  const d = destinationOf(t);
  if (d) out.destination = d;
  return out;
}

/** The destination as the form has it. */
export function trafficFromDestination(d: Destination | undefined): Pick<TrafficForm, 'destKind' | 'destination'> {
  if (!d) return { destKind: 'any', destination: '' };
  if (d.uplink) return { destKind: 'uplink', destination: '' };
  if (d.network !== undefined) return { destKind: 'network', destination: d.network };
  if (d.hostname !== undefined) return { destKind: 'hostname', destination: d.hostname };
  return { destKind: 'cidr', destination: d.cidr ?? '' };
}

/** The form of a selector. */
export function trafficFromMatch(m: TrafficMatch | undefined): TrafficForm {
  return { protocol: m?.protocol ?? 'any', ports: formatPorts(m), ...trafficFromDestination(m?.destination) };
}

/** The field of the form that a problem the gateway reported names, by its pointer ("…/ports/0", "…/destination/cidr"). */
export function trafficFieldOf(path: string | undefined): TrafficField | undefined {
  const p = path ?? '';
  if (/\/destination(\/|$)/.test(p)) return 'destination';
  const segments = p.split('/').filter(Boolean);
  if (segments.includes('ports') || segments.includes('port_ranges')) return 'ports';
  // the protocol has no message of its own: what is wrong with it is mostly the ports that need it
  if (segments.at(-1) === 'protocol') return 'ports';
  return undefined;
}
