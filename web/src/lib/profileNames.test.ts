import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { builtinProfileNames, profileTitle } from './profileNames';

// The names are the server's catalogue (internal/domain/builtin.go); a name missing here would let a custom profile take a
// reserved name in the form and be refused only by the API.
describe('profileNames', () => {
  it('lists every built-in profile, the time ones included', () => {
    expect(builtinProfileNames).toEqual([
      'normal',
      'lte',
      'bad-lte',
      'satellite',
      'congested-wifi',
      'offline',
      'intermittent',
      'dns-broken',
      'tls-broken',
      'time-plus-1y',
      'time-minus-1d',
      'time-1970',
      'time-2038',
      'time-ntp-era-1',
      'time-leap-second',
      'time-unsynchronized',
      'time-kiss-of-death',
    ]);
  });

  it('is the catalogue of the server', () => {
    const go = readFileSync(resolve(process.cwd(), '../internal/domain/builtin.go'), 'utf8');
    const names = [...go.matchAll(/^\t\t\tName:\s+"([^"]+)"/gm)].map((m) => m[1]);
    expect(names.length).toBeGreaterThan(0);
    expect([...builtinProfileNames].sort()).toEqual([...names].sort());
  });

  it('gives a built-in profile a label and leaves a custom one alone', () => {
    expect(profileTitle('time-ntp-era-1')).toBe('Time NTP era 1');
    expect(profileTitle('time-leap-second')).toBe('Time leap second');
    expect(profileTitle('my profile')).toBe('my profile');
    for (const n of builtinProfileNames) expect(profileTitle(n)).not.toBe(n);
  });
});
