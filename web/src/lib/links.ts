// Where an object of the gateway is shown (plan §2.17 Overview: "every item links to its detail"):
// a device, a network, or the screen that lists the object when it has no page of its own.
import type { Event as GatewayEvent, Scope, TunnelRef } from '../api/generated/model';

/** The page of an object by its kind and id (an event's subject, an audit entry's object); undefined when there is none. */
export function objectLink(kind: string | undefined, id: string | undefined): string | undefined {
  if (!kind) return undefined;
  switch (kind) {
    case 'device':
    case 'wireguard_client':
      return id ? `/devices/${id}` : undefined;
    case 'network':
      return id ? `/networks/${id}` : undefined;
    case 'overlay':
    case 'fault':
      return '/faults';
    case 'rule':
    case 'access_rule':
      return '/rules';
    case 'profile':
      return '/profiles';
    case 'capture':
      return '/captures';
    case 'routing':
      return '/networks/routing';
    case 'uplink':
      return '/networks';
    default:
      return undefined;
  }
}

/** The page an event is about: its subject, else the device or the network its data names. */
export function eventLink(e: GatewayEvent): string | undefined {
  const own = objectLink(e.subject?.kind, e.subject?.id);
  if (own) return own;
  const data = (e.data ?? {}) as Record<string, unknown>;
  if (typeof data.device === 'string' && data.device) return `/devices/${data.device}`;
  if (typeof data.network === 'string' && data.network) return `/networks/${data.network}`;
  return undefined;
}

/**
 * The page of a fault's target: the device, the network (a link's remote networks: the link), a
 * WireGuard client; a group or everything has no page of its own, so the faults screen.
 */
export function scopeLink(s: Scope | undefined, tunnel?: TunnelRef): string {
  if (s?.device) return `/devices/${s.device}`;
  if (s?.network) return `/networks/${s.network}`;
  if (s?.remote_network?.link) return `/networks/${s.remote_network.link}`;
  if (s?.remote_network?.client) return `/devices/${s.remote_network.client}`;
  if (!s && tunnel?.client) return `/devices/${tunnel.client}`;
  if (!s && tunnel?.link) return `/networks/${tunnel.link}`;
  return '/faults';
}
