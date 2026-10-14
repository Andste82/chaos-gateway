// The impairment parameters as a form (plan §2.5, §2.17 "Add fault"): the fields of one direction
// (latency, jitter, loss and the advanced ones: delay distribution, burst loss, loss correlation,
// rate, queue limit, reorder, duplicate, corrupt, keep order, flapping), the direction they apply
// to (both, one, or each its own set), the checks under the fields and the parameters they become.
// The fault dialog and the profile editor share it (P3-M13-03, P3-M13-04); a tunnel fault takes only
// latency, jitter, loss, burst loss, blackout and flapping.
import type { DelayDistribution, ImpairmentParams, NetemParams } from '../api/generated/model';
import { durationMs } from './format';

/** `each`: upload and download each with their own parameters. */
export type Direction = 'both' | 'upload' | 'download' | 'each';
export type LossModel = 'random' | 'burst';

export interface NetemForm {
  latency: string;
  jitter: string;
  /** empty for the default (uniform) */
  distribution: DelayDistribution | '';
  /** random loss (`loss`, 100 is a blackout) or the Gilbert-Elliott burst model (`burst_loss`) */
  lossModel: LossModel;
  loss: string;
  lossCorrelation: string;
  /** Gilbert-Elliott: good → bad, bad → good, delivered in the bad state, delivered in the good state (%) */
  burstP: string;
  burstR: string;
  burstH: string;
  burstK: string;
  rate: string;
  queueLimit: string;
  reorder: string;
  duplicate: string;
  corrupt: string;
  keepOrder: boolean;
  flapping: boolean;
  /** seconds */
  flapUp: string;
  flapDown: string;
}

export interface ImpairmentForm extends NetemForm {
  direction: Direction;
  /** The download parameters when the direction is `each` (the fields above are the upload's then). */
  download: NetemForm;
}

export type NetemField =
  | 'latency'
  | 'jitter'
  | 'loss'
  | 'lossCorrelation'
  | 'burstP'
  | 'burstR'
  | 'burstH'
  | 'burstK'
  | 'rate'
  | 'queueLimit'
  | 'reorder'
  | 'duplicate'
  | 'corrupt'
  | 'flapUp'
  | 'flapDown';

/** A problem of the form: a field of the first set, or `download.<field>` of the download set (`each`). */
export type ImpairmentField = NetemField | `download.${NetemField}`;

export function emptyNetem(): NetemForm {
  return {
    latency: '',
    jitter: '',
    distribution: '',
    lossModel: 'random',
    loss: '',
    lossCorrelation: '',
    burstP: '',
    burstR: '',
    burstH: '',
    burstK: '',
    rate: '',
    queueLimit: '',
    reorder: '',
    duplicate: '',
    corrupt: '',
    keepOrder: false,
    flapping: false,
    flapUp: '',
    flapDown: '',
  };
}

export function emptyImpairment(): ImpairmentForm {
  return { ...emptyNetem(), direction: 'both', download: emptyNetem() };
}

/** "Start from": the parameters of the built-in profiles of the same name (domain/builtin.go). */
export const presets: { label: string; values: Partial<NetemForm> }[] = [
  { label: 'LTE', values: { latency: '50', jitter: '10', loss: '0.1', lossModel: 'random', rate: '' } },
  { label: 'Bad LTE', values: { latency: '150', jitter: '50', loss: '3', lossModel: 'random', rate: '2 Mbit/s' } },
  { label: 'Satellite', values: { latency: '600', jitter: '30', loss: '1', lossModel: 'random', rate: '' } },
  { label: 'Offline', values: { latency: '', jitter: '', loss: '100', lossModel: 'random', rate: '', flapping: false } },
];

/** The parameters a tunnel fault takes (plan §2.2.1, `domain.tunnelParams`). */
export const tunnelFields: ReadonlySet<NetemField> = new Set(['latency', 'jitter', 'loss', 'burstP', 'burstR', 'burstH', 'burstK', 'flapUp', 'flapDown']);

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
const secondsText = (d: string | undefined) => {
  const ms = durationMs(d);
  return ms === undefined ? '' : String(ms / 1000);
};
/** Seconds as a duration of the API: "20" → "20s", "0.5" → "500ms". */
const seconds = (s: string): string | undefined => {
  const v = num(s);
  if (v === undefined || !Number.isFinite(v) || v <= 0) return undefined;
  return Number.isInteger(v) ? `${v}s` : `${Math.round(v * 1000)}ms`;
};
const pct = (s: string) => {
  const v = num(s);
  return v === undefined || !Number.isFinite(v) ? undefined : `${v}%`;
};

/** The problems of one direction's fields. */
export function netemErrors(p: NetemForm, tunnel = false): Partial<Record<NetemField, string>> {
  const e: Partial<Record<NetemField, string>> = {};
  const shown = (f: NetemField) => !tunnel || tunnelFields.has(f);
  const nonNegative = (field: NetemField, max?: number) => {
    if (!shown(field)) return;
    const v = num(p[field] as string);
    if (v === undefined) return;
    if (!Number.isFinite(v) || v < 0) e[field] = 'A number of 0 or more.';
    else if (max !== undefined && v > max) e[field] = `At most ${max}.`;
  };
  nonNegative('latency', 3_600_000);
  nonNegative('jitter', 3_600_000);
  for (const f of ['reorder', 'duplicate', 'corrupt'] as const) nonNegative(f, 100);
  if (p.lossModel === 'burst') {
    for (const f of ['burstP', 'burstR', 'burstH', 'burstK'] as const) nonNegative(f, 100);
    for (const f of ['burstP', 'burstR'] as const) if (!e[f] && !p[f].trim()) e[f] = 'Needed for burst loss.';
  } else {
    nonNegative('loss', 100);
    nonNegative('lossCorrelation', 100);
  }
  const latency = num(p.latency) ?? 0;
  const jitter = num(p.jitter) ?? 0;
  if (!e.jitter && jitter > 0 && jitter > latency) e.jitter = 'Jitter cannot be larger than the latency.';
  if (shown('reorder') && !e.reorder && (num(p.reorder) ?? 0) > 0 && latency <= 0) e.reorder = 'Reordering needs a latency.';
  if (shown('rate') && p.rate.trim()) {
    const r = parseRate(p.rate);
    if (!r) e.rate = 'A rate like 2 Mbit/s, 500 kbit/s or 64kbit.';
    else if (rateBits(r) < 8) e.rate = 'At least 8 bit/s.';
  }
  if (shown('queueLimit') && p.queueLimit.trim()) {
    const q = Number(p.queueLimit);
    if (!Number.isInteger(q) || q < 1 || q > 1_000_000) e.queueLimit = 'A number of packets from 1 to 1000000.';
  }
  if (p.flapping) {
    for (const f of ['flapUp', 'flapDown'] as const) if (!seconds(p[f])) e[f] = 'A time in seconds above 0.';
    if (!e.flapUp && p.lossModel === 'random' && (num(p.loss) ?? 0) >= 100) e.flapUp = 'Flapping is a timed blackout: set the loss below 100 %.';
  }
  return e;
}

/** The parameters of one direction. A loss of 100 % is a blackout. */
export function netemParams(p: NetemForm, tunnel = false): NetemParams {
  const out: NetemParams = {};
  const loss = p.lossModel === 'random' ? num(p.loss) : undefined;
  if (loss !== undefined && loss >= 100) {
    out.blackout = true;
    return out;
  }
  const latency = num(p.latency);
  const jitter = num(p.jitter);
  if (latency) out.latency = `${latency}ms`;
  if (jitter) out.jitter = `${jitter}ms`;
  if (!tunnel && p.distribution && p.distribution !== 'uniform') out.distribution = p.distribution;
  if (p.lossModel === 'random') {
    if (loss) out.loss = `${loss}%`;
    if (!tunnel && num(p.lossCorrelation)) out.loss_correlation = pct(p.lossCorrelation);
  } else if (p.burstP.trim() || p.burstR.trim()) {
    out.burst_loss = { p: pct(p.burstP) ?? '0%', r: pct(p.burstR) ?? '0%' };
    if (p.burstH.trim()) out.burst_loss.h = pct(p.burstH);
    if (p.burstK.trim()) out.burst_loss.k = pct(p.burstK);
  }
  if (!tunnel) {
    const rate = p.rate.trim() ? parseRate(p.rate) : undefined;
    if (rate) out.rate = rate;
    if (p.queueLimit.trim() && Number.isInteger(Number(p.queueLimit))) out.queue_limit = Number(p.queueLimit);
    for (const k of ['reorder', 'duplicate', 'corrupt'] as const) if (num(p[k])) out[k] = pct(p[k]);
    if (p.keepOrder) out.keep_order = true;
  }
  if (p.flapping) {
    const up = seconds(p.flapUp);
    const down = seconds(p.flapDown);
    if (up && down) out.flapping = { up, down };
  }
  return out;
}

/** Whether a parameter set changes anything (keeping the order alone does not). */
export function netemEffective(p: NetemParams | undefined): boolean {
  if (!p) return false;
  return Object.entries(p).some(([k, v]) => k !== 'keep_order' && k !== 'upload' && k !== 'download' && v !== undefined && v !== false);
}

/** The parameters of the form: flat for both directions, one direction's, or each its own. */
export function impairmentParams(f: ImpairmentForm, tunnel = false): ImpairmentParams {
  const p = netemParams(f, tunnel);
  switch (f.direction) {
    case 'both':
      return p;
    case 'upload':
      return { upload: p };
    case 'download':
      return { download: p };
    case 'each': {
      const d = netemParams(f.download, tunnel);
      // a direction without parameters is unimpaired: it is left out
      return { ...(netemEffective(p) ? { upload: p } : {}), ...(netemEffective(d) ? { download: d } : {}) };
    }
  }
}

/** Whether the parameters change anything at all. */
export function impairmentEffective(i: ImpairmentParams): boolean {
  return netemEffective(i) || netemEffective(i.upload) || netemEffective(i.download);
}

export const NO_PARAMETER = 'Set at least one parameter: latency, loss, a rate, …';

/** The problems of the form, by field; a parameter set that changes nothing is one too (on the latency). */
export function impairmentErrors(f: ImpairmentForm, tunnel = false): Partial<Record<ImpairmentField, string>> {
  const e: Partial<Record<ImpairmentField, string>> = { ...netemErrors(f, tunnel) };
  if (f.direction === 'each') for (const [k, v] of Object.entries(netemErrors(f.download, tunnel))) e[`download.${k as NetemField}`] = v;
  if (!e.latency && !impairmentEffective(impairmentParams(f, tunnel))) e.latency = NO_PARAMETER;
  return e;
}

/** Fills the fields of one direction from its parameters. */
export function netemToForm(p: NetemParams | undefined, into: NetemForm = emptyNetem()): NetemForm {
  if (!p) return into;
  into.latency = msText(p.latency);
  into.jitter = msText(p.jitter);
  into.distribution = p.distribution && p.distribution !== 'uniform' ? p.distribution : '';
  if (p.burst_loss) {
    into.lossModel = 'burst';
    into.burstP = pctText(p.burst_loss.p);
    into.burstR = pctText(p.burst_loss.r);
    into.burstH = pctText(p.burst_loss.h);
    into.burstK = pctText(p.burst_loss.k);
  }
  into.loss = p.blackout ? '100' : pctText(p.loss);
  into.lossCorrelation = pctText(p.loss_correlation);
  into.rate = rateText(p.rate);
  into.queueLimit = p.queue_limit !== undefined ? String(p.queue_limit) : '';
  into.reorder = pctText(p.reorder);
  into.duplicate = pctText(p.duplicate);
  into.corrupt = pctText(p.corrupt);
  into.keepOrder = !!p.keep_order;
  into.flapping = !!p.flapping;
  into.flapUp = secondsText(p.flapping?.up);
  into.flapDown = secondsText(p.flapping?.down);
  return into;
}

const flatOf = (i: ImpairmentParams): NetemParams => {
  const { upload: _u, download: _d, ...flat } = i as ImpairmentParams & Record<string, unknown>;
  return flat as NetemParams;
};

/** Fills the form from parameters: flat, one direction, or upload and download apart. */
export function impairmentToForm(i: ImpairmentParams | undefined, into: ImpairmentForm = emptyImpairment()): ImpairmentForm {
  if (!i) return into;
  if (i.upload && !i.download) {
    into.direction = 'upload';
    netemToForm(i.upload, into);
  } else if (i.download && !i.upload) {
    into.direction = 'download';
    netemToForm(i.download, into);
  } else if (i.upload && i.download) {
    const same = JSON.stringify(i.upload) === JSON.stringify(i.download);
    into.direction = same ? 'both' : 'each';
    netemToForm(i.upload, into);
    if (!same) into.download = netemToForm(i.download);
  } else {
    into.direction = 'both';
    netemToForm(flatOf(i), into);
  }
  return into;
}

/** Whether any advanced field of a set is filled (the dialog opens Advanced for it). */
export function hasAdvanced(p: NetemForm): boolean {
  return !!(
    p.distribution ||
    p.lossModel === 'burst' ||
    p.lossCorrelation ||
    p.rate ||
    p.queueLimit ||
    p.reorder ||
    p.duplicate ||
    p.corrupt ||
    p.keepOrder ||
    p.flapping
  );
}

/** The advanced fields among the problems (the dialog opens Advanced for them). */
export const advancedFields: ReadonlySet<NetemField> = new Set([
  'lossCorrelation',
  'burstP',
  'burstR',
  'burstH',
  'burstK',
  'rate',
  'queueLimit',
  'reorder',
  'duplicate',
  'corrupt',
  'flapUp',
  'flapDown',
]);

/** The field of a parameter by its name in the API's JSON pointer ("burst_loss/p" → burstP). */
export function netemFieldOf(segments: string[]): NetemField | undefined {
  const last = segments.at(-1) ?? '';
  const parent = segments.at(-2) ?? '';
  if (parent === 'burst_loss') return ({ p: 'burstP', r: 'burstR', h: 'burstH', k: 'burstK' } as const)[last as 'p'];
  if (parent === 'flapping') return ({ up: 'flapUp', down: 'flapDown' } as const)[last as 'up'];
  const map: Record<string, NetemField> = {
    latency: 'latency',
    jitter: 'jitter',
    distribution: 'jitter',
    keep_order: 'jitter',
    loss: 'loss',
    blackout: 'loss',
    loss_correlation: 'lossCorrelation',
    burst_loss: 'burstP',
    rate: 'rate',
    queue_limit: 'queueLimit',
    reorder: 'reorder',
    duplicate: 'duplicate',
    corrupt: 'corrupt',
    flapping: 'flapUp',
  };
  return map[last];
}
