// Formatting of numbers, rates and times for the UI. Addresses, counters and times are shown in
// IBM Plex Mono by the components; these functions only make the text.

/** 18342 → "18 342" (the prototype's grouping). */
export function formatCount(n: number | undefined | null): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return '—';
  return Math.round(n)
    .toString()
    .replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
}

function scaled(value: number, units: string[], base = 1000): string {
  let i = 0;
  let v = value;
  while (Math.abs(v) >= base && i < units.length - 1) {
    v /= base;
    i++;
  }
  const digits = i === 0 || v >= 100 ? 0 : 1;
  return `${v.toFixed(digits)} ${units[i]}`;
}

/** A byte rate as the device list shows it: "3.1 KB/s". */
export function formatByteRate(bytesPerSecond: number | undefined | null): string {
  if (bytesPerSecond === undefined || bytesPerSecond === null || !Number.isFinite(bytesPerSecond)) return '—';
  return scaled(bytesPerSecond, ['B/s', 'KB/s', 'MB/s', 'GB/s']);
}

/** A bit rate as the uplink shows it: "6.2 Mbit/s". */
export function formatBitRate(bitsPerSecond: number | undefined | null): string {
  if (bitsPerSecond === undefined || bitsPerSecond === null || !Number.isFinite(bitsPerSecond)) return '—';
  return scaled(bitsPerSecond, ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s']);
}

/** A size in bytes: "1.8 MB". */
export function formatBytes(bytes: number | undefined | null): string {
  if (bytes === undefined || bytes === null || !Number.isFinite(bytes)) return '—';
  return scaled(bytes, ['B', 'KB', 'MB', 'GB', 'TB']);
}

const pad = (n: number) => n.toString().padStart(2, '0');

/** The local wall-clock time of a time stamp: "12:43:12". */
export function formatClock(at: Date | string | undefined): string {
  if (!at) return '—';
  const d = typeof at === 'string' ? new Date(at) : at;
  if (Number.isNaN(d.getTime())) return '—';
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Hours and minutes of a time stamp: "12:40". */
export function formatHM(at: Date | string | undefined): string {
  return formatClock(at).slice(0, 5);
}

/** A remaining time as a countdown: "4:12", "1:02:03"; "0:00" when it has passed. */
export function formatRemaining(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

/** How long ago a time stamp was, coarsely: "just now", "5 s ago", "3 min ago", "2 h ago". */
export function formatAgo(at: Date | string | undefined, now: Date = new Date()): string {
  if (!at) return 'never';
  const d = typeof at === 'string' ? new Date(at) : at;
  const s = Math.round((now.getTime() - d.getTime()) / 1000);
  if (s < 3) return 'just now';
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

const durationUnits: Record<string, string> = { ns: 'ns', us: 'µs', ms: 'ms', s: 's', m: 'min', h: 'h' };

/** A Go duration as text: "200ms" → "200 ms", "1h30m" → "1 h 30 min". */
export function formatDuration(d: string | undefined): string {
  if (!d) return '';
  const parts = [...d.matchAll(/([0-9.]+)(ns|us|ms|s|m|h)/g)];
  if (parts.length === 0) return d;
  return parts.map(([, n, u]) => `${n} ${durationUnits[u ?? ''] ?? u}`).join(' ');
}

/** The number of a Go duration in milliseconds ("200ms" → 200, "1.5s" → 1500). */
export function durationMs(d: string | undefined): number | undefined {
  if (!d) return undefined;
  const factor: Record<string, number> = { ns: 1e-6, us: 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };
  let total = 0;
  let any = false;
  for (const [, n, u] of d.matchAll(/([0-9.]+)(ns|us|ms|s|m|h)/g)) {
    total += Number(n) * (factor[u ?? ''] ?? 0);
    any = true;
  }
  return any ? total : undefined;
}

/** The network of a gateway address: "10.10.0.1/24" → "10.10.0.0/24". */
export function subnetOf(address: string | undefined): string {
  if (!address) return '';
  const [ip, lenText] = address.split('/');
  const len = Number(lenText);
  const octets = (ip ?? '').split('.').map(Number);
  if (octets.length !== 4 || octets.some((o) => !Number.isInteger(o)) || !Number.isInteger(len)) return address;
  const value = octets.reduce((a, o) => a * 256 + o, 0);
  const mask = len === 0 ? 0 : (0xffffffff << (32 - len)) >>> 0;
  const net = (value & mask) >>> 0;
  return `${[24, 16, 8, 0].map((s) => (net >>> s) & 255).join('.')}/${len}`;
}
