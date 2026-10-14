// Profiles in the user's words (plan §2.9, §2.17 "Profiles"): the built-in profiles' display names,
// the parameters of a profile as fault chips, and where it is active.
import type { ProfileParts } from '../api/generated/model';
import { faultChips } from './faults';

const titles: Record<string, string> = {
  normal: 'Normal',
  lte: 'LTE',
  'bad-lte': 'Bad LTE',
  satellite: 'Satellite',
  'congested-wifi': 'Congested WiFi',
  offline: 'Offline',
  intermittent: 'Intermittent',
  'dns-broken': 'DNS broken',
  'tls-broken': 'TLS broken',
};

/** The names of the built-in profiles: reserved, a custom profile cannot have one (case-insensitive). */
export const builtinProfileNames = Object.keys(titles);

/** "bad-lte" → "Bad LTE"; a custom profile keeps its name. */
export function profileTitle(name: string): string {
  return titles[name] ?? name;
}

/** The parameters of a profile as chips, part by part. */
export function profileChips(parts: ProfileParts): string[] {
  const out: string[] = [];
  if (parts.impairment) out.push(...faultChips(parts.impairment));
  if (parts.mtu) out.push(...faultChips({ family: 'mtu', mtu: parts.mtu }));
  if (parts.dns) out.push(`DNS ${parts.dns.action}`);
  if (parts.tls) out.push(`TLS ${parts.tls.case.replace(/_/g, ' ')}`);
  return out;
}
