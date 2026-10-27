import { describe, expect, it } from 'vitest';
import type { LanNetwork, WireGuardNetwork } from '../api/generated/model';
import { CompleteSetupBody } from '../api/generated/zod/system/system';
import { networkChanges } from './networkChanges';
import {
  advancedFields,
  emptyNetworkForm,
  formFromNetwork,
  hostPortKey,
  networkFieldOf,
  networkFormErrors,
  networkFromForm,
  peerOf,
  proposeAddress,
  proposeListenPort,
  type NetworkContext,
} from './networkForm';

// The network form (P3-M14-01): its checks under the fields, the network it builds (which the spec
// accepts), the form of a network to edit it, and the gateway's messages by field.

const ctx: NetworkContext = {
  others: [
    { name: 'IoT', address: '10.10.0.1/24', kind: 'lan', ports: ['eth1'] },
    { name: 'lab-hub', address: '10.99.0.1/24', kind: 'hub', listenPort: 51820, ports: [] },
  ],
  reserved: ['eth0'],
};
const hosts = [
  { name: 'eth3', mac: '52:54:00:00:00:04' },
  { name: 'eth4', mac: '52:54:00:00:00:05' },
];

/** The configuration with the network, as the setup body validates it against the spec. */
function acceptedBySpec(id: string, network: unknown) {
  const configuration = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth0' } }, networks: { [id]: network } };
  return CompleteSetupBody.safeParse({ admin_password: 'correct horse battery', configuration }).error;
}

describe('the network form (P3-M14-01)', () => {
  it('a test network with several ports, DHCP and DNS: no problem, and the network the spec accepts', () => {
    const f = {
      ...emptyNetworkForm('lan', '10.30.0.1/24'),
      name: 'cams',
      ports: ['eth3', 'eth4'],
      pools: '10.30.0.100-10.30.0.149, 10.30.0.200 - 10.30.0.219',
      leaseTime: '30m',
      dhcpDns: '1.1.1.1, 9.9.9.9',
      customOptions: '66 tftp.lab',
      redirectDns: true,
      staticEntries: 'broker.lab 10.30.0.5, 10.30.0.6',
      routes: '10.40.0.0/24 via 10.30.0.254',
    };
    expect(networkFormErrors(f, ctx)).toEqual({});
    const n = networkFromForm(f, undefined, hosts as never);
    expect(n).toEqual({
      type: 'lan',
      name: 'cams',
      address: '10.30.0.1/24',
      interfaces: [
        { name: 'eth3', mac: '52:54:00:00:00:04' },
        { name: 'eth4', mac: '52:54:00:00:00:05' },
      ],
      dhcp: {
        lease_time: '30m',
        pools: [
          { start: '10.30.0.100', end: '10.30.0.149' },
          { start: '10.30.0.200', end: '10.30.0.219' },
        ],
        options: { dns_servers: ['1.1.1.1', '9.9.9.9'], custom: [{ code: 66, value: 'tftp.lab' }] },
      },
      dns: { redirect_hardcoded: true, static_entries: [{ name: 'broker.lab', addresses: ['10.30.0.5', '10.30.0.6'] }] },
      routes: [{ destination: '10.40.0.0/24', via: '10.30.0.254' }],
    });
    expect(acceptedBySpec('e1000000-0000-4000-8000-000000000001', n)).toBeUndefined();
    // the defaults stay out: name, address, one port and DHCP on as an empty block (without it the gateway has no DHCP there, P5-AUDIT-10)
    expect(networkFromForm({ ...emptyNetworkForm('lan', '10.30.0.1/24'), name: 'cams', ports: ['eth5'] })).toEqual({
      type: 'lan',
      name: 'cams',
      address: '10.30.0.1/24',
      interfaces: [{ name: 'eth5' }],
      dhcp: {},
    });
  });

  it('says under each field what is wrong', () => {
    const f = {
      ...emptyNetworkForm('lan', '10.10.0.9/24'),
      name: 'IoT',
      ports: [],
      pools: '10.10.0.200-10.10.0.100',
      leaseTime: 'soon',
      router: 'x',
      staticEntries: 'broker.lab',
      routes: '10.40.0.0/24 via 10.99.0.1',
    };
    const e = networkFormErrors(f, ctx);
    expect(e.name).toBe('A network is already called IoT.');
    expect(e.address).toBe('10.10.0.0/24 overlaps IoT (10.10.0.0/24).');
    expect(e.ports).toMatch(/at least one port/);
    expect(e.leaseTime).toMatch(/A duration/);
    expect(e.router).toMatch(/An IPv4 address/);
    expect(e.staticEntries).toMatch(/one entry per line/);
    expect(networkFormErrors({ ...f, address: '10.30.0.0/24' }, ctx).address).toMatch(/network or broadcast address/);
    expect(networkFormErrors({ ...f, address: '10.30.0.1/31' }, ctx).address).toMatch(/at most 30/);
    const ok = { ...f, name: 'cams', address: '10.30.0.1/24', ports: ['eth1'] };
    expect(networkFormErrors(ok, ctx).ports).toBe('eth1 is already a port of IoT.');
    expect(networkFormErrors({ ...ok, ports: ['eth0'] }, ctx).ports).toMatch(/eth0 is the uplink or the management interface/);
    expect(networkFormErrors({ ...ok, ports: ['eth3'] }, ctx).pools).toBe('The end 10.10.0.100 is before the start 10.10.0.200.');
    expect(networkFormErrors({ ...ok, ports: ['eth3'], pools: '10.30.0.1-10.30.0.20' }, ctx).pools).toBe('10.30.0.1-10.30.0.20 contains the gateway address 10.30.0.1.');
    expect(networkFormErrors({ ...ok, ports: ['eth3'], pools: '10.31.0.1-10.31.0.20' }, ctx).pools).toMatch(/must lie inside 10.30.0.0\/24/);
    expect(networkFormErrors({ ...ok, ports: ['eth3'], pools: '', routes: '10.40.0.0/24 via 10.99.0.1' }, ctx).routes).toBe('The router 10.99.0.1 must be a host of 10.30.0.0/24 other than the gateway.');
  });

  it('makes the checks of the gateway the browser can make: reserved ranges and option codes, host bits, the shortest lease', () => {
    const f = { ...emptyNetworkForm('lan', '10.30.0.1/24'), name: 'cams', ports: ['eth3'] };
    expect(networkFormErrors(f, ctx)).toEqual({});
    expect(networkFormErrors({ ...f, address: '127.1.0.1/24' }, ctx).address).toBe('127.1.0.0/24 overlaps the reserved range 127.0.0.0/8.');
    expect(networkFormErrors({ ...f, address: '169.254.10.1/24' }, ctx).address).toMatch(/reserved range 169.254.0.0\/16/);
    expect(networkFormErrors({ ...f, customOptions: '66 tftp.lab\n6 10.0.0.53' }, ctx).customOptions).toMatch(/^Option 6 is set by the gateway/);
    expect(networkFormErrors({ ...f, leaseTime: '0s' }, ctx).leaseTime).toMatch(/at least 1s/);
    expect(networkFormErrors({ ...f, leaseTime: '500ms' }, ctx).leaseTime).toMatch(/at least 1s/);
    expect(networkFormErrors({ ...f, leaseTime: '1s' }, ctx).leaseTime).toBeUndefined();
    expect(networkFormErrors({ ...f, routes: '10.40.0.1/24 via 10.30.0.254' }, ctx).routes).toBe('10.40.0.1/24 has host bits set; the network is 10.40.0.0/24.');
    expect(networkFormErrors({ ...f, routes: '224.1.0.0/16 via 10.30.0.254' }, ctx).routes).toMatch(/reserved range 224.0.0.0\/4/);
    const link = { ...emptyNetworkForm('link', '10.255.0.2/31', '51831'), name: 'site-c' };
    expect(networkFormErrors({ ...link, routes: '10.61.0.0/24, 10.62.0.9/24' }, ctx).routes).toBe('10.62.0.9/24 has host bits set; the network is 10.62.0.0/24.');
  });

  it('DHCP off: the hidden pools that no longer fit a changed subnet go, those that fit stay', () => {
    const lan: LanNetwork = { type: 'lan', name: 'IoT', address: '10.10.0.1/24', interfaces: [{ name: 'eth1' }], dhcp: { pools: [{ start: '10.10.0.100', end: '10.10.0.199' }], options: { domain: 'lab' } } };
    const off = { ...formFromNetwork(lan), dhcpEnabled: false };
    expect((networkFromForm(off, lan) as LanNetwork).dhcp).toEqual({ ...lan.dhcp, enabled: false });
    const moved = networkFromForm({ ...off, address: '10.11.0.1/24' }, lan) as LanNetwork;
    expect(moved.dhcp).toEqual({ enabled: false, options: { domain: 'lab' } });
    expect(acceptedBySpec('e1000000-0000-4000-8000-000000000003', moved)).toBeUndefined();
    // the hidden fields are not checked while DHCP is off
    expect(networkFormErrors({ ...off, name: 'cams', ports: ['eth3'], address: '10.11.0.1/24' }, ctx)).toEqual({});
  });

  it('a WireGuard hub and a link: role, port, MTU, endpoint, the peer of a link', () => {
    const hub = { ...emptyNetworkForm('hub', '10.98.0.1/24', '51830'), name: 'admin', role: 'management' as const, mtu: '1380', endpoint: 'gw.example.org:51830' };
    expect(networkFormErrors(hub, ctx)).toEqual({});
    expect(networkFromForm(hub)).toEqual({ type: 'wireguard', kind: 'hub', name: 'admin', role: 'management', address: '10.98.0.1/24', listen_port: 51830, mtu: 1380, endpoint: 'gw.example.org:51830' });
    expect(networkFormErrors({ ...hub, listenPort: '51820' }, ctx).listenPort).toBe('UDP port 51820 is already used by lab-hub.');
    expect(networkFormErrors({ ...hub, mtu: '9000', endpoint: 'nope' }, ctx)).toMatchObject({ mtu: expect.stringMatching(/1280 to 1500/), endpoint: expect.stringMatching(/Host and port/) });

    const link = { ...emptyNetworkForm('link', '10.255.0.2/31', '51831'), name: 'site-c', peerEndpoint: '203.0.113.50:51821', routes: '10.61.0.0/24, 10.62.0.0/24' };
    expect(link.peerAddress).toBe('10.255.0.3');
    expect(networkFormErrors(link, ctx)).toEqual({});
    const n = networkFromForm(link);
    expect(n).toEqual({
      type: 'wireguard',
      kind: 'link',
      name: 'site-c',
      address: '10.255.0.2/31',
      listen_port: 51831,
      peer: { address: '10.255.0.3', endpoint: '203.0.113.50:51821', key: { mode: 'generated' } },
      routes: ['10.61.0.0/24', '10.62.0.0/24'],
    });
    expect(acceptedBySpec('e1000000-0000-4000-8000-000000000002', n)).toBeUndefined();
    expect(networkFormErrors({ ...link, address: '10.255.0.2/30' }, ctx).address).toMatch(/needs a \/31/);
    expect(networkFormErrors({ ...link, peerAddress: '10.255.0.9' }, ctx).peerAddress).toMatch(/transfer network 10.255.0.2\/31/);
    expect(networkFormErrors({ ...link, peerKeyMode: 'provided', peerPublicKey: 'short' }, ctx).peerPublicKey).toMatch(/public key/);
    expect(peerOf('10.255.0.1/31')).toBe('10.255.0.0');
  });

  it('opens a network again as its form, and keeps what the form does not edit', () => {
    const lan: LanNetwork = {
      type: 'lan',
      name: 'IoT',
      address: '10.10.0.1/24',
      interfaces: [{ mac: '52:54:00:00:00:02' }, { name: 'eth3' }],
      dhcp: { enabled: true, pools: [{ start: '10.10.0.100', end: '10.10.0.199' }], options: { domain: 'lab', ntp_servers: ['10.10.0.1'] } },
      dns: { block_dot: true },
    };
    const f = formFromNetwork(lan);
    expect(f.ports).toEqual(['52:54:00:00:00:02', 'eth3']);
    expect(f.pools).toBe('10.10.0.100-10.10.0.199');
    expect(networkFromForm(f, lan)).toEqual(lan);
    // another port: the one the network had keeps its MAC-only reference
    expect((networkFromForm({ ...f, ports: [...f.ports, 'eth4'] }, lan, hosts as never) as LanNetwork).interfaces).toEqual([{ mac: '52:54:00:00:00:02' }, { name: 'eth3' }, { name: 'eth4', mac: '52:54:00:00:00:05' }]);
    // DHCP off keeps the pools for when it is switched on again
    expect((networkFromForm({ ...f, dhcpEnabled: false }, lan) as LanNetwork).dhcp).toEqual({ ...lan.dhcp, enabled: false });

    const hub: WireGuardNetwork = { type: 'wireguard', kind: 'hub', name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820, clients: { c: { name: 'rA', address: '10.99.0.2' } } };
    expect(networkFromForm({ ...formFromNetwork(hub), listenPort: '51825' }, hub)).toEqual({ ...hub, listen_port: 51825 });
    const link: WireGuardNetwork = { type: 'wireguard', kind: 'link', name: 'site-b', address: '10.255.0.0/31', listen_port: 51821, peer: { address: '10.255.0.1', key: { mode: 'generated', generation: 3, preshared_key: true } } };
    expect(networkFromForm(formFromNetwork(link), link)).toEqual(link);
    expect(networkChanges(link, networkFromForm({ ...formFromNetwork(link), peerEndpoint: '203.0.113.40:51821' }, link))).toEqual([
      { field: 'peer', from: '10.255.0.1', to: '10.255.0.1 at 203.0.113.40:51821' },
    ]);
    // each setting of the peer is named, also next to another change
    expect(networkChanges(link, networkFromForm({ ...formFromNetwork(link), mtu: '1380', peerKeepalive: '15s', peerPresharedKey: false, peerEnabled: false }, link))).toEqual([
      { field: 'MTU', from: '1420', to: '1380' },
      { field: 'peer keepalive', from: '—', to: '15s' },
      { field: 'preshared key', from: 'yes', to: 'no' },
      { field: 'peer enabled', from: 'yes', to: 'no' },
    ]);
  });

  it('the port picker holds a port the configuration names by MAC only under its MAC, and every other port by name', () => {
    const lan: LanNetwork = { type: 'lan', name: 'IoT', address: '10.10.0.1/24', interfaces: [{ mac: '52:54:00:00:00:04' }] };
    const f = formFromNetwork(lan);
    expect(hostPortKey(hosts[0]!, f.ports)).toBe('52:54:00:00:00:04');
    expect(hostPortKey(hosts[1]!, f.ports)).toBe('eth4');
    expect(hostPortKey(hosts[0]!, ['eth3'])).toBe('eth3');
    // unticked, it is ticked again by its MAC
    expect(hostPortKey(hosts[0]!, [], f.ports)).toBe('52:54:00:00:00:04');
    // unticked by that key, the port is gone; ticked again, it is one reference, not two
    expect((networkFromForm({ ...f, ports: [] }, lan, hosts as never) as LanNetwork).interfaces).toEqual([]);
    expect((networkFromForm({ ...f, ports: [hostPortKey(hosts[0]!, f.ports)] }, lan, hosts as never) as LanNetwork).interfaces).toEqual([{ mac: '52:54:00:00:00:04' }]);
  });

  it('knows which fields stand under Advanced, so a check of a hidden field opens it', () => {
    const lan = formFromNetwork({ type: 'lan', name: 'cams', address: '10.30.0.1/24', interfaces: [{ name: 'eth3' }], routes: [{ destination: '10.40.0.0/24', via: '10.30.0.254' }] });
    expect(networkFormErrors(lan, ctx)).toEqual({});
    const e = networkFormErrors({ ...lan, address: '10.31.0.1/24' }, ctx);
    expect(Object.keys(e)).toEqual(['routes']);
    expect(advancedFields('lan')).toContain('routes');
    expect(advancedFields('link')).not.toContain('routes');
    expect(advancedFields('link')).toContain('peerPublicKey');
    expect(advancedFields('hub')).toEqual(['description', 'endpoint', 'mtu']);
  });

  it("puts the gateway's messages under the field they are about", () => {
    expect(networkFieldOf('address')).toBe('address');
    expect(networkFieldOf('interfaces/1/name')).toBe('ports');
    expect(networkFieldOf('dhcp/pools/0/end')).toBe('pools');
    expect(networkFieldOf('dhcp/options/dns_servers/0')).toBe('dhcpDns');
    expect(networkFieldOf('dns/static_entries/0/name')).toBe('staticEntries');
    expect(networkFieldOf('listen_port')).toBe('listenPort');
    expect(networkFieldOf('peer/key/public_key')).toBe('peerPublicKey');
    expect(networkFieldOf('peer')).toBe('peerAddress');
    expect(networkFieldOf('clients/x/address')).toBeUndefined();
    expect(networkFieldOf('network')).toBeUndefined();
  });

  it('proposes a free subnet and a free listen port', () => {
    expect(proposeAddress('lan', ['10.10.0.1/24', '10.30.0.1/24'])).toBe('10.31.0.1/24');
    expect(proposeAddress('hub', ['10.99.0.1/24'])).toBe('10.90.0.1/24');
    expect(proposeAddress('link', ['10.255.0.0/31'])).toBe('10.255.0.2/31');
    expect(proposeListenPort([51820, 51821])).toBe('51822');
  });
});

describe('the dhcp block of a LAN (P5-AUDIT-10)', () => {
  it('is written whenever DHCP is on, empty with the defaults, because the gateway reads a LAN without it as DHCP off', () => {
    const f = { ...emptyNetworkForm('lan', '10.30.0.1/24'), name: 'lab2' };
    expect((networkFromForm(f) as LanNetwork).dhcp).toEqual({});
    expect((networkFromForm({ ...f, dhcpEnabled: false }) as LanNetwork).dhcp).toEqual({ enabled: false });
  });

  it('reads a LAN without the block as DHCP off, and one with an empty block as on', () => {
    const lan: LanNetwork = { type: 'lan', name: 'IoT', address: '10.10.0.1/24', interfaces: [{ name: 'eth1' }] };
    expect(formFromNetwork(lan).dhcpEnabled).toBe(false);
    expect(formFromNetwork({ ...lan, dhcp: {} }).dhcpEnabled).toBe(true);
    expect(formFromNetwork({ ...lan, dhcp: { enabled: false } }).dhcpEnabled).toBe(false);
  });
});

