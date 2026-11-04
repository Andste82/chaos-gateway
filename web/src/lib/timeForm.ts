// The form of a time fault (plan §2.18, §2.17 "Add fault", M34): what the gateway's NTP server answers instead of the correct
// time (a shifted time, an absolute time, a drift, a leap second, an answer that says the server is not to be trusted or
// does not answer at all, an invalid answer), the server the device asked (an address, a prefix or a host name; none: every
// server), the presets to start from, the checks under the fields and the `time` part of the request it becomes. The "Add
// fault" dialog (an overlay of the kind `time`) and the step dialog (the step type `time`) use the same fields, the same
// checks and the same body.
import type { TimeFault } from '../api/generated/model';
import { createOverlayBodyTimeTwoOffsetMax as offsetMax, createOverlayBodyTimeTwoOffsetRegExp as offsetRegExp } from '../api/generated/zod/overlays/overlays';
import { instantText } from './format';
import { parsePorts } from './rules';
import { destinationOf, trafficErrors, trafficFromMatch, trafficMatch, type TrafficField, type TrafficForm } from './trafficForm';

export type TimeAnswer = NonNullable<TimeFault['answer']>;
export type TimeKissCode = NonNullable<TimeFault['kiss_code']>;
export type TimeLeap = NonNullable<TimeFault['leap']>;
/** What the answer `normal` does to the time: nothing, adds an offset, or serves an instant. */
export type TimeShift = 'none' | 'offset' | 'absolute';

export interface TimeForm {
  answer: TimeAnswer;
  shift: TimeShift;
  /** A signed span: `+400d`, `-90s`, `-1y2d`. */
  offset: string;
  /** An RFC 3339 instant with a time zone: `1970-01-01T00:00:00Z`. */
  absolute: string;
  /** Parts per million, empty for none; positive runs fast, negative slow. */
  skew: string;
  /** `''`: no leap indicator. */
  leap: '' | TimeLeap;
  kissCode: TimeKissCode;
  /** The server the device asked: ports and a destination (an address, a prefix or a host name). The protocol is UDP and stays as the fault had it. */
  traffic: TrafficForm;
}

export type TimeField = 'timeAnswer' | 'timeOffset' | 'timeAbsolute' | 'timeSkew' | 'timeLeap' | 'timeKiss' | 'timeChange' | TrafficField;

export interface TimeAnswerInfo {
  value: TimeAnswer;
  label: string;
  group: 'Time' | 'Answer' | 'Invalid answer';
  hint: string;
}

export const timeAnswers: TimeAnswerInfo[] = [
  { value: 'normal', label: 'A time of your choice', group: 'Time', hint: 'The server answers with a time you set: the correct time plus an offset, or a fixed instant; a drift and a leap indicator can be added.' },
  { value: 'unsynchronized', label: 'Unsynchronized · leap indicator 3, stratum 16', group: 'Answer', hint: 'A correct client ignores the server; a careless one takes its time.' },
  { value: 'kiss_of_death', label: 'Kiss-of-death · stratum 0', group: 'Answer', hint: 'RATE: the client must poll less often. DENY and RSTR: it must stop using the server.' },
  { value: 'silence', label: 'Silence · no answer', group: 'Answer', hint: 'The query is not answered but counted: no time at boot, fallback servers, retry back-off. Unlike a drop rule it acts only on NTP.' },
  { value: 'origin_mismatch', label: 'Origin timestamp wrong', group: 'Invalid answer', hint: 'The origin timestamp is not the one the client sent. A correct client discards the reply (it is its protection against a spoofed answer).' },
  { value: 'zero_transmit', label: 'Transmit timestamp zero', group: 'Invalid answer', hint: 'The reply has a transmit timestamp of zero. A correct client discards it.' },
  { value: 'wrong_mode', label: 'Wrong mode', group: 'Invalid answer', hint: 'The mode of the reply is not 4 (server). A correct client discards it.' },
];

export const timeAnswerGroups = ['Time', 'Answer', 'Invalid answer'] as const;

export const timeKissCodes: { value: TimeKissCode; label: string }[] = [
  { value: 'RATE', label: 'RATE · poll less often' },
  { value: 'DENY', label: 'DENY · stop using the server' },
  { value: 'RSTR', label: 'RSTR · access restricted' },
];

export function emptyTime(): TimeForm {
  return {
    answer: 'normal',
    shift: 'offset',
    offset: '+1y',
    absolute: '2038-01-19T03:09:07Z',
    skew: '',
    leap: '',
    kissCode: 'RATE',
    traffic: { protocol: 'any', ports: '', destKind: 'any', destination: '' },
  };
}

export interface TimePreset {
  id: string;
  label: string;
  hint: string;
  /** What the preset sets; the server selection stays as it is. */
  set: Partial<Omit<TimeForm, 'traffic'>>;
}

/** The starting points of the plan (§2.18): the presets of the dialog, the built-in profiles `time-*` and the API examples are the same faults. */
export const timePresets: TimePreset[] = [
  { id: 'plus-1y', label: '+1 year', hint: 'A certificate that is not valid yet, a token from the future.', set: { answer: 'normal', shift: 'offset', offset: '+1y', skew: '', leap: '' } },
  { id: 'minus-1d', label: '-1 day', hint: 'A certificate that has expired, a token that has not been issued yet.', set: { answer: 'normal', shift: 'offset', offset: '-1d', skew: '', leap: '' } },
  { id: '1970', label: '1970', hint: 'A device that lost its clock.', set: { answer: 'normal', shift: 'absolute', absolute: '1970-01-01T00:00:00Z', skew: '', leap: '' } },
  { id: '2038', label: '2038', hint: 'Five minutes before a signed 32-bit time_t overflows (2038-01-19 03:14:07 UTC).', set: { answer: 'normal', shift: 'absolute', absolute: '2038-01-19T03:09:07Z', skew: '', leap: '' } },
  { id: 'ntp-era-1', label: 'NTP era 1', hint: 'Five minutes before the 32-bit seconds of NTP roll over (2036-02-07 06:28:16 UTC).', set: { answer: 'normal', shift: 'absolute', absolute: '2036-02-07T06:23:16Z', skew: '', leap: '' } },
  { id: 'leap-second', label: 'Leap second', hint: 'Five minutes before the end of 2016-12-31 with a second inserted (23:59:60).', set: { answer: 'normal', shift: 'absolute', absolute: '2016-12-31T23:55:00Z', skew: '', leap: 'insert' } },
  { id: 'unsynchronized', label: 'Unsynchronized', hint: 'Leap indicator 3 and stratum 16.', set: { answer: 'unsynchronized' } },
  { id: 'kiss-of-death', label: 'Kiss-of-death', hint: 'Stratum 0 with the code DENY.', set: { answer: 'kiss_of_death', kissCode: 'DENY' } },
];

/** The form with a preset applied: the preset's fields replace the form's, the selection of the server stays. */
export function applyTimePreset(f: TimeForm, id: string): TimeForm {
  const p = timePresets.find((x) => x.id === id);
  return p ? { ...f, ...p.set, traffic: f.traffic } : f;
}

/** The ports 53, 67 and 68 are the gateway's own DNS and DHCP services: their traffic would end in the NTP server. */
const reservedPorts = [53, 67, 68];

/** The range of a signed 64-bit count of nanoseconds without its most negative value (the gateway keeps the range symmetric): about 292 years. */
const maxOffsetNanos = (1n << 63n) - 1n;

const unitNanos: Record<string, bigint> = {
  ms: 1_000_000n,
  s: 1_000_000_000n,
  m: 60_000_000_000n,
  h: 3_600_000_000_000n,
  d: 86_400_000_000_000n,
  w: 7n * 86_400_000_000_000n,
  y: 365n * 86_400_000_000_000n,
};

/**
 * The span of an offset in whole nanoseconds, or undefined for a text that is none. It is exact like the gateway's reading of the
 * span (a number with a fraction is a rational, a fraction of a nanosecond is cut off towards zero), so the form and the gateway
 * refuse the same offsets: `9223372036.854775807s` is the last one it takes.
 */
export function offsetNanos(text: string): bigint | undefined {
  const t = text.trim();
  if (t.length > offsetMax || !offsetRegExp.test(t)) return undefined;
  // every number is scaled to the longest fraction, then the sum is one division
  const parts = [...t.matchAll(/([0-9]+)(?:\.([0-9]+))?(ms|s|m|h|d|w|y)/g)];
  const digits = Math.max(0, ...parts.map((m) => (m[2] ?? '').length));
  let total = 0n;
  for (const m of parts) total += BigInt(m[1]! + (m[2] ?? '').padEnd(digits, '0')) * unitNanos[m[3]!]!;
  total /= 10n ** BigInt(digits);
  return t.startsWith('-') ? -total : total;
}

/** The span of an offset in seconds, or undefined for a text that is none. */
export function offsetSeconds(text: string): number | undefined {
  const ns = offsetNanos(text);
  return ns === undefined ? undefined : Number(ns) / 1e9;
}

const instantRegExp = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/;

/**
 * The milliseconds since 1970 of an RFC 3339 instant, or NaN when the text is none. `Date.parse` takes a 30th of February and the
 * hour 24 (it rolls them over); the gateway's parser refuses them, so the fields are checked against the calendar first.
 */
function parseInstant(text: string): number {
  const m = instantRegExp.exec(text);
  if (!m) return NaN;
  const [year, month, day, hour, minute, second, zoneHour, zoneMinute] = m.slice(1).map((x) => (x === undefined ? 0 : Number(x))) as [number, number, number, number, number, number, number, number];
  const leapYear = (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
  const monthDays = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1];
  if (monthDays === undefined || day < 1 || day > monthDays || hour > 23 || minute > 59 || second > 59 || zoneHour > 23 || zoneMinute > 59) return NaN;
  return Date.parse(text);
}
/** The beginning of the NTP era 0: an earlier instant has no NTP timestamp. */
const ntpEpochMs = Date.UTC(1900, 0, 1);

/** The problems of the form, by field; empty when the fault can be sent. */
export function timeErrors(f: TimeForm): Partial<Record<TimeField, string>> {
  // the ports of a time fault need no protocol: the protocol is UDP
  const e: Partial<Record<TimeField, string>> = { ...trafficErrors({ ...f.traffic, protocol: 'udp' }) };
  if (f.traffic.destKind === 'network' || f.traffic.destKind === 'uplink') {
    e.destination = 'The destination of a time fault is the server the device asked: an address, a prefix or a host name.';
  }
  const p = parsePorts(f.traffic.ports);
  const reserved = reservedPorts.find((port) => (p.ports ?? []).includes(port) || (p.port_ranges ?? []).some((r) => r.from <= port && port <= r.to));
  if (!e.ports && reserved !== undefined) e.ports = `Port ${reserved} is used by the gateway's own DNS and DHCP services: their traffic would end in the NTP server.`;

  if (f.answer === 'kiss_of_death' && !timeKissCodes.some((k) => k.value === f.kissCode)) e.timeKiss = 'Choose RATE, DENY or RSTR.';
  if (f.answer !== 'normal') return e;

  if (f.shift === 'offset') {
    const ns = offsetNanos(f.offset);
    if (ns === undefined) e.timeOffset = 'A signed span such as +400d, -90s or -1y2d, with the units ms, s, m, h, d, w or y.';
    else if (ns > maxOffsetNanos || ns < -maxOffsetNanos) e.timeOffset = 'About 292 years either way at most: use an absolute time for a time further away.';
  }
  if (f.shift === 'absolute') {
    const a = f.absolute.trim();
    const ms = parseInstant(a);
    if (Number.isNaN(ms)) e.timeAbsolute = 'An instant with a time zone, such as 1970-01-01T00:00:00Z.';
    else if (ms < ntpEpochMs) e.timeAbsolute = 'An NTP timestamp starts at 1900-01-01T00:00:00Z: an earlier time cannot be served.';
  }
  const skew = f.skew.trim();
  if (skew) {
    if (!/^[+-]?\d+(\.\d+)?$/.test(skew)) e.timeSkew = 'Parts per million, such as 500 or -1000.';
    else if (Math.abs(Number(skew)) > 1_000_000) e.timeSkew = 'Within ±1,000,000 ppm; -1000000 is a clock that stands still.';
  }
  // a fault must change something; "+0s" is the way to say "the correct time" for a server a wider fault would otherwise hit
  if (f.shift === 'none' && !skew && !f.leap) e.timeChange = 'Give the fault an offset, an absolute time, a drift or a leap indicator, or choose an answer.';
  return e;
}

/** The time fault of the form: only the fields its answer uses (the gateway refuses the others). */
export function timeBody(f: TimeForm): TimeFault {
  const match = trafficMatch({ ...f.traffic, protocol: 'any' });
  // ports need the protocol UDP (the selector's rule); the protocol is no field of the form
  const out: TimeFault = { ...match, ...(match.ports || match.port_ranges ? { protocol: 'udp' as const } : {}) };
  if (f.answer !== 'normal') {
    out.answer = f.answer;
    if (f.answer === 'kiss_of_death') out.kiss_code = f.kissCode;
    return out;
  }
  if (f.shift === 'offset') out.offset = f.offset.trim();
  if (f.shift === 'absolute') out.absolute = f.absolute.trim();
  if (f.skew.trim()) out.skew_ppm = Number(f.skew.trim());
  if (f.leap) out.leap = f.leap;
  return out;
}

/** The form of a time fault. */
export function timeToForm(t: TimeFault | undefined): TimeForm {
  const f = emptyTime();
  if (!t) return f;
  f.traffic = { ...trafficFromMatch(t), protocol: t.protocol ?? 'any' };
  f.answer = t.answer ?? 'normal';
  f.shift = t.offset !== undefined ? 'offset' : t.absolute !== undefined ? 'absolute' : 'none';
  if (t.offset !== undefined) f.offset = t.offset;
  if (t.absolute !== undefined) f.absolute = t.absolute;
  f.skew = t.skew_ppm !== undefined ? String(t.skew_ppm) : '';
  f.leap = t.leap ?? '';
  if (t.kiss_code) f.kissCode = t.kiss_code;
  return f;
}

/** What the fault does, in words: "the time plus +400d", "the time 1970-01-01 00:00:00 UTC, drifting 500 ppm", "kiss-of-death RATE". */
export function timeEffect(t: TimeFault): string {
  switch (t.answer ?? 'normal') {
    case 'unsynchronized':
      return 'an unsynchronized server (leap indicator 3, stratum 16)';
    case 'kiss_of_death':
      return `a kiss-of-death${t.kiss_code ? ` ${t.kiss_code}` : ''} (stratum 0)`;
    case 'silence':
      return 'no answer (the query is counted)';
    case 'origin_mismatch':
      return 'an answer with the wrong origin timestamp';
    case 'zero_transmit':
      return 'an answer with a transmit timestamp of zero';
    case 'wrong_mode':
      return 'an answer with the wrong mode';
  }
  const parts: string[] = [];
  if (t.offset !== undefined) parts.push(`the correct time ${/^[+-]/.test(t.offset) ? t.offset : `+${t.offset}`}`);
  else if (t.absolute !== undefined) parts.push(`the time ${instantText(t.absolute)}`);
  else parts.push('the correct time');
  if (t.skew_ppm !== undefined) parts.push(`drifting ${t.skew_ppm} ppm`);
  if (t.leap) parts.push(`leap second ${t.leap === 'insert' ? 'inserted' : 'deleted'}`);
  return parts.join(', ');
}

/** The queries the fault applies to, in words: "NTP queries to time.google.com", "NTP queries to every server", "NTP queries to UDP 12300". */
export function timeScope(f: TimeForm): string {
  const d = destinationOf(f.traffic);
  const target = d?.hostname ?? d?.cidr;
  const ports = trafficMatch(f.traffic);
  const portText = [...(ports.ports ?? []).map(String), ...(ports.port_ranges ?? []).map((r) => `${r.from}-${r.to}`)].join(', ');
  return `NTP queries to ${target ?? 'every server'}${portText ? ` on UDP ${portText}` : ''}`;
}

/** The field of the form a problem the gateway reported names, by its pointer below the fault ("/offset", "/destination/cidr", "/ports/0"). */
export function timeFieldOf(path: string | undefined): TimeField | undefined {
  const segments = (path ?? '').split('/').filter(Boolean);
  switch (segments[0]) {
    case 'offset':
      return 'timeOffset';
    case 'absolute':
      return 'timeAbsolute';
    case 'skew_ppm':
      return 'timeSkew';
    case 'leap':
      return 'timeLeap';
    case 'answer':
      return 'timeAnswer';
    case 'kiss_code':
      return 'timeKiss';
    case 'destination':
      return 'destination';
    case 'ports':
    case 'port_ranges':
    case 'protocol':
      return 'ports';
    // the fault as a whole ("a time fault changes something")
    case undefined:
      return 'timeChange';
    default:
      return undefined;
  }
}
