import { describe, expect, it } from 'vitest';
import type { DeviceView, DhcpLease, Device, LanNetwork, Network } from '../api/generated/model';
import { CreateRevisionBody } from '../api/generated/zod/revisions/revisions';
import {
  adoptReservation,
  deviceFieldOf,
  deviceFormErrors,
  deviceFromForm,
  deviceNameError,
  draftDevices,
  fixedIpError,
  fixedIpHints,
  foreignLeaseMac,
  formFromDevice,
  macOwner,
  renewalTime,
  reservationMacs,
  warningHint,
  withFirstMac,
  type ReservationHintContext,
} from './deviceForm';
import { reservationPoolHint } from './networks';

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';
const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const CAM = 'a1b2c3d4-0000-4000-8000-000000000057';
const MAC = '02:00:00:00:00:2a';
const WIFI = '02:00:00:00:00:2b';

const iot: Network = { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24', dhcp: { enabled: true } };
const hub: Network = { type: 'wireguard', kind: 'hub', name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820 };

function ctx(over: Partial<ReservationHintContext> = {}): ReservationHintContext {
  return {
    device: ESP,
    network: iot,
    networkId: IOT,
    macs: [MAC],
    devices: { [ESP]: { name: 'ESP32-42', network: IOT }, [CAM]: { name: 'cam-1', network: IOT, fixed_ip: '10.10.0.20' } },
    ...over,
  };
}

/** The device as the spec checks it in a revision. */
const valid = (d: Device) =>
  CreateRevisionBody.safeParse({ schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, devices: { [ESP]: d } }).success;

describe('the fixed IP of a device (#73)', () => {
  it('accepts a host address of the network that nobody else has reserved', () => {
    expect(fixedIpError('10.10.0.50', ctx())).toBeUndefined();
    expect(fixedIpError(' 10.10.0.254 ', ctx())).toBeUndefined();
    // the device's own reservation is no duplicate
    expect(fixedIpError('10.10.0.20', ctx({ device: CAM }))).toBeUndefined();
  });

  it('refuses what the gateway refuses, in its words', () => {
    expect(fixedIpError('10.10.0', ctx())).toBe('Not an IPv4 address, such as 10.10.0.50.');
    expect(fixedIpError('10.10.0.300', ctx())).toBe('Not an IPv4 address, such as 10.10.0.50.');
    // the gateway refuses an octet with a leading zero
    expect(fixedIpError('10.10.0.050', ctx())).toBe('Not an IPv4 address, such as 10.10.0.50.');
    expect(fixedIpError('10.10.0.50', ctx({ network: undefined }))).toBe('A fixed address is a DHCP reservation in a network: choose the network.');
    expect(fixedIpError('10.99.0.5', ctx({ network: hub }))).toBe('A DHCP reservation needs a local test network; lab-hub is a WireGuard network.');
    expect(fixedIpError('10.10.0.50', ctx({ macs: [] }))).toBe("The reservation is for the device's first MAC address: the device has none.");
    expect(fixedIpError('10.20.0.5', ctx())).toBe('10.20.0.5 is not a host address of IoT (10.10.0.0/24).');
    expect(fixedIpError('10.10.0.0', ctx())).toBe('10.10.0.0 is not a host address of IoT (10.10.0.0/24).');
    expect(fixedIpError('10.10.0.255', ctx())).toBe('10.10.0.255 is not a host address of IoT (10.10.0.0/24).');
    expect(fixedIpError('10.10.0.1', ctx())).toBe("10.10.0.1 is the gateway's own address in IoT.");
    expect(fixedIpError('10.10.0.20', ctx())).toBe('10.10.0.20 is already reserved for device cam-1.');
    // a reservation of the same address in another network is none
    expect(fixedIpError('10.10.0.20', ctx({ devices: { [CAM]: { name: 'cam-1', network: 'other', fixed_ip: '10.10.0.20' } } }))).toBeUndefined();
  });

  it('notes an address in the dynamic pool, but allows it (decision on #76)', () => {
    // the default pool: the second half of the subnet
    const hints = fixedIpHints('10.10.0.200', ctx());
    expect(fixedIpError('10.10.0.200', ctx())).toBeUndefined();
    expect(hints.find((h) => h.kind === 'pool')?.text).toBe(
      '10.10.0.200 lies in the default DHCP pool 10.10.0.128–10.10.0.254 of IoT. The gateway allows it, but while another client holds the address the device gets a different one from the pool, until that lease is released; an address outside the pool is safer.',
    );
    expect(fixedIpHints('10.10.0.50', ctx()).some((h) => h.kind === 'pool')).toBe(false);
    // configured pools
    const pools: Network = { ...iot, dhcp: { pools: [{ start: '10.10.0.10', end: '10.10.0.60' }] } };
    expect(fixedIpHints('10.10.0.50', ctx({ network: pools })).find((h) => h.kind === 'pool')?.text).toBe(reservationPoolHint(pools as LanNetwork, '10.10.0.50'));
    expect(fixedIpHints('10.10.0.50', ctx({ network: pools })).find((h) => h.kind === 'pool')?.text).toContain('lies in the DHCP pool 10.10.0.10–10.10.0.60 of IoT.');
    expect(fixedIpHints('10.10.0.200', ctx({ network: pools })).some((h) => h.kind === 'pool')).toBe(false);
    // a network without a DHCP scope makes no reservation, as the gateway's fixed_ip_in_pool has it
    const { dhcp: _dhcp, ...bare } = iot as LanNetwork;
    expect(fixedIpHints('10.10.0.200', ctx({ network: bare })).some((h) => h.kind === 'pool')).toBe(false);
  });

  it('notes an address leased to another client now', () => {
    const leases: DhcpLease[] = [
      { ip: '10.10.0.133', mac: '3a:f1:9c:22:07:b4', hostname: 'phone', network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active' },
      { ip: '10.10.0.42', mac: MAC, network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active' },
      { ip: '10.10.0.60', mac: '3a:f1:9c:22:07:b5', network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'expired' },
    ];
    expect(fixedIpHints('10.10.0.133', ctx({ leases })).find((h) => h.kind === 'leased')?.text).toBe(
      '10.10.0.133 is leased to 3a:f1:9c:22:07:b4 (phone) now: the device gets it only once that lease is released or runs out, and an address from the pool until then.',
    );
    // its own lease and an expired one are no obstacle
    expect(fixedIpHints('10.10.0.42', ctx({ leases })).some((h) => h.kind === 'leased')).toBe(false);
    expect(fixedIpHints('10.10.0.60', ctx({ leases })).some((h) => h.kind === 'leased')).toBe(false);
  });

  it("takes a lease of any of the device's MACs for its own, and says when it is not the reserved MAC's (#96)", () => {
    const wifi: DhcpLease[] = [{ ip: '10.10.0.42', mac: WIFI.toUpperCase(), network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active' }];
    // an unchanged reservation stays on the first MAC, while the device holds the address with its second
    const unchanged = fixedIpHints('10.10.0.42', ctx({ macs: [MAC, WIFI], leases: wifi, before: '10.10.0.42', beforeNetwork: IOT }));
    expect(unchanged.some((h) => h.kind === 'leased')).toBe(false);
    expect(unchanged.find((h) => h.kind === 'other_mac')?.text).toBe(
      `The device holds 10.10.0.42 with its MAC ${WIFI.toUpperCase()} now, but the reservation is for its first MAC, ${MAC}: it keeps the address only while it asks with that one. Put ${WIFI.toUpperCase()} first among the device's MACs to reserve the address for the MAC that holds the lease.`,
    );
    // a new reservation on the MAC of the lease puts it first: then the lease is the reserved MAC's
    const moved = fixedIpHints('10.10.0.42', ctx({ macs: [MAC, WIFI], leases: wifi, leaseMac: WIFI, leaseIp: '10.10.0.42' }));
    expect(moved.map((h) => h.kind)).toEqual(['kept', 'first_mac']);
    expect(warningHint('other_mac')).toBe(true);
    expect(warningHint('first_mac')).toBe(false);
    // a lease of the first MAC needs no note; another client's lease still is one, and wins over the device's own
    expect(fixedIpHints('10.10.0.42', ctx({ macs: [WIFI, MAC], leases: wifi })).some((h) => h.kind === 'leased' || h.kind === 'other_mac')).toBe(false);
    const both: DhcpLease[] = [...wifi, { ip: '10.10.0.42', mac: '3a:f1:9c:22:07:b4', network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active' }];
    expect(fixedIpHints('10.10.0.42', ctx({ macs: [MAC, WIFI], leases: both, before: '10.10.0.42' })).map((h) => h.kind)).toEqual(['leased', 'first_mac']);
  });

  it('notes that DHCP is off, and when a new reservation takes effect', () => {
    const off: Network = { ...iot, dhcp: { enabled: false } };
    expect(fixedIpHints('10.10.0.50', ctx({ network: off })).map((h) => h.kind)).toEqual(['dhcp_off', 'first_mac']);
    // no scope at all is DHCP off as well (#94): no pool hint for the default pool, no renewal note
    const none: Network = { ...iot, dhcp: undefined };
    expect(fixedIpHints('10.10.0.150', ctx({ network: none })).map((h) => h.kind)).toEqual(['dhcp_off', 'first_mac']);
    expect(fixedIpHints('10.10.0.50', ctx()).find((h) => h.kind === 'dhcp_off')).toBeUndefined();
    expect(fixedIpHints('10.10.0.50', ctx()).find((h) => h.kind === 'renewal')?.text).toBe(
      'The device gets 10.10.0.50 when it renews its lease, which takes up to half the lease time (30 min).',
    );
    // unchanged: no renewal note
    expect(fixedIpHints('10.10.0.50', ctx({ before: '10.10.0.50' })).some((h) => h.kind === 'renewal')).toBe(false);
    expect(fixedIpHints('10.10.0.50', ctx({ before: '10.10.0.50', beforeNetwork: IOT })).some((h) => h.kind === 'renewal')).toBe(false);
    // the same address in another network is a new reservation
    expect(fixedIpHints('10.10.0.50', ctx({ before: '10.10.0.50', beforeNetwork: 'other' })).some((h) => h.kind === 'renewal')).toBe(true);
    const short: Network = { ...iot, dhcp: { lease_time: '10m' } };
    expect(fixedIpHints('10.10.0.50', ctx({ network: short })).find((h) => h.kind === 'renewal')?.text).toContain('(5 min)');
    // cleared
    expect(fixedIpHints('', ctx({ before: '10.10.0.50' }))).toEqual([
      { kind: 'removed', text: 'Without the reservation the device keeps 10.10.0.50 until its lease runs out; then it gets an address from the pool.' },
    ]);
    expect(fixedIpHints('', ctx())).toEqual([]);
  });

  it('says which MAC the reservation is for (decision on #77)', () => {
    expect(fixedIpHints('10.10.0.50', ctx()).find((h) => h.kind === 'first_mac')?.text).toBe(`The reservation is for the device's MAC ${MAC}.`);
    expect(fixedIpHints('10.10.0.50', ctx({ macs: [MAC, WIFI] })).find((h) => h.kind === 'first_mac')?.text).toBe(
      `The reservation is for the device's first MAC, ${MAC}, not for its other one.`,
    );
    expect(fixedIpHints('10.10.0.50', ctx({ macs: [MAC, WIFI], leaseMac: WIFI })).find((h) => h.kind === 'first_mac')?.text).toBe(
      `The reservation is for the device's first MAC; ${WIFI}, the MAC of its current lease, is put first.`,
    );
    // an unchanged reservation stays on the first MAC, as deviceFromForm leaves the order alone
    expect(fixedIpHints('10.10.0.50', ctx({ macs: [MAC, WIFI], leaseMac: WIFI, before: '10.10.0.50', beforeNetwork: IOT })).find((h) => h.kind === 'first_mac')?.text).toBe(
      `The reservation is for the device's first MAC, ${MAC}, not for its other one.`,
    );
    expect(reservationMacs([MAC, WIFI], WIFI.toUpperCase())).toEqual([WIFI, MAC]);
    expect(reservationMacs([MAC, WIFI], MAC)).toEqual([MAC, WIFI]);
    expect(reservationMacs([MAC], '02:00:00:00:00:ff')).toEqual([MAC]);
    expect(renewalTime(undefined)).toBe('30 min');
    expect(renewalTime('3h')).toBe('1 h 30 min');
    expect(renewalTime('90s')).toBe('45 s');
  });
});

describe('the device dialog (#73)', () => {
  const esp: Device = { name: 'ESP32-42', identifiers: { macs: [MAC, WIFI] }, network: IOT, trusts_test_ca: true, labels: ['kitchen'] };

  it('sets, changes and clears the fixed IP and keeps the rest of the device', () => {
    const f = formFromDevice(esp);
    expect(f).toEqual({ name: 'ESP32-42', network: IOT, fixedIp: '' });
    const set = deviceFromForm({ ...f, fixedIp: ' 10.10.0.50 ' }, esp);
    expect(set).toEqual({ ...esp, fixed_ip: '10.10.0.50' });
    expect(valid(set)).toBe(true);
    // a new reservation puts the MAC of the current lease first
    expect(deviceFromForm({ ...f, fixedIp: '10.10.0.50' }, esp, WIFI).identifiers).toEqual({ macs: [WIFI, MAC] });
    // an unchanged one leaves the order alone
    expect(deviceFromForm({ ...f, name: 'esp', fixedIp: '10.10.0.50' }, { ...esp, fixed_ip: '10.10.0.50' }, WIFI)).toEqual({ ...esp, name: 'esp', fixed_ip: '10.10.0.50' });
    // the same address in another network is a new reservation too
    expect(deviceFromForm({ ...f, network: 'other', fixedIp: '10.10.0.50' }, { ...esp, fixed_ip: '10.10.0.50' }, WIFI).identifiers).toEqual({ macs: [WIFI, MAC] });
    const cleared = deviceFromForm({ ...f, fixedIp: '' }, { ...esp, fixed_ip: '10.10.0.50' });
    expect(cleared).toEqual(esp);
    expect('fixed_ip' in cleared).toBe(false);
    const noNet = deviceFromForm({ ...f, network: '' }, esp);
    expect('network' in noNet).toBe(false);
  });

  it("offers the MAC of a lease that is not among the device's MACs as its first (#98)", () => {
    const LEASE = '02:00:00:00:00:99';
    expect(foreignLeaseMac([MAC, WIFI], WIFI.toUpperCase())).toBeUndefined();
    expect(foreignLeaseMac([MAC], undefined)).toBeUndefined();
    expect(foreignLeaseMac([MAC], LEASE)).toBe(LEASE);
    expect(foreignLeaseMac([], LEASE)).toBe(LEASE);
    expect(withFirstMac({ ...esp, identifiers: { macs: [MAC, WIFI], ipv4: ['10.10.0.60'] } }, LEASE)).toEqual({
      ...esp,
      identifiers: { macs: [LEASE, MAC, WIFI], ipv4: ['10.10.0.60'] },
    });
    expect(withFirstMac({ name: 'x' }, LEASE).identifiers).toEqual({ macs: [LEASE] });
    // added, a new reservation stays on it (the lease's MAC is first already)
    const reserved = deviceFromForm({ ...formFromDevice(esp), fixedIp: '10.10.0.60' }, withFirstMac(esp, LEASE), LEASE);
    expect(reserved.identifiers).toEqual({ macs: [LEASE, MAC, WIFI] });
    expect(valid(reserved)).toBe(true);
    const devices: Record<string, Device> = { [ESP]: esp, [CAM]: { name: 'cam-1', identifiers: { macs: [LEASE.toUpperCase()] } } };
    expect(macOwner(LEASE, ESP, devices)?.name).toBe('cam-1');
    expect(macOwner(MAC, ESP, devices)).toBeUndefined();
  });

  it('checks the name and the fixed IP', () => {
    const f = formFromDevice(esp);
    expect(deviceFormErrors(f, ctx({ macs: esp.identifiers!.macs! }))).toEqual({});
    expect(deviceFormErrors({ ...f, name: '-x', fixedIp: '10.10.0.1' }, ctx())).toEqual({
      name: 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.',
      fixedIp: "10.10.0.1 is the gateway's own address in IoT.",
    });
    expect(deviceFormErrors({ ...f, name: 'CAM-1' }, { ...ctx(), takenNames: ['cam-1'] })).toEqual({ name: 'The name CAM-1 is already used by another device.' });
    expect(deviceNameError('esp32-42', ['ESP32-43'])).toBeUndefined();
  });

  it("maps the gateway's pointers to the fields", () => {
    expect(deviceFieldOf('fixed_ip')).toBe('fixedIp');
    expect(deviceFieldOf('name')).toBe('name');
    expect(deviceFieldOf('network')).toBe('network');
    expect(deviceFieldOf('identifiers')).toBeUndefined();
  });
});

describe('the adopt dialog (#74)', () => {
  const lease = { ip: '10.10.0.42', mac: WIFI, network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active' as const };
  const seen = (over: Partial<DeviceView['observed']> = {}): DeviceView => ({
    id: ESP,
    name: 'dev-02-00-00-00-00-2b',
    origin: 'discovered',
    network: IOT,
    observed: { online: true, macs: [MAC, WIFI], addresses: ['10.10.0.42'], lease, ...over },
  });

  it('offers to keep the address of the current lease only where a reservation can work', () => {
    expect(adoptReservation(seen(), iot)).toEqual({ ip: '10.10.0.42', mac: WIFI });
    expect(adoptReservation(seen({ macs: [] }), iot)).toBeUndefined();
    expect(adoptReservation({ ...seen(), network: undefined }, iot)).toBeUndefined();
    expect(adoptReservation(seen(), undefined)).toBeUndefined();
    expect(adoptReservation(seen(), hub)).toBeUndefined();
    expect(adoptReservation(seen(), { ...iot, dhcp: { enabled: false } })).toBeUndefined();
    expect(adoptReservation(seen({ lease: undefined }), iot)).toBeUndefined();
    expect(adoptReservation(seen({ lease: { ...lease, network: 'other' } }), iot)).toBeUndefined();
  });

  it('notes that the device keeps the address of its lease, on the MAC of the lease', () => {
    const hints = fixedIpHints('10.10.0.42', ctx({ macs: [MAC, WIFI], leaseMac: WIFI, leaseIp: '10.10.0.42', leases: [{ ...lease }] }));
    expect(hints).toEqual([
      { kind: 'kept', text: 'The device has 10.10.0.42 from its current lease and keeps it; from now on the address is reserved for it.' },
      { kind: 'first_mac', text: `The reservation is for the device's first MAC; ${WIFI}, the MAC of its current lease, is put first.` },
    ]);
    // another address takes the renewal
    expect(fixedIpHints('10.10.0.50', ctx({ leaseMac: MAC, leaseIp: '10.10.0.42' })).map((h) => h.kind)).toEqual(['renewal', 'first_mac']);
  });

  it('builds the devices of the draft from the base and the edits', () => {
    const base = { [ESP]: { name: 'ESP32-42' }, [CAM]: { name: 'cam-1' } };
    expect(draftDevices(base, { [CAM]: null, x: { name: 'x' } })).toEqual({ [ESP]: { name: 'ESP32-42' }, x: { name: 'x' } });
    expect(draftDevices(undefined, {})).toEqual({});
  });
});
