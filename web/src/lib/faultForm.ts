// The add/edit fault dialog (plan §2.17 "Add fault"): the form with the three fields of the common
// case (latency, jitter, loss) and the advanced ones, the presets to start from, the checks under
// the fields, the live preview sentence, and the request it becomes — an overlay
// (`POST /overlays`) or a configured fault in a candidate revision.
import type { ConfigFault, FaultBody, NetemParams, OverlayRequest, Protocol, Scope } from '../api/generated/model';
import {
  createOverlayBodyFaultOneDestinationCidrRegExp as cidrRegExp,
} from '../api/generated/zod/overlays/overlays';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { faultChips } from './faults';
import { durationMs } from './format';
import { formatPorts, parsePorts } from './rules';

export type Direction = 'both' | 'upload' | 'download';

export interface FaultForm {
  /** `device:<id>`, `group:<id>`, `network:<id>` or `global` */
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
  /** a key of `durations` */
  duration: string;
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
    name: '',
  };
}

export function scopeOf(target: string): Scope {
  if (target === 'global' || !target) return { global: true };
  const [kind, id] = target.split(':', 2) as [string, string];
  return { [kind]: id } as Scope;
}

export function targetOf(s: Scope | undefined): string {
  if (!s || s.global) return 'global';
  if (s.device) return `device:${s.device}`;
  if (s.group) return `group:${s.group}`;
  if (s.network) return `network:${s.network}`;
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

export function formToOverlay(f: FaultForm): OverlayRequest {
  const ttl = durations.find((d) => d.value === f.duration)?.ttl;
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
export function faultToForm(fault: FaultBody | undefined, target: Scope | undefined, extra: { ttl?: string; name?: string } = {}): FaultForm {
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
  const ttl = extra.ttl ? durations.find((d) => d.ttl === extra.ttl)?.value : undefined;
  f.duration = extra.ttl ? (ttl ?? '15m') : 'perm';
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
      : f.duration === 'perm'
        ? 'until removed'
        : `expires after ${durations.find((d) => d.value === f.duration)?.label ?? f.duration}`;
  return `${targetName} · ${traffic} · ${directionText[f.direction]} → ${chips.join(', ')} · ${until}`;
}

/**
 * The form's field of a problem the gateway reported, by its JSON pointer: "/fault/jitter",
 * "/fault/upload/latency", "/target", "/faults/<id>/source/device"; undefined for the fault as a whole.
 */
export function fieldOf(path: string | undefined): FaultField | undefined {
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
