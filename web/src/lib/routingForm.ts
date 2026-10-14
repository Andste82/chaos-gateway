// The routing forms (plan §2.2.2; P3-M14-01): the settings of the managed BIRD instance (router id,
// local AS, the external mode with its import filter) and a routing protocol on a WireGuard link (BGP,
// OSPF or Babel with their neighbor, area and timers, the prefixes it announces, the import filter, a
// custom snippet). The forms are text as typed; the checks stand under the fields (plan §2.17
// "Validation"), the builders keep what a form does not edit, and the gateway's messages go under the
// field they are about.
import type { AnnounceEntry, ImportFilter, PrefixFilterEntry, Routing, RoutingProtocol, RoutingProtocolType } from '../api/generated/model';
import {
  completeSetupBodyConfigurationOneNetworksOneDhcpLeaseTimeOneRegExp as durationRegExp,
  completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp,
  completeSetupBodyConfigurationOneNetworksOneRoutesItemDestinationRegExp as cidrRegExp,
  completeSetupBodyConfigurationOneRoutingProtocolsOspfAreaRegExp as areaRegExp,
} from '../api/generated/zod/system/system';
import { cloneJSON } from './mergePatch';
import { parseIPv4, parseSubnet, subnetText } from './networks';

const lines = (text: string) =>
  text
    .split(/[\n,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

/** A duration in seconds ("1m30s" → 90), or undefined. */
export function seconds(d: string): number | undefined {
  if (!new RegExp(durationRegExp).test(d)) return undefined;
  const unit: Record<string, number> = { ns: 1e-9, us: 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  let total = 0;
  for (const m of d.matchAll(/([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h)/g)) total += Number(m[1]) * unit[m[2]!]!;
  return total;
}

// ---- the import filter -------------------------------------------------------------------------

/** "10.70.0.0/16 le 24" per line → the prefix filter entries, or the first line that is none. */
export function parsePrefixes(text: string): { entries: PrefixFilterEntry[]; bad?: string } {
  const entries: PrefixFilterEntry[] = [];
  for (const line of lines(text)) {
    const m = /^(\S+)(?:\s+le\s+\/?(\d+))?$/.exec(line);
    if (!m || !new RegExp(cidrRegExp).test(m[1]!)) return { entries, bad: line };
    entries.push(m[2] !== undefined ? { prefix: m[1]!, max_length: Number(m[2]) } : { prefix: m[1]! });
  }
  return { entries };
}

export function prefixesText(f: ImportFilter | undefined): string {
  return (f?.allowed_prefixes ?? []).map((e) => (e.max_length !== undefined ? `${e.prefix} le ${e.max_length}` : e.prefix)).join('\n');
}

function prefixesError(text: string): string | undefined {
  const { entries, bad } = parsePrefixes(text);
  if (bad) return `${bad}: one prefix per line, such as 10.70.0.0/16 or 10.70.0.0/16 le 24.`;
  for (const e of entries) {
    const len = Number(e.prefix.split('/')[1]);
    if (e.max_length !== undefined && (e.max_length > 32 || e.max_length < len)) return `${e.prefix} le ${e.max_length}: the longest length is from /${len} to /32.`;
  }
  return undefined;
}

function maxPrefixesError(text: string): string | undefined {
  const t = text.trim();
  if (t && (!/^\d+$/.test(t) || Number(t) < 1)) return 'At least 1; empty for the default of 100.';
  return undefined;
}

/** The import filter with the form's prefixes, default route switch and limit; what it does not edit stays. */
function importFrom(before: ImportFilter | undefined, prefixes: string, allowDefault: boolean, maxPrefixes: string): ImportFilter | undefined {
  const f: ImportFilter = { ...(before ?? {}) };
  const entries = parsePrefixes(prefixes).entries;
  if (entries.length) f.allowed_prefixes = entries;
  else delete f.allowed_prefixes;
  if (allowDefault) f.allow_default = true;
  else if (before?.allow_default === false) f.allow_default = false;
  else delete f.allow_default;
  if (maxPrefixes.trim()) f.max_prefixes = Number(maxPrefixes.trim());
  else delete f.max_prefixes;
  return Object.keys(f).length ? f : undefined;
}

// ---- the settings of the BIRD instance ---------------------------------------------------------

export interface RoutingSettingsForm {
  /** Empty for the default: the lowest gateway address of the local networks. */
  routerId: string;
  /** The local AS for BGP. */
  asn: string;
  externalEnabled: boolean;
  externalTable: string;
  externalPrefixes: string;
  externalAllowDefault: boolean;
  externalMaxPrefixes: string;
}

export type RoutingSettingsField = 'routerId' | 'asn' | 'externalTable' | 'externalPrefixes' | 'externalMaxPrefixes';

export function formFromRouting(r: Routing | undefined): RoutingSettingsForm {
  const x = r?.external;
  return {
    routerId: r?.router_id ?? '',
    asn: r?.asn !== undefined ? String(r.asn) : '',
    externalEnabled: !!x?.enabled,
    externalTable: x?.table !== undefined ? String(x.table) : '',
    externalPrefixes: prefixesText(x?.import),
    externalAllowDefault: !!x?.import?.allow_default,
    externalMaxPrefixes: x?.import?.max_prefixes !== undefined ? String(x.import.max_prefixes) : '',
  };
}

/** The tables of the system (253 to 255) and of Chaos Gateway (100 to 110, its own and the PMTU mirrors). */
const reservedTable = (t: number) => (t >= 100 && t <= 110) || (t >= 253 && t <= 255);

/** The checks of the routing settings, each under its field. `bgp`: a BGP protocol is configured (it needs the local AS). */
export function routingSettingsErrors(f: RoutingSettingsForm, bgp: boolean): Partial<Record<RoutingSettingsField, string>> {
  const e: Partial<Record<RoutingSettingsField, string>> = {};
  const id = f.routerId.trim();
  if (id && (parseIPv4(id) === undefined || parseIPv4(id) === 0 || parseIPv4(id)! >>> 28 === 14)) e.routerId = 'A unicast IPv4 address; empty for the lowest gateway address of the local networks.';
  const asn = f.asn.trim();
  if (asn && (!/^\d+$/.test(asn) || Number(asn) < 1 || Number(asn) > 4294967295)) e.asn = 'An AS number from 1 to 4294967295 (private: 64512 to 65534).';
  else if (!asn && bgp) e.asn = 'BGP needs the local AS number.';
  if (f.externalEnabled) {
    const t = f.externalTable.trim();
    if (!t) e.externalTable = 'The kernel table the other daemon writes its routes into is required.';
    else if (!/^\d+$/.test(t) || Number(t) < 1 || Number(t) > 4294967294) e.externalTable = 'A table number from 1 to 4294967294.';
    else if (reservedTable(Number(t))) e.externalTable = `Table ${t} belongs to the system or to Chaos Gateway.`;
  }
  const p = prefixesError(f.externalPrefixes);
  if (p) e.externalPrefixes = p;
  const m = maxPrefixesError(f.externalMaxPrefixes);
  if (m) e.externalMaxPrefixes = m;
  return e;
}

/** The routing with the settings of the form; the protocols and what the form does not edit stay. */
export function routingFromForm(f: RoutingSettingsForm, before: Routing | undefined): Routing {
  const r: Routing = cloneJSON(before) ?? {};
  if (f.routerId.trim()) r.router_id = f.routerId.trim();
  else delete r.router_id;
  if (f.asn.trim()) r.asn = Number(f.asn.trim());
  else delete r.asn;
  const x = { ...(r.external ?? {}) };
  if (f.externalEnabled) x.enabled = true;
  else if (x.enabled !== undefined) x.enabled = false;
  if (f.externalTable.trim()) x.table = Number(f.externalTable.trim());
  else delete x.table;
  const imp = importFrom(x.import, f.externalPrefixes, f.externalAllowDefault, f.externalMaxPrefixes);
  if (imp) x.import = imp;
  else delete x.import;
  // an external mode that was never set and is off stays out
  if (Object.keys(x).length && !(Object.keys(x).length === 1 && x.enabled === false && !before?.external)) r.external = x;
  else delete r.external;
  return r;
}

/** The field of the settings form for a message of the gateway, by its pointer below /routing. */
export function routingSettingsFieldOf(pointer: string): RoutingSettingsField | undefined {
  if (pointer === 'router_id') return 'routerId';
  if (pointer === 'asn') return 'asn';
  if (pointer.startsWith('external/table')) return 'externalTable';
  if (pointer.startsWith('external/import/allowed_prefixes')) return 'externalPrefixes';
  if (pointer.startsWith('external/import/max_prefixes')) return 'externalMaxPrefixes';
  return undefined;
}

// ---- a routing protocol ------------------------------------------------------------------------

export interface ProtocolForm {
  name: string;
  type: RoutingProtocolType;
  /** The WireGuard link it runs on, by id. */
  link: string;
  enabled: boolean;
  /** What it announces: `network:<id>` and `client:<id>` keys. */
  announce: string[];
  /** Further prefixes it announces: "10.80.0.0/24, …". */
  announceCidrs: string;
  prefixes: string;
  allowDefault: boolean;
  maxPrefixes: string;
  // BGP
  neighborAddress: string;
  neighborAsn: string;
  holdTime: string;
  keepaliveTime: string;
  passive: boolean;
  // OSPF
  area: string;
  helloInterval: string;
  deadInterval: string;
  cost: string;
  // Babel
  babelHello: string;
  rxcost: string;
  customSnippet: string;
}

export type ProtocolField =
  | 'name'
  | 'type'
  | 'link'
  | 'announceCidrs'
  | 'prefixes'
  | 'maxPrefixes'
  | 'neighborAddress'
  | 'neighborAsn'
  | 'holdTime'
  | 'keepaliveTime'
  | 'area'
  | 'helloInterval'
  | 'deadInterval'
  | 'cost'
  | 'babelHello'
  | 'rxcost'
  | 'customSnippet';

export function emptyProtocolForm(link = ''): ProtocolForm {
  return {
    name: '',
    type: 'bgp',
    link,
    enabled: true,
    announce: [],
    announceCidrs: '',
    prefixes: '',
    allowDefault: false,
    maxPrefixes: '',
    neighborAddress: '',
    neighborAsn: '',
    holdTime: '',
    keepaliveTime: '',
    passive: false,
    area: '',
    helloInterval: '',
    deadInterval: '',
    cost: '',
    babelHello: '',
    rxcost: '',
    customSnippet: '',
  };
}

export function formFromProtocol(p: RoutingProtocol): ProtocolForm {
  const f = emptyProtocolForm(p.link);
  f.name = p.name;
  f.type = p.type;
  f.enabled = p.enabled !== false;
  f.announce = (p.announce ?? []).filter((a) => a.network || a.client).map((a) => (a.network ? `network:${a.network}` : `client:${a.client}`));
  f.announceCidrs = (p.announce ?? [])
    .filter((a) => a.cidr)
    .map((a) => a.cidr)
    .join(', ');
  f.prefixes = prefixesText(p.import);
  f.allowDefault = !!p.import?.allow_default;
  f.maxPrefixes = p.import?.max_prefixes !== undefined ? String(p.import.max_prefixes) : '';
  f.neighborAddress = p.bgp?.neighbor_address ?? '';
  f.neighborAsn = p.bgp?.neighbor_asn !== undefined ? String(p.bgp.neighbor_asn) : '';
  f.holdTime = p.bgp?.hold_time ?? '';
  f.keepaliveTime = p.bgp?.keepalive_time ?? '';
  f.passive = !!p.bgp?.passive;
  f.area = p.ospf?.area ?? '';
  f.helloInterval = p.ospf?.hello_interval ?? '';
  f.deadInterval = p.ospf?.dead_interval ?? '';
  f.cost = p.ospf?.cost !== undefined ? String(p.ospf.cost) : '';
  f.babelHello = p.babel?.hello_interval ?? '';
  f.rxcost = p.babel?.rxcost !== undefined ? String(p.babel.rxcost) : '';
  f.customSnippet = p.custom_snippet ?? '';
  return f;
}

/** A link of the draft, as the checks of the protocol form need it. */
export interface LinkInfo {
  id: string;
  name: string;
  /** The gateway's address in the /31 transfer network. */
  address: string;
}

export interface ProtocolContext {
  links: LinkInfo[];
  /** The other protocols of the draft (not the one edited). */
  others: { name: string; type: RoutingProtocolType; link: string }[];
}

function durationError(text: string, def: string): string | undefined {
  const t = text.trim();
  if (t && seconds(t) === undefined) return `A duration such as ${def}; empty for the default of ${def}.`;
  if (t && seconds(t)! < 1) return 'At least 1s.';
  return undefined;
}

function intError(text: string, min: number, max: number, def: string): string | undefined {
  const t = text.trim();
  if (t && (!/^\d+$/.test(t) || Number(t) < min || Number(t) > max)) return `From ${min} to ${max}; empty for the default of ${def}.`;
  return undefined;
}

/** The checks of the protocol form, each under its field (plan §2.17 "Validation"). */
export function protocolFormErrors(f: ProtocolForm, ctx: ProtocolContext): Partial<Record<ProtocolField, string>> {
  const e: Partial<Record<ProtocolField, string>> = {};
  const name = f.name.trim();
  if (!name) e.name = 'A name is required.';
  else if (!new RegExp(nameRegExp).test(name)) e.name = 'Letters, digits, dot, dash and underscore; it starts with a letter or digit (at most 63).';
  else if (ctx.others.some((o) => o.name.toLowerCase() === name.toLowerCase())) e.name = `A protocol is already called ${name}.`;
  const link = ctx.links.find((l) => l.id === f.link);
  if (!f.link) e.link = ctx.links.length ? 'Choose the WireGuard link the protocol runs on.' : 'Dynamic routing runs on a WireGuard link: add a link network first.';
  else if (!link) e.link = 'That is no WireGuard link of the configuration.';
  else {
    const dup = ctx.others.find((o) => o.link === f.link && o.type === f.type);
    if (dup) e.type = `${link.name} already runs a ${f.type.toUpperCase()} protocol (${dup.name}).`;
  }
  const bad = lines(f.announceCidrs).find((c) => !new RegExp(cidrRegExp).test(c));
  if (bad) e.announceCidrs = `${bad} is no IPv4 network such as 10.80.0.0/24.`;
  const p = prefixesError(f.prefixes);
  if (p) e.prefixes = p;
  const m = maxPrefixesError(f.maxPrefixes);
  if (m) e.maxPrefixes = m;
  if (f.type === 'bgp') {
    const asn = f.neighborAsn.trim();
    if (!asn) e.neighborAsn = "The neighbor's AS number is required.";
    else if (!/^\d+$/.test(asn) || Number(asn) < 1 || Number(asn) > 4294967295) e.neighborAsn = 'An AS number from 1 to 4294967295.';
    const na = f.neighborAddress.trim();
    if (na) {
      const ip = parseIPv4(na);
      const s = parseSubnet(link?.address);
      if (ip === undefined) e.neighborAddress = 'An IPv4 address; empty for the peer of the link.';
      else if (s && (ip < s.network || ip > s.broadcast || ip === s.address)) e.neighborAddress = `The neighbor is the peer in the link's transfer network ${subnetText(link!.address)}; empty for it.`;
    }
    const keep = durationError(f.keepaliveTime, '30s');
    if (keep) e.keepaliveTime = keep;
    const hold = durationError(f.holdTime, '90s');
    if (hold) e.holdTime = hold;
    if (!keep && !hold) {
      const k = f.keepaliveTime.trim() ? seconds(f.keepaliveTime.trim())! : 30;
      const h = f.holdTime.trim() ? seconds(f.holdTime.trim())! : 90;
      if (h < 3 * k) e.holdTime = `The hold time must be at least three keepalive intervals (${3 * k} s).`;
    }
  }
  if (f.type === 'ospf') {
    if (f.area.trim() && !new RegExp(areaRegExp).test(f.area.trim())) e.area = 'An area such as 0 or 0.0.0.0.';
    const hello = durationError(f.helloInterval, '10s');
    if (hello) e.helloInterval = hello;
    const dead = durationError(f.deadInterval, '40s');
    if (dead) e.deadInterval = dead;
    if (!hello && !dead) {
      const h = f.helloInterval.trim() ? seconds(f.helloInterval.trim())! : 10;
      const d = f.deadInterval.trim() ? seconds(f.deadInterval.trim())! : 40;
      if (d <= h) e.deadInterval = `The dead interval must be longer than the hello interval (${h} s).`;
    }
    const c = intError(f.cost, 1, 65535, '10');
    if (c) e.cost = c;
  }
  if (f.type === 'babel') {
    const hello = durationError(f.babelHello, '4s');
    if (hello) e.babelHello = hello;
    const r = intError(f.rxcost, 1, 65535, '96');
    if (r) e.rxcost = r;
  }
  if (f.customSnippet.length > 8192) e.customSnippet = 'At most 8192 characters.';
  return e;
}

/** Sets a member from a text, or removes it when the text is empty. */
function setText<T extends object>(o: T, k: keyof T, v: string, num = false) {
  const t = v.trim();
  if (t) (o as Record<keyof T, unknown>)[k] = num ? Number(t) : t;
  else delete o[k];
}

/**
 * The protocol the form describes, as the configuration stores it; `before` is the protocol as the draft
 * has it, and what the form does not edit stays. Only the settings of the protocol's own type are kept
 * (the gateway refuses the settings of another type).
 */
export function protocolFromForm(f: ProtocolForm, before?: RoutingProtocol): RoutingProtocol {
  const p: RoutingProtocol = cloneJSON(before) ?? { name: '', type: f.type, link: '' };
  p.name = f.name.trim();
  p.type = f.type;
  p.link = f.link;
  if (!f.enabled) p.enabled = false;
  else if (before?.enabled === true) p.enabled = true;
  else delete p.enabled;
  const announce: AnnounceEntry[] = [
    ...f.announce.map((k) => {
      const [kind, id] = k.split(':');
      return kind === 'client' ? { client: id! } : { network: id! };
    }),
    ...lines(f.announceCidrs).map((cidr) => ({ cidr })),
  ];
  if (announce.length) p.announce = announce;
  else delete p.announce;
  const imp = importFrom(p.import, f.prefixes, f.allowDefault, f.maxPrefixes);
  if (imp) p.import = imp;
  else delete p.import;
  for (const t of ['bgp', 'ospf', 'babel'] as const) if (t !== f.type) delete p[t];
  if (f.type === 'bgp') {
    const b = { ...(p.bgp ?? { neighbor_asn: 0 }) };
    b.neighbor_asn = Number(f.neighborAsn.trim());
    setText(b, 'neighbor_address', f.neighborAddress);
    setText(b, 'hold_time', f.holdTime);
    setText(b, 'keepalive_time', f.keepaliveTime);
    if (f.passive) b.passive = true;
    else delete b.passive;
    p.bgp = b;
  }
  if (f.type === 'ospf') {
    const o = { ...(p.ospf ?? {}) };
    setText(o, 'area', f.area);
    setText(o, 'hello_interval', f.helloInterval);
    setText(o, 'dead_interval', f.deadInterval);
    setText(o, 'cost', f.cost, true);
    if (Object.keys(o).length) p.ospf = o;
    else delete p.ospf;
  }
  if (f.type === 'babel') {
    const b = { ...(p.babel ?? {}) };
    setText(b, 'hello_interval', f.babelHello);
    setText(b, 'rxcost', f.rxcost, true);
    if (Object.keys(b).length) p.babel = b;
    else delete p.babel;
  }
  if (f.customSnippet.trim()) p.custom_snippet = f.customSnippet;
  else delete p.custom_snippet;
  return p;
}

/** The field of the protocol form for a message of the gateway, by its pointer below the protocol. */
export function protocolFieldOf(pointer: string): ProtocolField | undefined {
  const p = pointer.split('/');
  switch (p[0]) {
    case 'name':
    case 'type':
    case 'link':
      return p[0];
    case 'custom_snippet':
      return 'customSnippet';
    case 'announce':
      return 'announceCidrs';
    case 'import':
      return p[1] === 'max_prefixes' ? 'maxPrefixes' : p[1] === 'allowed_prefixes' ? 'prefixes' : undefined;
    case 'bgp':
      return ({ neighbor_address: 'neighborAddress', neighbor_asn: 'neighborAsn', hold_time: 'holdTime', keepalive_time: 'keepaliveTime' } as const)[p[1] as 'neighbor_asn'] ?? 'neighborAsn';
    case 'ospf':
      return ({ area: 'area', hello_interval: 'helloInterval', dead_interval: 'deadInterval', cost: 'cost' } as const)[p[1] as 'area'];
    case 'babel':
      return ({ hello_interval: 'babelHello', rxcost: 'rxcost' } as const)[p[1] as 'rxcost'];
    default:
      return undefined;
  }
}

/** A protocol in words for the changes: "BGP on site-b, neighbor AS 65002". */
export function describeProtocol(p: RoutingProtocol, linkName: (id: string) => string | undefined): string {
  const parts = [`${p.type.toUpperCase()} on ${linkName(p.link) ?? p.link}`];
  if (p.bgp) parts.push(`neighbor AS ${p.bgp.neighbor_asn}`);
  if (p.ospf?.area) parts.push(`area ${p.ospf.area}`);
  if (p.enabled === false) parts.push('disabled');
  return parts.join(', ');
}
