// Port forwards (plan §2.2 layer 4, M35a, D34) in the UI: the form of a forward (protocol, entry side,
// external port or range, a device or an address as the target, target port or range, enabled), its
// checks under the fields, the forward as the configuration stores it, the gateway's messages by field,
// the forward in words for the cards, the device detail and the unapplied-changes bar, and the rows the
// lists show: the forwards of the draft with the status and the counter the gateway reads for them.
import type { PortForward, PortForwardEntry, PortForwardProtocol, PortForwardView } from '../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { cloneJSON, jsonEqual } from './mergePatch';
import { parseIPv4, parseSubnet, subnetText } from './networks';
import type { FieldChange } from './rules';

/** A port or an inclusive range of ports. */
export interface PortRange {
  from: number;
  to: number;
}

/** "8080" → 8080-8080, "8000-8010" → 8000-8010; undefined for anything else (a range may end before it starts: the checks say so). */
export function parsePortRange(text: string): PortRange | undefined {
  const m = /^\s*(\d{1,5})\s*(?:[-–]\s*(\d{1,5})\s*)?$/.exec(text);
  if (!m) return undefined;
  const from = Number(m[1]);
  const to = m[2] !== undefined ? Number(m[2]) : from;
  if (from < 1 || from > 65535 || to < 1 || to > 65535) return undefined;
  return { from, to };
}

/** 8080 → "8080", 8000 to 8010 → "8000-8010". */
export function rangeText(from: number, to?: number): string {
  return to !== undefined && to !== from ? `${from}-${to}` : String(from);
}

/** The external ports of a forward. */
export function externalRange(p: PortForward): PortRange {
  return { from: p.external_port, to: p.external_port_end ?? p.external_port };
}

/** The target ports of a forward: by default the external ones, a range shifted one to one. */
export function targetRange(p: PortForward): PortRange {
  const ext = externalRange(p);
  const from = p.target_port ?? p.external_port;
  return { from, to: p.target_port_end ?? from + (ext.to - ext.from) };
}

export const entryLabels: Record<PortForwardEntry, string> = { uplink: 'uplink', management: 'management network' };

/** Names of the devices a forward targets, by id. */
export type DeviceName = (id: string) => string | undefined;

/** The target of a forward in words: the device's name, or the address. */
export function targetText(p: PortForward, deviceName: DeviceName = () => undefined): string {
  if (p.target.device) return deviceName(p.target.device) ?? p.target.device;
  return p.target.address ?? '—';
}

/** "TCP 8080" — what a forward without a name is called. */
export function forwardTitle(p: PortForward): string {
  if (p.name) return p.name;
  const e = externalRange(p);
  return `${p.protocol.toUpperCase()} ${rangeText(e.from, e.to)}`;
}

/** "TCP 8080 → ESP32-42:80", "UDP 8000-8010 → 10.10.0.20:9000-9010". */
export function forwardSummary(p: PortForward, deviceName?: DeviceName): string {
  const e = externalRange(p);
  const t = targetRange(p);
  return `${p.protocol.toUpperCase()} ${rangeText(e.from, e.to)} → ${targetText(p, deviceName)}:${rangeText(t.from, t.to)}`;
}

// ---- the form ----------------------------------------------------------------------------------

export interface ForwardForm {
  name: string;
  description: string;
  protocol: PortForwardProtocol;
  entry: PortForwardEntry;
  /** "8080" or "8000-8010". */
  external: string;
  targetKind: 'device' | 'address';
  /** The target device by id. */
  device: string;
  address: string;
  /** Empty: the external port(s); "80", or "9000-9010" for a range. */
  targetPort: string;
  enabled: boolean;
}

export type ForwardField = 'name' | 'description' | 'protocol' | 'entry' | 'external' | 'target' | 'targetPort';

export function emptyForwardForm(entry: PortForwardEntry = 'uplink', device = ''): ForwardForm {
  return { name: '', description: '', protocol: 'tcp', entry, external: '', targetKind: 'device', device, address: '', targetPort: '', enabled: true };
}

export function formFromForward(p: PortForward): ForwardForm {
  const e = externalRange(p);
  const explicit = p.target_port !== undefined || p.target_port_end !== undefined;
  const t = targetRange(p);
  return {
    name: p.name ?? '',
    description: p.description ?? '',
    protocol: p.protocol,
    entry: p.entry,
    external: rangeText(e.from, e.to),
    targetKind: p.target.address !== undefined && p.target.device === undefined ? 'address' : 'device',
    device: p.target.device ?? '',
    address: p.target.address ?? '',
    // a target port the forward leaves to its default stays empty, so a changed external port takes it along
    targetPort: explicit ? (p.target_port_end !== undefined ? rangeText(t.from, t.to) : String(t.from)) : '',
    enabled: p.enabled !== false,
  };
}

/** What the checks of the form need to know of the rest of the configuration. */
export interface ForwardContext {
  /** The other forwards of the draft (not the one edited). */
  others: PortForward[];
  /** The local test networks: a target address lies in one of them. */
  networks: { name: string; address: string }[];
  /** The management interface is the uplink interface (two-port topology): `management` is refused. */
  twoPort: boolean;
  /**
   * The devices a forward can go to (configured ones in a local test network, and the target the forward
   * edited has), once they are known: another device is refused, as the gateway refuses it.
   */
  devices?: string[];
}

const portHint = 'A port from 1 to 65535, or a range such as 8000-8010.';

/** The checks of the forward form, each under its field (plan §2.17 "Validation"); the gateway checks the reserved ports. */
export function forwardFormErrors(f: ForwardForm, ctx: ForwardContext): Partial<Record<ForwardField, string>> {
  const e: Partial<Record<ForwardField, string>> = {};
  const name = f.name.trim();
  if (name && !new RegExp(nameRegExp).test(name)) e.name = 'Letters, digits, dot, dash and underscore; it starts with a letter or digit (at most 63).';
  else if (name && ctx.others.some((o) => o.name?.toLowerCase() === name.toLowerCase())) e.name = `A forward is already called ${name}.`;
  if (f.description.length > 500) e.description = 'At most 500 characters.';
  if (f.entry === 'management' && ctx.twoPort) e.entry = 'In the two-port topology the management network is the uplink: forward from the uplink.';

  const ext = parsePortRange(f.external);
  if (!f.external.trim()) e.external = 'The external port is required.';
  else if (!ext) e.external = portHint;
  else if (ext.to < ext.from) e.external = `The range ends before it starts (${ext.from} to ${ext.to}).`;
  else {
    const hit = ctx.others.find((o) => {
      if (o.protocol !== f.protocol) return false;
      const r = externalRange(o);
      return r.from <= ext.to && ext.from <= r.to;
    });
    if (hit) e.external = `${f.protocol.toUpperCase()} ${rangeText(externalRange(hit).from, externalRange(hit).to)} is forwarded already (${forwardTitle(hit)}).`;
  }

  if (f.targetKind === 'device') {
    if (!f.device) e.target = 'Choose the device the connections go to.';
    else if (ctx.devices && !ctx.devices.includes(f.device))
      e.target = 'A forward goes to a configured device in a local test network: adopt a discovered device first, or forward to its address.';
  } else {
    const ip = parseIPv4(f.address);
    if (!f.address.trim()) e.target = 'The target address is required.';
    else if (ip === undefined) e.target = 'An IPv4 address such as 10.10.0.20.';
    else {
      const net = ctx.networks.find((n) => {
        const s = parseSubnet(n.address);
        return s && ip >= s.network && ip <= s.broadcast;
      });
      const s = net ? parseSubnet(net.address) : undefined;
      if (!net || !s) e.target = 'The target lies in a local test network.';
      else if (ip === s.address) e.target = `${f.address.trim()} is the gateway's own address in ${net.name}.`;
      else if (ip === s.network || ip === s.broadcast) e.target = `${f.address.trim()} is no host address of ${net.name} (${subnetText(net.address)}).`;
    }
  }

  const tp = f.targetPort.trim();
  if (tp) {
    const t = parsePortRange(tp);
    if (!t) e.targetPort = `${portHint} Empty: the external port.`;
    else if (t.to < t.from) e.targetPort = `The range ends before it starts (${t.from} to ${t.to}).`;
    else if (ext && ext.to >= ext.from) {
      const len = ext.to - ext.from;
      const ranged = /[-–]/.test(tp);
      if (ranged && t.to - t.from !== len) e.targetPort = `The target range must be as long as the external one (${len + 1} ${len ? 'ports' : 'port'}).`;
      else if (!ranged && t.from + len > 65535) e.targetPort = `The range shifted to ${t.from} ends after 65535.`;
    }
  }
  return e;
}

/** Sets a member from a text, or removes it when the text is empty. */
function setText<K extends 'name' | 'description'>(p: PortForward, k: K, v: string) {
  const t = v.trim();
  if (t) p[k] = t;
  else delete p[k];
}

/**
 * The forward the form describes, as the configuration stores it; `before` is the forward as the draft
 * has it, and what the form does not edit stays. A target port left empty stays the default (the
 * external port), so it is not written.
 */
export function forwardFromForm(f: ForwardForm, before?: PortForward): PortForward {
  const p: PortForward = cloneJSON(before) ?? { protocol: f.protocol, entry: f.entry, external_port: 0, target: {} };
  setText(p, 'name', f.name);
  setText(p, 'description', f.description);
  p.protocol = f.protocol;
  p.entry = f.entry;
  const ext = parsePortRange(f.external);
  if (ext) {
    p.external_port = ext.from;
    if (ext.to !== ext.from) p.external_port_end = ext.to;
    else delete p.external_port_end;
  }
  p.target = f.targetKind === 'device' ? { device: f.device } : { address: f.address.trim() };
  const tp = f.targetPort.trim();
  const t = tp ? parsePortRange(tp) : undefined;
  if (!tp) {
    delete p.target_port;
    delete p.target_port_end;
  } else if (t) {
    p.target_port = t.from;
    // a range as typed; a single port with an external range is shifted one to one by the gateway
    if (/[-–]/.test(tp) && t.to !== t.from) p.target_port_end = t.to;
    else delete p.target_port_end;
  }
  if (!f.enabled) p.enabled = false;
  else if (before?.enabled === true) p.enabled = true;
  else delete p.enabled;
  return p;
}

/** The field of the form for a message of the gateway, by its pointer below /port_forwards/{id}. */
export function forwardFieldOf(pointer: string): ForwardField | undefined {
  const first = pointer.split('/')[0];
  switch (first) {
    case 'external_port':
    case 'external_port_end':
      return 'external';
    case 'target_port':
    case 'target_port_end':
      return 'targetPort';
    case 'target':
      return 'target';
    case 'entry':
    case 'protocol':
    case 'name':
    case 'description':
      return first;
    default:
      return undefined;
  }
}

// ---- in words ----------------------------------------------------------------------------------

/** The changes of one forward in words: added, deleted, or field by field (the unapplied-changes bar). */
export function forwardChanges(before: PortForward | undefined, after: PortForward | null, deviceName?: DeviceName): FieldChange[] {
  if (after === null) return before ? [{ field: 'forward', from: 'present', to: 'deleted' }] : [];
  if (!before) return [{ field: 'forward', from: '—', to: `added (${forwardSummary(after, deviceName)} from the ${entryLabels[after.entry]})` }];
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => a !== b && out.push({ field, from: a || '—', to: b || '—' });
  const ports = (r: PortRange) => rangeText(r.from, r.to);
  add('name', before.name ?? '', after.name ?? '');
  add('description', before.description ?? '', after.description ?? '');
  add('protocol', before.protocol.toUpperCase(), after.protocol.toUpperCase());
  add('entry', entryLabels[before.entry], entryLabels[after.entry]);
  add('external ports', ports(externalRange(before)), ports(externalRange(after)));
  add('target', targetText(before, deviceName), targetText(after, deviceName));
  add('target ports', ports(targetRange(before)), ports(targetRange(after)));
  add('enabled', before.enabled === false ? 'no' : 'yes', after.enabled === false ? 'no' : 'yes');
  // a change the words above do not show (the draft keeps no edit that equals the base)
  if (!out.length && !jsonEqual(before, after)) out.push({ field: 'settings', from: 'as before', to: 'changed' });
  return out;
}

// ---- the rows of the lists ---------------------------------------------------------------------

export interface ForwardRow {
  id: string;
  /** The forward as the draft has it (as the base had it, for a deleted one). */
  p: PortForward;
  /** What the gateway reads for the applied forward: status and counter. */
  view?: PortForwardView;
  mark?: 'NEW' | 'CHANGED' | 'DELETED';
}

/**
 * The forwards to list: those of the base revision with the draft's edits, the ones the draft adds, and
 * the deleted ones (marked), each with the gateway's view of it; sorted by entry side, protocol and port.
 */
export function forwardRows(base: Record<string, PortForward>, edits: Record<string, PortForward | null>, views: PortForwardView[]): ForwardRow[] {
  const byId = new Map(views.map((v) => [v.id, v]));
  const rows: ForwardRow[] = [];
  for (const id of new Set([...Object.keys(base), ...Object.keys(edits)])) {
    const was = base[id];
    const edit = edits[id];
    if (edit === null) {
      if (was) rows.push({ id, p: was, view: byId.get(id), mark: 'DELETED' });
      continue;
    }
    const p = edit ?? was!;
    const mark = !was ? 'NEW' : edit && !jsonEqual(was, edit) ? 'CHANGED' : undefined;
    rows.push({ id, p, view: byId.get(id), mark });
  }
  return rows.sort((a, b) => a.p.entry.localeCompare(b.p.entry) || a.p.protocol.localeCompare(b.p.protocol) || a.p.external_port - b.p.external_port);
}

/**
 * The status of a row in words and its tone: active, inactive and why, or not applied yet. The gateway's
 * status describes the applied forward, so a changed one says that its change is not applied yet.
 */
export function rowStatus(r: ForwardRow): { tone: 'normal' | 'fault' | 'offline'; label: string } {
  if (r.mark === 'NEW') return { tone: 'offline', label: 'not applied yet' };
  const changed = r.mark === 'CHANGED';
  if (r.p.enabled === false && r.mark !== 'DELETED') return { tone: 'offline', label: changed ? 'disabled · not applied yet' : 'disabled' };
  const s = r.view?.status;
  if (!s) return { tone: 'offline', label: changed ? 'status unknown · not applied yet' : 'status unknown' };
  if (s.reason === 'disabled') return { tone: 'offline', label: changed ? 'enabled · not applied yet' : 'disabled' };
  // the applied forward's target address is not the changed one's
  if (changed) return { tone: s.state === 'active' ? 'normal' : 'fault', label: `${s.state === 'active' ? 'active' : 'inactive'} · change not applied yet` };
  if (s.state === 'active') return { tone: 'normal', label: s.target_address ? `active · to ${s.target_address}` : 'active' };
  return { tone: 'fault', label: s.message ?? `inactive: ${s.reason ?? 'unknown reason'}` };
}

/**
 * The counter of a row: "12 hits", like the hit counters of the access rules; empty while the forward is
 * not in the kernel. The kernel counts the packets of connections not yet established, so a retransmitted
 * SYN or an unanswered UDP packet counts again: the number is no count of connections (counterHint).
 */
export function counterText(r: ForwardRow): string {
  const c = r.view?.counters;
  if (!c) return '';
  return `${c.packets.toLocaleString('en-US')} ${c.packets === 1 ? 'hit' : 'hits'}`;
}

/** What the counter counts, as the tooltip of counterText. */
export const counterHint =
  'Packets the forward let through before their connection was established: one per connection, more when a SYN is retransmitted or a UDP packet gets no answer';
