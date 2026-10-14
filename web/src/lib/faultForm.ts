// The add/edit fault dialog (plan §2.17 "Add fault"): the form with the three fields of the common
// case (latency, jitter, loss) and the advanced ones, the presets to start from, the checks under
// the fields, the live preview sentence, and the request it becomes — an overlay
// (`POST /overlays`) or a configured fault in a candidate revision.
import type { ConfigFault, FaultBody, NetemParams, OverlayRequest, Protocol, Scope } from '../api/generated/model';
import {
  createOverlayBodyFaultOneDestinationCidrRegExp as cidrRegExp,
} from '../api/generated/zod/overlays/overlays';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { gatewayNow } from './clock';
import { faultChips } from './faults';
import { durationMs, formatDuration, formatHM, formatRemaining } from './format';
import { formatPorts, parsePorts } from './rules';
import { remoteCidrError } from './scopes';

export type Direction = 'both' | 'upload' | 'download';

export interface FaultForm {
  /** `device:<id>`, `group:<id>`, `network:<id>`, `remote_network:<client|link|cidr>:<value>` or `global` */
  target: string;
  /** a key of `trafficPresets`, or `custom` */
  traffic: string;
  protocol: Protocol;
  ports: string;
  /** an address or prefix; empty for any destination */
  destination: string;
  direction: Direction;
  latency: string;
  jitter: string;
  loss: string;
  rate: string;
  reorder: string;
  duplicate: string;
  corrupt: string;
  keepOrder: boolean;
  /** a key of `durations`, `keep` (the edited overlay's remaining time) or `ttl:<its own TTL>` */
  duration: string;
  /** the expiry of the edited overlay, which `keep` keeps; empty for a new fault or one without a TTL */
  keepUntil: string;
  /** the name of a configured fault */
  name: string;
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

export const durations: { value: string; label: string; ttl?: string }[] = [
  { value: '1m', label: '1 min', ttl: '1m' },
  { value: '5m', label: '5 min', ttl: '5m' },
  { value: '15m', label: '15 min', ttl: '15m' },
  { value: '1h', label: '1 hour', ttl: '1h' },
  { value: 'perm', label: 'Until removed' },
];

/** "Start from": the parameters of the built-in profiles of the same name (domain/builtin.go). */
export const presets: { label: string; values: Partial<FaultForm> }[] = [
  { label: 'LTE', values: { latency: '50', jitter: '10', loss: '0.1', rate: '' } },
  { label: 'Bad LTE', values: { latency: '150', jitter: '50', loss: '3', rate: '2 Mbit/s' } },
  { label: 'Satellite', values: { latency: '600', jitter: '30', loss: '1', rate: '' } },
  { label: 'Offline', values: { latency: '', jitter: '', loss: '100', rate: '' } },
];

export function emptyForm(target = 'global'): FaultForm {
  return {
    target,
    traffic: 'all',
    protocol: 'any',
    ports: '',
    destination: '',
    direction: 'both',
    latency: '',
    jitter: '',
    loss: '',
    rate: '',
    reorder: '',
    duplicate: '',
    corrupt: '',
    keepOrder: false,
    duration: '15m',
    keepUntil: '',
    name: '',
  };
}

/** A TTL in words: "30m" → "30 min", "1199s" (a kept remaining time) → "19 min 59 s". */
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
export function durationChoices(f: FaultForm, ownTtl: string | undefined, now: Date): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = [];
  if (f.keepUntil) {
    const left = formatRemaining(new Date(f.keepUntil).getTime() - now.getTime());
    out.push({ value: 'keep', label: `Keep: ${left} left${ownTtl ? ` of ${ttlText(ownTtl)}` : ''}` });
  }
  out.push(...durations.map(({ value, label }) => ({ value, label })));
  if (ownTtl && !durations.some((d) => d.ttl === ownTtl)) out.push({ value: `ttl:${ownTtl}`, label: `${ttlText(ownTtl)} (its own TTL)` });
  return out;
}

/** The remaining time of an expiry as a TTL in whole seconds, at least one. */
export function remainingTtl(until: string, now: Date): string {
  return `${Math.max(1, Math.ceil((new Date(until).getTime() - now.getTime()) / 1000))}s`;
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
  if (r?.cidr) return `remote_network:cidr:${r.cidr}`;
  return 'global';
}

/** "2 Mbit/s", "64kbit", "500 kbit/s" → "2Mbit", "64kbit"; undefined when it is no rate. */
export function parseRate(text: string): string | undefined {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*(bit|kbit|mbit|gbit)(?:\/s)?\s*$/i.exec(text);
  if (!m) return undefined;
  const unit = { bit: 'bit', kbit: 'kbit', mbit: 'Mbit', gbit: 'Gbit' }[m[2]!.toLowerCase()]!;
  return `${m[1]}${unit}`;
}

function rateBits(rate: string): number {
  const m = /^([0-9.]+)(bit|kbit|Mbit|Gbit)$/.exec(rate);
  if (!m) return 0;
  return Number(m[1]) * { bit: 1, kbit: 1e3, Mbit: 1e6, Gbit: 1e9 }[m[2] as 'bit']!;
}

/** "2Mbit" → "2 Mbit/s" (the form's text). */
export function rateText(rate: string | undefined): string {
  const m = rate ? /^([0-9.]+)(bit|kbit|Mbit|Gbit)$/.exec(rate) : null;
  return m ? `${m[1]} ${m[2]}/s` : (rate ?? '');
}

const num = (s: string) => (s.trim() === '' ? undefined : Number(s));
const pctText = (p: string | undefined) => (p ? p.replace(/%$/, '') : '');
const msText = (d: string | undefined) => {
  const ms = durationMs(d);
  return ms === undefined ? '' : String(ms);
};

export type FaultField = 'latency' | 'jitter' | 'loss' | 'rate' | 'reorder' | 'duplicate' | 'corrupt' | 'ports' | 'destination' | 'name' | 'target';

/** The problems of the form, by field; empty when it can be applied. */
export function faultFormErrors(f: FaultForm, layer: 'overlay' | 'config' = 'overlay'): Partial<Record<FaultField, string>> {
  const e: Partial<Record<FaultField, string>> = {};
  const nonNegative = (field: FaultField, max?: number) => {
    const v = num(f[field as keyof FaultForm] as string);
    if (v === undefined) return;
    if (!Number.isFinite(v) || v < 0) e[field] = 'A number of 0 or more.';
    else if (max !== undefined && v > max) e[field] = `At most ${max}.`;
  };
  nonNegative('latency', 3_600_000);
  nonNegative('jitter', 3_600_000);
  for (const p of ['loss', 'reorder', 'duplicate', 'corrupt'] as const) nonNegative(p, 100);
  const latency = num(f.latency) ?? 0;
  const jitter = num(f.jitter) ?? 0;
  if (!e.jitter && jitter > 0 && jitter > latency) e.jitter = 'Jitter cannot be larger than the latency.';
  if (!e.reorder && (num(f.reorder) ?? 0) > 0 && latency <= 0) e.reorder = 'Reordering needs a latency.';
  if (f.rate.trim()) {
    const r = parseRate(f.rate);
    if (!r) e.rate = 'A rate like 2 Mbit/s, 500 kbit/s or 64kbit.';
    else if (rateBits(r) < 8) e.rate = 'At least 8 bit/s.';
  }
  if (f.traffic === 'custom') {
    const p = parsePorts(f.ports);
    if (p.error) e.ports = p.error;
    else if ((p.ports || p.port_ranges) && f.protocol !== 'tcp' && f.protocol !== 'udp') e.ports = 'Ports need the protocol TCP or UDP.';
    if (f.destination.trim() && !cidrRegExp.test(f.destination.trim())) e.destination = 'An IPv4 address or a prefix like 203.0.113.0/24.';
  }
  // the gateway refuses a fault that changes nothing ("the fault sets no parameter"); keeping the
  // packet order alone changes nothing either
  const { keep_order: _keep, ...params } = netem(f);
  if (!e.latency && Object.keys(params).length === 0) e.latency = 'Set at least one parameter: latency, loss, a rate, …';
  if (layer === 'config' && f.name && !nameRegExp.test(f.name)) e.name = 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
  if (!f.target) e.target = 'Choose a target.';
  const remote = remoteCidrError(f.target);
  if (remote) e.target = remote;
  return e;
}

/** The parameters of one direction, from the form. */
function netem(f: FaultForm): NetemParams {
  const p: NetemParams = {};
  const loss = num(f.loss);
  if (loss !== undefined && loss >= 100) {
    p.blackout = true;
    return p;
  }
  const latency = num(f.latency);
  const jitter = num(f.jitter);
  if (latency) p.latency = `${latency}ms`;
  if (jitter) p.jitter = `${jitter}ms`;
  if (loss) p.loss = `${loss}%`;
  const rate = f.rate.trim() ? parseRate(f.rate) : undefined;
  if (rate) p.rate = rate;
  const pct = (s: string) => {
    const v = num(s);
    return v ? `${v}%` : undefined;
  };
  if (pct(f.reorder)) p.reorder = pct(f.reorder);
  if (pct(f.duplicate)) p.duplicate = pct(f.duplicate);
  if (pct(f.corrupt)) p.corrupt = pct(f.corrupt);
  if (f.keepOrder) p.keep_order = true;
  return p;
}

/** The selector of the traffic: protocol, ports and destination. */
function match(f: FaultForm): Pick<FaultBody, 'protocol' | 'ports' | 'port_ranges' | 'destination'> {
  if (f.traffic !== 'custom') {
    const t = trafficPresets[f.traffic] ?? trafficPresets.all!;
    return t.protocol === 'any' ? {} : { protocol: t.protocol, ports: [...t.ports] };
  }
  const out: Pick<FaultBody, 'protocol' | 'ports' | 'port_ranges' | 'destination'> = {};
  if (f.protocol !== 'any') out.protocol = f.protocol;
  const p = parsePorts(f.ports);
  if (p.ports) out.ports = p.ports;
  if (p.port_ranges) out.port_ranges = p.port_ranges;
  if (f.destination.trim()) out.destination = { cidr: f.destination.trim() };
  return out;
}

/** The fault body of the form: flat parameters for both directions, or only one direction's. */
export function formToFault(f: FaultForm): FaultBody {
  const params = netem(f);
  const body: FaultBody = { ...match(f) };
  if (f.direction === 'both') Object.assign(body, params);
  else body[f.direction] = params;
  return body;
}

/**
 * The overlay of the form. `keep` sends the remaining time of the edited overlay as its TTL (the
 * gateway starts a written overlay's TTL over), on the gateway's clock.
 */
export function formToOverlay(f: FaultForm, now: Date = gatewayNow()): OverlayRequest {
  const ttl = f.duration === 'keep' && f.keepUntil ? remainingTtl(f.keepUntil, now) : durationTtl(f.duration);
  return { target: scopeOf(f.target), fault: formToFault(f), ...(ttl ? { ttl } : {}) };
}

/** A configured fault from the form; the fields the form does not show are kept from `base`. */
export function formToConfigFault(f: FaultForm, base?: ConfigFault): ConfigFault {
  const keep: ConfigFault = {};
  if (base?.description) keep.description = base.description;
  if (base?.enabled === false) keep.enabled = false;
  return { ...keep, ...(f.name ? { name: f.name } : {}), source: scopeOf(f.target), ...formToFault(f) };
}

/** The form of an existing fault (an overlay's or a configured one). */
export function faultToForm(fault: FaultBody | undefined, target: Scope | undefined, extra: { ttl?: string; expiresAt?: string; name?: string } = {}): FaultForm {
  const f = emptyForm(targetOf(target));
  if (!fault) return f;
  let p: NetemParams = fault;
  if (fault.upload && !fault.download) {
    f.direction = 'upload';
    p = fault.upload;
  } else if (fault.download && !fault.upload) {
    f.direction = 'download';
    p = fault.download;
  } else if (fault.upload) {
    p = fault.upload;
  }
  f.latency = msText(p.latency);
  f.jitter = msText(p.jitter);
  f.loss = p.blackout ? '100' : pctText(p.loss);
  f.rate = rateText(p.rate);
  f.reorder = pctText(p.reorder);
  f.duplicate = pctText(p.duplicate);
  f.corrupt = pctText(p.corrupt);
  f.keepOrder = !!p.keep_order;
  const preset = Object.entries(trafficPresets).find(
    ([, t]) =>
      !fault.destination &&
      !fault.port_ranges?.length &&
      (fault.protocol ?? 'any') === t.protocol &&
      (fault.ports ?? []).join(',') === t.ports.join(','),
  );
  if (preset) f.traffic = preset[0];
  else {
    f.traffic = 'custom';
    f.protocol = fault.protocol ?? 'any';
    f.ports = formatPorts(fault);
    f.destination = fault.destination?.cidr ?? '';
  }
  // an overlay with a TTL keeps its remaining time unless another duration is chosen (P3-M13-06)
  const ttl = extra.ttl ? (durations.find((d) => d.ttl === extra.ttl)?.value ?? `ttl:${extra.ttl}`) : undefined;
  f.keepUntil = extra.expiresAt ?? '';
  f.duration = f.keepUntil ? 'keep' : (ttl ?? 'perm');
  f.name = extra.name ?? '';
  return f;
}

/** The parameters the form leaves out that an existing fault has (they would be lost on save). */
export function hiddenParameters(fault: FaultBody | undefined): string[] {
  if (!fault) return [];
  const out: string[] = [];
  const both = fault.upload && fault.download;
  if (both && JSON.stringify(fault.upload) !== JSON.stringify(fault.download)) out.push('different upload and download parameters');
  const ps = [fault, fault.upload, fault.download].filter(Boolean) as NetemParams[];
  if (ps.some((p) => p.burst_loss)) out.push('burst loss');
  if (ps.some((p) => p.flapping)) out.push('flapping');
  if (ps.some((p) => p.distribution && p.distribution !== 'uniform')) out.push('the delay distribution');
  if (ps.some((p) => p.queue_limit)) out.push('the queue limit');
  if (ps.some((p) => p.loss_correlation)) out.push('the loss correlation');
  if (fault.destination && !fault.destination.cidr) out.push('the destination');
  return out;
}

const directionText: Record<Direction, string> = { both: 'upload & download', upload: 'upload only', download: 'download only' };

/** The live preview: "ESP32-42 · MQTT · TCP 8883 · upload & download → ↑↓ 200 ms ±50, 5 % loss · expires after 5 min". */
export function previewSentence(f: FaultForm, targetName: string, layer: 'overlay' | 'config' = 'overlay'): string {
  let traffic: string;
  if (f.traffic === 'custom') {
    const proto = f.protocol === 'any' ? 'any protocol' : f.protocol.toUpperCase();
    traffic = `${proto}${f.ports.trim() ? ' ' + f.ports.trim() : ''}${f.destination.trim() ? ' to ' + f.destination.trim() : ''}`;
  } else traffic = trafficPresets[f.traffic]?.label ?? 'All traffic';
  const chips = faultChips(formToFault(f));
  const until =
    layer === 'config'
      ? 'configured until removed (after preview and apply)'
      : f.duration === 'keep' && f.keepUntil
        ? `expires at ${formatHM(f.keepUntil)} as before`
        : f.duration === 'perm'
          ? 'until removed'
          : `expires after ${durations.find((d) => d.value === f.duration)?.label ?? ttlText(durationTtl(f.duration) ?? f.duration)}`;
  return `${targetName} · ${traffic} · ${directionText[f.direction]} → ${chips.join(', ')} · ${until}`;
}

/**
 * The form's field of a problem the gateway reported, by its JSON pointer: "/fault/jitter",
 * "/fault/upload/latency", "/target", "/faults/<id>/source/device"; undefined for the fault as a whole.
 */
export function fieldOf(path: string | undefined): FaultField | undefined {
  // a remote network's prefix is the target's, not the destination's ("/target/remote_network/cidr")
  if (/\/remote_network(\/|$)/.test(path ?? '')) return 'target';
  // the last name in the pointer, past the index of a list ("/ports/0")
  const last = (path ?? '').split('/').filter((s) => s && !/^\d+$/.test(s)).at(-1) ?? '';
  const map: Record<string, FaultField> = {
    latency: 'latency',
    jitter: 'jitter',
    loss: 'loss',
    rate: 'rate',
    reorder: 'reorder',
    duplicate: 'duplicate',
    corrupt: 'corrupt',
    ports: 'ports',
    port_ranges: 'ports',
    destination: 'destination',
    cidr: 'destination',
    name: 'name',
    target: 'target',
    source: 'target',
    device: 'target',
    network: 'target',
    group: 'target',
  };
  return map[last];
}
