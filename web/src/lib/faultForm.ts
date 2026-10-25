// The add/edit fault dialog (plan §2.17 "Add fault"): the form of every fault kind the API offers
// (P3-M13-04) — an impairment fault with the three fields of the common case (latency, jitter, loss)
// and the advanced ones (`netemForm`), an MTU/PMTUD fault (size and mode) and a tunnel fault (the
// impairment of a WireGuard client's or a link's encrypted traffic, M10) — the presets to start
// from, the checks under the fields, the live preview sentence, and the request it becomes: an
// overlay (`POST /overlays`) or a configured fault in a candidate revision. A DNS fault and a TLS case (M23b) are
// kinds of the dialog as well, with the fields of their own (`dnsForm`, `tlsForm`); they are overlays only, as the API
// has no configured fault of these families.
import type { ConfigFault, FaultBody, MtuParams, Overlay, OverlayRequest, Protocol, Scope, TunnelRef } from '../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { gatewayNow } from './clock';
import { dnsBody, dnsEffect, dnsErrors, dnsFieldOf, dnsScope, dnsToForm, emptyDns, type DnsField, type DnsForm } from './dnsForm';
import { faultChips, type Names } from './faults';
import { durationMs, formatDuration, formatHM, formatRemaining } from './format';
import {
  emptyImpairment,
  impairmentErrors,
  impairmentParams,
  impairmentToForm,
  netemFieldOf,
  type Direction,
  type ImpairmentField,
  type ImpairmentForm,
} from './netemForm';
import { describeDestination, formatPorts } from './rules';
import { remoteCidrError } from './scopes';
import { emptyTls, tlsBody, tlsEffect, tlsErrors, tlsFieldOf, tlsScope, tlsToForm, type TlsField, type TlsForm } from './tlsForm';
import { destinationOf, trafficErrors, trafficMatch, type DestKind } from './trafficForm';

export { parseRate, presets, rateText, type Direction } from './netemForm';

/** The kinds of the dialog: the three families of a fault body, and the DNS fault and the TLS case, which are overlays of their own kinds. */
export type FaultFamily = 'impairment' | 'mtu' | 'tunnel' | 'dns' | 'tls';
/** The kinds of the dialog that are overlays only: the API has no configured DNS fault or TLS case. */
export const overlayOnlyFamilies: readonly FaultFamily[] = ['dns', 'tls'];
export type MtuMode = NonNullable<MtuParams['mode']>;
export type { DestKind } from './trafficForm';

export interface FaultForm extends ImpairmentForm {
  family: FaultFamily;
  /** `device:<id>`, `group:<id>`, `network:<id>`, `remote_network:<client|link|cidr>:<value>` or `global` */
  target: string;
  /** a tunnel fault's tunnel: `client:<id>` or `link:<id>` */
  tunnel: string;
  /** a key of `trafficPresets`, or `custom` */
  traffic: string;
  protocol: Protocol;
  ports: string;
  /** the kind of destination of custom traffic; `destination` holds its network, address or host name */
  destKind: DestKind;
  destination: string;
  /** an MTU fault's packet size (bytes) and mode */
  mtuSize: string;
  mtuMode: MtuMode;
  /** a key of `durations`, `keep` (the edited overlay's remaining time) or `ttl:<its own TTL>` */
  duration: string;
  /** the expiry of the edited overlay, which `keep` keeps; empty for a new fault or one without a TTL */
  keepUntil: string;
  /** the edited overlay's own TTL, which `keep` keeps as its TTL (`expires_in` carries the remaining time) */
  keepTtl: string;
  /** the name of a configured fault */
  name: string;
  /** a DNS fault's action, names and what the action needs; a TLS case's case, selection and settings */
  dns: DnsForm;
  tls: TlsForm;
}

export interface TrafficPreset {
  label: string;
  protocol: Protocol;
  ports: number[];
}

export const trafficPresets: Record<string, TrafficPreset> = {
  all: { label: 'All traffic', protocol: 'any', ports: [] },
  mqtts: { label: 'MQTT · TCP 8883', protocol: 'tcp', ports: [8883] },
  mqtt: { label: 'MQTT · TCP 1883', protocol: 'tcp', ports: [1883] },
  https: { label: 'HTTPS · TCP 443', protocol: 'tcp', ports: [443] },
  http: { label: 'HTTP · TCP 80', protocol: 'tcp', ports: [80] },
  dns: { label: 'DNS · UDP 53', protocol: 'udp', ports: [53] },
};

export const familyLabels: Record<FaultFamily, string> = { impairment: 'Impairment', mtu: 'MTU / PMTUD', tunnel: 'Tunnel', dns: 'DNS', tls: 'TLS' };

export const mtuModes: { value: MtuMode; label: string; hint: string }[] = [
  { value: 'icmp', label: 'ICMP · fragmentation needed', hint: 'Larger packets are refused with "fragmentation needed": path MTU discovery works. The server caches the smaller path MTU for the shared NAT address (about 10 min), which reaches other devices talking to it.' },
  { value: 'blackhole', label: 'Black hole · dropped silently', hint: 'Larger packets are dropped without a message: path MTU discovery fails, TCP transfers stall.' },
  { value: 'mss_clamp', label: 'MSS clamp · TCP only', hint: 'The MSS of a TCP SYN is set to the size minus 40: only TCP of the target, no side effect on others.' },
];

export const durations: { value: string; label: string; ttl?: string }[] = [
  { value: '1m', label: '1 min', ttl: '1m' },
  { value: '5m', label: '5 min', ttl: '5m' },
  { value: '15m', label: '15 min', ttl: '15m' },
  { value: '1h', label: '1 hour', ttl: '1h' },
  { value: 'perm', label: 'Until removed' },
];

export function emptyForm(target = 'global'): FaultForm {
  return {
    ...emptyImpairment(),
    family: 'impairment',
    target,
    tunnel: '',
    traffic: 'all',
    protocol: 'any',
    ports: '',
    destKind: 'any',
    destination: '',
    mtuSize: '1280',
    mtuMode: 'icmp',
    duration: '15m',
    keepUntil: '',
    keepTtl: '',
    name: '',
    dns: emptyDns(),
    tls: emptyTls(),
  };
}

/** A TTL in words: "30m" → "30 min", "4530s" (a TTL set over the API in seconds) → "1 h 15 min 30 s". */
export function ttlText(ttl: string): string {
  const ms = durationMs(ttl);
  if (ms === undefined || ms < 1000) return formatDuration(ttl);
  const total = Math.round(ms / 1000);
  const parts: [number, string][] = [
    [Math.floor(total / 3600), 'h'],
    [Math.floor((total % 3600) / 60), 'min'],
    [total % 60, 's'],
  ];
  return parts
    .filter(([n]) => n > 0)
    .map(([n, u]) => `${n} ${u}`)
    .join(' ');
}

/** The TTL of a duration choice: one of `durations`, or an overlay's own TTL (`ttl:30m`); undefined until removed. */
export function durationTtl(value: string): string | undefined {
  if (value.startsWith('ttl:')) return value.slice(4);
  return durations.find((d) => d.value === value)?.ttl;
}

/**
 * The duration choices of the dialog. An edited overlay with a TTL (P3-M13-06) first offers to keep
 * its remaining time ("Keep: 4:12 left of 30 min", the default), and a TTL of its own that is not one
 * of the choices (`30m` set over the API) is offered as such, starting over.
 */
export function durationChoices(f: Pick<FaultForm, 'keepUntil'>, ownTtl: string | undefined, now: Date): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = [];
  if (f.keepUntil) {
    const ms = new Date(f.keepUntil).getTime() - now.getTime();
    // an expiry that passed while the dialog was open keeps nothing (keepError says so)
    out.push({ value: 'keep', label: ms > 0 ? `Keep: ${formatRemaining(ms)} left${ownTtl ? ` of ${ttlText(ownTtl)}` : ''}` : `Keep: expired at ${formatHM(f.keepUntil)}` });
  }
  out.push(...durations.map(({ value, label }) => ({ value, label })));
  if (ownTtl && !durations.some((d) => d.ttl === ownTtl)) out.push({ value: `ttl:${ownTtl}`, label: `${ttlText(ownTtl)} (its own TTL)` });
  return out;
}

/**
 * The edited overlay expired while the dialog was open and the form still keeps its remaining time:
 * there is none left, and sending it would write the ended fault again for a second (Phase 3 audit).
 */
export function keepError(f: Pick<FaultForm, 'duration' | 'keepUntil'>, now: Date): string | undefined {
  if (f.duration !== 'keep' || !f.keepUntil || new Date(f.keepUntil).getTime() > now.getTime()) return undefined;
  return `The fault expired at ${formatHM(f.keepUntil)}: choose how long it runs now.`;
}

/** The remaining time of an expiry in whole seconds, at least one, and at most `max` (a TTL) when given. */
export function remainingTtl(until: string, now: Date, max?: string): string {
  let s = Math.max(1, Math.ceil((new Date(until).getTime() - now.getTime()) / 1000));
  const maxMs = max ? durationMs(max) : undefined;
  if (maxMs !== undefined && maxMs >= 1000) s = Math.min(s, Math.floor(maxMs / 1000));
  return `${s}s`;
}

export function scopeOf(target: string): Scope {
  if (target === 'global' || !target) return { global: true };
  const i = target.indexOf(':');
  const kind = target.slice(0, i);
  const rest = target.slice(i + 1);
  if (kind === 'remote_network') {
    // `remote_network:client:<id>`, `remote_network:link:<id>` or `remote_network:cidr:<prefix>`
    const j = rest.indexOf(':');
    return { remote_network: { [rest.slice(0, j)]: rest.slice(j + 1) } } as Scope;
  }
  return { [kind]: rest } as Scope;
}

export function targetOf(s: Scope | undefined): string {
  if (!s || s.global) return 'global';
  if (s.device) return `device:${s.device}`;
  if (s.group) return `group:${s.group}`;
  if (s.network) return `network:${s.network}`;
  const r = s.remote_network;
  if (r?.client) return `remote_network:client:${r.client}`;
  if (r?.link) return `remote_network:link:${r.link}`;
  // an empty prefix is the CIDR choice still waiting for its prefix (P3-M13-08), not "everything"
  if (r?.cidr !== undefined) return `remote_network:cidr:${r.cidr}`;
  return 'global';
}

/** `client:<id>` → `{client: id}`; undefined while no tunnel is chosen. */
export function tunnelOf(tunnel: string): TunnelRef | undefined {
  const m = /^(client|link):(.+)$/.exec(tunnel);
  return m ? ({ [m[1]!]: m[2] } as TunnelRef) : undefined;
}

export function tunnelValue(t: TunnelRef | undefined): string {
  if (t?.client) return `client:${t.client}`;
  if (t?.link) return `link:${t.link}`;
  return '';
}

/** A problem of the form: a parameter (`download.<field>` of the download set), or a field of the fault. */
export type FaultField = ImpairmentField | DnsField | TlsField | 'ports' | 'destination' | 'name' | 'target' | 'tunnel' | 'mtuSize' | 'mtuMode' | 'duration';

/** The problems of the form, by field; empty when it can be applied. */
export function faultFormErrors(f: FaultForm, layer: 'overlay' | 'config' = 'overlay'): Partial<Record<FaultField, string>> {
  const e: Partial<Record<FaultField, string>> = {};
  if (f.family === 'dns') Object.assign(e, dnsErrors(f.dns));
  else if (f.family === 'tls') Object.assign(e, tlsErrors(f.tls));
  else if (f.family === 'mtu') {
    const size = Number(f.mtuSize);
    if (!f.mtuSize.trim() || !Number.isInteger(size) || size < 552 || size > 1500) e.mtuSize = 'A packet size from 552 to 1500 bytes.';
  } else {
    // the gateway refuses a fault that changes nothing ("the fault sets no parameter"); keeping the
    // packet order alone changes nothing either
    Object.assign(e, impairmentErrors(f, f.family === 'tunnel'));
  }
  if (f.family !== 'tunnel' && !overlayOnlyFamilies.includes(f.family) && f.traffic === 'custom') Object.assign(e, trafficErrors(f));
  if (layer === 'config' && f.name && !nameRegExp.test(f.name)) e.name = 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
  if (f.family === 'tunnel') {
    if (!tunnelOf(f.tunnel)) e.tunnel = 'Choose the WireGuard client or link whose tunnel is impaired.';
  } else {
    if (!f.target) e.target = 'Choose a target.';
    const remote = remoteCidrError(f.target);
    if (remote) e.target = remote;
  }
  return e;
}

/** The selector of the traffic: protocol, ports and destination. */
function match(f: FaultForm): Pick<FaultBody, 'protocol' | 'ports' | 'port_ranges' | 'destination'> {
  if (f.traffic !== 'custom') {
    const t = trafficPresets[f.traffic] ?? trafficPresets.all!;
    return t.protocol === 'any' ? {} : { protocol: t.protocol, ports: [...t.ports] };
  }
  return trafficMatch(f);
}

/**
 * The fault body of the form: an impairment fault's traffic and parameters (flat for both
 * directions, one direction's, or each its own), an MTU fault's traffic and size, or a tunnel
 * fault's tunnel and parameters.
 */
export function formToFault(f: FaultForm): FaultBody {
  switch (f.family) {
    case 'dns':
    case 'tls':
      // a DNS fault and a TLS case are no fault body: `formToOverlay` makes their own parts
      return {};
    case 'mtu':
      return { family: 'mtu', ...match(f), mtu: mtuOf(f.mtuSize, f.mtuMode) };
    case 'tunnel':
      return { family: 'tunnel', ...(tunnelOf(f.tunnel) ? { tunnel: tunnelOf(f.tunnel) } : {}), ...impairmentParams(f, true) };
    default:
      return { ...match(f), ...impairmentParams(f) };
  }
}

/**
 * The overlay of the form; a tunnel fault has no target. `keep` sends the edited overlay's own TTL
 * again and its remaining time on the gateway's clock as `expires_in` (P3-M13-06): the gateway
 * removes it at the same time as before, and the overlay keeps its TTL (`30m` stays `30m`).
 */
export function formToOverlay(f: FaultForm, now: Date = gatewayNow()): OverlayRequest {
  const base: OverlayRequest = {
    ...(f.family === 'tunnel' ? {} : { target: scopeOf(f.target) }),
    ...(f.family === 'dns' ? { dns: dnsBody(f.dns) } : f.family === 'tls' ? { tls: tlsBody(f.tls) } : { fault: formToFault(f) }),
  };
  return { ...base, ...lifetimeOf(f, now) };
}

/** The part of the duration choice (and the keep of an edited overlay's remaining time) in the form. */
export type Lifetime = Pick<FaultForm, 'duration' | 'keepUntil' | 'keepTtl'>;

/** The `ttl` (and `expires_in`) of the overlay request a duration choice makes; none for "until removed". */
export function lifetimeOf(f: Lifetime, now: Date = gatewayNow()): Pick<OverlayRequest, 'ttl' | 'expires_in'> {
  if (f.duration === 'keep' && f.keepUntil) {
    if (!f.keepTtl) return { ttl: remainingTtl(f.keepUntil, now) };
    return { ttl: f.keepTtl, expires_in: remainingTtl(f.keepUntil, now, f.keepTtl) };
  }
  const ttl = durationTtl(f.duration);
  return ttl ? { ttl } : {};
}

/** The end of an overlay in words, for the preview sentence: "until removed", "expires after 5 min", "expires at 12:40 as before". */
export function untilText(f: Lifetime, layer: 'overlay' | 'config' = 'overlay'): string {
  if (layer === 'config') return 'configured until removed (after preview and apply)';
  if (f.duration === 'keep' && f.keepUntil) return `expires at ${formatHM(f.keepUntil)} as before`;
  if (f.duration === 'perm') return 'until removed';
  return `expires after ${durations.find((d) => d.value === f.duration)?.label ?? ttlText(durationTtl(f.duration) ?? f.duration)}`;
}

/**
 * The MTU part of the form. The mode `icmp` is the API's default: a base without a mode keeps
 * leaving it out, so an edit elsewhere does not write the default into the profile or the
 * configured fault.
 */
export function mtuOf(size: string, mode: MtuMode, base?: { mode?: MtuMode }): { size: number; mode?: MtuMode } {
  return base && base.mode === undefined && mode === 'icmp' ? { size: Number(size) } : { size: Number(size), mode };
}

/** A configured fault from the form (a tunnel fault has no source); the description and the switch are kept from `base`. */
export function formToConfigFault(f: FaultForm, base?: ConfigFault): ConfigFault {
  const keep: ConfigFault = {};
  if (base?.description) keep.description = base.description;
  if (base?.enabled === false) keep.enabled = false;
  const body = formToFault(f);
  if (f.family === 'mtu' && base?.family === 'mtu') body.mtu = mtuOf(f.mtuSize, f.mtuMode, base.mtu);
  return { ...keep, ...(f.name ? { name: f.name } : {}), ...(f.family === 'tunnel' ? {} : { source: scopeOf(f.target) }), ...body };
}

/** The form of an existing fault (an overlay's or a configured one), whatever its family. */
export function faultToForm(fault: FaultBody | undefined, target: Scope | undefined, extra: { ttl?: string; expiresAt?: string; name?: string } = {}): FaultForm {
  const f = emptyForm(targetOf(target));
  if (!fault) return withLifetime(f, extra);
  f.family = fault.family ?? 'impairment';
  if (f.family === 'mtu' && fault.mtu) {
    f.mtuSize = String(fault.mtu.size);
    f.mtuMode = fault.mtu.mode ?? 'icmp';
  } else {
    impairmentToForm(fault, f);
  }
  f.tunnel = tunnelValue(fault.tunnel);
  const d = fault.destination;
  const preset = Object.entries(trafficPresets).find(
    ([, t]) => !d && !fault.port_ranges?.length && (fault.protocol ?? 'any') === t.protocol && (fault.ports ?? []).join(',') === t.ports.join(','),
  );
  if (preset) f.traffic = preset[0];
  else {
    f.traffic = 'custom';
    f.protocol = fault.protocol ?? 'any';
    f.ports = formatPorts(fault);
    f.destKind = !d ? 'any' : d.uplink ? 'uplink' : d.network !== undefined ? 'network' : d.hostname !== undefined ? 'hostname' : 'cidr';
    f.destination = d?.network ?? d?.hostname ?? d?.cidr ?? '';
  }
  return withLifetime(f, extra);
}

/** The duration choice of an existing overlay: it keeps its remaining time unless another duration is chosen (P3-M13-06). */
export function overlayLifetime(o: { ttl?: string; expires_at?: string }): Lifetime {
  const ttl = o.ttl ? (durations.find((x) => x.ttl === o.ttl)?.value ?? `ttl:${o.ttl}`) : undefined;
  const keepUntil = o.expires_at ?? '';
  return { keepUntil, keepTtl: o.ttl ?? '', duration: keepUntil ? 'keep' : (ttl ?? 'perm') };
}

/** The duration and the name of an existing fault (an overlay's lifetime, a configured fault's name) in the form. */
function withLifetime(f: FaultForm, extra: { ttl?: string; expiresAt?: string; name?: string }): FaultForm {
  Object.assign(f, overlayLifetime({ ttl: extra.ttl, expires_at: extra.expiresAt }));
  f.name = extra.name ?? '';
  return f;
}

/** The form of an overlay the dialog edits: a fault, a DNS fault or a TLS case. */
export function overlayToForm(o: Overlay): FaultForm {
  const extra = { ttl: o.ttl, expiresAt: o.expires_at };
  const f = faultToForm(o.kind === 'fault' ? o.fault : undefined, o.target, extra);
  if (o.kind === 'dns') {
    f.family = 'dns';
    f.dns = dnsToForm(o.dns);
  }
  if (o.kind === 'tls') {
    f.family = 'tls';
    f.tls = tlsToForm(o.tls);
  }
  return f;
}

const directionText: Record<Direction, string> = { both: 'upload & download', upload: 'upload only', download: 'download only', each: 'upload and download apart' };

/**
 * The live preview: "ESP32-42 · MQTT · TCP 8883 · upload & download → ↑↓ 200 ms ±50, 5 % loss ·
 * expires after 5 min"; an MTU fault has no direction, a tunnel fault no traffic (`targetName` is
 * the tunnel's name then).
 */
export function previewSentence(f: FaultForm, targetName: string, layer: 'overlay' | 'config' = 'overlay', names: Names = {}): string {
  let traffic: string;
  if (f.traffic === 'custom') {
    const proto = f.protocol === 'any' ? 'any protocol' : f.protocol.toUpperCase();
    const dest = f.destKind === 'any' ? '' : ` to ${describeDestination(destinationOf(f), names)}`;
    traffic = `${proto}${f.ports.trim() ? ' ' + f.ports.trim() : ''}${dest}`;
  } else traffic = trafficPresets[f.traffic]?.label ?? 'All traffic';
  const chips = faultChips(formToFault(f));
  const until = untilText(f, layer);
  const mode = mtuModes.find((m) => m.value === f.mtuMode)?.label.split(' · ')[0];
  if (f.family === 'dns') {
    const d = dnsBody(f.dns);
    return `${targetName} · ${dnsScope(d)} → ${dnsEffect(d)} · ${until}`;
  }
  if (f.family === 'tls') {
    const to = f.tls.traffic.destKind === 'any' ? '' : `to ${describeDestination(destinationOf(f.tls.traffic), names)}`;
    return `${targetName} · ${tlsScope(f.tls, to)} → ${tlsEffect(f.tls.case, f.tls.case === 'intercept' ? f.tls.intercept.rules.length : 0)} · ${until}`;
  }
  if (f.family === 'mtu') return `${targetName} · ${traffic} → packets above ${f.mtuSize} bytes: ${mode} · ${until}`;
  if (f.family === 'tunnel') return `${targetName} · ${directionText[f.direction]} → ${chips.join(', ')} · ${until}`;
  return `${targetName} · ${traffic} · ${directionText[f.direction]} → ${chips.join(', ')} · ${until}`;
}

/**
 * The form's field of a problem the gateway reported, by its JSON pointer: "/fault/jitter",
 * "/fault/upload/latency", "/fault/burst_loss/p", "/fault/mtu/size", "/target", "/faults/<id>/source/device";
 * undefined for the fault as a whole. With `each` (upload and download apart), a download
 * parameter is `download.<field>`.
 */
export function fieldOf(path: string | undefined, each = false): FaultField | undefined {
  const p = path ?? '';
  // the parts of a DNS fault and a TLS case: "/dns/delay", "/tls/ports/0", "/tls/sni/1"
  if (/^\/dns(\/|$)/.test(p)) return dnsFieldOf(p.slice(4));
  if (/^\/tls(\/|$)/.test(p)) return tlsFieldOf(p.slice(4));
  // a remote network's prefix is the target's, not the destination's ("/target/remote_network/cidr")
  if (/\/remote_network(\/|$)/.test(p)) return 'target';
  if (/\/destination(\/|$)/.test(p)) return 'destination';
  if (/\/tunnel(\/|$)/.test(p)) return 'tunnel';
  if (/\/mtu\/mode$/.test(p)) return 'mtuMode';
  if (/\/mtu(\/|$)/.test(p)) return 'mtuSize';
  // the names in the pointer, past the index of a list ("/ports/0")
  const segments = p.split('/').filter((s) => s && !/^\d+$/.test(s));
  const last = segments.at(-1) ?? '';
  const map: Record<string, FaultField> = {
    ports: 'ports',
    port_ranges: 'ports',
    protocol: 'ports',
    name: 'name',
    target: 'target',
    source: 'target',
    device: 'target',
    network: 'target',
    group: 'target',
  };
  if (map[last]) return map[last];
  const field = netemFieldOf(segments);
  if (!field) return undefined;
  return each && segments.includes('download') ? `download.${field}` : field;
}
