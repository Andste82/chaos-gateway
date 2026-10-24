// The device editor and the DHCP reservation (plan §2.3, §2.7, §2.17 "Devices"): a configured device's
// name, network and fixed IP. The checks mirror the gateway's (`validate_device.go`): a fixed IP is a
// host address of a local test network's subnet, not the gateway's own and not reserved for another
// device, and the device has a MAC to reserve it for. The hints say what the gateway accepts but the
// admin should know: the address lies in the dynamic pool (decision on #76: allowed, a warning; the
// check and its wording are `reservationPoolHint`'s, #101), it is leased to another client now, or the
// device holds it with a MAC other than the one the reservation is for (#96), DHCP is off in the
// network, a new reservation takes effect when the device renews its lease (the address of its current
// lease it simply keeps), and the reservation is for the device's first MAC (decision on #77). The admin
// can make any of the device's MACs the first one (#111, reordering only: `withFirstMac`); the hint of a
// lease held with another of its MACs names that MAC, so the dialog can offer it. The helpers for the
// fixed IP take a plain context, so the adopt dialog and the leases card can use them.
import type { DeviceView, DhcpLease, Device, LanNetwork, Network } from '../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { durationMs, formatDuration } from './format';
import { formatIPv4, parseIPv4, parseSubnet, reservationPoolHint, subnetText } from './networks';

// ---- the fixed IP ------------------------------------------------------------------------------

/** What the checks of a fixed IP need to know of the device and its surroundings. */
export interface ReservationContext {
  /** The device the reservation is for (its id); another device's reservation of the address is an error. */
  device?: string;
  /** The network the address is reserved in, as the draft has it; undefined without one. */
  network?: Network;
  /** The device's MACs in the order of its configuration (the reservation is for the first). */
  macs: string[];
  /** The fixed IPs the other devices have, by device id, with their network and name (as the draft has them). */
  devices?: Record<string, Pick<Device, 'name' | 'network' | 'fixed_ip'>>;
  /** The id of the network (to find the other devices' reservations in it). */
  networkId?: string;
}

/** The problem of a fixed IP that the gateway would refuse, in the gateway's words; undefined when it is fine. */
export function fixedIpError(ip: string, ctx: ReservationContext): string | undefined {
  const text = ip.trim();
  const n = parseIPv4(text);
  // the gateway parses the address strictly: no octet with a leading zero ("10.10.0.050")
  if (n === undefined || formatIPv4(n) !== text) return 'Not an IPv4 address, such as 10.10.0.50.';
  const net = ctx.network;
  if (!net) return 'A fixed address is a DHCP reservation in a network: choose the network.';
  if (net.type !== 'lan') return `A DHCP reservation needs a local test network; ${net.name} is a WireGuard network.`;
  if (ctx.macs.length === 0) return "The reservation is for the device's first MAC address: the device has none.";
  const s = parseSubnet((net as LanNetwork).address);
  if (s) {
    const host = s.len >= 31 ? n >= s.network && n <= s.broadcast : n > s.network && n < s.broadcast;
    if (!host) return `${formatIPv4(n)} is not a host address of ${net.name} (${subnetText((net as LanNetwork).address)}).`;
    if (n === s.address) return `${formatIPv4(n)} is the gateway's own address in ${net.name}.`;
  }
  for (const [id, d] of Object.entries(ctx.devices ?? {})) {
    if (id === ctx.device || d.network !== ctx.networkId || parseIPv4(d.fixed_ip) !== n) continue;
    return `${formatIPv4(n)} is already reserved for device ${d.name}.`;
  }
  return undefined;
}

export type ReservationHintKind = 'pool' | 'leased' | 'other_mac' | 'dhcp_off' | 'renewal' | 'kept' | 'removed' | 'first_mac';

/** A note on a reservation that does not stop it. */
export interface ReservationHint {
  kind: ReservationHintKind;
  text: string;
  /** `other_mac`: the device's MAC that holds the lease, the one to put first (#111). */
  mac?: string;
}

/** A hint that warns (the dialogs show it in the fault color): the address may not reach the device. */
export function warningHint(kind: ReservationHintKind): boolean {
  return kind === 'pool' || kind === 'leased' || kind === 'other_mac' || kind === 'dhcp_off';
}

/** What the hints of a fixed IP need beyond the checks. */
export interface ReservationHintContext extends ReservationContext {
  /** The fixed IP the device has in the configuration now (the active revision or the draft's base). */
  before?: string;
  /** The network the device is in in the configuration now; a reservation moved to another network is a new one. */
  beforeNetwork?: string;
  /** The current DHCP leases of the network. */
  leases?: DhcpLease[];
  /** The MAC of the device's current lease (the reservation is put on it, see `reservationMacs`). */
  leaseMac?: string;
  /** The address of the device's current lease: a new reservation of it keeps the address, no renewal moves it. */
  leaseIp?: string;
  /**
   * The order of `macs` is final: a new reservation does not put the lease's MAC first. So when the admin
   * chose the first MAC (#111; the dialog passes no lease MAC to `deviceFromForm` then), and when the
   * change keeps the order of the draft (a reservation in the draft already, `deviceFromForm` compares with it).
   */
  ordered?: boolean;
}

/** Half a lease time in words ("30 min" for the default of 1 h). */
export function renewalTime(leaseTime: string | undefined): string {
  const ms = durationMs(leaseTime || '1h') ?? 3_600_000;
  const half = Math.round(ms / 2000);
  const h = Math.floor(half / 3600);
  const m = Math.floor((half % 3600) / 60);
  const s = half % 60;
  return formatDuration(`${h ? `${h}h` : ''}${m ? `${m}m` : ''}${s || (!h && !m) ? `${s}s` : ''}`);
}

/**
 * The MACs of a device with the one the reservation is for first (decision on #77: the gateway reserves
 * for the first MAC): the MAC of its current lease, when the device has it, is the one that asks for DHCP.
 */
export function reservationMacs(macs: string[], leaseMac: string | undefined): string[] {
  const lower = leaseMac?.toLowerCase();
  const i = lower ? macs.findIndex((m) => m.toLowerCase() === lower) : -1;
  if (i <= 0) return macs;
  return [macs[i]!, ...macs.slice(0, i), ...macs.slice(i + 1)];
}

/**
 * A lease's MAC that is not among the device's MACs (#98: the gateway names the device of a lease by its
 * address, say): undefined when the device has it. Reserving the lease for the device's first MAC would
 * reserve it for a MAC that does not hold it, so the device dialog offers to add this one first.
 */
export function foreignLeaseMac(macs: string[], leaseMac: string | undefined): string | undefined {
  if (!leaseMac) return undefined;
  const lower = leaseMac.toLowerCase();
  return macs.some((m) => m.toLowerCase() === lower) ? undefined : leaseMac;
}

/**
 * The device with a MAC as its first: added (decision on #98), or one of its MACs moved to the front
 * (#111); the reservation is for the first MAC.
 */
export function withFirstMac(d: Device, mac: string): Device {
  const lower = mac.toLowerCase();
  const rest = (d.identifiers?.macs ?? []).filter((m) => m.toLowerCase() !== lower);
  return { ...d, identifiers: { ...d.identifiers, macs: [mac, ...rest] } };
}

/** A device's MAC as its configuration spells it (a lease may give it in capitals); undefined when it has none such. */
export function ownMac(macs: string[], mac: string | undefined): string | undefined {
  const lower = mac?.toLowerCase();
  return lower ? macs.find((m) => m.toLowerCase() === lower) : undefined;
}

/** The other device that a MAC identifies already, as the draft has the devices (a MAC identifies one device). */
export function macOwner(mac: string, self: string | undefined, devices: Record<string, Device>): Device | undefined {
  const lower = mac.toLowerCase();
  return Object.entries(devices).find(([id, d]) => id !== self && d.identifiers?.macs?.some((m) => m.toLowerCase() === lower))?.[1];
}

/**
 * The notes on a fixed IP that the gateway accepts (call it for an address without `fixedIpError`):
 * in the dynamic pool, leased to another client now or held by the device with another of its MACs,
 * DHCP off, when it takes effect, the MAC it is for.
 * An empty address after one that was set says what the removal does.
 */
export function fixedIpHints(ip: string, ctx: ReservationHintContext): ReservationHint[] {
  const out: ReservationHint[] = [];
  const text = ip.trim();
  const net = ctx.network?.type === 'lan' ? (ctx.network as LanNetwork) : undefined;
  if (!text) {
    if (ctx.before) out.push({ kind: 'removed', text: `Without the reservation the device keeps ${ctx.before} until its lease runs out; then it gets an address from the pool.` });
    return out;
  }
  const n = parseIPv4(text);
  if (n === undefined || !net) return out;
  const addr = formatIPv4(n);
  // no scope is DHCP off too, as for the gateway's warning fixed_ip_dhcp_off (#94)
  const dhcpOff = !net.dhcp || net.dhcp.enabled === false;
  if (dhcpOff) {
    out.push({ kind: 'dhcp_off', text: `DHCP is off in ${net.name}: the reservation has no effect until it is turned on.` });
  } else {
    // the one pool check of the web app, in the gateway's terms (`fixed_ip_in_pool`, #101)
    const pool = reservationPoolHint(net, addr);
    if (pool) out.push({ kind: 'pool', text: pool });
  }
  // only a new or changed reservation puts the lease's MAC first (as `deviceFromForm` does)
  const changed = parseIPv4(ctx.before) !== n || (ctx.beforeNetwork !== undefined && ctx.beforeNetwork !== ctx.networkId);
  const macs = changed && !ctx.ordered ? reservationMacs(ctx.macs, ctx.leaseMac) : ctx.macs;
  const mine = macs[0]?.toLowerCase();
  const own = new Set(macs.map((m) => m.toLowerCase()));
  // a lease of the address by one of the device's MACs is its own (#96); by its first MAC it is the reserved one
  const held = (ctx.leases ?? []).filter((l) => parseIPv4(l.ip) === n && l.state !== 'expired' && l.state !== 'declined' && l.mac.toLowerCase() !== mine);
  const other = held.find((l) => !own.has(l.mac.toLowerCase()));
  const ownOther = held.find((l) => own.has(l.mac.toLowerCase()));
  if (other) {
    out.push({
      kind: 'leased',
      text: `${addr} is leased to ${other.mac}${other.hostname ? ` (${other.hostname})` : ''} now: the device gets it only once that lease is released or runs out, and an address from the pool until then.`,
    });
  } else if (ownOther) {
    out.push({
      kind: 'other_mac',
      mac: ownOther.mac,
      text: `The device holds ${addr} with its MAC ${ownOther.mac} now, but the reservation is for its first MAC, ${macs[0]}: it keeps the address only while it asks with that one. Put ${ownOther.mac} first among the device's MACs to reserve the address for the MAC that holds the lease.`,
    });
  }
  const kept = ctx.leaseIp !== undefined && parseIPv4(ctx.leaseIp) === n && (!ctx.leaseMac || ctx.leaseMac.toLowerCase() === mine);
  if (!dhcpOff && changed && kept) {
    out.push({ kind: 'kept', text: `The device has ${addr} from its current lease and keeps it; from now on the address is reserved for it.` });
  } else if (!dhcpOff && changed) {
    out.push({
      kind: 'renewal',
      text: `The device gets ${addr} when it renews its lease, which takes up to half the lease time (${renewalTime(net.dhcp?.lease_time)}).`,
    });
  }
  if (macs.length) {
    const moved = macs[0] !== ctx.macs[0];
    out.push({
      kind: 'first_mac',
      text: moved
        ? `The reservation is for the device's first MAC; ${macs[0]}, the MAC of its current lease, is put first.`
        : macs.length > 1
          ? `The reservation is for the device's first MAC, ${macs[0]}, not for its other ${macs.length === 2 ? 'one' : `${macs.length - 1}`}.`
          : `The reservation is for the device's MAC ${macs[0]}.`,
    });
  }
  return out;
}

/**
 * The address a discovered device can keep when it is adopted (#74): its current lease, when it has a MAC
 * and the lease is in its network, a local test network with DHCP on; undefined when no reservation is
 * offered. The MAC is the lease's, the one the reservation is put on (`reservationMacs`).
 */
export function adoptReservation(device: DeviceView, network: Network | undefined): { ip: string; mac: string } | undefined {
  const lease = device.observed.lease;
  if (!lease?.ip || !lease.mac || !device.observed.macs?.length || !device.network) return undefined;
  if (lease.network && lease.network !== device.network) return undefined;
  if (network?.type !== 'lan' || (network as LanNetwork).dhcp?.enabled === false) return undefined;
  return { ip: lease.ip, mac: lease.mac };
}

/** The devices as the draft has them: the base's with the edits (`null` deletes one). */
export function draftDevices(base: Record<string, Device> | undefined, edits: Record<string, Device | null>): Record<string, Device> {
  const out: Record<string, Device> = { ...(base ?? {}) };
  for (const [id, d] of Object.entries(edits)) {
    if (d === null) delete out[id];
    else out[id] = d;
  }
  return out;
}

// ---- the device dialog -------------------------------------------------------------------------

export interface DeviceForm {
  name: string;
  /** The network's id; empty for none. */
  network: string;
  /** The fixed IP as typed; empty for none. */
  fixedIp: string;
}

export type DeviceField = 'name' | 'network' | 'fixedIp';

export function formFromDevice(d: Device): DeviceForm {
  return { name: d.name, network: d.network ?? '', fixedIp: d.fixed_ip ?? '' };
}

export interface DeviceFormContext extends ReservationContext {
  /** The names devices, WireGuard clients and probes have besides this device (one namespace, plan §2.15). */
  takenNames?: string[];
}

/** The name's problem: the pattern of a name and unique among the devices (ignoring case). */
export function deviceNameError(name: string, taken: string[] = []): string | undefined {
  if (!nameRegExp.test(name)) return 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
  const lower = name.toLowerCase();
  if (taken.some((t) => t.toLowerCase() === lower)) return `The name ${name} is already used by another device.`;
  return undefined;
}

/** The form's problems by field; empty when the device can go into the draft. */
export function deviceFormErrors(f: DeviceForm, ctx: DeviceFormContext): Partial<Record<DeviceField, string>> {
  const e: Partial<Record<DeviceField, string>> = {};
  const name = deviceNameError(f.name.trim(), ctx.takenNames);
  if (name) e.name = name;
  if (f.fixedIp.trim()) {
    const ip = fixedIpError(f.fixedIp, ctx);
    if (ip) e.fixedIp = ip;
  }
  return e;
}

/**
 * The device with the form's name, network and fixed IP; the rest of `before` is kept, the order of its
 * MACs too. A new or changed reservation puts the MAC of the current lease first (decision on #77: the
 * reservation is for the first MAC); without `leaseMac` (the admin chose the first MAC, #111) it does not.
 */
export function deviceFromForm(f: DeviceForm, before: Device, leaseMac?: string): Device {
  const { network: _n, fixed_ip: _f, ...rest } = before;
  const out: Device = { ...rest, name: f.name.trim() };
  if (f.network) out.network = f.network;
  const ip = f.fixedIp.trim();
  if (ip) {
    out.fixed_ip = ip;
    const macs = before.identifiers?.macs;
    const changed = parseIPv4(ip) !== parseIPv4(before.fixed_ip) || f.network !== (before.network ?? '');
    if (macs?.length && changed) {
      const ordered = reservationMacs(macs, leaseMac);
      if (ordered !== macs) out.identifiers = { ...before.identifiers, macs: ordered };
    }
  }
  return out;
}

/** The field of a pointer below the device (`fixed_ip`, `name`, `network`), as the gateway's 422 names it. */
export function deviceFieldOf(pointer: string): DeviceField | undefined {
  const first = pointer.split('/')[0];
  if (first === 'name') return 'name';
  if (first === 'network') return 'network';
  if (first === 'fixed_ip') return 'fixedIp';
  return undefined;
}
