// Captures in the UI (plan §2.11, §2.17 "Captures"; M23a): the state of a capture in words and tones, its scope in
// words, the storage against the quota and the free-space limit, the device a capture belongs to, and the form of
// "New capture" with the checks the gateway makes (the limits of plan §2.11) and the request it becomes.
import type { Capture, CaptureRequest, CaptureScope, CaptureStorage, Scope, WireGuardNetwork } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { describeMatch, describeScope, type Names } from './faults';
import { scopeOf } from './faultForm';
import { durationMs } from './format';
import { emptyTraffic, trafficErrors, trafficFieldOf, trafficMatch, type TrafficForm } from './trafficForm';

// ---- the state of a capture

export interface CaptureState {
  /** The state in a word or two, never by color alone. */
  label: string;
  tone: DotTone;
  /** Why it ended, in a sentence; empty while it runs and after a stop by a user. */
  hint: string;
  /** It ended before its time: a quota that stopped it, a volume that ran low, a failure. What it recorded stays. */
  early: boolean;
}

/**
 * What a capture is doing, or why it ended. A stop by the size limit with a message is the quota (the capture
 * reached the space left in it, or the captures together passed it): the limit the user chose has no message.
 */
export function captureState(c: Pick<Capture, 'status' | 'stop_reason' | 'message'>): CaptureState {
  if (c.status === 'running') return { label: 'Running', tone: 'normal', hint: 'Recording; the file can be downloaded while it runs.', early: false };
  const message = c.message ?? '';
  if (c.status === 'failed') return { label: 'Failed', tone: 'block', hint: message || 'The capture failed.', early: true };
  switch (c.stop_reason) {
    case 'disk_low':
      return { label: 'Stopped · disk low', tone: 'block', hint: message || 'The volume of the captures has less than 10 % free space.', early: true };
    case 'size':
      return message
        ? { label: 'Stopped · quota', tone: 'block', hint: message, early: true }
        : { label: 'Ended · size limit', tone: 'offline', hint: 'The capture reached its size limit.', early: false };
    case 'duration':
      return { label: 'Ended · duration', tone: 'offline', hint: 'The capture ran for its duration.', early: false };
    case 'run_end':
      return { label: 'Ended with its run', tone: 'offline', hint: 'The run it belongs to ended.', early: false };
    case 'error':
      return { label: 'Failed', tone: 'block', hint: message || 'The capture failed.', early: true };
    default:
      return { label: 'Stopped', tone: 'offline', hint: '', early: false };
  }
}

/** A capture that records now (it can be stopped, not deleted). */
export function isRunning(c: Pick<Capture, 'status'>): boolean {
  return c.status === 'running';
}

// ---- sizes

const sizeUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];

/** A size in the binary units of the API: "130 KiB", "5 GiB". */
export function formatSize(bytes: number | undefined | null): string {
  if (bytes === undefined || bytes === null || !Number.isFinite(bytes)) return '—';
  let v = bytes;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < sizeUnits.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = i === 0 || v >= 100 ? 0 : 1;
  return `${v.toFixed(digits)} ${sizeUnits[i]}`;
}

const sizeRegExp = /^([0-9]+)(B|KiB|MiB|GiB|TiB)$/;

/** The bytes of a size like `512MiB`; undefined when it is not one (the gateway's `ByteSize`). */
export function parseSize(text: string): number | undefined {
  const m = sizeRegExp.exec(text.trim().replace(/\s+/g, ''));
  if (!m) return undefined;
  return Number(m[1]) * 1024 ** sizeUnits.indexOf(m[2]!);
}

/** The least a capture file or a ring buffer's file may be (`invalid_capture_limit`). */
export const minFileSize = 64 * 1024;
export const defaultSnaplen = 262144;

// ---- the storage

export interface StorageView {
  /** What the captures take of the quota, in percent (at most 100 for the bar). */
  usedPercent: number;
  usedText: string;
  quotaText: string;
  /** The free space of the volume, or undefined when the gateway cannot read it. */
  freeText?: string;
  limitText: string;
  /** The volume is below the limit: new captures are refused and running ones stop. */
  low: boolean;
  /** The captures use the whole quota: the next one deletes the oldest stopped captures, or is refused. */
  full: boolean;
  runningText: string;
}

export function storageView(s: CaptureStorage): StorageView {
  const percent = s.quota_bytes > 0 ? (s.used_bytes * 100) / s.quota_bytes : 0;
  const pct = (n: number) => `${n.toFixed(n >= 10 || Number.isInteger(n) ? 0 : 1)} %`;
  return {
    usedPercent: Math.min(100, Math.round(percent)),
    usedText: formatSize(s.used_bytes),
    quotaText: formatSize(s.quota_bytes),
    ...(s.free_percent !== undefined ? { freeText: `${pct(s.free_percent)} free${s.volume_free_bytes !== undefined ? ` (${formatSize(s.volume_free_bytes)})` : ''}` } : {}),
    limitText: pct(s.min_free_percent),
    low: s.disk_low,
    full: s.used_bytes >= s.quota_bytes,
    runningText: `${s.running} of ${s.max_running} running`,
  };
}

// ---- scope in words, and the device a capture belongs to

/** The scope of a capture: "Device ESP32-42", "Network IoT", "Uplink", "Group sensors → TCP 8883". */
export function describeCaptureScope(scope: CaptureScope | undefined, names: Names = {}): string {
  if (!scope) return '';
  if (scope.uplink) return 'Uplink';
  if (scope.device) return `Device ${names.device?.(scope.device) ?? scope.device}`;
  if (scope.network) return `Network ${names.network?.(scope.network) ?? scope.network}`;
  if (scope.selector) {
    const { source, ...match } = scope.selector;
    return ['Selector', describeScope(source as Scope, names), describeMatch(match, names)].filter(Boolean).join(' ');
  }
  return '';
}

/**
 * The capture is of the device: its scope is the device, or a selector whose source is the device. The scope names the
 * device as the request had it, by UUID or by name.
 */
export function isDeviceCapture(c: Pick<Capture, 'scope'>, device: { id: string; name: string }): boolean {
  const ref = c.scope.device ?? c.scope.selector?.source.device;
  return ref !== undefined && (ref === device.id || ref.toLowerCase() === device.name.toLowerCase());
}

/**
 * Whether the gateway records a network: never a WireGuard network of the role management, the admin's tunnel to the
 * control plane (it refuses the capture with `management_network`, P4-AUDIT-16), so the dialog does not offer it.
 */
export function isRecordable(n: { config: { type?: string } }): boolean {
  return !(n.config.type === 'wireguard' && (n.config as WireGuardNetwork).role === 'management');
}

/**
 * Whether the gateway records a device: not a client of a management WireGuard network, since its packets are the
 * admin's tunnel too and the gateway refuses the capture as for the network (P5-AUDIT-31). A device whose network is
 * not known is offered; the gateway decides.
 */
export function isRecordableDevice(d: { network?: string }, networks: { id: string; config: { type?: string } }[] | undefined): boolean {
  const n = d.network ? networks?.find((x) => x.id === d.network) : undefined;
  return !n || isRecordable(n);
}

/** The URL of the capture's file, or of its TLS key log. */
export function downloadUrl(id: string, part: 'capture' | 'keylog' = 'capture'): string {
  return `/api/v1/captures/${id}/download${part === 'keylog' ? '?part=keylog' : ''}`;
}

// ---- the form of "New capture"

export type ScopeKind = 'device' | 'network' | 'selector' | 'uplink';
export type LimitKind = 'size' | 'ring';

export interface CaptureForm {
  name: string;
  kind: ScopeKind;
  /** The device or the network, by UUID. */
  device: string;
  network: string;
  /** The source of a selector, as `faultForm.targetOf` writes it: `global`, `network:<id>`, `group:<id>`, `device:<id>`. */
  source: string;
  traffic: TrafficForm;
  format: 'pcapng' | 'pcap';
  /** Bytes kept of each packet; empty keeps the whole packet. */
  snaplen: string;
  /** A Go duration; empty runs until stopped. */
  duration: string;
  limit: LimitKind;
  /** A size like `512MiB`; empty is the gateway's 1 GiB. */
  maxSize: string;
  ringFiles: string;
  ringFileSize: string;
}

export type CaptureField = 'name' | 'device' | 'network' | 'source' | 'ports' | 'destination' | 'format' | 'snaplen' | 'duration' | 'maxSize' | 'ringFiles' | 'ringFileSize';

export function emptyCaptureForm(kind: ScopeKind = 'device', device = ''): CaptureForm {
  return {
    name: '',
    kind,
    device,
    network: '',
    source: 'global',
    traffic: emptyTraffic(),
    format: 'pcapng',
    snaplen: '',
    duration: '',
    limit: 'size',
    maxSize: '',
    ringFiles: '4',
    ringFileSize: '64MiB',
  };
}

/** The problems of the form, by field; empty when the request can be sent. */
export function captureErrors(f: CaptureForm): Partial<Record<CaptureField, string>> {
  const e: Partial<Record<CaptureField, string>> = {};
  if (f.name.trim().length > 64) e.name = 'At most 64 characters.';
  if (f.kind === 'device' && !f.device) e.device = 'Choose a device.';
  if (f.kind === 'network' && !f.network) e.network = 'Choose a network.';
  if (f.kind === 'selector') {
    if (!f.source) e.source = 'Choose the source.';
    Object.assign(e, trafficErrors(f.traffic));
  }
  const snap = f.snaplen.trim();
  if (snap) {
    const n = Number(snap);
    if (!/^[0-9]+$/.test(snap) || n < 64 || n > defaultSnaplen) e.snaplen = `A number of bytes from 64 to ${defaultSnaplen}.`;
  }
  const duration = f.duration.trim();
  if (duration) {
    // the gateway wants at least a second (`invalid_duration`); a text that is no duration is another message
    if (!/^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$/.test(duration)) e.duration = 'A duration like 90s, 5m or 1h.';
    else if ((durationMs(duration) ?? 0) < 1000) e.duration = 'At least 1s.';
  }
  if (f.limit === 'size') {
    const size = f.maxSize.trim();
    if (size) {
      const bytes = parseSize(size);
      if (bytes === undefined) e.maxSize = 'A size like 512MiB or 2GiB.';
      else if (bytes < minFileSize) e.maxSize = 'At least 64KiB.';
    }
  } else {
    const files = f.ringFiles.trim();
    if (!/^[0-9]+$/.test(files) || Number(files) < 2 || Number(files) > 100) e.ringFiles = 'From 2 to 100 files.';
    const size = parseSize(f.ringFileSize);
    if (size === undefined) e.ringFileSize = 'A size like 64MiB.';
    else if (size < minFileSize) e.ringFileSize = 'At least 64KiB.';
  }
  return e;
}

/** The request the form stands for. */
export function formToCaptureRequest(f: CaptureForm): CaptureRequest {
  let scope: CaptureScope;
  switch (f.kind) {
    case 'device':
      scope = { device: f.device };
      break;
    case 'network':
      scope = { network: f.network };
      break;
    case 'uplink':
      scope = { uplink: true };
      break;
    default:
      scope = { selector: { source: scopeOf(f.source), ...trafficMatch(f.traffic) } };
  }
  const out: CaptureRequest = { scope, format: f.format };
  const name = f.name.trim();
  if (name) out.name = name;
  if (f.snaplen.trim()) out.snaplen = Number(f.snaplen.trim());
  if (f.duration.trim()) out.duration = f.duration.trim();
  if (f.limit === 'ring') out.ring_buffer = { files: Number(f.ringFiles.trim()), file_size: f.ringFileSize.trim().replace(/\s+/g, '') };
  else if (f.maxSize.trim()) out.max_size = f.maxSize.trim().replace(/\s+/g, '');
  return out;
}

/** The field of the form a problem of the gateway names (its pointer, "/scope/selector/ports/0", "/max_size"). */
export function captureFieldOf(path: string | undefined): CaptureField | undefined {
  const p = path ?? '';
  if (p.startsWith('/scope/selector/source')) return 'source';
  if (p.startsWith('/scope/selector')) return trafficFieldOf(p);
  if (p.startsWith('/scope/device')) return 'device';
  if (p.startsWith('/scope/network')) return 'network';
  if (p.startsWith('/ring_buffer/files')) return 'ringFiles';
  if (p.startsWith('/ring_buffer')) return 'ringFileSize';
  if (p.startsWith('/max_size')) return 'maxSize';
  if (p.startsWith('/duration')) return 'duration';
  if (p.startsWith('/snaplen')) return 'snaplen';
  if (p.startsWith('/format')) return 'format';
  if (p.startsWith('/name')) return 'name';
  return undefined;
}
