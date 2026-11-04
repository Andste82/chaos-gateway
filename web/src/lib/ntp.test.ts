import { describe, expect, it } from 'vitest';
import type { Overlay, ProfileView } from '../api/generated/model';
import { activeTimeFaults, ntpAnswerText, ntpTone, ntsNote, offsetText, serverText } from './ntp';

// The words of the Time panel of the device detail (M34): how a query was answered, the tone of its row, the difference of the
// time served from the correct time, the server asked.

describe('how a query was answered', () => {
  it('names each answer of the spec', () => {
    expect(ntpAnswerText({ answer: 'normal' })).toBe('answered');
    expect(ntpAnswerText({})).toBe('answered');
    expect(ntpAnswerText({ answer: 'unsynchronized' })).toMatch(/leap indicator 3, stratum 16/);
    expect(ntpAnswerText({ answer: 'kiss_of_death' })).toBe('kiss-of-death');
    expect(ntpAnswerText({ answer: 'silence' })).toBe('no answer (silence)');
    expect(ntpAnswerText({ answer: 'origin_mismatch' })).toBe('wrong origin timestamp');
    expect(ntpAnswerText({ answer: 'zero_transmit' })).toBe('transmit timestamp zero');
    expect(ntpAnswerText({ answer: 'wrong_mode' })).toBe('wrong mode');
    expect(ntpAnswerText({ answer: 'dropped' })).toMatch(/rate limit of 16 per second/);
    // an NTS connection has no answer to name
    expect(ntpAnswerText({ kind: 'nts_ke' })).toBe('NTS, not answerable');
  });

  it('reads a row orange when a fault shaped it and red when the server did not answer', () => {
    expect(ntpTone({ answer: 'normal' })).toBe('normal');
    expect(ntpTone({ answer: 'normal', fault: 'f1' })).toBe('fault');
    expect(ntpTone({ answer: 'kiss_of_death', fault: 'f1' })).toBe('fault');
    expect(ntpTone({ answer: 'silence', fault: 'f1' })).toBe('block');
    expect(ntpTone({ answer: 'dropped' })).toBe('block');
    expect(ntpTone({ kind: 'nts_ke' })).toBe('block');
  });
});

describe('the difference from the correct time', () => {
  const day = 86_400_000;
  it('has a sign and the two largest units', () => {
    expect(offsetText(0)).toBe('0 ms');
    expect(offsetText(250)).toBe('+250 ms');
    expect(offsetText(-0.5)).toBe('-0.5 ms');
    expect(offsetText(1500)).toBe('+1.5 s');
    // the unit follows the rounded value, at both boundaries
    expect(offsetText(999.9)).toBe('+999.9 ms');
    expect(offsetText(999.96)).toBe('+1 s');
    expect(offsetText(1000)).toBe('+1 s');
    expect(offsetText(59_940)).toBe('+59.9 s');
    expect(offsetText(59_960)).toBe('+1 min');
    expect(offsetText(-59_960)).toBe('-1 min');
    expect(offsetText(60_000)).toBe('+1 min');
    expect(offsetText(-2000)).toBe('-2 s');
    expect(offsetText(90_000)).toBe('+1 min 30 s');
    expect(offsetText(3_600_000)).toBe('+1 h');
    expect(offsetText(-day)).toBe('-1 d');
    expect(offsetText(400 * day)).toBe('+1 y 35 d');
    expect(offsetText(365 * day)).toBe('+1 y');
    // 1970 seen from 2026: more than 56 years behind
    expect(offsetText(-20_000 * day)).toBe('-54 y 290 d');
  });

  it('is a dash for an offset that is not known', () => {
    expect(offsetText(undefined)).toBe('—');
    expect(offsetText(Number.NaN)).toBe('—');
  });
});

describe('the server asked', () => {
  it('is the address the API knows, else that it is not known', () => {
    expect(serverText('192.0.2.123:123')).toBe('192.0.2.123:123');
    expect(serverText('an external server (redirected)')).toBe('an external server (redirected)');
    expect(serverText(undefined)).toBe('not known');
    expect(serverText('  ')).toBe('not known');
  });
});

describe('a device that uses NTS', () => {
  it('gets a note once the server has seen one, and none before', () => {
    expect(ntsNote({ nts_ke: 0 })).toBeUndefined();
    expect(ntsNote({ nts_ke: 1 })).toMatch(/^1 NTS key-establishment connection or query: .*TCP 4460/);
    expect(ntsNote({ nts_ke: 3 })).toMatch(/^3 NTS key-establishment connections or queries/);
  });
});

describe('the time faults of a device', () => {
  const o = (id: string, kind: Overlay['kind'], updated: string): Overlay =>
    ({ id, kind, owner: { type: 'user', id: 'admin' }, created_at: updated, updated_at: updated, generation: 1 }) as Overlay;

  it('are the overlays of the kind time, the newest first', () => {
    const list = [o('a', 'time', '2026-11-04T10:00:00Z'), o('b', 'fault', '2026-11-04T12:00:00Z'), o('c', 'time', '2026-11-04T11:00:00Z'), o('d', 'dns', '2026-11-04T13:00:00Z')];
    expect(activeTimeFaults(list).map((x) => x.id)).toEqual(['c', 'a']);
    expect(activeTimeFaults([])).toEqual([]);
  });

  const profile = (id: string, name: string, parts: ProfileView['config']['parts']): ProfileView => ({ id, builtin: true, available: true, config: { name, parts } });
  const profiles = [profile('p-time', 'time-1970', { time: { absolute: '1970-01-01T00:00:00Z' } }), profile('p-lte', 'lte', { impairment: { upload: { latency: '50ms' } } })];
  const activation = (id: string, ref: string, updated: string, state?: Overlay['state']): Overlay => ({ ...o(id, 'profile', updated), profile: ref, state });

  it('include the activation of a profile with a time part, by id or by name, and not that of another profile', () => {
    const list = [
      o('a', 'time', '2026-11-04T10:00:00Z'),
      activation('b', 'p-time', '2026-11-04T12:00:00Z'),
      activation('c', 'time-1970', '2026-11-04T11:00:00Z'),
      activation('d', 'p-lte', '2026-11-04T13:00:00Z'),
      activation('e', 'unknown', '2026-11-04T14:00:00Z'),
    ];
    expect(activeTimeFaults(list, profiles).map((x) => x.id)).toEqual(['b', 'c', 'a']);
    // without the profiles an activation cannot be told from another one
    expect(activeTimeFaults(list).map((x) => x.id)).toEqual(['a']);
  });

  it('leave out a fault that is overridden or disabled, and keep one that is partly overridden', () => {
    const state = (id: string, s: Overlay['state']): Overlay => ({ ...o(id, 'time', '2026-11-04T10:00:00Z'), state: s });
    const list = [state('a', 'effective'), state('b', 'overridden'), state('c', 'disabled'), state('d', 'partially_overridden'), state('e', undefined), activation('f', 'p-time', '2026-11-04T09:00:00Z', 'overridden')];
    expect(activeTimeFaults(list, profiles).map((x) => x.id).sort()).toEqual(['a', 'd', 'e']);
  });
});
