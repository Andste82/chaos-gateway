// What the NTP server did with a device's queries, in the user's words (plan §2.17 "Device detail", §2.18, M34): the *Time* panel
// of the device page shows the last query, the server asked, the time served and the time fault that shaped it. These functions
// make the text and the tone of the rows (`GET /devices/{id}/ntp`).
import type { DeviceNtp, NtpQueryLogEntry, Overlay, ProfileView } from '../api/generated/model';

const answerLabels: Record<string, string> = {
  normal: 'answered',
  unsynchronized: 'unsynchronized (leap indicator 3, stratum 16)',
  kiss_of_death: 'kiss-of-death',
  silence: 'no answer (silence)',
  origin_mismatch: 'wrong origin timestamp',
  zero_transmit: 'transmit timestamp zero',
  wrong_mode: 'wrong mode',
  dropped: 'dropped (over the rate limit of 16 per second, logged at most once a second)',
};

/** How the server answered, in words; a connection or query with NTS has no answer to name. */
export function ntpAnswerText(q: Pick<NtpQueryLogEntry, 'answer' | 'kind'>): string {
  if (q.kind === 'nts_ke') return 'NTS, not answerable';
  return q.answer ? (answerLabels[q.answer] ?? q.answer) : 'answered';
}

/**
 * How a query reads in the list: a time fault shaped the answer (orange: it changes what the device believes), the server did not
 * answer (red: silence, dropped, NTS), or it was an ordinary answer.
 */
export function ntpTone(q: Pick<NtpQueryLogEntry, 'answer' | 'fault' | 'kind'>): 'normal' | 'fault' | 'block' {
  if (q.answer === 'silence' || q.answer === 'dropped' || q.kind === 'nts_ke') return 'block';
  if (q.fault) return 'fault';
  return 'normal';
}

/**
 * The served time minus the correct time, in words with a sign: "+250 ms", "-1.5 s", "+1 y 35 d", "-1 d". The two largest units
 * that are not zero (a year is 365 days, as in the offset of the fault); "0 ms" for no difference.
 */
export function offsetText(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) return '—';
  if (ms === 0) return '0 ms';
  const sign = ms < 0 ? '-' : '+';
  let rest = Math.abs(ms);
  // the unit is chosen after the rounding: 999.96 ms is "+1 s", not "+1000 ms", and 59.96 s is "+1 min", not "+60 s"
  const tenthMs = Math.round(rest * 10) / 10;
  if (tenthMs < 1000) return `${sign}${tenthMs} ms`;
  const tenthS = Math.round(rest / 100) / 10;
  if (tenthS < 60) return `${sign}${tenthS} s`;
  if (rest < 60_000) rest = 60_000;
  const units: [number, string][] = [
    [365 * 86_400_000, 'y'],
    [86_400_000, 'd'],
    [3_600_000, 'h'],
    [60_000, 'min'],
    [1000, 's'],
  ];
  const parts: string[] = [];
  for (const [size, name] of units) {
    const n = Math.floor(rest / size);
    if (n > 0) {
      parts.push(`${n} ${name}`);
      rest -= n * size;
    }
    if (parts.length === 2) break;
  }
  return `${sign}${parts.join(' ')}`;
}

/** The server a device asked, as the panel names it: the `ip:port` the API knows, else that it is not known. */
export function serverText(server: string | undefined): string {
  return server && server.trim() ? server : 'not known';
}

/** The note for a device that uses NTS (RFC 8915), whose key establishment or queries the server cannot answer; nothing without any. */
export function ntsNote(n: Pick<DeviceNtp, 'nts_ke'>): string | undefined {
  if (!n.nts_ke) return undefined;
  const count = n.nts_ke === 1 ? '1 NTS key-establishment connection or query' : `${n.nts_ke} NTS key-establishment connections or queries`;
  return `${count}: a device that uses NTS cannot get its time from the gateway. A rule that blocks TCP 4460 makes it fall back to plain NTP; otherwise it keeps the real server.`;
}

/**
 * The time faults that shape a device's answers now, newest first: what the *Time* panel lists as active. An overlay of the kind
 * `time`, or the activation of a profile with a `time` part (the built-in `time-*` profiles are the usual way to start one); a
 * fault that is overridden or disabled changes nothing, so it is left out. The profiles are the list of `GET /profiles`: an
 * overlay names its profile by id or by name.
 */
export function activeTimeFaults(overlays: readonly Overlay[], profiles: readonly ProfileView[] = []): Overlay[] {
  const hasTime = (ref: string | undefined) => !!ref && profiles.some((p) => (p.id === ref || p.config.name === ref) && p.config.parts.time);
  return overlays
    .filter((o) => (o.kind === 'time' || (o.kind === 'profile' && hasTime(o.profile))) && o.state !== 'overridden' && o.state !== 'disabled')
    .sort((a, b) => (a.updated_at < b.updated_at ? 1 : a.updated_at > b.updated_at ? -1 : 0));
}
