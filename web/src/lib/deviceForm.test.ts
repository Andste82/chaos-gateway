import { describe, expect, it } from 'vitest';
import type { DhcpLease, Device, Network } from '../api/generated/model';
import { CreateRevisionBody } from '../api/generated/zod/revisions/revisions';
import {
  deviceFieldOf,
  deviceFormErrors,
  deviceFromForm,
  deviceNameError,
  fixedIpError,
  fixedIpHints,
  formFromDevice,
  renewalTime,
  reservationMacs,
  type ReservationHintContext,
} from './deviceForm';

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
      '10.10.0.200 lies in the DHCP pool 10.10.0.128–10.10.0.254 of IoT. The gateway allows it, but the pool may hand it to another client first; an address outside the pool is safer.',
    );
    expect(fixedIpHints('10.10.0.50', ctx()).some((h) => h.kind === 'pool')).toBe(false);
    // configured pools
    const pools: Network = { ...iot, dhcp: { pools: [{ start: '10.10.0.10', end: '10.10.0.60' }] } };
    expect(fixedIpHints('10.10.0.50', ctx({ network: pools })).some((h) => h.kind === 'pool')).toBe(true);
    expect(fixedIpHints('10.10.0.200', ctx({ network: pools })).some((h) => h.kind === 'pool')).toBe(false);
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

  it('notes that DHCP is off, and when a new reservation takes effect', () => {
    const off: Network = { ...iot, dhcp: { enabled: false } };
    expect(fixedIpHints('10.10.0.50', ctx({ network: off })).map((h) => h.kind)).toEqual(['dhcp_off', 'first_mac']);
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
