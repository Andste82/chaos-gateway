// The form of a DHCP test action (plan §2.7, §2.17 "Device detail" and "Networks", M23b): a short lease, a deleted lease, a
// forced new IP, other options, or silence, for a device or a network, with what the action needs (the lease time, the
// address a device is moved to, the options), the checks under the fields and the `dhcp` part of the request it becomes.
// The DHCP dialog of the device detail and the network card (an overlay of the kind `dhcp`) and the step dialog (the step
// type `dhcp`) use the same fields, the same checks and the same body.
import type { DhcpAction, DhcpCustomOption, DhcpOptions, Scope } from '../api/generated/model';
import {
  createOverlayBodyDhcpLeaseTimeOneRegExp as durationRegExp,
  createOverlayBodyDnsNamesItemRegExp as hostnameRegExp,
} from '../api/generated/zod/overlays/overlays';
import { durationMs } from './format';
import { isUnicast, list, parseCustom, reservedDHCPOptions } from './networkForm';
import { parseIPv4 } from './networks';

export type DhcpActionName = DhcpAction['action'];

export interface DhcpForm {
  action: DhcpActionName;
  /** The lease time of a short lease: a duration of at least `1s`. */
  leaseTime: string;
  /** The options of `set_options`; a field left empty keeps what the network is configured with. */
  router: string;
  dnsServers: string;
  domain: string;
  /** Take the domain away (the action's `domain` is the empty string). */
  removeDomain: boolean;
  ntp: string;
  /** One option per line: "code value". */
  custom: string;
  /** The address `force_new_ip` moves the device to; empty: the gateway chooses. */
  ip: string;
}

export type DhcpField = 'dhcpAction' | 'leaseTime' | 'router' | 'dhcpDns' | 'domain' | 'ntp' | 'custom' | 'options' | 'ip';

/** What a DHCP action can be aimed at: a device or a network (never a group or everything), `force_new_ip` at a device only. */
export type DhcpScope = 'device' | 'network';

export const dhcpActions: { value: DhcpActionName; label: string; hint: string; device?: boolean }[] = [
  {
    value: 'short_lease',
    label: 'Short lease',
    hint: 'The lease time is this one (also below 120 s), and a client that asks for a longer one is given the short one. A client keeps the lease it has until it renews it, so this is how to bring a renewal forward.',
  },
  {
    value: 'delete_lease',
    label: 'Delete lease',
    hint: 'The leases are deleted once, when the action is applied. A client that renews gets the same address again.',
  },
  {
    value: 'force_new_ip',
    label: 'Force a new IP',
    device: true,
    hint: "The device's reservation moves to another address of its network. The renewing client is refused and asks for the new address; the server cannot make it renew at once, so use a short lease to bring that forward.",
  },
  {
    value: 'set_options',
    label: 'Set options',
    hint: 'The options of the network, or of the device, are the configured ones with the fields set here replaced. A client takes them when it asks again.',
  },
  { value: 'silence', label: 'Silence', hint: 'The DHCP server does not answer: no offer, no acknowledgement and no refusal, until the action is removed.' },
];

export const dhcpActionLabels: Record<string, string> = Object.fromEntries(dhcpActions.map((a) => [a.value, a.label]));

/** The actions of a scope: a network has no `force_new_ip`. */
export function dhcpActionsFor(scope: DhcpScope): typeof dhcpActions {
  return dhcpActions.filter((a) => scope === 'device' || !a.device);
}

/** The kind of scope an action is aimed at; undefined for a group, everything and the remote networks. */
export function dhcpScopeOf(s: Scope | undefined): DhcpScope | undefined {
  if (s?.device) return 'device';
  if (s?.network) return 'network';
  return undefined;
}

export function emptyDhcp(action: DhcpActionName = 'short_lease'): DhcpForm {
  return { action, leaseTime: '30s', router: '', dnsServers: '', domain: '', removeDomain: false, ntp: '', custom: '', ip: '' };
}

/** The options of the form: only the fields that are set. */
function optionsOf(f: DhcpForm): DhcpOptions {
  const o: DhcpOptions = {};
  if (f.router.trim()) o.router = f.router.trim();
  const dns = list(f.dnsServers);
  if (dns.length) o.dns_servers = dns;
  if (f.removeDomain) o.domain = '';
  else if (f.domain.trim()) o.domain = f.domain.trim();
  const ntp = list(f.ntp);
  if (ntp.length) o.ntp_servers = ntp;
  const custom = parseCustom(f.custom).options;
  if (custom.length) o.custom = custom;
  return o;
}

/** The problems of the form, by field; empty when the action can be sent (the gateway has the last word on the address and Kea's). */
export function dhcpErrors(f: DhcpForm): Partial<Record<DhcpField, string>> {
  const e: Partial<Record<DhcpField, string>> = {};
  switch (f.action) {
    case 'short_lease': {
      const t = f.leaseTime.trim();
      if (!durationRegExp.test(t) || (durationMs(t) ?? 0) < 1000) e.leaseTime = 'A lease time of at least 1s, such as 30s or 7s.';
      break;
    }
    case 'force_new_ip': {
      const ip = f.ip.trim();
      if (ip && (!isUnicast(ip) || parseIPv4(ip)! >>> 24 === 127)) e.ip = 'An IPv4 address in the network of the device; empty: the gateway chooses one.';
      break;
    }
    case 'set_options': {
      if (f.router.trim() && !isUnicast(f.router.trim())) e.router = 'An IPv4 address; empty keeps the configured router.';
      if (list(f.dnsServers).some((x) => !isUnicast(x))) e.dhcpDns = 'IPv4 addresses, separated by commas.';
      const domain = f.domain.trim();
      if (!f.removeDomain && domain && (domain.length > 253 || !hostnameRegExp.test(domain))) e.domain = 'A domain name such as lab.example.';
      if (list(f.ntp).some((x) => !isUnicast(x))) e.ntp = 'IPv4 addresses, separated by commas.';
      const custom = parseCustom(f.custom);
      if (custom.bad) e.custom = `${custom.bad}: one option per line, its code and its value, such as 66 tftp.lab.`;
      else {
        const codes = custom.options.map((o) => o.code);
        const out = custom.options.find((o) => o.code < 1 || o.code > 254);
        const reserved = custom.options.find((o) => reservedDHCPOptions.includes(o.code));
        if (out) e.custom = `Option ${out.code}: the code is from 1 to 254.`;
        else if (reserved) e.custom = `Option ${reserved.code} is set by the gateway (router, lease time, DNS and NTP servers, …); it cannot be a custom option.`;
        else if (codes.some((c, i) => codes.indexOf(c) !== i)) e.custom = 'Each option code once.';
      }
      if (Object.keys(e).length === 0 && Object.keys(optionsOf(f)).length === 0) e.options = 'Set at least one option: a router, DNS servers, a domain, NTP servers or a custom option.';
      break;
    }
  }
  return e;
}

/** The DHCP action of the form: only the fields its action uses (the gateway refuses the others). */
export function dhcpBody(f: DhcpForm): DhcpAction {
  switch (f.action) {
    case 'short_lease':
      return { action: 'short_lease', lease_time: f.leaseTime.trim() };
    case 'force_new_ip':
      return { action: 'force_new_ip', ...(f.ip.trim() ? { ip: f.ip.trim() } : {}) };
    case 'set_options':
      return { action: 'set_options', options: optionsOf(f) };
    default:
      return { action: f.action };
  }
}

/** The form of a DHCP action. */
export function dhcpToForm(d: DhcpAction | undefined): DhcpForm {
  const f = emptyDhcp();
  if (!d) return f;
  f.action = d.action;
  if (d.lease_time !== undefined) f.leaseTime = d.lease_time;
  if (d.ip !== undefined) f.ip = d.ip;
  const o = d.options;
  if (o) {
    f.router = o.router ?? '';
    f.dnsServers = (o.dns_servers ?? []).join(', ');
    f.removeDomain = o.domain === '';
    f.domain = o.domain ?? '';
    f.ntp = (o.ntp_servers ?? []).join(', ');
    f.custom = (o.custom ?? []).map((c: DhcpCustomOption) => `${c.code} ${c.value}`).join('\n');
  }
  return f;
}

/** What the action does, in words: "leases of 7s", "the leases are deleted", "moved to 10.10.0.254", "the server stays silent". */
export function dhcpEffect(d: DhcpAction): string {
  switch (d.action) {
    case 'short_lease':
      return `leases of ${d.lease_time ?? '?'}`;
    case 'delete_lease':
      return 'the leases are deleted';
    case 'force_new_ip':
      return d.ip ? `moved to ${d.ip}` : 'moved to an address the gateway chooses';
    case 'set_options': {
      const o = d.options;
      const named = [
        o?.router ? `router ${o.router}` : '',
        o?.dns_servers?.length ? `DNS ${o.dns_servers.join(', ')}` : '',
        o?.domain === '' ? 'no domain' : o?.domain ? `domain ${o.domain}` : '',
        o?.ntp_servers?.length ? `NTP ${o.ntp_servers.join(', ')}` : '',
        ...(o?.custom ?? []).map((c) => `option ${c.code}`),
      ].filter(Boolean);
      return named.length ? `options: ${named.join(', ')}` : 'options';
    }
    case 'silence':
      return 'the server stays silent';
    default:
      return String(d.action);
  }
}

/** The field of the form a problem the gateway reported names, by its pointer below the action ("/lease_time", "/ip", "/options/custom/0/code"). */
export function dhcpFieldOf(path: string | undefined): DhcpField | undefined {
  const segments = (path ?? '').split('/').filter(Boolean);
  switch (segments[0]) {
    case 'action':
      return 'dhcpAction';
    case 'lease_time':
      return 'leaseTime';
    case 'ip':
      return 'ip';
    case 'options':
      switch (segments[1]) {
        case 'router':
          return 'router';
        case 'dns_servers':
          return 'dhcpDns';
        case 'domain':
          return 'domain';
        case 'ntp_servers':
          return 'ntp';
        case 'custom':
          return 'custom';
        default:
          return 'options';
      }
    default:
      return undefined;
  }
}
