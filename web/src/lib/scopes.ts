// The remote networks in the scope selectors (plan §2.4, §2.17; P3-M13-08): a fault, a profile
// activation and an access rule can act on the client networks of a WireGuard client, the prefixes
// routed via a link, or a CIDR inside them (`remote_network`). The selectors offer them next to
// everything, network, group and device; the target is the text of `faultForm.targetOf`.
import type { NetworkView, WireGuardNetwork } from '../api/generated/model';
import { createOverlayBodyTargetRemoteNetworkCidrRegExp as prefixRegExp } from '../api/generated/zod/overlays/overlays';
import { parseSubnet, subnetText } from './networks';

/** The choice "a prefix inside a remote network"; the prefix follows it in the target. */
export const CIDR_CHOICE = 'remote_network:cidr:';

export interface ScopeOption {
  value: string;
  label: string;
}

/** The prefixes of a link: its static routes and the ones it learned (dynamic routing). */
function linkPrefixes(n: NetworkView, w: WireGuardNetwork): string[] {
  return [...new Set([...(w.routes ?? []), ...(n.status.link_peer?.learned_routes ?? [])])];
}

/**
 * The remote networks of the gateway as choices: each WireGuard client with client networks, each
 * link, and a prefix inside them.
 */
export function remoteScopeOptions(networks: NetworkView[]): ScopeOption[] {
  const out: ScopeOption[] = [];
  for (const n of networks) {
    if (n.config.type !== 'wireguard') continue;
    const w = n.config as WireGuardNetwork;
    if (w.kind === 'hub') {
      for (const [id, c] of Object.entries(w.clients ?? {})) {
        if (!c.client_networks?.length) continue;
        out.push({ value: `remote_network:client:${id}`, label: `Client networks · ${c.name} (${c.client_networks.join(', ')})` });
      }
    } else {
      const prefixes = linkPrefixes(n, w);
      out.push({ value: `remote_network:link:${n.id}`, label: `Remote networks · link ${w.name}${prefixes.length ? ` (${prefixes.join(', ')})` : ''}` });
    }
  }
  if (out.length) out.push({ value: CIDR_CHOICE, label: 'Remote network · a prefix inside them…' });
  return out;
}

/** The selector's value of a target: a CIDR target is the CIDR choice with its prefix beside it. */
export function scopeChoice(target: string): string {
  return target.startsWith(CIDR_CHOICE) ? CIDR_CHOICE : target;
}

/**
 * The problem of a remote-network prefix, or undefined. Whether it lies inside a client network or a
 * route of a link the gateway checks (`unknown_remote_network`; a link with dynamic routing takes
 * any prefix), and its message stands under the field.
 */
export function remoteCidrError(target: string): string | undefined {
  if (!target.startsWith(CIDR_CHOICE)) return undefined;
  const cidr = target.slice(CIDR_CHOICE.length).trim();
  if (!cidr) return 'Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.';
  if (!prefixRegExp.test(cidr)) return 'A prefix like 10.50.0.0/25.';
  const p = parseSubnet(cidr)!;
  if (p.address !== p.network) return `The host bits must be zero, like ${subnetText(cidr)}.`;
  return undefined;
}

/**
 * The tunnels a tunnel fault can impair (plan §2.2.1, M10): each WireGuard client of a hub and each
 * link, as `client:<id>` and `link:<id>`.
 */
export function tunnelOptions(networks: NetworkView[]): ScopeOption[] {
  const out: ScopeOption[] = [];
  for (const n of networks) {
    if (n.config.type !== 'wireguard') continue;
    const w = n.config as WireGuardNetwork;
    if (w.kind === 'hub') {
      for (const [id, c] of Object.entries(w.clients ?? {})) out.push({ value: `client:${id}`, label: `Client · ${c.name} (${w.name})` });
    } else out.push({ value: `link:${n.id}`, label: `Link · ${w.name}` });
  }
  return out;
}
