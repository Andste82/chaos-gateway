// The network form (plan §2.2, §2.2.1, §2.7, §2.6; P3-M14-01): a local test network with its subnet,
// its gateway address, its ports (one or several physical interfaces of its bridge), DHCP and DNS, its
// downstream routes; a WireGuard hub or link with its role, tunnel subnet, listen port, MTU, endpoint and,
// for a link, its peer and static routes. The form is text as typed; `networkFormErrors` checks it field by
// field (plan §2.17 "Validation"), `networkFromForm` builds the network the configuration stores, keeping
// what the form does not edit (a hub's clients, a key generation), and `networkFieldOf` puts a message of
// the gateway under the field it is about.
import type {
  DhcpCustomOption,
  DhcpPool,
  DnsStaticEntry,
  DownstreamRoute,
  HostInterface,
  InterfaceRef,
  LanNetwork,
  Network,
  WireGuardLinkPeer,
  WireGuardNetwork,
} from '../api/generated/model';
import {
  completeSetupBodyConfigurationOneNetworksOneDhcpLeaseTimeOneRegExp as durationRegExp,
  completeSetupBodyConfigurationOneNetworksOneDnsStaticEntriesItemNameRegExp as hostnameRegExp,
  completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp,
  completeSetupBodyConfigurationOneNetworksOneRoutesItemDestinationRegExp as cidrRegExp,
  completeSetupBodyConfigurationOneNetworksTwoEndpointOneRegExp as endpointRegExp,
  completeSetupBodyConfigurationOneNetworksTwoPeerOneKeyPublicKeyOneRegExp as keyRegExp,
} from '../api/generated/zod/system/system';
import { cloneJSON } from './mergePatch';
import { formatIPv4, hostBitsError, inSubnet, parseIPv4, parseSubnet, reservedRangeOf, subnetText, type Subnet } from './networks';
import { seconds } from './routingForm';

/** What a network is: a local test network, a WireGuard hub or a WireGuard link. */
export type NetworkKind = 'lan' | 'hub' | 'link';

export interface NetworkForm {
  kind: NetworkKind;
  name: string;
  description: string;
  /** The gateway's address with the prefix length: "10.30.0.1/24" (a link: "10.255.0.0/31"). */
  address: string;
  /** Masquerade towards the uplink. */
  nat: boolean;
  // ---- a local test network
  /** The ports of the bridge by interface name (or MAC, for a port the configuration names by MAC only). */
  ports: string[];
  dhcpEnabled: boolean;
  /** Empty for the default of 1 h. */
  leaseTime: string;
  /** "10.30.0.100-10.30.0.199, …"; empty for the default, the second half of the subnet. */
  pools: string;
  router: string;
  dhcpDns: string;
  domain: string;
  ntp: string;
  /** One custom option per line: "code value". */
  customOptions: string;
  redirectDns: boolean;
  blockDot: boolean;
  /** One static entry per line: "name address, address". */
  staticEntries: string;
  /** A local network: one downstream route per line, "10.40.0.0/24 via 10.30.0.254"; a link: its static routes, "10.60.0.0/24, …". */
  routes: string;
  // ---- a WireGuard network
  role: 'test' | 'management';
  listenPort: string;
  /** Empty for the default of 1420. */
  mtu: string;
  /** Empty for the default: the uplink address and the listen port. */
  endpoint: string;
  // ---- the peer of a link
  peerAddress: string;
  peerEndpoint: string;
  peerKeepalive: string;
  peerKeyMode: 'generated' | 'provided';
  peerPublicKey: string;
  peerPresharedKey: boolean;
  peerEnabled: boolean;
}

export type NetworkField =
  | 'name'
  | 'description'
  | 'address'
  | 'ports'
  | 'leaseTime'
  | 'pools'
  | 'router'
  | 'dhcpDns'
  | 'domain'
  | 'ntp'
  | 'customOptions'
  | 'staticEntries'
  | 'routes'
  | 'listenPort'
  | 'mtu'
  | 'endpoint'
  | 'peerAddress'
  | 'peerEndpoint'
  | 'peerKeepalive'
  | 'peerPublicKey';

export function emptyNetworkForm(kind: NetworkKind, address = '', listenPort = ''): NetworkForm {
  return {
    kind,
    name: '',
    description: '',
    address,
    nat: true,
    ports: [],
    dhcpEnabled: true,
    leaseTime: '',
    pools: '',
    router: '',
    dhcpDns: '',
    domain: '',
    ntp: '',
    customOptions: '',
    redirectDns: false,
    blockDot: false,
    staticEntries: '',
    routes: '',
    role: 'test',
    listenPort,
    mtu: '',
    endpoint: '',
    peerAddress: kind === 'link' ? peerOf(address) : '',
    peerEndpoint: '',
    peerKeepalive: '',
    peerKeyMode: 'generated',
    peerPublicKey: '',
    peerPresharedKey: false,
    peerEnabled: true,
  };
}

/** The other address of a /31 transfer network: the peer of "10.255.0.0/31" is 10.255.0.1. */
export function peerOf(address: string): string {
  const s = parseSubnet(address);
  if (!s || s.len !== 31) return '';
  return formatIPv4(s.address === s.network ? s.broadcast : s.network);
}

/** The kind of a network of the configuration. */
export function kindOf(n: Network): NetworkKind {
  return n.type === 'lan' ? 'lan' : (n as WireGuardNetwork).kind;
}

/** The key of a port: its name, or its MAC for a port the configuration names by MAC only. */
export function portKey(i: InterfaceRef): string {
  return i.name ?? i.mac ?? '';
}

/**
 * The key under which the form holds a host interface as a port: its MAC when the form or the network as
 * it was has the port by MAC (the configuration names it by MAC only), else its name. The port picker
 * ticks and unticks it by that key, so a MAC-only port is shown as chosen, is not added a second time by
 * name, and ticked again after it was unticked keeps its reference by MAC.
 */
export function hostPortKey(h: Pick<HostInterface, 'name' | 'mac'>, ports: string[], had: string[] = []): string {
  if (!h.mac || ports.includes(h.name)) return h.name;
  return ports.includes(h.mac) || (had.includes(h.mac) && !had.includes(h.name)) ? h.mac : h.name;
}

/** The fields under "Advanced" in the network form, by kind (a link's static routes stand above it). */
export function advancedFields(kind: NetworkKind): NetworkField[] {
  return kind === 'lan'
    ? ['description', 'router', 'dhcpDns', 'domain', 'ntp', 'customOptions', 'staticEntries', 'routes']
    : kind === 'link'
      ? ['description', 'endpoint', 'mtu', 'peerKeepalive', 'peerPublicKey']
      : ['description', 'endpoint', 'mtu'];
}

/** Words or lines, as typed: "a, b c" → ["a", "b", "c"]. */
export function list(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function lines(text: string): string[] {
  return text
    .split('\n')
    .map((x) => x.trim())
    .filter(Boolean);
}

/** The form of a network of the configuration, to edit it. */
export function formFromNetwork(n: Network): NetworkForm {
  const kind = kindOf(n);
  const f = emptyNetworkForm(kind, n.address);
  f.name = n.name;
  f.description = n.description ?? '';
  f.nat = n.nat !== false;
  if (n.type === 'lan') {
    const lan = n as LanNetwork;
    f.ports = lan.interfaces.map(portKey);
    f.dhcpEnabled = lan.dhcp?.enabled !== false;
    f.leaseTime = lan.dhcp?.lease_time ?? '';
    f.pools = (lan.dhcp?.pools ?? []).map((p) => `${p.start}-${p.end}`).join(', ');
    const o = lan.dhcp?.options;
    f.router = o?.router ?? '';
    f.dhcpDns = (o?.dns_servers ?? []).join(', ');
    f.domain = o?.domain ?? '';
    f.ntp = (o?.ntp_servers ?? []).join(', ');
    f.customOptions = (o?.custom ?? []).map((c) => `${c.code} ${c.value}`).join('\n');
    f.redirectDns = !!lan.dns?.redirect_hardcoded;
    f.blockDot = !!lan.dns?.block_dot;
    f.staticEntries = (lan.dns?.static_entries ?? []).map((e) => `${e.name} ${e.addresses.join(', ')}`).join('\n');
    f.routes = (lan.routes ?? []).map((r) => `${r.destination} via ${r.via}`).join('\n');
    return f;
  }
  const wg = n as WireGuardNetwork;
  f.role = wg.role === 'management' ? 'management' : 'test';
  f.listenPort = String(wg.listen_port);
  f.mtu = wg.mtu !== undefined ? String(wg.mtu) : '';
  f.endpoint = wg.endpoint ?? '';
  if (wg.kind === 'link') {
    f.routes = (wg.routes ?? []).join(', ');
    f.peerAddress = wg.peer?.address ?? '';
    f.peerEndpoint = wg.peer?.endpoint ?? '';
    f.peerKeepalive = wg.peer?.keepalive ?? '';
    f.peerKeyMode = wg.peer?.key?.mode === 'provided' ? 'provided' : 'generated';
    f.peerPublicKey = wg.peer?.key?.mode === 'provided' ? (wg.peer.key.public_key ?? '') : '';
    f.peerPresharedKey = !!wg.peer?.key?.preshared_key;
    f.peerEnabled = wg.peer?.enabled !== false;
  }
  return f;
}

/** "10.30.0.100-10.30.0.199, 10.30.0.220 - 10.30.0.229" → the pools, or the first part that is none. */
function parsePools(text: string): { pools: DhcpPool[]; bad?: string } {
  const pools: DhcpPool[] = [];
  for (const part of text.split(',').map((x) => x.trim()).filter(Boolean)) {
    const m = /^(\S+)\s*[-–]\s*(\S+)$/.exec(part);
    if (!m || parseIPv4(m[1]) === undefined || parseIPv4(m[2]) === undefined) return { pools, bad: part };
    pools.push({ start: m[1]!, end: m[2]! });
  }
  return { pools };
}

/** "10.40.0.0/24 via 10.30.0.254" per line → the downstream routes, or the first line that is none. */
function parseRoutes(text: string): { routes: DownstreamRoute[]; bad?: string } {
  const routes: DownstreamRoute[] = [];
  for (const line of lines(text)) {
    const m = /^(\S+)\s+(?:via\s+)?(\S+)$/.exec(line);
    if (!m) return { routes, bad: line };
    routes.push({ destination: m[1]!, via: m[2]! });
  }
  return { routes };
}

/** "name 10.30.0.5, 10.30.0.6" per line → the static DNS entries, or the first line that is none. */
function parseStatic(text: string): { entries: DnsStaticEntry[]; bad?: string } {
  const entries: DnsStaticEntry[] = [];
  for (const line of lines(text)) {
    const [name, ...rest] = list(line);
    if (!name || rest.length === 0) return { entries, bad: line };
    entries.push({ name, addresses: rest });
  }
  return { entries };
}

/** "66 tftp.lab" per line → the custom DHCP options, or the first line that is none. */
export function parseCustom(text: string): { options: DhcpCustomOption[]; bad?: string } {
  const options: DhcpCustomOption[] = [];
  for (const line of lines(text)) {
    const m = /^(\d+)\s+(.+)$/.exec(line);
    if (!m) return { options, bad: line };
    options.push({ code: Number(m[1]), value: m[2]!.trim() });
  }
  return { options };
}

/** Another network of the draft, as the checks of the form need it. */
export interface OtherNetwork {
  name: string;
  address: string;
  kind: NetworkKind;
  listenPort?: number;
  /** The ports of a local network, by key. */
  ports: string[];
}

export interface NetworkContext {
  /** The other networks of the draft (not the one edited). */
  others: OtherNetwork[];
  /** The interfaces of the uplink and the management network, by name or MAC: no test network may have them. */
  reserved: string[];
}

function overlaps(a: Subnet, b: Subnet): boolean {
  return a.network <= b.broadcast && b.network <= a.broadcast;
}

const isDuration = (s: string) => new RegExp(durationRegExp).test(s);
/** A unicast IPv4 address, as the gateway takes one for a router, a DNS or an NTP server. */
export const isUnicast = (s: string) => {
  const ip = parseIPv4(s);
  return ip !== undefined && ip !== 0 && ip >>> 28 !== 14 && ip !== 0xffffffff;
};

/** The DHCP option codes the gateway sets itself; no custom option may have them (the gateway's `reserved_option`). */
export const reservedDHCPOptions = [1, 3, 6, 12, 15, 28, 42, 50, 51, 53, 54, 55, 58, 59, 61, 82, 255];

/** The first problem of DHCP pools in the subnet `s` (the network's address `address`), or undefined. */
function poolsError(pools: DhcpPool[], s: Subnet, address: string): string | undefined {
  for (const [i, p] of pools.entries()) {
    const a = parseIPv4(p.start);
    const b = parseIPv4(p.end);
    if (a === undefined || b === undefined) return `${p.start}-${p.end} is no range such as 10.30.0.100-10.30.0.199.`;
    if (b < a) return `The end ${p.end} is before the start ${p.start}.`;
    if (!inSubnet(a, s) || !inSubnet(b, s) || a === s.network || b === s.broadcast) return `${p.start}-${p.end} must lie inside ${subnetText(address)}, without its network and broadcast address.`;
    if (s.address >= a && s.address <= b) return `${p.start}-${p.end} contains the gateway address ${formatIPv4(s.address)}.`;
    if (pools.slice(0, i).some((q) => parseIPv4(q.start)! <= b && a <= parseIPv4(q.end)!)) return `${p.start}-${p.end} overlaps another pool.`;
  }
  return undefined;
}

/** The checks of the network form, each under its field (plan §2.17 "Validation"). */
export function networkFormErrors(f: NetworkForm, ctx: NetworkContext): Partial<Record<NetworkField, string>> {
  const e: Partial<Record<NetworkField, string>> = {};
  const name = f.name.trim();
  if (!name) e.name = 'A name is required.';
  else if (!new RegExp(nameRegExp).test(name)) e.name = 'Letters, digits, dot, dash and underscore; it starts with a letter or digit (at most 63).';
  else if (ctx.others.some((o) => o.name.toLowerCase() === name.toLowerCase())) e.name = `A network is already called ${name}.`;
  if (f.description.length > 500) e.description = 'At most 500 characters.';

  const s = parseSubnet(f.address.trim());
  if (!f.address.trim()) e.address = f.kind === 'link' ? 'The gateway address in the /31 transfer network is required.' : 'The gateway address with the prefix length is required.';
  else if (!new RegExp(cidrRegExp).test(f.address.trim()) || !s) e.address = 'An IPv4 address with its prefix length, such as 10.30.0.1/24.';
  else if (f.kind === 'link' && s.len !== 31) e.address = 'A link needs a /31 transfer network, such as 10.255.0.0/31.';
  else if (f.kind !== 'link' && s.len > 30) e.address = `A ${f.kind === 'hub' ? 'hub' : 'test network'} needs a prefix length of at most 30.`;
  else if (f.kind !== 'link' && (s.address === s.network || s.address === s.broadcast)) e.address = 'That is the network or broadcast address; give the gateway a host address.';
  else if (reservedRangeOf(f.address)) e.address = `${subnetText(f.address)} overlaps the reserved range ${reservedRangeOf(f.address)}.`;
  else {
    const other = ctx.others.find((o) => {
      const os = parseSubnet(o.address);
      return os && overlaps(s, os);
    });
    if (other) e.address = `${subnetText(f.address)} overlaps ${other.name} (${subnetText(other.address)}).`;
  }

  if (f.kind === 'lan') {
    if (f.ports.length === 0) e.ports = 'Choose at least one port: the physical interface the devices are connected to.';
    else {
      const reserved = f.ports.find((p) => ctx.reserved.includes(p));
      const taken = f.ports.map((p) => [p, ctx.others.find((o) => o.ports.includes(p))] as const).find(([, o]) => o);
      if (reserved) e.ports = `${reserved} is the uplink or the management interface; the operating system owns it.`;
      else if (taken) e.ports = `${taken[0]} is already a port of ${taken[1]!.name}.`;
    }
    if (f.dhcpEnabled) {
      const lease = f.leaseTime.trim();
      if (lease && (!isDuration(lease) || seconds(lease)! < 1)) e.leaseTime = 'A duration of at least 1s, such as 1h or 30m; empty for the default of 1 h.';
      const { pools, bad } = parsePools(f.pools);
      if (bad) e.pools = `${bad} is no range such as 10.30.0.100-10.30.0.199.`;
      else if (s && !e.address) {
        const p = poolsError(pools, s, f.address);
        if (p) e.pools = p;
      }
      if (f.router.trim() && !isUnicast(f.router.trim())) e.router = 'An IPv4 address; empty for the gateway address.';
      if (list(f.dhcpDns).some((x) => !isUnicast(x))) e.dhcpDns = 'IPv4 addresses, separated by commas; empty for the gateway (the DNS proxy).';
      if (f.domain.trim().length > 253 || (f.domain.trim() && !new RegExp(hostnameRegExp).test(f.domain.trim()))) e.domain = 'A domain name such as lab.example.';
      if (list(f.ntp).some((x) => !isUnicast(x))) e.ntp = 'IPv4 addresses, separated by commas.';
      const custom = parseCustom(f.customOptions);
      if (custom.bad) e.customOptions = `${custom.bad}: one option per line, its code and its value, such as 66 tftp.lab.`;
      else {
        const codes = custom.options.map((o) => o.code);
        const out = custom.options.find((o) => o.code < 1 || o.code > 254);
        const reserved = custom.options.find((o) => reservedDHCPOptions.includes(o.code));
        if (out) e.customOptions = `Option ${out.code}: the code is from 1 to 254.`;
        else if (reserved) e.customOptions = `Option ${reserved.code} is set by the gateway (router, lease time, DNS and NTP servers, …); it cannot be a custom option.`;
        else if (codes.some((c, i) => codes.indexOf(c) !== i)) e.customOptions = 'Each option code once.';
      }
    }
    const st = parseStatic(f.staticEntries);
    if (st.bad) e.staticEntries = `${st.bad}: one entry per line, a name and its addresses, such as broker.lab 10.30.0.5.`;
    else {
      const bad = st.entries.find((x) => !new RegExp(hostnameRegExp).test(x.name) || x.addresses.some((a) => parseIPv4(a) === undefined));
      const names = st.entries.map((x) => x.name.toLowerCase());
      if (bad) e.staticEntries = `${bad.name}: a host name and IPv4 addresses.`;
      else if (names.some((n, i) => names.indexOf(n) !== i)) e.staticEntries = 'Each name once.';
    }
    const rt = parseRoutes(f.routes);
    if (rt.bad) e.routes = `${rt.bad}: one route per line, such as 10.40.0.0/24 via 10.30.0.254.`;
    else {
      for (const r of rt.routes) {
        const via = parseIPv4(r.via);
        if (!new RegExp(cidrRegExp).test(r.destination)) e.routes = `${r.destination} is no IPv4 network such as 10.40.0.0/24.`;
        else if (hostBitsError(r.destination)) e.routes = hostBitsError(r.destination);
        else if (reservedRangeOf(r.destination)) e.routes = `${r.destination} overlaps the reserved range ${reservedRangeOf(r.destination)}.`;
        else if (via === undefined) e.routes = `${r.via} is no IPv4 address.`;
        else if (s && !e.address && (!inSubnet(via, s) || via === s.address || via === s.network || via === s.broadcast))
          e.routes = `The router ${r.via} must be a host of ${subnetText(f.address)} other than the gateway.`;
        if (e.routes) break;
      }
    }
    return e;
  }

  const port = Number(f.listenPort.trim());
  if (!f.listenPort.trim()) e.listenPort = 'The UDP port the gateway listens on is required.';
  else if (!/^\d+$/.test(f.listenPort.trim()) || port < 1 || port > 65535) e.listenPort = 'A port from 1 to 65535.';
  else {
    const other = ctx.others.find((o) => o.listenPort === port);
    if (other) e.listenPort = `UDP port ${port} is already used by ${other.name}.`;
  }
  if (f.mtu.trim() && (!/^\d+$/.test(f.mtu.trim()) || Number(f.mtu) < 1280 || Number(f.mtu) > 1500)) e.mtu = 'From 1280 to 1500; empty for the default of 1420.';
  if (f.endpoint.trim() && !new RegExp(endpointRegExp).test(f.endpoint.trim())) e.endpoint = 'Host and port, such as gw.example.org:51820; empty for the uplink address.';
  if (f.kind === 'link') {
    const peer = parseIPv4(f.peerAddress.trim());
    if (!f.peerAddress.trim()) e.peerAddress = "The remote side's address in the transfer network is required.";
    else if (peer === undefined) e.peerAddress = 'An IPv4 address such as 10.255.0.1.';
    else if (s && !e.address && !inSubnet(peer, s)) e.peerAddress = `It must lie in the transfer network ${subnetText(f.address)}.`;
    else if (s && peer === s.address) e.peerAddress = "That is the gateway's own address.";
    if (f.peerEndpoint.trim() && !new RegExp(endpointRegExp).test(f.peerEndpoint.trim())) e.peerEndpoint = 'Host and port, such as 203.0.113.40:51821; empty when the remote side connects.';
    if (f.peerKeepalive.trim() && !isDuration(f.peerKeepalive.trim())) e.peerKeepalive = 'A duration such as 25s; empty for the default of 25 s.';
    if (f.peerKeyMode === 'provided' && !new RegExp(keyRegExp).test(f.peerPublicKey.trim())) e.peerPublicKey = "The remote side's WireGuard public key (44 characters of base64).";
    for (const r of list(f.routes)) {
      if (!new RegExp(cidrRegExp).test(r)) e.routes = `${r} is no IPv4 network such as 10.60.0.0/24.`;
      else if (hostBitsError(r)) e.routes = hostBitsError(r);
      else if (reservedRangeOf(r)) e.routes = `${r} overlaps the reserved range ${reservedRangeOf(r)}.`;
      if (e.routes) break;
    }
  }
  return e;
}

/** Sets a member, or removes it when it is undefined (so a field emptied in the form leaves the configuration). */
function put<T extends object, K extends keyof T>(o: T, k: K, v: T[K] | undefined) {
  if (v === undefined) delete o[k];
  else o[k] = v;
}

/** An object without the members that are undefined, or undefined when none is left. */
function compact<T extends object>(o: T): T | undefined {
  for (const k of Object.keys(o) as (keyof T)[]) if (o[k] === undefined) delete o[k];
  return Object.keys(o).length ? o : undefined;
}

const text = (s: string) => s.trim() || undefined;
const nonEmpty = <T>(xs: T[]) => (xs.length ? xs : undefined);

/**
 * The network the form describes, as the configuration stores it. `before` is the network as the draft
 * has it: what the form does not edit stays (a hub's clients, the key generation of a link's peer, an
 * option the form has no field for), and a default is written only where `before` had it already.
 * `hosts` names the MAC of a new port, like the setup does.
 */
export function networkFromForm(f: NetworkForm, before?: Network, hosts: HostInterface[] = []): Network {
  const b = before && kindOf(before) === f.kind ? cloneJSON(before) : undefined;
  if (f.kind === 'lan') {
    const n = (b as LanNetwork | undefined) ?? ({ type: 'lan', name: '', address: '', interfaces: [] } as LanNetwork);
    n.name = f.name.trim();
    put(n, 'description', text(f.description));
    n.address = f.address.trim();
    n.interfaces = f.ports.map((key) => {
      const had = (b as LanNetwork | undefined)?.interfaces.find((i) => portKey(i) === key);
      if (had) return had;
      const h = hosts.find((x) => x.name === key || x.mac === key);
      return h ? { name: h.name, mac: h.mac } : { name: key };
    });
    put(n, 'nat', f.nat ? (b?.nat === true ? true : undefined) : false);
    const dhcp = { ...(n.dhcp ?? {}) };
    put(dhcp, 'enabled', f.dhcpEnabled ? (n.dhcp?.enabled === true ? true : undefined) : false);
    if (f.dhcpEnabled) {
      put(dhcp, 'lease_time', text(f.leaseTime));
      put(dhcp, 'pools', nonEmpty(parsePools(f.pools).pools));
      const o = { ...(dhcp.options ?? {}) };
      put(o, 'router', text(f.router));
      put(o, 'dns_servers', nonEmpty(list(f.dhcpDns)));
      put(o, 'domain', text(f.domain));
      put(o, 'ntp_servers', nonEmpty(list(f.ntp)));
      put(o, 'custom', nonEmpty(parseCustom(f.customOptions).options));
      put(dhcp, 'options', compact(o));
    } else if (dhcp.pools?.length) {
      // DHCP is off and the form hides the pools, but the gateway still checks them: pools that no longer
      // fit the subnet (the address changed) go, and the default pools follow the subnet when DHCP is on again
      const s = parseSubnet(n.address);
      if (!s || poolsError(dhcp.pools, s, n.address)) delete dhcp.pools;
    }
    put(n, 'dhcp', compact(dhcp));
    const dns = { ...(n.dns ?? {}) };
    put(dns, 'redirect_hardcoded', f.redirectDns ? true : n.dns?.redirect_hardcoded === false ? false : undefined);
    put(dns, 'block_dot', f.blockDot ? true : n.dns?.block_dot === false ? false : undefined);
    put(dns, 'static_entries', nonEmpty(parseStatic(f.staticEntries).entries));
    put(n, 'dns', compact(dns));
    put(n, 'routes', nonEmpty(parseRoutes(f.routes).routes));
    return n;
  }
  const n = (b as WireGuardNetwork | undefined) ?? ({ type: 'wireguard', kind: f.kind, name: '', address: '', listen_port: 0 } as WireGuardNetwork);
  n.name = f.name.trim();
  put(n, 'description', text(f.description));
  n.address = f.address.trim();
  put(n, 'role', f.role === 'management' ? 'management' : b && (b as WireGuardNetwork).role === 'test' ? 'test' : undefined);
  n.listen_port = Number(f.listenPort.trim());
  put(n, 'mtu', f.mtu.trim() ? Number(f.mtu.trim()) : undefined);
  put(n, 'endpoint', text(f.endpoint));
  put(n, 'nat', f.nat ? (b?.nat === true ? true : undefined) : false);
  if (f.kind === 'link') {
    const p: WireGuardLinkPeer = { ...(n.peer ?? { address: '' }) };
    p.address = f.peerAddress.trim();
    put(p, 'endpoint', text(f.peerEndpoint));
    put(p, 'keepalive', text(f.peerKeepalive));
    put(p, 'enabled', f.peerEnabled ? (n.peer?.enabled === true ? true : undefined) : false);
    const key = { ...(p.key ?? {}) };
    if (f.peerKeyMode === 'provided') {
      key.mode = 'provided';
      key.public_key = f.peerPublicKey.trim();
      delete key.generation;
      delete key.export_once;
    } else {
      key.mode = 'generated';
      // the server sets the public key of a generated pair
      if (p.key?.mode === 'provided') delete key.public_key;
    }
    put(key, 'preshared_key', f.peerPresharedKey ? true : undefined);
    p.key = key;
    n.peer = p;
    put(n, 'routes', nonEmpty(list(f.routes)));
  }
  return n;
}

/**
 * The field of the network form a message of the gateway belongs to, by its pointer below the network
 * (`address`, `dhcp/pools/0/end`, `peer/key/public_key`, …); undefined for one about the network as a
 * whole or a part the form does not have (the clients of a hub have their own dialog).
 */
export function networkFieldOf(pointer: string): NetworkField | undefined {
  const p = pointer.split('/');
  switch (p[0]) {
    case 'name':
    case 'description':
    case 'address':
    case 'endpoint':
    case 'mtu':
    case 'routes':
      return p[0] as NetworkField;
    case 'interfaces':
      return 'ports';
    case 'listen_port':
      return 'listenPort';
    case 'dhcp':
      if (p[1] === 'pools') return 'pools';
      if (p[1] === 'lease_time') return 'leaseTime';
      if (p[1] === 'options') return ({ router: 'router', dns_servers: 'dhcpDns', domain: 'domain', ntp_servers: 'ntp', custom: 'customOptions' } as const)[p[2] as 'router'];
      return undefined;
    case 'dns':
      return p[1] === 'static_entries' ? 'staticEntries' : undefined;
    case 'peer':
      if (p[1] === 'endpoint') return 'peerEndpoint';
      if (p[1] === 'keepalive') return 'peerKeepalive';
      if (p[1] === 'key') return 'peerPublicKey';
      return 'peerAddress';
    default:
      return undefined;
  }
}

/**
 * A free subnet to propose for a new network: a local network or a hub the first free 10.N.0.1/24 (from
 * 10.30 on, a hub from 10.90 on), a link the first free /31 of 10.255.0.0/24.
 */
export function proposeAddress(kind: NetworkKind, taken: string[]): string {
  const used = taken.map(parseSubnet).filter((x): x is Subnet => !!x);
  const free = (cidr: string) => {
    const s = parseSubnet(cidr)!;
    return !used.some((u) => overlaps(s, u));
  };
  if (kind === 'link') {
    for (let i = 0; i < 256; i += 2) if (free(`10.255.0.${i}/31`)) return `10.255.0.${i}/31`;
    return '';
  }
  const start = kind === 'hub' ? 90 : 30;
  for (let n = start; n < 255; n++) if (free(`10.${n}.0.1/24`)) return `10.${n}.0.1/24`;
  return '';
}

/** The first UDP port from 51820 on that no WireGuard network listens on. */
export function proposeListenPort(taken: number[]): string {
  let p = 51820;
  while (taken.includes(p)) p++;
  return String(p);
}
