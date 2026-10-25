// The structural rules of a scenario that the gateway's `ValidateScenario` (internal/domain/validate_scenario.go)
// refuses and that the UI can see before the draft is sent (plan §2.10, §2.17 "Scenarios"; P4-AUDIT-36): a step
// that widens the target, a `remove` of a step that makes no overlay, the reserved step id, a `capture: stop`
// without its start, a tunnel outside the target, a TCP reset of another protocol, and a timeout or window of
// zero. The step editor offers only what passes them, the editors show them under the field, and
// `scenarioProblems` finds them in a scenario that comes from a text. Each one is silent where the
// configuration does not tell (a device whose network is not known yet): the gateway has the last word.
import type { Scope, Step } from '../api/generated/model';

/** What the browser knows of the configuration that the rules need; any lookup may be unknown. */
export interface Topology {
  /** The network a device or a WireGuard client is in. */
  networkOf?: (device: string) => string | undefined;
  /** The ids of the members of a group. */
  membersOf?: (group: string) => string[] | undefined;
}

/** The id the gateway keeps for the beginning of the run (`start` in a window). */
export const reservedStepId = 'start';

export const messages = {
  reservedId: 'start is reserved for the beginning of the run.',
  widened: "A step may narrow the scenario's target, never widen it.",
  noOverlay: (id: string) => `Step ${id} creates no overlay that could be removed.`,
  captureStop: 'No step before this one starts a capture of the same target.',
  tunnel: 'A tunnel fault or WireGuard action affects everything in the tunnel: the target must be that client or its WireGuard network.',
  zeroTimeout: 'The timeout must be longer than zero.',
  zeroWindow: 'The window must be longer than zero.',
};

const overlayKeys = ['profile', 'fault', 'rule', 'dns', 'tls', 'dhcp', 'wireguard'] as const;

/** A step of these types creates an overlay of the run that a later `remove` can take away. */
export function createsOverlay(s: Step): boolean {
  return overlayKeys.some((k) => (s as unknown as Record<string, unknown>)[k] !== undefined);
}

const same = (a: string | undefined, b: string | undefined): boolean => a !== undefined && b !== undefined && a.toLowerCase() === b.toLowerCase();

function kindOf(s: Scope | undefined): 'device' | 'group' | 'network' | 'remote_network' | 'global' | undefined {
  if (!s) return undefined;
  if (s.device !== undefined) return 'device';
  if (s.group !== undefined) return 'group';
  if (s.network !== undefined) return 'network';
  if (s.remote_network !== undefined) return 'remote_network';
  return s.global ? 'global' : undefined;
}

/**
 * Whether everything the scope `inner` selects is selected by `outer` (the gateway's `contained`). True where
 * the configuration does not say, and for an outer scope that is none of a device, a group or a network (that
 * has its own problem).
 */
export function scopeWithin(inner: Scope, outer: Scope, topo: Topology = {}): boolean {
  const ik = kindOf(inner);
  switch (kindOf(outer)) {
    case 'device':
      return ik === 'device' && same(inner.device, outer.device);
    case 'group': {
      if (ik === 'group') return same(inner.group, outer.group);
      if (ik !== 'device') return false;
      const members = topo.membersOf?.(outer.group!);
      return members === undefined || members.some((m) => same(m, inner.device));
    }
    case 'network': {
      const network = outer.network;
      switch (ik) {
        case 'network':
          return same(inner.network, network);
        case 'device': {
          const n = topo.networkOf?.(inner.device!);
          return n === undefined || same(n, network);
        }
        case 'group': {
          const members = topo.membersOf?.(inner.group!);
          return members === undefined || members.every((m) => { const n = topo.networkOf?.(m); return n === undefined || same(n, network); });
        }
        case 'remote_network': {
          const r = inner.remote_network!;
          if (r.client !== undefined) {
            const n = topo.networkOf?.(r.client);
            return n === undefined || same(n, network);
          }
          return r.link !== undefined && same(r.link, network);
        }
        default:
          return false;
      }
    }
    default:
      return true;
  }
}

/** Whether the tunnel of a client or a link lies outside the target: the target must be that client or its WireGuard network (a link: the network). */
export function tunnelOutsideTarget(tunnel: { client?: string; link?: string } | undefined, target: Scope, topo: Topology = {}): boolean {
  if (!tunnel) return false;
  if (tunnel.client !== undefined) {
    if (same(target.device, tunnel.client)) return false;
    if (target.network === undefined) return true;
    const n = topo.networkOf?.(tunnel.client);
    return n !== undefined && !same(n, target.network);
  }
  if (tunnel.link !== undefined) return !same(target.network, tunnel.link);
  return false;
}

/**
 * What the gateway refuses of the target of a DHCP action (`dhcpTarget`): a device or a network, and for `force_new_ip`
 * a device, because a reservation is one device's. Undefined for a target that is none of the kinds, as the
 * rules above do not say anything for what the configuration leaves open.
 */
export function dhcpTargetProblem(action: string | undefined, target: Scope): string | undefined {
  const k = kindOf(target);
  if (k === undefined) return undefined;
  if (k !== 'device' && k !== 'network') return `A DHCP action targets a device or a network, not a ${k.replace('_', ' ')}: narrow the step to one.`;
  if (k !== 'device' && action === 'force_new_ip') return "The action force_new_ip moves a device's reservation: it targets a device, not a network. Narrow the step to a device.";
  return undefined;
}

/** A canonical text of a scope to tell whether two name the same thing. */
export function scopeKey(s: Scope): string {
  const sort = (v: unknown): unknown =>
    v && typeof v === 'object' && !Array.isArray(v)
      ? Object.fromEntries(Object.entries(v as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : 1)).map(([k, x]) => [k, sort(x)]))
      : v;
  return JSON.stringify(sort(s)).toLowerCase();
}

/** Whether one of the steps (those before a `capture: stop`) starts a capture of the target the stop is about. */
export function startsCapture(before: Step[], scenarioTarget: Scope, target: Scope): boolean {
  return before.some((s) => s.capture === 'start' && scopeKey(s.target ?? scenarioTarget) === scopeKey(target));
}
