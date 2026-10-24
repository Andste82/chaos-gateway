import { describe, expect, it } from 'vitest';
import type { Device, DeviceView, DhcpLease } from '../api/generated/model';
import { leaseDevice, leaseReserved, leaseTarget, reservationRows, undoReservation } from './reservations';

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const TEST = '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7';
const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const CAM = 'a1b2c3d4-0000-4000-8000-000000000057';
const MAC = '02:00:00:00:00:2a';
const CAM_MAC = '02:00:00:00:00:39';
const OTHER = '3a:f1:9c:22:07:b4';

const esp: Device = { name: 'ESP32-42', identifiers: { macs: [MAC] }, network: IOT, fixed_ip: '10.10.0.42' };
const cam: Device = { name: 'Cam', identifiers: { macs: [CAM_MAC] }, network: IOT };

function lease(ip: string, mac: string, over: Partial<DhcpLease> = {}): DhcpLease {
  return { ip, mac, network: IOT, expires_at: '2026-10-23T12:00:00Z', state: 'active', ...over };
}

describe('reservationRows', () => {
  it('lists the devices with a fixed IP in the network, sorted by address; active when the device holds the address', () => {
    const base = { [ESP]: esp, [CAM]: { ...cam, fixed_ip: '10.10.0.7' }, x: { name: 'x', identifiers: { macs: ['02:00:00:00:00:01'] }, network: TEST, fixed_ip: '10.20.0.5' } };
    const rows = reservationRows(IOT, base, {}, [lease('10.10.0.42', MAC)]);
    expect(rows.map((r) => [r.ip, r.device.name, r.mac, r.mark, r.status, r.label])).toEqual([
      ['10.10.0.7', 'Cam', CAM_MAC, undefined, 'pending', 'pending: no lease yet'],
      ['10.10.0.42', 'ESP32-42', MAC, undefined, 'active', 'active'],
    ]);
  });

  it('pending while the device holds another address, conflict when another MAC holds it; expired leases do not count', () => {
    expect(reservationRows(IOT, { [ESP]: esp }, {}, [lease('10.10.0.150', MAC.toUpperCase())])[0]).toMatchObject({ status: 'pending', label: 'pending: has 10.10.0.150 until it renews its lease' });
    expect(reservationRows(IOT, { [ESP]: esp }, {}, [lease('10.10.0.42', OTHER, { hostname: 'phone' })])[0]).toMatchObject({ status: 'conflict', label: `conflict: leased to ${OTHER} (phone)` });
    expect(reservationRows(IOT, { [ESP]: esp }, {}, [lease('10.10.0.42', OTHER, { state: 'expired' })])[0]).toMatchObject({ status: 'pending', label: 'pending: no lease yet' });
    // the gateway's device of the lease is the device, whatever MAC it has
    expect(reservationRows(IOT, { [ESP]: esp }, {}, [lease('10.10.0.42', OTHER, { device: ESP })])[0]!.status).toBe('active');
  });

  it('marks the edits of the draft: NEW, CHANGED (address or first MAC), DELETED (fixed IP removed, device deleted or moved)', () => {
    const base = { [ESP]: esp, [CAM]: cam, a: { ...esp, name: 'a', fixed_ip: '10.10.0.60' }, b: { ...esp, name: 'b', fixed_ip: '10.10.0.61' }, c: { ...esp, name: 'c', fixed_ip: '10.10.0.62' } };
    const { fixed_ip: _f, ...aWithout } = base.a;
    const edits: Record<string, Device | null> = {
      [ESP]: { ...esp, fixed_ip: '10.10.0.43' },
      [CAM]: { ...cam, fixed_ip: '10.10.0.7' },
      a: aWithout,
      b: null,
      c: { ...base.c, network: TEST },
      new: { name: 'adopted', identifiers: { macs: [OTHER] }, network: IOT, fixed_ip: '10.10.0.133' },
    };
    const rows = reservationRows(IOT, base, edits, [lease('10.10.0.43', MAC), lease('10.10.0.60', MAC)]);
    expect(rows.map((r) => [r.ip, r.device.name, r.mark, r.label])).toEqual([
      ['10.10.0.7', 'Cam', 'NEW', 'pending: not applied yet'],
      ['10.10.0.43', 'ESP32-42', 'CHANGED', 'pending: not applied yet'],
      ['10.10.0.60', 'a', 'DELETED', 'active · removal not applied yet'],
      ['10.10.0.61', 'b', 'DELETED', 'pending: has 10.10.0.43 until it renews its lease'],
      ['10.10.0.62', 'c', 'DELETED', 'pending: has 10.10.0.43 until it renews its lease'],
      ['10.10.0.133', 'adopted', 'NEW', 'pending: not applied yet'],
    ]);
    // a new MAC order is a changed reservation; a new name is not
    const reordered = { ...esp, identifiers: { macs: ['02:00:00:00:00:2b', MAC] } };
    expect(reservationRows(IOT, { [ESP]: { ...esp, identifiers: { macs: [MAC, '02:00:00:00:00:2b'] } } }, { [ESP]: reordered })[0]!.mark).toBe('CHANGED');
    expect(reservationRows(IOT, { [ESP]: esp }, { [ESP]: { ...esp, name: 'renamed' } })[0]!.mark).toBeUndefined();
  });

  it('a reservation not applied yet whose address another MAC holds is a conflict', () => {
    expect(reservationRows(IOT, {}, { [ESP]: esp }, [lease('10.10.0.42', OTHER)])[0]).toMatchObject({ mark: 'NEW', status: 'conflict' });
  });
});

describe('undoReservation', () => {
  const rows = (base: Record<string, Device>, id: string, now: Device | null) => {
    const undone = undoReservation(IOT, base[id], now);
    return reservationRows(IOT, base, undone === null ? {} : { [id]: undone }).filter((r) => r.id === id);
  };

  it('restores the fixed IP and the MAC order of the base and keeps the other edits', () => {
    const base = { ...esp, identifiers: { macs: [MAC, CAM_MAC] } };
    const now: Device = { ...base, name: 'renamed', labels: ['lab'], fixed_ip: '10.10.0.43', identifiers: { macs: [OTHER, CAM_MAC, MAC] } };
    // the MAC the draft added stays, the base's first MAC is first again
    expect(undoReservation(IOT, base, now)).toEqual({ ...now, fixed_ip: '10.10.0.42', identifiers: { macs: [MAC, OTHER, CAM_MAC] } });
    expect(rows({ [ESP]: base }, ESP, now).map((r) => [r.ip, r.mark])).toEqual([['10.10.0.42', undefined]]);
    // a fixed IP the draft removed comes back; so does the base's first MAC when the draft took it away
    const { fixed_ip: _f, ...without } = esp;
    expect(undoReservation(IOT, esp, { ...without, name: 'renamed', identifiers: { macs: [OTHER] } })).toEqual({ ...esp, name: 'renamed', identifiers: { macs: [MAC, OTHER] } });
  });

  it('a device the draft moved away from the network comes back to it with its fixed IP', () => {
    expect(undoReservation(IOT, esp, { ...esp, name: 'moved', network: TEST, fixed_ip: '10.20.0.5' })).toEqual({ ...esp, name: 'moved' });
  });

  it('a reservation the base does not have in the network loses only its fixed IP', () => {
    // adopted in the draft: stays adopted
    const adopted: Device = { name: 'Thermostat-A', identifiers: { macs: [OTHER] }, network: IOT, fixed_ip: '10.10.0.133' };
    expect(undoReservation(IOT, undefined, adopted)).toEqual({ name: 'Thermostat-A', identifiers: { macs: [OTHER] }, network: IOT });
    expect(rows({}, ESP, adopted)).toEqual([]);
    // no fixed IP in the base: the MAC order of the base comes back with it (the reservation put a MAC first)
    const now: Device = { ...cam, name: 'renamed', identifiers: { macs: [OTHER, CAM_MAC] }, fixed_ip: '10.10.0.7' };
    expect(undoReservation(IOT, cam, now)).toEqual({ ...cam, name: 'renamed', identifiers: { macs: [CAM_MAC, OTHER] } });
    // moved here from a network where it had one: stays moved, without a fixed IP
    expect(undoReservation(IOT, { ...esp, network: TEST, fixed_ip: '10.20.0.5' }, { ...esp, name: 'moved' })).toEqual({ name: 'moved', identifiers: { macs: [MAC] }, network: IOT });
  });

  it('a device the draft deletes comes back as the base has it', () => {
    expect(undoReservation(IOT, esp, null)).toEqual(esp);
    expect(undoReservation(IOT, undefined, null)).toBeNull();
  });
});

describe('the leases card', () => {
  const view = (over: Partial<DeviceView>): DeviceView => ({ id: ESP, name: 'ESP32-42', origin: 'configured', network: IOT, observed: { online: true, macs: [MAC] }, ...over });

  it('finds the device of a lease by the gateway\'s id, else by its MAC', () => {
    const devices = [view({}), view({ id: CAM, name: 'dev', origin: 'discovered', observed: { online: true, macs: [OTHER] } })];
    expect(leaseDevice(lease('10.10.0.42', '02:00:00:00:00:ff', { device: ESP }), devices)?.id).toBe(ESP);
    expect(leaseDevice(lease('10.10.0.133', OTHER.toUpperCase()), devices)?.id).toBe(CAM);
    expect(leaseDevice(lease('10.10.0.9', '02:00:00:00:00:ff'), devices)).toBeUndefined();
  });

  it('marks a lease reserved when its address is the applied fixed IP of its device', () => {
    const reserved = view({ config: esp });
    expect(leaseReserved(lease('10.10.0.42', MAC), reserved)).toBe(true);
    expect(leaseReserved(lease('10.10.0.43', MAC), reserved)).toBe(false);
    expect(leaseReserved(lease('10.10.0.42', MAC, { network: TEST }), reserved)).toBe(false);
    expect(leaseReserved(lease('10.10.0.42', MAC), view({ config: { ...esp, fixed_ip: undefined } }))).toBe(false);
  });

  it('Reserve adopts a discovered device and edits a configured one; nothing when reserved, unknown or deleted in the draft', () => {
    const l = lease('10.10.0.42', MAC);
    expect(leaseTarget(l, view({ origin: 'discovered' }))).toBe('adopt');
    expect(leaseTarget(l, view({ config: { ...esp, fixed_ip: '10.10.0.50' } }))).toBe('edit');
    expect(leaseTarget(l, view({ config: esp }))).toBeUndefined();
    expect(leaseTarget(l, undefined)).toBeUndefined();
    expect(leaseTarget(l, view({ origin: 'probe' }))).toBeUndefined();
    // the draft's edit: an adopted device is edited, one deleted is not reserved for, one reserved there already neither
    expect(leaseTarget(l, view({ origin: 'discovered' }), { ...esp, fixed_ip: undefined })).toBe('edit');
    expect(leaseTarget(l, view({}), null)).toBeUndefined();
    expect(leaseTarget(l, view({ origin: 'discovered' }), esp)).toBeUndefined();
  });
});
