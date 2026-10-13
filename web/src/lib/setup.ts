// The logic of the first-start setup wizard (plan §2.16, M12): what the user picks, how it is
// checked before it is sent, and the request it becomes (`POST /setup`: revision 1 and the admin
// password). Kept out of the component so it can be tested on its own.
import type { HostInterface, SetupRequest } from '../api/generated/model';
import {
  CompleteSetupBody,
  completeSetupBodyAdminPasswordMin,
  completeSetupBodyConfigurationOneNetworksOneAddressRegExp,
  completeSetupBodyConfigurationOneNetworksOneNameRegExp,
} from '../api/generated/zod/system/system';

/** One test network of the setup: a bridge with one physical port. */
export interface NetworkDraft {
  iface: string;
  name: string;
  address: string;
  dhcp: boolean;
}

export interface SetupDraft {
  uplink: string;
  management: string;
  networks: NetworkDraft[];
  password: string;
  repeat: string;
}

export const minPassword = completeSetupBodyAdminPasswordMin;

/** The gateway address proposed for the i-th test network: 10.10.0.1/24, 10.20.0.1/24, … */
export function suggestAddress(i: number): string {
  return `10.${(10 * (i + 1)) % 256}.0.1/24`;
}

/** The name proposed for the i-th test network. */
export function suggestName(i: number): string {
  return i === 0 ? 'test' : `test-${i + 1}`;
}

function ipValue(ip: string): number | undefined {
  const o = ip.split('.').map(Number);
  if (o.length !== 4 || o.some((x) => !Number.isInteger(x) || x < 0 || x > 255)) return undefined;
  return o.reduce((a, x) => a * 256 + x, 0);
}

/** The first and the last address of the prefix of an address with prefix length. */
export function prefixRange(cidr: string): [number, number] | undefined {
  const [ip, lenText] = cidr.split('/');
  const v = ipValue(ip ?? '');
  const len = Number(lenText);
  if (v === undefined || !Number.isInteger(len) || len < 0 || len > 32) return undefined;
  const size = 2 ** (32 - len);
  const first = Math.floor(v / size) * size;
  return [first, first + size - 1];
}

function overlaps(a: string, b: string): boolean {
  const ra = prefixRange(a);
  const rb = prefixRange(b);
  return !!ra && !!rb && ra[0] <= rb[1] && rb[0] <= ra[1];
}

/** Problems of the interface choice; an empty object means it is complete. */
export function validateInterfaces(d: Pick<SetupDraft, 'uplink' | 'management' | 'networks'>): Record<string, string> {
  const e: Record<string, string> = {};
  if (!d.uplink) e.uplink = 'Choose the interface towards the Internet.';
  if (!d.management) e.management = 'Choose the interface you reach the gateway on.';
  if (d.networks.length === 0) e.tests = 'Assign at least one interface to a test network.';
  if (d.networks.some((n) => n.iface === d.uplink || n.iface === d.management)) {
    e.tests = 'The uplink and the management interface cannot be test interfaces.';
  }
  return e;
}

/**
 * Problems of the test networks, keyed `<index>.name` and `<index>.address`. `taken` are the
 * prefixes already in use on the host (uplink and management), which a test network must not
 * overlap.
 */
export function validateNetworks(networks: NetworkDraft[], taken: string[] = []): Record<string, string> {
  const e: Record<string, string> = {};
  networks.forEach((n, i) => {
    if (!completeSetupBodyConfigurationOneNetworksOneNameRegExp.test(n.name)) {
      e[`${i}.name`] = 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
    } else if (networks.some((m, j) => j < i && m.name.toLowerCase() === n.name.toLowerCase())) {
      e[`${i}.name`] = 'Another test network has this name.';
    }
    if (!completeSetupBodyConfigurationOneNetworksOneAddressRegExp.test(n.address)) {
      e[`${i}.address`] = 'The gateway address with prefix length, e.g. 10.10.0.1/24.';
      return;
    }
    const len = Number(n.address.split('/')[1]);
    const range = prefixRange(n.address);
    const v = ipValue(n.address.split('/')[0] ?? '');
    if (len < 8 || len > 30) {
      e[`${i}.address`] = 'Use a prefix length between /8 and /30.';
    } else if (range && v !== undefined && (v === range[0] || v === range[1])) {
      e[`${i}.address`] = 'This is the network or broadcast address of the prefix; use a host address.';
    } else if (networks.some((m, j) => j < i && overlaps(m.address, n.address))) {
      e[`${i}.address`] = 'This prefix overlaps another test network.';
    } else if (taken.some((t) => overlaps(t, n.address))) {
      e[`${i}.address`] = 'This prefix overlaps the uplink or the management network.';
    }
  });
  return e;
}

export function validatePassword(password: string, repeat: string): Record<string, string> {
  const e: Record<string, string> = {};
  if (password.length < minPassword) e.password = `At least ${minPassword} characters.`;
  else if (password !== repeat) e.repeat = 'The passwords differ.';
  return e;
}

/**
 * The hint for an interface that the OS configures (netplan or NetworkManager): a test interface
 * must belong to Chaos Gateway alone (plan §3.4, §3.8: the host-setup script prints the netplan
 * change), else both would configure it.
 */
export function netplanHint(i: HostInterface | undefined): string | undefined {
  if (!i?.os_managed) return undefined;
  return (
    `${i.name} is configured by the operating system. Remove it from the netplan configuration ` +
    `(/etc/netplan/*.yaml: delete its entry under "ethernets", or set "${i.name}: {dhcp4: false, dhcp6: false, ` +
    `link-local: []}") and run "sudo netplan apply" before the setup applies.`
  );
}

function uuidV4(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x40;
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function ref(interfaces: HostInterface[], name: string) {
  const i = interfaces.find((x) => x.name === name);
  return i?.mac ? { name, mac: i.mac } : { name };
}

/** The request of `POST /setup`: revision 1 with uplink, management and the test networks. */
export function buildSetupRequest(d: SetupDraft, interfaces: HostInterface[], newId: () => string = uuidV4): SetupRequest {
  const networks: Record<string, unknown> = {};
  for (const n of d.networks) {
    networks[newId()] = {
      type: 'lan',
      name: n.name,
      interfaces: [ref(interfaces, n.iface)],
      address: n.address,
      dhcp: { enabled: n.dhcp },
    };
  }
  const body = {
    admin_password: d.password,
    configuration: {
      schema_version: 1,
      uplink: { interface: ref(interfaces, d.uplink) },
      management: { interface: ref(interfaces, d.management) },
      networks,
    },
  };
  // the generated schema of the spec is the last check before the request goes out; the body is
  // sent as built, without the defaults the schema would fill in
  CompleteSetupBody.parse(body);
  return body as SetupRequest;
}

/** The address the browser reaches the gateway at after the setup (its management address). */
export function managementURL(i: HostInterface | undefined, location: { protocol: string; port: string }): string | undefined {
  const addr = i?.addresses?.[0]?.split('/')[0];
  if (!addr) return undefined;
  return `${location.protocol}//${addr}${location.port ? `:${location.port}` : ''}/`;
}
