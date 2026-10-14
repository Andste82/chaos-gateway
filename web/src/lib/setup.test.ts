import { describe, expect, it } from 'vitest';
import type { HostInterface } from '../api/generated/model';
import {
  buildSetupRequest,
  managementURL,
  prefixRange,
  proposeNetwork,
  suggestAddress,
  validateInterfaces,
  validateNetworks,
  validatePassword,
} from './setup';

const ifaces: HostInterface[] = [
  { name: 'eth0', mac: '52:54:00:00:00:01', addresses: ['192.168.1.57/24'] },
  { name: 'eth1', mac: '52:54:00:00:00:02', addresses: ['192.168.56.10/24'] },
  { name: 'eth2', mac: '52:54:00:00:00:03' },
];

describe('setup wizard', () => {
  it('proposes distinct test networks', () => {
    expect(suggestAddress(0)).toBe('10.10.0.1/24');
    expect(suggestAddress(1)).toBe('10.20.0.1/24');
    expect(prefixRange('10.10.0.1/24')).toEqual([168427520, 168427775]);
  });

  it('proposes a network that no kept network uses yet', () => {
    // lan0 and lan1 chosen, lan0 unchecked again, lan2 chosen: lan1 keeps test-2 and 10.20.0.1/24
    const lan1 = { iface: 'lan1', name: 'test-2', address: '10.20.0.1/24', dhcp: true };
    const lan2 = proposeNetwork('lan2', [lan1]);
    expect(lan2).toEqual({ iface: 'lan2', name: 'test', address: '10.10.0.1/24', dhcp: true });
    expect(validateNetworks([lan1, lan2], [])).toEqual({});
    // a name or prefix typed by the user counts too, and so do the uplink and management networks
    const typed = { iface: 'lan0', name: 'TEST', address: '10.30.0.1/16', dhcp: true };
    expect(proposeNetwork('lan3', [typed, lan1], ['10.40.0.5/24'])).toMatchObject({ name: 'test-5', address: '10.50.0.1/24' });
  });

  it('needs an uplink, a management interface and a test interface that is neither', () => {
    expect(validateInterfaces({ uplink: '', management: '', networks: [] }, 'eth0')).toEqual({
      uplink: expect.any(String),
      management: expect.any(String),
      tests: 'Assign at least one interface to a test network.',
    });
    const net = { iface: 'eth0', name: 'x', address: '10.10.0.1/24', dhcp: true };
    expect(validateInterfaces({ uplink: 'eth0', management: 'eth1', networks: [net] }, 'eth0').tests).toMatch(/cannot be test interfaces/);
    // the two-port topology: management on the uplink
    expect(validateInterfaces({ uplink: 'eth0', management: 'eth0', networks: [{ ...net, iface: 'eth2' }] }, 'eth0')).toEqual({});
  });

  it('wants the router named for an uplink without the default route, inside its network', () => {
    const d = { uplink: 'eth1', management: 'eth0', networks: [{ iface: 'eth2', name: 'x', address: '10.10.0.1/24', dhcp: true }] };
    expect(validateInterfaces(d, 'eth0').gateway).toMatch(/does not leave through eth1: name the router/);
    expect(validateInterfaces({ ...d, gateway: '192.168.56.1' }, 'eth0', ['192.168.56.10/24'])).toEqual({});
    expect(validateInterfaces({ ...d, gateway: '10.0.0.1' }, 'eth0', ['192.168.56.10/24']).gateway).toMatch(/in the uplink's network/);
    expect(validateInterfaces({ ...d, gateway: '300.1.1.1' }, 'eth0').gateway).toMatch(/An IPv4 address/);
    // the uplink of the default route may name one, but need not
    expect(validateInterfaces({ ...d, uplink: 'eth0', management: 'eth1' }, 'eth0')).toEqual({});
  });

  it('validates the test networks under each field', () => {
    const ok = { iface: 'eth2', name: 'IoT', address: '10.10.0.1/24', dhcp: true };
    expect(validateNetworks([ok])).toEqual({});
    expect(validateNetworks([{ ...ok, name: '-x' }])['0.name']).toMatch(/Letters, digits/);
    expect(validateNetworks([{ ...ok, address: '10.10.0.1' }])['0.address']).toMatch(/with prefix length/);
    expect(validateNetworks([{ ...ok, address: '10.10.0.0/24' }])['0.address']).toMatch(/network or broadcast/);
    expect(validateNetworks([{ ...ok, address: '10.10.0.255/24' }])['0.address']).toMatch(/network or broadcast/);
    expect(validateNetworks([{ ...ok, address: '10.0.0.1/31' }])['0.address']).toMatch(/between \/8 and \/30/);
    const two = validateNetworks([ok, { ...ok, iface: 'eth3', name: 'iot', address: '10.10.0.129/25' }]);
    expect(two['1.name']).toBe('Another test network has this name.');
    expect(two['1.address']).toBe('This prefix overlaps another test network.');
    expect(validateNetworks([{ ...ok, address: '192.168.56.1/24' }], ['192.168.56.10/24'])['0.address']).toMatch(/uplink or the management/);
  });

  it('wants a password of 12 characters, typed twice', () => {
    expect(validatePassword('short', 'short').password).toBe('At least 12 characters.');
    expect(validatePassword('long enough pw', 'long enough pX').repeat).toBe('The passwords differ.');
    expect(validatePassword('long enough pw', 'long enough pw')).toEqual({});
  });

  it('builds revision 1 of the request from the choices, as the spec wants it', () => {
    let n = 0;
    const body = buildSetupRequest(
      {
        uplink: 'eth0',
        gateway: '192.168.1.1',
        management: 'eth1',
        networks: [{ iface: 'eth2', name: 'IoT', address: '10.10.0.1/24', dhcp: false }],
        password: 'correct horse battery staple',
        repeat: 'correct horse battery staple',
      },
      ifaces,
      () => `00000000-0000-4000-8000-00000000000${++n}`,
    );
    expect(body).toEqual({
      admin_password: 'correct horse battery staple',
      configuration: {
        schema_version: 1,
        uplink: { interface: { name: 'eth0', mac: '52:54:00:00:00:01' }, gateway: '192.168.1.1' },
        management: { interface: { name: 'eth1', mac: '52:54:00:00:00:02' } },
        networks: {
          '00000000-0000-4000-8000-000000000001': {
            type: 'lan',
            name: 'IoT',
            interfaces: [{ name: 'eth2', mac: '52:54:00:00:00:03' }],
            address: '10.10.0.1/24',
            dhcp: { enabled: false },
          },
        },
      },
    });
    // the generated schema refuses what the spec refuses
    expect(() =>
      buildSetupRequest({ uplink: 'eth0', management: 'eth1', networks: [], password: 'short', repeat: 'short' }, ifaces),
    ).toThrow();
  });

  it('knows where the gateway answers after the setup', () => {
    expect(managementURL(ifaces[1], { protocol: 'https:', port: '8443' })).toBe('https://192.168.56.10:8443/');
    expect(managementURL(ifaces[2], { protocol: 'https:', port: '' })).toBeUndefined();
  });
});
