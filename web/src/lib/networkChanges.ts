// Networks and the routing in words, for the unapplied-changes bar and the drawer (plan §2.17 "Unapplied
// changes"; P3-M14-01): a network added ("test network 10.30.0.0/24, ports eth3, eth4"), a field changed
// ("ports: eth1 → eth1, eth3"), a protocol of the routing added or changed.
import type { LanNetwork, Network, Routing, RoutingProtocol, WireGuardNetwork } from '../api/generated/model';
import { jsonEqual } from './mergePatch';
import { subnetText } from './networks';
import { portKey } from './networkForm';
import { prefixesText } from './routingForm';
import type { FieldChange } from './rules';

/** A network without the clients of a hub (the clients are edited on their own). */
export function withoutClients(n: Network): Network {
  if (n.type !== 'wireguard' || !('clients' in n)) return n;
  const { clients: _clients, ...rest } = n as WireGuardNetwork;
  return rest as Network;
}

/** What a network is, in words: "test network 10.30.0.0/24", "WireGuard hub 10.90.0.0/24 (management)". */
export function networkSummary(n: Network): string {
  if (n.type === 'lan') {
    const lan = n as LanNetwork;
    const ports = lan.interfaces.map(portKey);
    return `test network ${subnetText(lan.address)}, ${ports.length === 1 ? 'port' : 'ports'} ${ports.join(', ')}`;
  }
  const wg = n as WireGuardNetwork;
  return `WireGuard ${wg.kind} ${subnetText(wg.address)}, UDP ${wg.listen_port}${wg.role === 'management' ? ' (management)' : ''}`;
}

function dhcpText(n: LanNetwork): string {
  if (n.dhcp?.enabled === false) return 'off';
  const parts = ['on'];
  if (n.dhcp?.lease_time) parts.push(`lease ${n.dhcp.lease_time}`);
  if (n.dhcp?.pools?.length) parts.push(n.dhcp.pools.map((p) => `${p.start}-${p.end}`).join(', '));
  return parts.join(', ');
}

function dnsText(n: LanNetwork): string {
  const parts = [];
  if (n.dns?.redirect_hardcoded) parts.push('redirect');
  if (n.dns?.block_dot) parts.push('DoT blocked');
  for (const e of n.dns?.static_entries ?? []) parts.push(`${e.name} ${e.addresses.join(' ')}`);
  return parts.join(', ');
}

/** The key of a link's peer in words: "generated", "generated, generation 2", "provided AbCd…". */
function peerKeyText(n: WireGuardNetwork): string {
  const k = n.peer?.key;
  if (!n.peer) return '';
  if (k?.mode === 'provided') return `provided ${(k.public_key ?? '').slice(0, 8)}…`;
  return k?.generation ? `generated, generation ${k.generation}` : 'generated';
}

/** The changes of one network in words: added, deleted, or field by field. */
export function networkChanges(before: Network | undefined, after: Network | null): FieldChange[] {
  if (after === null) return before ? [{ field: 'network', from: 'present', to: 'deleted' }] : [];
  if (!before) return [{ field: 'network', from: '—', to: `added (${networkSummary(after)})` }];
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => a !== b && out.push({ field, from: a || '—', to: b || '—' });
  add('name', before.name, after.name);
  add('description', before.description ?? '', after.description ?? '');
  add('address', before.address, after.address);
  add('masquerade', before.nat === false ? 'no' : 'yes', after.nat === false ? 'no' : 'yes');
  if (before.type === 'lan' && after.type === 'lan') {
    const a = before as LanNetwork;
    const b = after as LanNetwork;
    add('ports', a.interfaces.map(portKey).join(', '), b.interfaces.map(portKey).join(', '));
    add('DHCP', dhcpText(a), dhcpText(b));
    if (!jsonEqual(a.dhcp?.options, b.dhcp?.options)) out.push({ field: 'DHCP options', from: 'as before', to: 'changed' });
    add('DNS', dnsText(a), dnsText(b));
    add('routes', (a.routes ?? []).map((r) => `${r.destination} via ${r.via}`).join(', '), (b.routes ?? []).map((r) => `${r.destination} via ${r.via}`).join(', '));
  } else if (before.type === 'wireguard' && after.type === 'wireguard') {
    const a = before as WireGuardNetwork;
    const b = after as WireGuardNetwork;
    add('role', a.role ?? 'test', b.role ?? 'test');
    add('listen port', String(a.listen_port), String(b.listen_port));
    add('MTU', String(a.mtu ?? 1420), String(b.mtu ?? 1420));
    add('endpoint', a.endpoint ?? '', b.endpoint ?? '');
    add('peer', a.peer ? `${a.peer.address}${a.peer.endpoint ? ` at ${a.peer.endpoint}` : ''}` : '', b.peer ? `${b.peer.address}${b.peer.endpoint ? ` at ${b.peer.endpoint}` : ''}` : '');
    add('peer keepalive', a.peer?.keepalive ?? '', b.peer?.keepalive ?? '');
    add('peer key', peerKeyText(a), peerKeyText(b));
    add('preshared key', a.peer?.key?.preshared_key ? 'yes' : 'no', b.peer?.key?.preshared_key ? 'yes' : 'no');
    if (a.peer || b.peer) add('peer enabled', a.peer?.enabled === false ? 'no' : 'yes', b.peer?.enabled === false ? 'no' : 'yes');
    add('routes', (a.routes ?? []).join(', '), (b.routes ?? []).join(', '));
  }
  // a change the words above do not show (setNetwork keeps no edit that equals the base)
  if (out.length === 0) out.push({ field: 'settings', from: 'as before', to: 'changed' });
  return out;
}

/** The changes of the BIRD settings (router id, AS, external mode) in words. */
export function routingSettingsChanges(before: Routing, after: Routing): FieldChange[] {
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => a !== b && out.push({ field, from: a || '—', to: b || '—' });
  add('router id', before.router_id ?? '', after.router_id ?? '');
  add('AS', before.asn !== undefined ? String(before.asn) : '', after.asn !== undefined ? String(after.asn) : '');
  const ext = (r: Routing) => (r.external?.enabled ? `table ${r.external.table ?? '—'}` : 'off');
  add('external mode', ext(before), ext(after));
  if (ext(before) === ext(after) && !jsonEqual(before.external, after.external)) out.push({ field: 'external import filter', from: 'as before', to: 'changed' });
  return out;
}

export interface ProtocolNames {
  network?: (id: string) => string | undefined;
  client?: (id: string) => string | undefined;
}

function announceText(p: RoutingProtocol, names: ProtocolNames): string {
  return (p.announce ?? [])
    .map((a) => (a.network ? (names.network?.(a.network) ?? a.network) : a.client ? `client ${names.client?.(a.client) ?? a.client}` : (a.cidr ?? '')))
    .join(', ');
}

/** The changes of one routing protocol in words: added, deleted, or field by field. */
export function protocolChanges(before: RoutingProtocol | undefined, after: RoutingProtocol | undefined, names: ProtocolNames = {}): FieldChange[] {
  const link = (id: string) => names.network?.(id) ?? id;
  if (!after) return before ? [{ field: '', from: 'present', to: 'deleted' }] : [];
  if (!before) return [{ field: '', from: '—', to: `added (${after.type.toUpperCase()} on ${link(after.link)})` }];
  if (jsonEqual(before, after)) return [];
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => a !== b && out.push({ field, from: a || '—', to: b || '—' });
  add('name', before.name, after.name);
  add('type', before.type.toUpperCase(), after.type.toUpperCase());
  add('link', link(before.link), link(after.link));
  add('enabled', before.enabled === false ? 'no' : 'yes', after.enabled === false ? 'no' : 'yes');
  add('announced', announceText(before, names), announceText(after, names));
  add('allowed prefixes', prefixesText(before.import).replace(/\n/g, ', '), prefixesText(after.import).replace(/\n/g, ', '));
  add('default route', before.import?.allow_default ? 'accepted' : 'filtered', after.import?.allow_default ? 'accepted' : 'filtered');
  add('max prefixes', String(before.import?.max_prefixes ?? 100), String(after.import?.max_prefixes ?? 100));
  add('neighbor AS', String(before.bgp?.neighbor_asn ?? ''), String(after.bgp?.neighbor_asn ?? ''));
  add('neighbor', before.bgp?.neighbor_address ?? '', after.bgp?.neighbor_address ?? '');
  add('hold time', before.bgp?.hold_time ?? '', after.bgp?.hold_time ?? '');
  add('keepalive time', before.bgp?.keepalive_time ?? '', after.bgp?.keepalive_time ?? '');
  if (before.bgp || after.bgp) add('passive', before.bgp?.passive ? 'yes' : 'no', after.bgp?.passive ? 'yes' : 'no');
  add('area', before.ospf?.area ?? '', after.ospf?.area ?? '');
  add('hello interval', before.ospf?.hello_interval ?? before.babel?.hello_interval ?? '', after.ospf?.hello_interval ?? after.babel?.hello_interval ?? '');
  add('dead interval', before.ospf?.dead_interval ?? '', after.ospf?.dead_interval ?? '');
  add('cost', before.ospf?.cost !== undefined ? String(before.ospf.cost) : '', after.ospf?.cost !== undefined ? String(after.ospf.cost) : '');
  add('rxcost', before.babel?.rxcost !== undefined ? String(before.babel.rxcost) : '', after.babel?.rxcost !== undefined ? String(after.babel.rxcost) : '');
  add('custom snippet', before.custom_snippet ? 'set' : '', after.custom_snippet ? (before.custom_snippet && before.custom_snippet !== after.custom_snippet ? 'changed' : 'set') : '');
  // a change the words above do not show
  if (out.length === 0) out.push({ field: 'settings', from: 'as before', to: 'changed' });
  return out;
}
