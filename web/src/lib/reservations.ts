// The DHCP reservations of a network (plan §2.7, #75): the devices with a fixed IP in it, as the draft has
// them, each with its status from the network's leases and the mark of an edit that is not applied yet
// (NEW, CHANGED, DELETED, like the port forwards). A reservation is for the device's first MAC (decision
// on #77). Its status:
//   - active: the device holds a lease on its reserved address
//   - pending: not applied yet, or the device has not renewed its lease yet (it still holds another address)
//   - conflict: the address is leased to another MAC
// The leases card asks `leaseTarget` what its Reserve action opens for a lease and `leaseReserved` whether
// a lease's address is the fixed IP of its device.
import type { Device, DeviceView, DhcpLease } from '../api/generated/model';
import { draftDevices } from './deviceForm';
import { parseIPv4 } from './networks';

export type ReservationMark = 'NEW' | 'CHANGED' | 'DELETED';
export type ReservationStatus = 'active' | 'pending' | 'conflict';

export interface ReservationRow {
  /** The device's id. */
  id: string;
  /** The device as the draft has it, or as the base had it for a deleted reservation. */
  device: Device;
  ip: string;
  /** The MAC the reservation is for: the device's first. */
  mac?: string;
  mark?: ReservationMark;
  status: ReservationStatus;
  /** The status in words ("pending: not applied yet"). */
  label: string;
}

const lower = (m: string | undefined) => m?.toLowerCase();
/** A lease that holds its address (an expired or declined one does not). */
export const liveLease = (l: DhcpLease) => l.state !== 'expired' && l.state !== 'declined';

function reservedIn(d: Device | null | undefined, network: string): d is Device {
  return !!d && d.network === network && !!d.fixed_ip;
}

function status(id: string, d: Device, mark: ReservationMark | undefined, leases: DhcpLease[]): Pick<ReservationRow, 'status' | 'label'> {
  const macs = new Set((d.identifiers?.macs ?? []).map((m) => m.toLowerCase()));
  const mine = (l: DhcpLease) => l.device === id || macs.has(l.mac.toLowerCase());
  const live = leases.filter(liveLease);
  const n = parseIPv4(d.fixed_ip);
  const holder = live.find((l) => parseIPv4(l.ip) === n);
  if (holder && !mine(holder)) return { status: 'conflict', label: `conflict: leased to ${holder.mac}${holder.hostname ? ` (${holder.hostname})` : ''}` };
  if (mark === 'NEW' || mark === 'CHANGED') return { status: 'pending', label: 'pending: not applied yet' };
  if (holder) return { status: 'active', label: mark === 'DELETED' ? 'active · removal not applied yet' : 'active' };
  const other = live.find(mine);
  if (other) return { status: 'pending', label: `pending: has ${other.ip} until it renews its lease` };
  return { status: 'pending', label: 'pending: no lease yet' };
}

/**
 * The reservations of a network: the devices of the draft (the base's with the edits) with a fixed IP in
 * it, and those of the base that the draft takes away (DELETED: the device is deleted, has no fixed IP
 * any more or moved to another network), sorted by address.
 */
export function reservationRows(network: string, base: Record<string, Device> | undefined, edits: Record<string, Device | null>, leases: DhcpLease[] = []): ReservationRow[] {
  const now = draftDevices(base, edits);
  const out: ReservationRow[] = [];
  for (const id of new Set([...Object.keys(base ?? {}), ...Object.keys(now)])) {
    const b = base?.[id];
    const d = now[id];
    let row: { device: Device; mark?: ReservationMark } | undefined;
    if (reservedIn(d, network)) {
      if (!reservedIn(b, network)) row = { device: d, mark: 'NEW' };
      else if (parseIPv4(b.fixed_ip) !== parseIPv4(d.fixed_ip) || lower(b.identifiers?.macs?.[0]) !== lower(d.identifiers?.macs?.[0])) row = { device: d, mark: 'CHANGED' };
      else row = { device: d };
    } else if (reservedIn(b, network)) row = { device: b, mark: 'DELETED' };
    if (!row) continue;
    out.push({ id, ...row, ip: row.device.fixed_ip!, mac: row.device.identifiers?.macs?.[0], ...status(id, row.device, row.mark, leases) });
  }
  return out.sort((a, b) => (parseIPv4(a.ip) ?? 0) - (parseIPv4(b.ip) ?? 0));
}

/** The device a lease belongs to: the one the gateway names, else the one that has its MAC. */
export function leaseDevice(l: DhcpLease, devices: DeviceView[]): DeviceView | undefined {
  if (l.device) {
    const d = devices.find((x) => x.id === l.device);
    if (d) return d;
  }
  const mac = l.mac.toLowerCase();
  return devices.find((x) => x.observed.macs?.some((m) => m.toLowerCase() === mac) || x.config?.identifiers?.macs?.some((m) => m.toLowerCase() === mac));
}

/** Whether a lease's address is the fixed IP of its device in the applied configuration. */
export function leaseReserved(l: DhcpLease, device: DeviceView | undefined): boolean {
  const c = device?.config;
  return !!c?.fixed_ip && c.network === l.network && parseIPv4(c.fixed_ip) === parseIPv4(l.ip);
}

/**
 * What the Reserve action of a lease opens: the adopt dialog for a discovered device (with the box "Keep
 * this address" checked), the device dialog for a configured one (with the lease's address as its fixed
 * IP); nothing for a lease that is reserved already or whose MAC belongs to no device the gateway knows.
 * `drafted` is the draft's edit of the device: a discovered device adopted in the draft is edited there
 * (the device dialog), a device the draft deletes is not reserved for.
 */
export function leaseTarget(l: DhcpLease, device: DeviceView | undefined, drafted?: Device | null): 'adopt' | 'edit' | undefined {
  if (!device || drafted === null || leaseReserved(l, device)) return undefined;
  if (drafted) return parseIPv4(drafted.fixed_ip) === parseIPv4(l.ip) && drafted.network === l.network ? undefined : 'edit';
  if (device.origin === 'configured') return 'edit';
  return device.origin === 'discovered' ? 'adopt' : undefined;
}
