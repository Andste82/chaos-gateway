// The device editor and the DHCP reservation (plan §2.3, §2.7, §2.17 "Devices"): a configured device's
// name, network and fixed IP. The checks mirror the gateway's (`validate_device.go`): a fixed IP is a
// host address of a local test network's subnet, not the gateway's own and not reserved for another
// device, and the device has a MAC to reserve it for. The hints say what the gateway accepts but the
// admin should know: the address lies in the dynamic pool (decision on #76: allowed, a warning), it is
// leased to another client now, DHCP is off in the network, a new reservation takes effect when the
// device renews its lease, and the reservation is for the device's first MAC (decision on #77). The
// helpers for the fixed IP take a plain context, so the adopt dialog and the leases card can use them.
import type { DhcpLease, Device, LanNetwork, Network } from '../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { durationMs, formatDuration } from './format';
import { effectivePools, formatIPv4, parseIPv4, parseSubnet, subnetText } from './networks';

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
  if (n === undefined) return 'Not an IPv4 address, such as 10.10.0.50.';
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

export type ReservationHintKind = 'pool' | 'leased' | 'dhcp_off' | 'renewal' | 'removed' | 'first_mac';

/** A note on a reservation that does not stop it. */
export interface ReservationHint {
  kind: ReservationHintKind;
  text: string;
}

/** What the hints of a fixed IP need beyond the checks. */
export interface ReservationHintContext extends ReservationContext {
  /** The fixed IP the device has in the configuration now (the active revision or the draft's base). */
  before?: string;
  /** The current DHCP leases of the network. */
  leases?: DhcpLease[];
  /** The MAC of the device's current lease (the reservation is put on it, see `reservationMacs`). */
  leaseMac?: string;
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
 * The notes on a fixed IP that the gateway accepts (call it for an address without `fixedIpError`):
 * in the dynamic pool, leased to another client now, DHCP off, when it takes effect, the MAC it is for.
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
  if (net.dhcp?.enabled === false) {
    out.push({ kind: 'dhcp_off', text: `DHCP is off in ${net.name}: the reservation has no effect until it is turned on.` });
  } else {
    const { pools } = effectivePools(net.address, net.dhcp?.pools);
    const pool = pools.find((p) => {
      const a = parseIPv4(p.start);
      const b = parseIPv4(p.end);
      return a !== undefined && b !== undefined && n >= a && n <= b;
    });
    if (pool) {
      out.push({
        kind: 'pool',
        text: `${addr} lies in the DHCP pool ${pool.start}–${pool.end} of ${net.name}. The gateway allows it, but the pool may hand it to another client first; an address outside the pool is safer.`,
      });
    }
  }
  const macs = reservationMacs(ctx.macs, ctx.leaseMac);
  const mine = macs[0]?.toLowerCase();
  const held = (ctx.leases ?? []).find((l) => parseIPv4(l.ip) === n && l.state !== 'expired' && l.state !== 'declined' && l.mac.toLowerCase() !== mine);
  if (held) {
    out.push({
      kind: 'leased',
      text: `${addr} is leased to ${held.mac}${held.hostname ? ` (${held.hostname})` : ''} now: the device gets it only once that lease is released or runs out, and an address from the pool until then.`,
    });
  }
  if (net.dhcp?.enabled !== false && parseIPv4(ctx.before) !== n) {
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
 * The device with the form's name, network and fixed IP; the rest of `before` is kept. A new or changed
 * reservation puts the MAC of the current lease first (decision on #77: the reservation is for the first MAC).
 */
export function deviceFromForm(f: DeviceForm, before: Device, leaseMac?: string): Device {
  const { network: _n, fixed_ip: _f, ...rest } = before;
  const out: Device = { ...rest, name: f.name.trim() };
  if (f.network) out.network = f.network;
  const ip = f.fixedIp.trim();
  if (ip) {
    out.fixed_ip = ip;
    const macs = before.identifiers?.macs;
    if (macs?.length && parseIPv4(ip) !== parseIPv4(before.fixed_ip)) {
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
