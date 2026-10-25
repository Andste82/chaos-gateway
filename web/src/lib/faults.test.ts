import { describe, expect, it } from 'vitest';
import type { Overlay } from '../api/generated/model';
import { describeMatch, describeOwner, describeScope, faultChips, impairmentCombines, impairmentOf, overlayChips, withImpairment } from './faults';

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
      'profile Bad LTE',
    ]);
    // the same label as on the faults and profiles screens, with or without a name lookup
    expect(overlayChips({ ...base, kind: 'profile', profile: 'lte' } as Overlay)).toEqual(['profile LTE']);
    expect(overlayChips({ ...base, kind: 'profile', profile: 'my-own' } as Overlay)).toEqual(['profile my-own']);
    expect(overlayChips({ ...base, kind: 'dhcp', dhcp: { action: 'silence' } } as Overlay)).toEqual(['DHCP silence']);
  });

  it('say what a DHCP action does', () => {
    const base = { id: 'o', owner: { type: 'user', id: 'admin' }, created_at: '', updated_at: '', generation: 1, kind: 'dhcp' } as const;
    expect(overlayChips({ ...base, dhcp: { action: 'short_lease', lease_time: '7s' } } as Overlay)).toEqual(['DHCP short lease 7s']);
    expect(overlayChips({ ...base, dhcp: { action: 'delete_lease' } } as Overlay)).toEqual(['DHCP delete lease']);
    expect(overlayChips({ ...base, dhcp: { action: 'force_new_ip', ip: '10.10.0.200' } } as Overlay)).toEqual(['DHCP new IP 10.10.0.200']);
    expect(overlayChips({ ...base, dhcp: { action: 'set_options', options: { router: '10.10.0.9', dns_servers: ['1.1.1.1'], custom: [{ code: 224, value: '0a' }] } } } as Overlay)).toEqual([
      'DHCP options · router, DNS, option 224',
    ]);
    expect(overlayChips({ ...base, dhcp: { action: 'silence' } } as Overlay)).toEqual(['DHCP silence']);
    expect(overlayChips({ ...base } as Overlay)).toEqual(['DHCP action']);
  });

  it('say what a DNS fault does and for which names', () => {
    const base = { id: 'o', owner: { type: 'user', id: 'admin' }, created_at: '', updated_at: '', generation: 1, kind: 'dns' } as const;
    expect(overlayChips({ ...base, dns: { action: 'nxdomain' } } as Overlay)).toEqual(['DNS nxdomain']);
    expect(overlayChips({ ...base, dns: { action: 'delay', delay: '2s', names: ['broker.example.com', '*.cdn.example.net'] } } as Overlay)).toEqual([
      'DNS delay 2s · broker.example.com, *.cdn.example.net',
    ]);
    expect(overlayChips({ ...base, dns: { action: 'wrong_answer', answers: ['10.9.0.1'], names: ['broker.example.com'] } } as Overlay)).toEqual([
      'DNS answer 10.9.0.1 · broker.example.com',
    ]);
    expect(overlayChips({ ...base, dns: { action: 'short_ttl', ttl: '1s' } } as Overlay)).toEqual(['DNS TTL 1s']);
    expect(overlayChips({ ...base, dns: { action: 'truncate' } } as Overlay)).toEqual(['DNS truncate']);
  });

  it('say which certificate case a TLS case shows and to which connections', () => {
    const base = { id: 'o', owner: { type: 'user', id: 'admin' }, created_at: '', updated_at: '', generation: 1, kind: 'tls' } as const;
    expect(overlayChips({ ...base, tls: { case: 'expired' } } as Overlay)).toEqual(['TLS expired']);
    expect(overlayChips({ ...base, tls: { case: 'self_signed', ports: [8883, 443], sni: ['broker.example.com'] } } as Overlay)).toEqual([
      'TLS self signed · 8883, 443 · SNI broker.example.com',
    ]);
    expect(
      overlayChips({ ...base, tls: { case: 'untrusted_ca', port_ranges: [{ from: 8000, to: 8100 }], destination: { cidr: '203.0.113.0/24' }, cut_existing: true } } as Overlay),
    ).toEqual(['TLS untrusted ca · 8000-8100 · 203.0.113.0/24 · cuts existing']);
    expect(overlayChips({ ...base, tls: { case: 'handshake_reset', destination: { hostname: '*.example.com' } } } as Overlay)).toEqual([
      'TLS handshake reset · *.example.com',
    ]);
    // the interception says what its settings change
    expect(overlayChips({ ...base, tls: { case: 'intercept' } } as Overlay)).toEqual(['TLS intercept']);
    expect(
      overlayChips({
        ...base,
        tls: { case: 'intercept', ports: [443], intercept: { block_quic: false, keylog: false, upstream: 'insecure', http_rules: [{ action: 'block' }, { action: 'delay', delay: '1s' }] } },
      } as Overlay),
    ).toEqual(['TLS intercept · 443 · 2 HTTP rules · QUIC allowed · no key log · servers not verified']);
    // the policy for the servers' certificates, each value; none when the installation decides
    for (const [upstream, chip] of [
      ['verify', 'TLS intercept · servers verified (bundle)'],
      ['own_ca', 'TLS intercept · servers verified (own CA)'],
    ] as const) {
      expect(overlayChips({ ...base, tls: { case: 'intercept', intercept: { upstream } } } as Overlay)).toEqual([chip]);
    }
    expect(overlayChips({ ...base, tls: { case: 'intercept', intercept: { http_rules: [{ action: 'abort' }] } } } as Overlay)).toEqual(['TLS intercept · 1 HTTP rule']);
    expect(overlayChips({ ...base } as Overlay)).toEqual(['TLS case']);
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
    // a WireGuard client is a device of the same id and name
    expect(describeScope({ remote_network: { client: 'd1' } }, names)).toBe('Client networks of ESP32-42');
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

describe('impairment parameter sets', () => {
  it('takes the flat parameters and the directions out of a fault body, and puts another set in', () => {
    const f = { name: 'x', source: { global: true }, family: 'impairment' as const, latency: '20ms', upload: { loss: '1%' } };
    expect(impairmentOf(f)).toEqual({ latency: '20ms', upload: { loss: '1%' } });
    expect(impairmentOf({ family: 'mtu', mtu: { size: 1280 } })).toBeUndefined();
    expect(withImpairment(f, { loss: '2%' })).toEqual({ name: 'x', source: { global: true }, family: 'impairment', loss: '2%' });
  });

  it('says whether the gateway takes a combination: no flat parameters with directions, no exclusive pair, no jitter above the latency', () => {
    expect(impairmentCombines(undefined)).toBe(true);
    expect(impairmentCombines({ latency: '20ms', jitter: '5ms', loss: '1%' })).toBe(true);
    expect(impairmentCombines({ upload: { latency: '20ms' }, download: { loss: '1%' } })).toBe(true);
    expect(impairmentCombines({ loss: '2%', upload: { latency: '20ms' } })).toBe(false);
    expect(impairmentCombines({ loss: '1%', burst_loss: { p: '5%', r: '30%' } })).toBe(false);
    expect(impairmentCombines({ blackout: true, flapping: { up: '20s', down: '10s' } })).toBe(false);
    expect(impairmentCombines({ download: { latency: '10ms', jitter: '20ms' } })).toBe(false);
  });
});
