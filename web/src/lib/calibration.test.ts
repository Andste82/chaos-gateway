import { describe, expect, it } from 'vitest';
import type { CalibrationResult } from '../api/generated/model';
import { calibrationErrors, calibrationFieldOf, calibrationRows, calibrationSeconds, calibrationSentence, checkLabel, emptyCalibrationForm, formToCalibrationRequest, verdictTone } from './calibration';

const good = { ...emptyCalibrationForm('b0000000-0000-5000-8000-0000000000e1'), host: '203.0.113.10' };

describe('the form of a calibration', () => {
  it('starts on the faults now, or on the profile it was opened for', () => {
    expect(emptyCalibrationForm().what).toBe('current');
    expect(emptyCalibrationForm('lte')).toMatchObject({ what: 'profile', profile: 'lte' });
  });

  it('needs a profile when it measures one, and an echo server that is an IPv4 address', () => {
    expect(calibrationErrors(good)).toEqual({});
    expect(calibrationErrors({ ...good, profile: '' })).toEqual({ profile: 'Choose the profile to measure.' });
    expect(calibrationErrors({ ...good, what: 'current', profile: '' })).toEqual({});
    expect(calibrationErrors({ ...good, host: '' }).host).toBe('The echo server is needed.');
    for (const h of ['broker.example.com', '203.0.113', '203.0.113.256', '2001:db8::1', '203.0.113.10:7']) expect(calibrationErrors({ ...good, host: h }).host, h).toBe('An IPv4 address.');
  });

  it('takes a port from 1 to 65535 and a duration from 5 s to 5 min, both optional', () => {
    expect(calibrationErrors({ ...good, port: '9001', duration: '90s' })).toEqual({});
    expect(calibrationErrors({ ...good, duration: '5m' })).toEqual({});
    expect(calibrationErrors({ ...good, duration: '5s' })).toEqual({});
    for (const p of ['0', '65536', 'x', '-1']) expect(calibrationErrors({ ...good, port: p }).port, p).toBeDefined();
    for (const d of ['4s', '301s', '6m', 'soon', '30']) expect(calibrationErrors({ ...good, duration: d }).duration, d).toBeDefined();
  });

  it('becomes the request, with only what was filled in', () => {
    expect(formToCalibrationRequest(good)).toEqual({ profile: 'b0000000-0000-5000-8000-0000000000e1', host: '203.0.113.10' });
    expect(formToCalibrationRequest({ ...good, what: 'current', port: '9001', duration: ' 20s ' })).toEqual({ host: '203.0.113.10', port: 9001, duration: '20s' });
  });

  it('names the field a refusal points at', () => {
    expect(calibrationFieldOf('/host')).toBe('host');
    expect(calibrationFieldOf('/duration')).toBe('duration');
    expect(calibrationFieldOf('/fault')).toBe('profile');
    expect(calibrationFieldOf('/mystery')).toBeUndefined();
    expect(calibrationFieldOf(undefined)).toBeUndefined();
  });

  it('says how long the call takes', () => {
    expect(calibrationSeconds(good)).toBe(30);
    expect(calibrationSeconds({ ...good, duration: '2m' })).toBe(120);
  });
});

const result: CalibrationResult = {
  within_tolerance: false,
  expected: { rtt_ms: 101, loss_percent: 9.75, rate_bps: 2_000_000 },
  measured: { baseline_rtt_ms: 1, rtt_median_ms: 52.1, rtt_p95_ms: 60, loss_percent: 9.5, loss_ci99: [8.1, 11.2], rate_bps: 1_900_000 },
  samples: { sent: 2000, received: 1810 },
  checks: [
    { name: 'latency', ok: false, detail: 'x' },
    { name: 'loss', ok: true, detail: 'y' },
    { name: 'rate', ok: false, detail: 'z' },
  ],
};

describe('the result of a calibration', () => {
  it('has a verdict, never by colour alone', () => {
    expect(verdictTone(result)).toEqual({ tone: 'block', label: 'Out of tolerance' });
    expect(verdictTone({ within_tolerance: true })).toEqual({ tone: 'normal', label: 'Within tolerance' });
  });

  it('names the checks that failed', () => {
    expect(calibrationSentence(result)).toBe('What the probe measured does not match the parameters: delay, rate.');
    expect(calibrationSentence({ within_tolerance: false })).toBe('What the probe measured does not match the parameters.');
    expect(calibrationSentence({ within_tolerance: true })).toContain('within the tolerances');
    expect([checkLabel('latency'), checkLabel('jitter'), checkLabel('loss'), checkLabel('rate')]).toEqual(['Delay', 'Jitter', 'Loss', 'Rate']);
  });

  it('lists expected and measured values side by side', () => {
    const rows = calibrationRows(result);
    expect(rows.map((r) => r.label)).toEqual(['Round trip (median)', 'Unimpaired round trip', 'Round trip (95th percentile)', 'Loss', 'Throughput']);
    expect(rows[0]).toEqual({ label: 'Round trip (median)', expected: '101 ms', measured: '52.1 ms' });
    expect(rows[3]).toEqual({ label: 'Loss', expected: '9.75 %', measured: '9.5 % (99 %: 8.1 % to 11.2 %)' });
    expect(rows[4]).toMatchObject({ expected: '2.0 Mbit/s', measured: '1.9 Mbit/s' });
    // nothing is invented for what was not measured
    expect(calibrationRows({ within_tolerance: true })).toEqual([]);
    expect(calibrationRows({ within_tolerance: true, expected: { rtt_ms: 5 } })[0]).toEqual({ label: 'Round trip (median)', expected: '5.0 ms', measured: '—' });
  });
});
