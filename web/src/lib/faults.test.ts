import { describe, expect, it } from 'vitest';
import type { Overlay } from '../api/generated/model';
import { describeMatch, describeOwner, describeScope, faultChips, overlayChips } from './faults';

const names = { device: (id: string) => ({ d1: 'ESP32-42' })[id], network: (id: string) => ({ n1: 'IoT' })[id], group: () => 'Sensors' };

describe('fault chips', () => {
  it('say what happens to both directions', () => {
    expect(faultChips({ latency: '200ms', jitter: '50ms', loss: '5%' })).toEqual(['↑↓ 200 ms ±50', '5 % loss']);
    expect(faultChips({ blackout: true })).toEqual(['↑↓ blackout']);
  });

  it('say what happens to each direction on its own', () => {
    expect(faultChips({ download: { rate: '64kbit' }, upload: { latency: '1s' } })).toEqual(['↑ 1 s', '↓ 64 kbit/s']);
  });

  it('name MTU and tunnel faults', () => {
    expect(faultChips({ family: 'mtu', mtu: { size: 1280 } })).toEqual(['MTU 1280']);
    expect(faultChips({ family: 'mtu', mtu: { size: 1280, mode: 'blackhole' } })).toEqual(['MTU 1280 blackhole']);
    expect(faultChips({ family: 'tunnel', tunnel: { client: 'rA' }, loss: '10%' })).toEqual(['tunnel 10 % loss']);
  });

  it('do not show an empty fault as nothing', () => {
    expect(faultChips({})).toEqual(['no impairment']);
  });

  it('name the kind of other overlays', () => {
    const base = { id: 'o', owner: { type: 'user', id: 'admin' }, created_at: '', updated_at: '', generation: 1 } as const;
    expect(overlayChips({ ...base, kind: 'profile', profile: 'p1' } as Overlay, (r) => (r === 'p1' ? 'bad-lte' : undefined))).toEqual([
      'profile bad-lte',
    ]);
    expect(overlayChips({ ...base, kind: 'dhcp', dhcp: { action: 'silence' } } as Overlay)).toEqual(['DHCP silence']);
  });
});

describe('selectors in words', () => {
  it('names the source by its object', () => {
    expect(describeScope({ device: 'd1' }, names)).toBe('ESP32-42');
    expect(describeScope({ device: 'unknown' }, names)).toBe('unknown');
    expect(describeScope({ network: 'n1' }, names)).toBe('Network IoT');
    expect(describeScope({ group: 'g' }, names)).toBe('Group Sensors');
    expect(describeScope({ global: true }, names)).toBe('Everything');
    expect(describeScope(undefined)).toBe('Everything');
    expect(describeScope({ remote_network: { cidr: '10.9.0.0/24' } })).toBe('Remote network 10.9.0.0/24');
    expect(describeScope({ remote_network: { link: 'n1' } }, names)).toBe('Remote networks of link IoT');
  });

  it('names the destination, protocol and ports', () => {
    expect(describeMatch({ protocol: 'tcp', ports: [8883] })).toBe('→ TCP 8883');
    expect(describeMatch({ destination: { cidr: '203.0.113.0/24' }, protocol: 'udp', port_ranges: [{ from: 5000, to: 5010 }] })).toBe(
      '→ 203.0.113.0/24 UDP 5000-5010',
    );
    expect(describeMatch({ destination: { network: 'n1' } }, names)).toBe('→ network IoT');
    expect(describeMatch({ protocol: 'any' })).toBe('');
    expect(describeMatch(undefined)).toBe('');
  });

  it('names who set an overlay', () => {
    expect(describeOwner({ type: 'user', id: 'admin', name: 'admin' })).toBe('admin');
    expect(describeOwner({ type: 'user', id: 'admin' })).toBe('the admin');
    expect(describeOwner({ type: 'token', id: 't1', name: 'ci-runner' })).toBe('the token ci-runner');
    expect(describeOwner({ type: 'token', id: 't1' })).toBe('the token t1');
    expect(describeOwner({ type: 'run', id: 'r1' })).toBe('a scenario run');
  });
});
