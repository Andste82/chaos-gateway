// The self-test "measure this profile" in the UI (plan §2.12, §2.17 "Probes"; M25): the form of a calibration with the checks
// the gateway makes (domain.ValidateCalibrationRequest), the request it becomes, the field a refusal names, and the result in
// words: one line for each of delay, jitter, loss and rate that the parameters ask for.
import type { CalibrationCheck, CalibrationRequest, CalibrationResult } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { durationMs, formatBitRate } from './format';

export const DEFAULT_DURATION = '30s';
export const DEFAULT_PORT = 7;
const MIN_MS = 5_000;
const MAX_MS = 300_000;

export interface CalibrationForm {
  /** What to measure: a profile that is activated on the probe for the run, or the faults that act on the probe now. */
  what: 'profile' | 'current';
  /** The profile: its id or name. */
  profile: string;
  /** The echo server: an IPv4 address. */
  host: string;
  port: string;
  duration: string;
}

export type CalibrationField = 'profile' | 'host' | 'port' | 'duration';

export function emptyCalibrationForm(profile = ''): CalibrationForm {
  return { what: profile === '' ? 'current' : 'profile', profile, host: '', port: '', duration: '' };
}

const ipv4 = /^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$/;

/** The problems of the form, by field; empty when the gateway can run it. */
export function calibrationErrors(f: CalibrationForm): Partial<Record<CalibrationField, string>> {
  const e: Partial<Record<CalibrationField, string>> = {};
  if (f.what === 'profile' && f.profile.trim() === '') e.profile = 'Choose the profile to measure.';
  if (f.host.trim() === '') e.host = 'The echo server is needed.';
  else if (!ipv4.test(f.host.trim())) e.host = 'An IPv4 address.';
  const port = f.port.trim();
  if (port !== '' && !(/^[0-9]+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535)) e.port = 'A port is a number from 1 to 65535.';
  const d = f.duration.trim();
  if (d !== '') {
    const ms = durationMs(d);
    if (ms === undefined || !/^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$/.test(d)) e.duration = 'A duration like 30s or 2m.';
    else if (ms < MIN_MS || ms > MAX_MS) e.duration = 'From 5s to 5m.';
  }
  return e;
}

/** The request the form becomes. */
export function formToCalibrationRequest(f: CalibrationForm): CalibrationRequest {
  const req: CalibrationRequest = { host: f.host.trim() };
  if (f.what === 'profile') req.profile = f.profile.trim();
  if (f.port.trim() !== '') req.port = Number(f.port.trim());
  if (f.duration.trim() !== '') req.duration = f.duration.trim();
  return req;
}

/** The field a refusal of the gateway names (`/host`, `/duration`, `/fault`), or undefined when it names none. */
export function calibrationFieldOf(path: string | undefined): CalibrationField | undefined {
  switch (path) {
    case '/host':
    case '/port':
    case '/duration':
      return path.slice(1) as CalibrationField;
    case '/profile':
    case '/fault':
      return 'profile';
    default:
      return undefined;
  }
}

/** The seconds the run takes, for the line that says the call waits. */
export function calibrationSeconds(f: CalibrationForm): number {
  const ms = f.duration.trim() === '' ? 30_000 : (durationMs(f.duration.trim()) ?? 30_000);
  return Math.round(ms / 1000);
}

const checkLabels: Record<CalibrationCheck['name'], string> = { latency: 'Delay', jitter: 'Jitter', loss: 'Loss', rate: 'Rate' };

export function checkLabel(name: CalibrationCheck['name']): string {
  return checkLabels[name] ?? name;
}

export function verdictTone(r: Pick<CalibrationResult, 'within_tolerance'>): { tone: DotTone; label: string } {
  return r.within_tolerance ? { tone: 'normal', label: 'Within tolerance' } : { tone: 'block', label: 'Out of tolerance' };
}

function ms(v: number | undefined): string {
  return v === undefined || !Number.isFinite(v) ? '—' : `${v >= 100 ? Math.round(v) : v.toFixed(1)} ms`;
}

function pct(v: number | undefined): string {
  return v === undefined || !Number.isFinite(v) ? '—' : `${Number(v.toFixed(2))} %`;
}

export interface CalibrationRow {
  label: string;
  expected: string;
  measured: string;
}

/** What was expected and what was measured, one row for each quantity the result has a value for. */
export function calibrationRows(r: CalibrationResult): CalibrationRow[] {
  const rows: CalibrationRow[] = [];
  const e = r.expected;
  const m = r.measured;
  if (e?.rtt_ms !== undefined || m?.rtt_median_ms !== undefined) rows.push({ label: 'Round trip (median)', expected: ms(e?.rtt_ms), measured: ms(m?.rtt_median_ms) });
  if (m?.baseline_rtt_ms !== undefined) rows.push({ label: 'Unimpaired round trip', expected: '—', measured: ms(m.baseline_rtt_ms) });
  if (m?.rtt_p95_ms !== undefined) rows.push({ label: 'Round trip (95th percentile)', expected: '—', measured: ms(m.rtt_p95_ms) });
  if (e?.loss_percent !== undefined || m?.loss_percent !== undefined) {
    const ci = m?.loss_ci99 && m.loss_ci99.length === 2 ? ` (99 %: ${pct(m.loss_ci99[0])} to ${pct(m.loss_ci99[1])})` : '';
    rows.push({ label: 'Loss', expected: pct(e?.loss_percent), measured: pct(m?.loss_percent) + ci });
  }
  if (e?.rate_bps !== undefined || m?.rate_bps !== undefined) rows.push({ label: 'Throughput', expected: e?.rate_bps !== undefined ? formatBitRate(e.rate_bps) : '—', measured: m?.rate_bps !== undefined ? formatBitRate(m.rate_bps) : '—' });
  return rows;
}

/** One sentence for the result. */
export function calibrationSentence(r: CalibrationResult): string {
  const failed = (r.checks ?? []).filter((c) => !c.ok).map((c) => checkLabel(c.name).toLowerCase());
  if (r.within_tolerance) return 'What the probe measured matches the parameters within the tolerances.';
  return failed.length ? `What the probe measured does not match the parameters: ${failed.join(', ')}.` : 'What the probe measured does not match the parameters.';
}
