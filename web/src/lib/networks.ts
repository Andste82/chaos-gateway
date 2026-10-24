// Networks in the user's words (plan §2.2, §2.2.1, §2.7, §2.17 "Networks"): IPv4 arithmetic for the
// subnets, the proposal of the next free tunnel address of a hub (computed here, M14), the add-client
// form with its checks, and the access matrix: the policy of a pair of networks, a toggle that keeps
// the configuration's entries minimal, and its changes in words.
import type { DhcpPool, MatrixEndpoint, MatrixEntry, MatrixEntryPolicy, NetworkView, WireGuardClient, WireGuardNetwork } from '../api/generated/model';
import {
  completeSetupBodyConfigurationOneNetworksTwoClientsClientNetworksItemRegExp as cidrRegExp,
  completeSetupBodyConfigurationOneNetworksTwoClientsKeepaliveOneRegExp as durationRegExp,
  completeSetupBodyConfigurationOneNetworksTwoClientsKeyPublicKeyOneRegExp as keyRegExp,
  completeSetupBodyConfigurationOneNetworksTwoClientsNameRegExp as nameRegExp,
} from '../api/generated/zod/system/system';
import type { FieldChange } from './rules';

// ---- IPv4 ------------------------------------------------------------------------------------

/** "10.99.0.1" → its number; undefined for anything else. */
export function parseIPv4(s: string | undefined): number | undefined {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec((s ?? '').trim());
  if (!m) return undefined;
  const o = m.slice(1).map(Number);
  if (o.some((x) => x > 255)) return undefined;
  return ((o[0]! << 24) >>> 0) + (o[1]! << 16) + (o[2]! << 8) + o[3]!;
}

export function formatIPv4(n: number): string {
  return [24, 16, 8, 0].map((s) => (n >>> s) & 255).join('.');
}

export interface Subnet {
  /** The address as written (the gateway's own address for a network). */
  address: number;
  len: number;
  network: number;
  broadcast: number;
}

/** "10.99.0.1/24" → the address, the network and the broadcast address. */
export function parseSubnet(cidr: string | undefined): Subnet | undefined {
  const [ip, lenText] = (cidr ?? '').split('/');
  const address = parseIPv4(ip);
  const len = Number(lenText);
  if (address === undefined || lenText === undefined || !/^\d+$/.test(lenText) || len > 32) return undefined;
  const mask = len === 0 ? 0 : (0xffffffff << (32 - len)) >>> 0;
  const network = (address & mask) >>> 0;
  const broadcast = (network | (~mask >>> 0)) >>> 0;
  return { address, len, network, broadcast };
}

export function inSubnet(ip: number, s: Subnet): boolean {
  return ip >= s.network && ip <= s.broadcast;
}

/** A network prefix with host bits set ("10.40.0.1/24"): the gateway's message for it, or undefined. */
export function hostBitsError(cidr: string): string | undefined {
  const s = parseSubnet(cidr.trim());
  if (!s || s.address === s.network) return undefined;
  return `${cidr.trim()} has host bits set; the network is ${formatIPv4(s.network)}/${s.len}.`;
}

/** The ranges no network, route or client network may touch (the gateway's `reserved_range`). */
const reservedRanges = ['0.0.0.0/8', '127.0.0.0/8', '169.254.0.0/16', '224.0.0.0/4', '240.0.0.0/4'];

/** The reserved range a subnet touches ("127.0.0.0/8" for "127.1.0.1/24"), or undefined. */
export function reservedRangeOf(cidr: string): string | undefined {
  const s = parseSubnet(cidr.trim());
  if (!s) return undefined;
  return reservedRanges.find((r) => {
    const x = parseSubnet(r)!;
    return s.network <= x.broadcast && x.network <= s.broadcast;
  });
}

/** "10.10.0.0/24" for "10.10.0.1/24". */
export function subnetText(cidr: string | undefined): string {
  const s = parseSubnet(cidr);
  return s ? `${formatIPv4(s.network)}/${s.len}` : (cidr ?? '');
}

/** The address without its prefix length: "10.10.0.1" for "10.10.0.1/24". */
export function hostOf(cidr: string | undefined): string {
  return (cidr ?? '').split('/')[0] ?? '';
}

/**
 * The DHCP pools of a network: the configured ones, or the default of the spec, the second half of the
 * subnet (`DhcpScope.pools`). The default leaves out the broadcast address and the gateway's own.
 */
export function effectivePools(address: string | undefined, pools: DhcpPool[] | undefined): { pools: DhcpPool[]; isDefault: boolean } {
  if (pools?.length) return { pools, isDefault: false };
  const s = parseSubnet(address);
  if (!s || s.len > 30) return { pools: [], isDefault: true };
  const size = s.broadcast - s.network + 1;
  let start = s.network + size / 2;
  let end = s.broadcast - 1;
  if (start === s.address) start++;
  if (end === s.address) end--;
  return { pools: [{ start: formatIPv4(start), end: formatIPv4(end) }], isDefault: true };
}

/**
 * The pool of a local test network that a fixed address (a DHCP reservation, `Device.fixed_ip`) lies in, or
 * undefined: the gateway's warning `fixed_ip_in_pool` (#76). A reservation inside a pool is allowed, but while
 * another client holds the address the device gets a different one from the pool until that lease is released,
 * so the device and adopt dialogs show it as a hint that does not block. A network with DHCP off makes no
 * reservation: no pool then.
 */
export function reservationPool(
  lan: { address?: string; dhcp?: { enabled?: boolean; pools?: DhcpPool[] } } | undefined,
  ip: string | undefined,
): { pool: DhcpPool; isDefault: boolean } | undefined {
  const a = parseIPv4(ip);
  if (!lan?.dhcp || lan.dhcp.enabled === false || a === undefined) return undefined;
  const { pools, isDefault } = effectivePools(lan.address, lan.dhcp.pools);
  const pool = pools.find((p) => {
    const from = parseIPv4(p.start);
    const to = parseIPv4(p.end);
    return from !== undefined && to !== undefined && from <= a && a <= to;
  });
  return pool && { pool, isDefault };
}

/**
 * The hint of `reservationPool` in words, or undefined when the address lies in no pool: the one wording of
 * the pool hint in the web app (the device and adopt dialogs use it through `fixedIpHints`, #101). The
 * network's name, when given, says whose pool it is.
 */
export function reservationPoolHint(
  lan: (Parameters<typeof reservationPool>[0] & { name?: string }) | undefined,
  ip: string | undefined,
): string | undefined {
  const r = reservationPool(lan, ip);
  if (!r) return undefined;
  const pool = `${r.isDefault ? 'the default DHCP pool' : 'the DHCP pool'} ${r.pool.start}–${r.pool.end}${lan?.name ? ` of ${lan.name}` : ''}`;
  return `${ip!.trim()} lies in ${pool}. The gateway allows it, but while another client holds the address the device gets a different one from the pool, until that lease is released; an address outside the pool is safer.`;
}

/**
 * The next free tunnel address of a hub (plan M14: "proposes the next free tunnel address of the hub,
 * computed client-side"): the lowest host address of the hub's subnet that is not the gateway's own,
 * not the network or broadcast address and not taken by another client. Undefined when the subnet is full.
 */
export function nextFreeAddress(hubAddress: string | undefined, used: string[]): string | undefined {
  const s = parseSubnet(hubAddress);
  if (!s) return undefined;
  const taken = new Set(used.map(parseIPv4).filter((x): x is number => x !== undefined));
  taken.add(s.address);
  const first = s.len >= 31 ? s.network : s.network + 1;
  const last = s.len >= 31 ? s.broadcast : s.broadcast - 1;
  for (let ip = first; ip <= last; ip++) if (!taken.has(ip)) return formatIPv4(ip);
  return undefined;
}

// ---- the add-client form -----------------------------------------------------------------------

export type DnsMode = 'gateway' | 'none' | 'custom';

export interface ClientForm {
  name: string;
  address: string;
  /** Subnets behind the client, as typed: "10.50.0.0/24, 10.51.0.0/24". */
  clientNetworks: string;
  /** What the client may reach, as matrix endpoint keys (`network:<id>`, `uplink`). */
  reachable: string[];
  dns: DnsMode;
  dnsServers: string;
  keepalive: string;
  keyMode: 'generated' | 'provided';
  publicKey: string;
  presharedKey: boolean;
  exportOnce: boolean;
}

export function emptyClientForm(address = ''): ClientForm {
  return { name: '', address, clientNetworks: '', reachable: [], dns: 'gateway', dnsServers: '', keepalive: '25s', keyMode: 'generated', publicKey: '', presharedKey: false, exportOnce: false };
}

export type ClientField = 'name' | 'address' | 'clientNetworks' | 'dnsServers' | 'keepalive' | 'publicKey';

export interface ClientContext {
  /** The hub's own address with its prefix ("10.99.0.1/24"). */
  hubAddress: string;
  /** The tunnel addresses other clients of the hub have. */
  usedAddresses: string[];
  /** The names devices, clients and probes have (one namespace, plan §2.15), lower case. */
  takenNames: string[];
}

function list(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/** The checks of the add-client dialog, each under its field (plan §2.17 "Validation"). */
export function clientFormErrors(f: ClientForm, ctx: ClientContext): Partial<Record<ClientField, string>> {
  const e: Partial<Record<ClientField, string>> = {};
  const name = f.name.trim();
  if (!name) e.name = 'A name is required.';
  else if (!new RegExp(nameRegExp).test(name)) e.name = 'Letters, digits, dot, dash and underscore; it starts with a letter or digit (at most 63).';
  else if (ctx.takenNames.includes(name.toLowerCase())) e.name = `A device or client is already called ${name}.`;
  const hub = parseSubnet(ctx.hubAddress);
  const ip = parseIPv4(f.address);
  if (!f.address.trim()) e.address = 'A tunnel address is required.';
  else if (ip === undefined) e.address = 'An IPv4 address such as 10.99.0.2.';
  else if (hub && !inSubnet(ip, hub)) e.address = `It must lie in the hub's subnet ${subnetText(ctx.hubAddress)}.`;
  else if (hub && hub.len < 31 && (ip === hub.network || ip === hub.broadcast)) e.address = 'The network and broadcast addresses cannot be used.';
  else if (hub && ip === hub.address) e.address = "That is the gateway's own address in the tunnel.";
  else if (ctx.usedAddresses.some((u) => parseIPv4(u) === ip)) e.address = 'Another client has this address.';
  const nets = list(f.clientNetworks);
  const bad = nets.find((n) => !new RegExp(cidrRegExp).test(n) || !parseSubnet(n));
  if (bad) e.clientNetworks = `${bad} is no IPv4 network such as 10.50.0.0/24.`;
  else if (hub && nets.some((n) => inSubnet(parseSubnet(n)!.network, hub))) e.clientNetworks = "A client network cannot lie in the hub's own subnet.";
  if (f.dns === 'custom') {
    const servers = list(f.dnsServers);
    if (servers.length === 0) e.dnsServers = 'Name at least one DNS server.';
    else if (servers.some((s) => parseIPv4(s) === undefined)) e.dnsServers = 'IPv4 addresses, separated by commas.';
  }
  if (f.keepalive.trim() && !new RegExp(durationRegExp).test(f.keepalive.trim())) e.keepalive = 'A duration such as 25s; empty for the default of 25 s.';
  if (f.keyMode === 'provided' && !new RegExp(keyRegExp).test(f.publicKey.trim())) e.publicKey = "The client's WireGuard public key (44 characters of base64).";
  return e;
}

/** The client the form describes, as the configuration stores it. */
export function clientFromForm(f: ClientForm): WireGuardClient {
  const c: WireGuardClient = { name: f.name.trim(), address: f.address.trim() };
  const nets = list(f.clientNetworks);
  if (nets.length) c.client_networks = nets;
  if (f.reachable.length) c.reachable = f.reachable.map(endpointOf);
  if (f.dns === 'none') c.dns = { mode: 'none' };
  if (f.dns === 'custom') c.dns = { mode: 'custom', servers: list(f.dnsServers) };
  if (f.keepalive.trim() && f.keepalive.trim() !== '25s') c.keepalive = f.keepalive.trim();
  c.key = f.keyMode === 'provided' ? { mode: 'provided', public_key: f.publicKey.trim() } : { mode: 'generated' };
  if (f.presharedKey) c.key.preshared_key = true;
  if (f.exportOnce && f.keyMode === 'generated') c.key.export_once = true;
  return c;
}

// ---- client actions (plan §2.2.1: enable/disable, rotate keys, delete; P3-M14-01) -----------------

/**
 * The client enabled or disabled; enabled is the default, so it carries no `enabled` then, unless
 * `base` (the client as the configuration has it) says `enabled: true`, which an enable keeps, so
 * that a disable and an enable leave no change.
 */
export function withEnabled(c: WireGuardClient, enabled: boolean, base?: WireGuardClient): WireGuardClient {
  const { enabled: _was, ...rest } = c;
  if (!enabled) return { ...rest, enabled: false };
  return base?.enabled === true ? { ...rest, enabled: true } : rest;
}

/** Whether the gateway generates the client's keys (and so rotates them itself). */
export function generatesKeys(c: WireGuardClient): boolean {
  return (c.key?.mode ?? 'generated') === 'generated';
}

/**
 * The client with new keys: with generated keys the next key generation (the gateway makes a new key
 * pair, and a new preshared key, when the revision is applied); with the client's own key its new
 * public key.
 */
export function withRotatedKeys(c: WireGuardClient, publicKey?: string): WireGuardClient {
  const key = { ...(c.key ?? {}) };
  if (generatesKeys(c)) key.generation = (key.generation ?? 1) + 1;
  else key.public_key = (publicKey ?? '').trim();
  return { ...c, key };
}

/** The problem of a new public key for a client with its own key, or undefined. */
export function rotatedKeyError(c: WireGuardClient, publicKey: string): string | undefined {
  const k = publicKey.trim();
  if (!new RegExp(keyRegExp).test(k)) return "The client's new WireGuard public key (44 characters of base64).";
  if (k === c.key?.public_key) return 'This is the key the client has now.';
  return undefined;
}

export interface ReachChoice {
  key: string;
  label: string;
}

/**
 * What a client of `hub` may reach (plan §2.2.1 "Create clients"): the internet through the uplink,
 * the other networks, and the client networks of every other client that has some, of this hub and
 * of the others (`clients(hub)`: a hub's clients as the draft has them; `except`: the client itself).
 */
export function reachChoices(hub: string, networks: NetworkView[], clients: (hub: string) => Record<string, WireGuardClient>, except?: string): ReachChoice[] {
  const out: ReachChoice[] = [{ key: 'uplink', label: 'Internet through the uplink (full tunnel)' }];
  for (const n of networks) {
    if (n.id === hub) continue;
    const c = n.config as { name: string; address: string };
    out.push({ key: `network:${n.id}`, label: `${c.name} (${subnetText(c.address)})` });
  }
  for (const n of networks) {
    const wg = n.config as WireGuardNetwork;
    if (wg.type !== 'wireguard' || wg.kind === 'link') continue;
    for (const [id, c] of Object.entries(clients(n.id))) {
      if (id === except || !c.client_networks?.length) continue;
      out.push({ key: `client:${id}`, label: `Client networks of ${c.name} (${c.client_networks.join(', ')})` });
    }
  }
  return out;
}

/** The keys of a client in words, for the changes: "generation 2", "public key qU1BFDFD…". */
export function keysText(c: WireGuardClient): string {
  if (generatesKeys(c)) return `generation ${c.key?.generation ?? 1}`;
  return c.key?.public_key ? `public key ${c.key.public_key.slice(0, 8)}…` : 'public key —';
}

/** The form of a client of the draft, to edit it again (the reverse of `clientFromForm`). */
export function formFromClient(c: WireGuardClient): ClientForm {
  return {
    name: c.name,
    address: c.address,
    clientNetworks: (c.client_networks ?? []).join(', '),
    reachable: (c.reachable ?? []).map(endpointKey),
    dns: c.dns?.mode === 'none' ? 'none' : c.dns?.mode === 'custom' ? 'custom' : 'gateway',
    dnsServers: (c.dns?.servers ?? []).join(', '),
    keepalive: c.keepalive ?? '25s',
    keyMode: c.key?.mode === 'provided' ? 'provided' : 'generated',
    publicKey: c.key?.public_key ?? '',
    presharedKey: !!c.key?.preshared_key,
    exportOnce: !!c.key?.export_once,
  };
}

/**
 * The field of the add-client form a message of the gateway belongs to, by the first part of its
 * pointer below the client (`address`, `client_networks`, …); undefined for one about the client as a
 * whole or a part the form does not have.
 */
export function clientFieldOf(pointer: string): ClientField | undefined {
  const fields: Record<string, ClientField> = { name: 'name', address: 'address', client_networks: 'clientNetworks', dns: 'dnsServers', keepalive: 'keepalive', key: 'publicKey' };
  return fields[pointer];
}

// ---- the access matrix -------------------------------------------------------------------------

/** A matrix endpoint as a key: `uplink`, `management`, `network:<id>`, `client:<id>`. */
export function endpointKey(ep: MatrixEndpoint): string {
  if (ep.uplink) return 'uplink';
  if (ep.management) return 'management';
  if (ep.network) return `network:${ep.network}`;
  if (ep.client) return `client:${ep.client}`;
  return '';
}

export function endpointOf(key: string): MatrixEndpoint {
  if (key === 'uplink') return { uplink: true };
  if (key === 'management') return { management: true };
  const [kind, id] = key.split(':');
  return kind === 'client' ? { client: id! } : { network: id! };
}

/**
 * An endpoint of an entry as a key with the network's UUID, whether the entry names it by UUID or by
 * name (the API accepts either, plan §2.15).
 */
export function entryKey(ep: MatrixEndpoint, networks: NetworkView[]): string {
  if (ep.network) {
    const n = networks.find((x) => x.id === ep.network || (x.config as { name?: string }).name?.toLowerCase() === ep.network!.toLowerCase());
    return `network:${n?.id ?? ep.network}`;
  }
  return endpointKey(ep);
}

/**
 * The policy without an entry (`AccessMatrix`): a local test network may reach the uplink, everything
 * else is denied.
 */
export function defaultPolicy(from: string, to: string, networks: NetworkView[]): MatrixEntryPolicy {
  if (to !== 'uplink' || !from.startsWith('network:')) return 'deny';
  const n = networks.find((x) => `network:${x.id}` === from);
  return n?.config.type === 'lan' ? 'allow' : 'deny';
}

export interface Cell {
  policy: MatrixEntryPolicy;
  /** The configuration has an entry for the pair (else the default applies). */
  explicit: boolean;
}

export function cellOf(entries: MatrixEntry[], from: string, to: string, networks: NetworkView[]): Cell {
  const e = entries.find((x) => entryKey(x.from, networks) === from && entryKey(x.to, networks) === to);
  return e ? { policy: e.policy, explicit: true } : { policy: defaultPolicy(from, to, networks), explicit: false };
}

/**
 * The entries after a click on a cell: the policy flips; an entry that would only say what the default
 * says is removed instead, so the configuration keeps the entries that matter.
 */
export function toggleCell(entries: MatrixEntry[], from: string, to: string, networks: NetworkView[]): MatrixEntry[] {
  const now = cellOf(entries, from, to, networks).policy;
  const next: MatrixEntryPolicy = now === 'allow' ? 'deny' : 'allow';
  const rest = entries.filter((x) => !(entryKey(x.from, networks) === from && entryKey(x.to, networks) === to));
  if (next === defaultPolicy(from, to, networks)) return rest;
  return [...rest, { from: endpointOf(from), to: endpointOf(to), policy: next }];
}

export interface EndpointNames {
  network?: (id: string) => string | undefined;
  client?: (id: string) => string | undefined;
}

/** "Internet", "Management", "IoT", "client rA". */
export function describeEndpoint(key: string, names: EndpointNames = {}): string {
  if (key === 'uplink') return 'Internet';
  if (key === 'management') return 'Management';
  const [kind, id] = key.split(':');
  if (kind === 'client') return `client ${names.client?.(id!) ?? id}`;
  return names.network?.(id!) ?? id!;
}

const policyWord = (p: MatrixEntryPolicy) => (p === 'allow' ? 'allowed' : 'blocked');

/** The pairs whose policy differs between two lists of entries, in words ("IoT → Internet: allowed → blocked"). */
export function matrixChanges(before: MatrixEntry[], after: MatrixEntry[], networks: NetworkView[], names: EndpointNames = {}): FieldChange[] {
  const pairs = new Map<string, [string, string]>();
  for (const e of [...before, ...after]) {
    const f = entryKey(e.from, networks);
    const t = entryKey(e.to, networks);
    pairs.set(`${f}|${t}`, [f, t]);
  }
  const out: FieldChange[] = [];
  for (const [f, t] of pairs.values()) {
    const a = cellOf(before, f, t, networks).policy;
    const b = cellOf(after, f, t, networks).policy;
    if (a !== b) out.push({ field: `${describeEndpoint(f, names)} → ${describeEndpoint(t, names)}`, from: policyWord(a), to: policyWord(b) });
  }
  return out;
}

/** The WireGuard network of a view, or undefined for a local one. */
export function wireguardOf(n: NetworkView | undefined): WireGuardNetwork | undefined {
  return n?.config.type === 'wireguard' ? (n.config as WireGuardNetwork) : undefined;
}
