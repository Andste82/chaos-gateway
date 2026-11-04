// The display names of the built-in profiles (plan §2.9): one label for a profile on every screen,
// in a module of its own so the fault chips can use it without importing the profile helpers.

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
  'time-plus-1y': 'Time +1 year',
  'time-minus-1d': 'Time -1 day',
  'time-1970': 'Time 1970',
  'time-2038': 'Time 2038',
  'time-ntp-era-1': 'Time NTP era 1',
  'time-leap-second': 'Time leap second',
  'time-unsynchronized': 'Time unsynchronized',
  'time-kiss-of-death': 'Time kiss-of-death',
};

/** The names of the built-in profiles: reserved, a custom profile cannot have one (case-insensitive). */
export const builtinProfileNames = Object.keys(titles);

/** "bad-lte" → "Bad LTE"; a custom profile keeps its name. */
export function profileTitle(name: string): string {
  return titles[name] ?? name;
}
