import { describe, expect, it } from 'vitest';
import { profileChips } from './profiles';

// The parameters of a profile as chips, part by part (M34 adds the time part).

describe('the chips of a profile', () => {
  it('say each part in turn', () => {
    expect(profileChips({ impairment: { latency: '200ms' }, mtu: { size: 1280 } })).toEqual(['↑↓ 200 ms', 'MTU 1280']);
    expect(profileChips({ dns: { action: 'nxdomain' }, tls: { case: 'self_signed' } })).toEqual(['DNS nxdomain', 'TLS self signed']);
  });

  it('say the time part like a time fault', () => {
    expect(profileChips({ time: { offset: '+1y' } })).toEqual(['Time +1y']);
    expect(profileChips({ time: { absolute: '2016-12-31T23:55:00Z', leap: 'insert' } })).toEqual(['Time 2016-12-31 23:55:00 UTC · leap insert']);
    expect(profileChips({ time: { answer: 'kiss_of_death', kiss_code: 'DENY' } })).toEqual(['Time kiss-of-death DENY']);
    expect(profileChips({ impairment: { loss: '5%' }, time: { offset: '-1d' } })).toEqual(['5 % loss', 'Time -1d']);
  });
});
