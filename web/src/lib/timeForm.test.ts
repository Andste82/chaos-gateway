import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import {
  applyTimePreset,
  emptyTime,
  offsetNanos,
  offsetSeconds,
  timeAnswers,
  timeBody,
  timeEffect,
  timeErrors,
  timeFieldOf,
  timePresets,
  timeScope,
  timeToForm,
  type TimeForm,
} from './timeForm';

// The time form (M34): the answers and the fields each one uses, the presets, the checks under the fields, the `time` part of the
// request it becomes (checked against the spec) and the field a message of the gateway belongs to.

const form = (over: Partial<TimeForm>): TimeForm => ({ ...emptyTime(), ...over });
const server = (destKind: TimeForm['traffic']['destKind'], destination = '', ports = ''): TimeForm['traffic'] => ({ protocol: 'any', ports, destKind, destination });
const spec = (time: unknown) => CreateOverlayBody.safeParse({ target: { device: 'esp32-42' }, time }).success;

describe('the time form', () => {
  it('sends only the fields its answer uses, whatever else the form holds', () => {
    expect(timeBody(form({ shift: 'offset', offset: ' +400d ' }))).toEqual({ offset: '+400d' });
    expect(timeBody(form({ shift: 'absolute', absolute: '1970-01-01T00:00:00Z', offset: '+1y' }))).toEqual({ absolute: '1970-01-01T00:00:00Z' });
    expect(timeBody(form({ shift: 'offset', offset: '-90s', skew: '500', leap: 'delete' }))).toEqual({ offset: '-90s', skew_ppm: 500, leap: 'delete' });
    expect(timeBody(form({ shift: 'none', skew: '-1000000' }))).toEqual({ skew_ppm: -1000000 });
    expect(timeBody(form({ shift: 'none', leap: 'insert' }))).toEqual({ leap: 'insert' });
    // an answer other than normal is the whole fault: the time fields and the kiss code of another answer stay out
    expect(timeBody(form({ answer: 'unsynchronized', offset: '+1y', skew: '5', leap: 'insert', kissCode: 'DENY' }))).toEqual({ answer: 'unsynchronized' });
    expect(timeBody(form({ answer: 'kiss_of_death', kissCode: 'RSTR', offset: '+1y' }))).toEqual({ answer: 'kiss_of_death', kiss_code: 'RSTR' });
    for (const answer of ['silence', 'origin_mismatch', 'zero_transmit', 'wrong_mode'] as const) expect(timeBody(form({ answer }))).toEqual({ answer });
  });

  it('narrows the fault to the server the device asked; the ports need the protocol UDP', () => {
    expect(timeBody(form({ traffic: server('hostname', 'time.google.com') })).destination).toEqual({ hostname: 'time.google.com' });
    expect(timeBody(form({ traffic: server('cidr', '192.0.2.123/32', '12300') }))).toMatchObject({ destination: { cidr: '192.0.2.123/32' }, protocol: 'udp', ports: [12300] });
    expect(timeBody(form({ traffic: server('any', '', '1000-2000') }))).toMatchObject({ protocol: 'udp', port_ranges: [{ from: 1000, to: 2000 }] });
    // no ports: the protocol is left out (the gateway reads UDP and port 123)
    expect(timeBody(form({})).protocol).toBeUndefined();
  });

  it('every fault the form makes is a time fault the spec accepts', () => {
    for (const f of [
      form({}),
      form({ shift: 'absolute', absolute: '2038-01-19T03:09:07Z' }),
      form({ shift: 'none', skew: '1000', leap: 'insert' }),
      form({ answer: 'unsynchronized' }),
      form({ answer: 'kiss_of_death', kissCode: 'RATE' }),
      form({ answer: 'silence', traffic: server('hostname', '*.pool.ntp.org') }),
      form({ answer: 'wrong_mode', traffic: server('cidr', '203.0.113.0/24', '12301') }),
      ...timePresets.map((p) => applyTimePreset(emptyTime(), p.id)),
    ]) {
      expect(timeErrors(f)).toEqual({});
      expect(spec(timeBody(f))).toBe(true);
    }
  });

  it('has the eight presets of the plan, the same faults as the built-in profiles', () => {
    expect(timePresets.map((p) => p.label)).toEqual(['+1 year', '-1 day', '1970', '2038', 'NTP era 1', 'Leap second', 'Unsynchronized', 'Kiss-of-death']);
    const body = (id: string) => timeBody(applyTimePreset(emptyTime(), id));
    expect(body('plus-1y')).toEqual({ offset: '+1y' });
    expect(body('minus-1d')).toEqual({ offset: '-1d' });
    expect(body('1970')).toEqual({ absolute: '1970-01-01T00:00:00Z' });
    expect(body('2038')).toEqual({ absolute: '2038-01-19T03:09:07Z' });
    expect(body('ntp-era-1')).toEqual({ absolute: '2036-02-07T06:23:16Z' });
    expect(body('leap-second')).toEqual({ absolute: '2016-12-31T23:55:00Z', leap: 'insert' });
    expect(body('unsynchronized')).toEqual({ answer: 'unsynchronized' });
    expect(body('kiss-of-death')).toEqual({ answer: 'kiss_of_death', kiss_code: 'DENY' });
  });

  it('the presets make the time part of the built-in time profiles of the gateway, name for name', () => {
    // the file is written by a test of internal/domain from the profiles the gateway ships
    const builtin = JSON.parse(readFileSync('../internal/domain/testdata/builtin_time_profiles.json', 'utf8')) as Record<string, unknown>;
    expect(Object.keys(builtin).sort()).toEqual(timePresets.map((p) => `time-${p.id}`).sort());
    for (const p of timePresets) expect(timeBody(applyTimePreset(emptyTime(), p.id)), p.id).toEqual(builtin[`time-${p.id}`]);
  });

  it('a preset replaces the answer and the time, never the server, and a drift of an earlier choice goes', () => {
    const f = form({ shift: 'offset', offset: '+5m', skew: '500', leap: 'delete', traffic: server('hostname', 'time.google.com') });
    const g = applyTimePreset(f, '1970');
    expect(g.traffic).toEqual(f.traffic);
    expect(timeBody(g)).toEqual({ destination: { hostname: 'time.google.com' }, absolute: '1970-01-01T00:00:00Z' });
    expect(applyTimePreset(f, 'nope')).toBe(f);
    // a preset after an answer other than normal brings the time back
    expect(applyTimePreset(form({ answer: 'silence' }), 'plus-1y').answer).toBe('normal');
  });

  it('checks the offset: the units, the sign, the range of about 292 years', () => {
    expect(timeErrors(form({ offset: '+400d' }))).toEqual({});
    for (const ok of ['-90s', '1y2d', '-1.5h', '250ms', '0s', '+0s', '292y']) expect(timeErrors(form({ offset: ok })).timeOffset, ok).toBeUndefined();
    for (const bad of ['', 'soon', '90', '1 y', '+-1d', '1x', '300y', '9999999999s']) expect(timeErrors(form({ offset: bad })).timeOffset, bad).toBeDefined();
    expect(timeErrors(form({ offset: '300y' })).timeOffset).toMatch(/292 years/);
    expect(offsetSeconds('-1y2d')).toBe(-(365 + 2) * 86_400);
    expect(offsetSeconds('1500ms')).toBe(1.5);
    expect(offsetSeconds('x')).toBeUndefined();
    // the limit is the gateway's, to the nanosecond: the last span of a signed 64-bit count is taken, the next one is not
    for (const ok of ['9223372036.854775807s', '-9223372036.854775807s', '9223372036s', '9223372036.5s', '+9223372036s'])
      expect(timeErrors(form({ offset: ok })).timeOffset, ok).toBeUndefined();
    for (const bad of ['9223372036.854775808s', '-9223372036.854775808s', '9223372037s', '9223372036s855ms'])
      expect(timeErrors(form({ offset: bad })).timeOffset, bad).toBeDefined();
    expect(offsetNanos('1.0000000009s')).toBe(1_000_000_000n);
    expect(offsetNanos('1.5h2m')).toBe(5_520_000_000_000n);
    expect(offsetNanos('-250ms')).toBe(-250_000_000n);
    // at most 64 characters, as the specification says
    expect(timeErrors(form({ offset: `${'1s'.repeat(32)}` })).timeOffset).toBeUndefined();
    expect(timeErrors(form({ offset: `${'1s'.repeat(32)}1s` })).timeOffset).toBeDefined();
    // the offset is not checked while the form serves an instant or answers otherwise
    expect(timeErrors(form({ shift: 'absolute', offset: 'x' }))).toEqual({});
    expect(timeErrors(form({ answer: 'silence', offset: 'x' }))).toEqual({});
  });

  it('checks the absolute time: an instant with a zone, not before 1900', () => {
    const e = (absolute: string) => timeErrors(form({ shift: 'absolute', absolute })).timeAbsolute;
    expect(e('1970-01-01T00:00:00Z')).toBeUndefined();
    expect(e('2038-01-19T03:09:07+01:00')).toBeUndefined();
    expect(e('1900-01-01T00:00:00Z')).toBeUndefined();
    expect(e('2016-12-31T23:55:00.500Z')).toBeUndefined();
    expect(e('1899-12-31T23:59:59Z')).toMatch(/1900-01-01/);
    expect(e('1970-01-01')).toMatch(/time zone/);
    expect(e('1970-01-01T00:00:00')).toMatch(/time zone/);
    expect(e('2024-13-45T00:00:00Z')).toBeDefined();
    expect(e('')).toBeDefined();
    // Date.parse rolls these over; the gateway's RFC 3339 parser refuses them
    for (const bad of ['2038-02-30T00:00:00Z', '2038-02-31T00:00:00Z', '2023-02-29T00:00:00Z', '2038-04-31T00:00:00Z', '2038-01-01T24:00:00Z', '2038-01-01T12:60:00Z', '2038-01-01T12:00:60Z', '2038-01-00T00:00:00Z', '2038-01-01T00:00:00+24:00'])
      expect(e(bad), bad).toMatch(/time zone/);
    expect(e('2024-02-29T00:00:00Z')).toBeUndefined();
    expect(e('2000-02-29T23:59:59Z')).toBeUndefined();
    expect(e('2100-02-29T00:00:00Z')).toBeDefined();
  });

  it('checks the drift and wants the fault to change something', () => {
    expect(timeErrors(form({ skew: '500' }))).toEqual({});
    expect(timeErrors(form({ skew: '-1000000' }))).toEqual({});
    expect(timeErrors(form({ skew: '1000001' })).timeSkew).toMatch(/1,000,000/);
    expect(timeErrors(form({ skew: 'fast' })).timeSkew).toBeDefined();
    expect(timeErrors(form({ shift: 'none' })).timeChange).toBeDefined();
    expect(timeErrors(form({ shift: 'none', skew: '0' })).timeChange).toBeUndefined();
    expect(timeErrors(form({ shift: 'none', leap: 'delete' })).timeChange).toBeUndefined();
    // an offset of zero is a fault: it is the way to say "the correct time" for a server a wider fault would otherwise hit
    expect(timeErrors(form({ shift: 'offset', offset: '+0s' }))).toEqual({});
    expect(timeErrors(form({ answer: 'silence', shift: 'none' }))).toEqual({});
  });

  it('checks the server asked: an address, a prefix or a host name, ports that are not the gateway\'s own services', () => {
    expect(timeErrors(form({ traffic: server('hostname', 'time.google.com') }))).toEqual({});
    expect(timeErrors(form({ traffic: server('cidr', '192.0.2.123') }))).toEqual({});
    expect(timeErrors(form({ traffic: server('cidr', 'nope') })).destination).toBeDefined();
    expect(timeErrors(form({ traffic: server('hostname', 'not a name!') })).destination).toBeDefined();
    // the server a device asked is no network and no uplink
    expect(timeErrors(form({ traffic: server('network', 'n1') })).destination).toMatch(/address, a prefix or a host name/);
    expect(timeErrors(form({ traffic: server('uplink') })).destination).toMatch(/address, a prefix or a host name/);
    expect(timeErrors(form({ traffic: server('any', '', '123, 12301') }))).toEqual({});
    expect(timeErrors(form({ traffic: server('any', '', '99999') })).ports).toBeDefined();
    for (const ports of ['53', '67', '68', '1-100', '60-70', '123, 53']) expect(timeErrors(form({ traffic: server('any', '', ports) })).ports, ports).toMatch(/DNS and DHCP/);
    expect(timeErrors(form({ traffic: server('any', '', '1000-2000') }))).toEqual({});
  });

  it('wants a kiss code only for the kiss-of-death', () => {
    expect(timeErrors(form({ answer: 'kiss_of_death', kissCode: 'DENY' }))).toEqual({});
    expect(timeErrors(form({ answer: 'kiss_of_death', kissCode: 'NOPE' as never })).timeKiss).toBeDefined();
    expect(timeErrors(form({ answer: 'silence', kissCode: 'NOPE' as never }))).toEqual({});
  });

  it('reads a time fault back into the form and sends it unchanged', () => {
    for (const t of [
      { offset: '+400d' },
      { absolute: '1970-01-01T00:00:00Z', skew_ppm: 500 },
      { absolute: '2016-12-31T23:55:00Z', leap: 'insert' as const },
      { skew_ppm: -1000000 },
      { leap: 'delete' as const },
      { answer: 'unsynchronized' as const },
      { answer: 'kiss_of_death' as const, kiss_code: 'RATE' as const },
      { answer: 'silence' as const, destination: { hostname: 'time.google.com' } },
      { offset: '-1d', destination: { cidr: '192.0.2.123/32' }, ports: [12300], protocol: 'udp' as const },
      { offset: '+1y', port_ranges: [{ from: 12301, to: 12310 }], protocol: 'udp' as const },
    ]) {
      expect(timeBody(timeToForm(t)), JSON.stringify(t)).toEqual(t);
    }
    expect(timeToForm(undefined)).toEqual(emptyTime());
    expect(timeToForm({ offset: '+1y' }).shift).toBe('offset');
    expect(timeToForm({ leap: 'insert' }).shift).toBe('none');
    expect(timeToForm({ answer: 'silence' }).answer).toBe('silence');
  });

  it('offers every answer of the spec once, in a group', () => {
    expect(timeAnswers.map((a) => a.value)).toEqual(['normal', 'unsynchronized', 'kiss_of_death', 'silence', 'origin_mismatch', 'zero_transmit', 'wrong_mode']);
    expect(timeAnswers.every((a) => a.hint && a.label && a.group)).toBe(true);
  });

  it('says what the fault does and whom it applies to', () => {
    expect(timeEffect({ offset: '+400d' })).toBe('the correct time +400d');
    expect(timeEffect({ offset: '90s' })).toBe('the correct time +90s');
    expect(timeEffect({ absolute: '1970-01-01T00:00:00Z' })).toBe('the time 1970-01-01 00:00:00 UTC');
    expect(timeEffect({ absolute: '2016-12-31T23:55:00Z', skew_ppm: 500, leap: 'insert' })).toBe('the time 2016-12-31 23:55:00 UTC, drifting 500 ppm, leap second inserted');
    expect(timeEffect({ skew_ppm: -20 })).toBe('the correct time, drifting -20 ppm');
    expect(timeEffect({ answer: 'unsynchronized' })).toMatch(/leap indicator 3, stratum 16/);
    expect(timeEffect({ answer: 'kiss_of_death', kiss_code: 'DENY' })).toBe('a kiss-of-death DENY (stratum 0)');
    expect(timeEffect({ answer: 'silence' })).toMatch(/no answer/);
    expect(timeEffect({ answer: 'origin_mismatch' })).toMatch(/origin/);
    expect(timeEffect({ answer: 'zero_transmit' })).toMatch(/zero/);
    expect(timeEffect({ answer: 'wrong_mode' })).toMatch(/mode/);
    expect(timeScope(form({}))).toBe('NTP queries to every server');
    expect(timeScope(form({ traffic: server('hostname', 'time.google.com') }))).toBe('NTP queries to time.google.com');
    expect(timeScope(form({ traffic: server('cidr', '192.0.2.0/24', '12300') }))).toBe('NTP queries to 192.0.2.0/24 on UDP 12300');
  });

  it("puts the gateway's pointers on the fields", () => {
    expect(timeFieldOf('/offset')).toBe('timeOffset');
    expect(timeFieldOf('/absolute')).toBe('timeAbsolute');
    expect(timeFieldOf('/skew_ppm')).toBe('timeSkew');
    expect(timeFieldOf('/leap')).toBe('timeLeap');
    expect(timeFieldOf('/answer')).toBe('timeAnswer');
    expect(timeFieldOf('/kiss_code')).toBe('timeKiss');
    expect(timeFieldOf('/destination/hostname')).toBe('destination');
    expect(timeFieldOf('/ports/0')).toBe('ports');
    expect(timeFieldOf('/port_ranges/1')).toBe('ports');
    expect(timeFieldOf('/protocol')).toBe('ports');
    expect(timeFieldOf('')).toBe('timeChange');
    expect(timeFieldOf(undefined)).toBe('timeChange');
    expect(timeFieldOf('/unknown')).toBeUndefined();
  });
});
