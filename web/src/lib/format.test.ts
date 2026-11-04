import { describe, expect, it } from 'vitest';
import {
  durationMs,
  formatAgo,
  formatBitRate,
  formatByteRate,
  formatBytes,
  formatClock,
  formatCount,
  formatDuration,
  formatRemaining,
  instantText,
  subnetOf,
} from './format';

describe('format', () => {
  it('groups counts like the prototype', () => {
    expect(formatCount(18342)).toBe('18 342');
    expect(formatCount(1234567)).toBe('1 234 567');
    expect(formatCount(12)).toBe('12');
    expect(formatCount(undefined)).toBe('—');
  });

  it('scales rates and sizes', () => {
    expect(formatByteRate(3100)).toBe('3.1 KB/s');
    expect(formatByteRate(900)).toBe('900 B/s');
    expect(formatByteRate(840_000)).toBe('840 KB/s');
    expect(formatBitRate(6_200_000)).toBe('6.2 Mbit/s');
    expect(formatBitRate(64_000)).toBe('64.0 kbit/s');
    expect(formatBitRate(undefined)).toBe('—');
    expect(formatBytes(1_800_000)).toBe('1.8 MB');
  });

  it('counts down remaining times', () => {
    expect(formatRemaining(252_000)).toBe('4:12');
    expect(formatRemaining(3_723_000)).toBe('1:02:03');
    expect(formatRemaining(400)).toBe('0:01');
    expect(formatRemaining(-5000)).toBe('0:00');
  });

  it('shows wall-clock times and how long ago', () => {
    const at = new Date(2026, 9, 13, 12, 43, 2);
    expect(formatClock(at)).toBe('12:43:02');
    expect(formatClock('not a date')).toBe('—');
    const now = new Date(at.getTime() + 125_000);
    expect(formatAgo(at, now)).toBe('2 min ago');
    expect(formatAgo(at, new Date(at.getTime() + 1000))).toBe('just now');
    expect(formatAgo(undefined, now)).toBe('never');
  });

  it('reads Go durations', () => {
    expect(formatDuration('200ms')).toBe('200 ms');
    expect(formatDuration('1h30m')).toBe('1 h 30 min');
    expect(durationMs('1.5s')).toBe(1500);
    expect(durationMs('1m30s')).toBe(90_000);
    expect(durationMs('')).toBeUndefined();
  });

  it('derives the subnet of a gateway address', () => {
    expect(subnetOf('10.10.0.1/24')).toBe('10.10.0.0/24');
    expect(subnetOf('172.16.5.200/20')).toBe('172.16.0.0/20');
    expect(subnetOf('garbage')).toBe('garbage');
  });
});

describe('an instant in words (M34)', () => {
  it('is the UTC date and time, without milliseconds that are zero', () => {
    expect(instantText('1970-01-01T00:00:00Z')).toBe('1970-01-01 00:00:00 UTC');
    expect(instantText('2038-01-19T04:09:07+01:00')).toBe('2038-01-19 03:09:07 UTC');
    expect(instantText('2016-12-31T23:55:00.500Z')).toBe('2016-12-31 23:55:00.500 UTC');
    // a text that is no instant is shown as it is
    expect(instantText('soon')).toBe('soon');
  });
});
