import { describe, expect, it } from 'vitest';
import type { DeviceView, Network, Overlay } from '../api/generated/model';
import type { AccessRule, ConfigFault, PortForward, Scenario } from '../api/generated/model';
import {
  compareDevices,
  deviceFaults,
  deviceNetworkInfo,
  deviceReferences,
  deviceStatus,
  displayName,
  idMac,
  matchesFilter,
  matchesSearch,
  nameDetail,
  sourcesLabel,
} from './devices';

const dev = (over: Partial<DeviceView>): DeviceView => ({
  id: 'd',
  name: 'ESP32-42',
  origin: 'configured',
  observed: { online: true, macs: ['02:00:00:00:00:2a'], addresses: ['10.10.0.42'], sources: ['dhcp', 'neighbor'] },
  ...over,
});

describe('devices', () => {
  it('filter by the chips of the list', () => {
    const faulted = dev({ effective: [{ family: 'impairment', layer: 'overlay', id: 'o', summary: '↓ 64 kbit/s' }] });
    const discovered = dev({ origin: 'discovered', observed: { online: false } });
    expect(matchesFilter(faulted, 'faulted')).toBe(true);
    expect(matchesFilter(dev({}), 'faulted')).toBe(false);
    expect(matchesFilter(discovered, 'unadopted')).toBe(true);
    expect(matchesFilter(discovered, 'online')).toBe(false);
    expect(matchesFilter(discovered, 'all')).toBe(true);
  });

  it('filter Adopted: the configured devices, not the discovered ones, WireGuard clients or probes', () => {
    expect(matchesFilter(dev({}), 'adopted')).toBe(true);
    expect(matchesFilter(dev({ origin: 'discovered' }), 'adopted')).toBe(false);
    expect(matchesFilter(dev({ origin: 'wireguard_client' }), 'adopted')).toBe(false);
    expect(matchesFilter(dev({ origin: 'probe' }), 'adopted')).toBe(false);
    expect(matchesFilter(dev({ origin: 'discovered' }), 'unadopted')).toBe(true);
    expect(matchesFilter(dev({}), 'unadopted')).toBe(false);
  });

  const lease = (hostname?: string) => ({ ip: '10.10.0.133', mac: '3a:f1:9c:22:07:b4', network: 'n', expires_at: '', hostname });
  const withHost = (over: Partial<DeviceView>, hostname?: string) => dev({ ...over, observed: { online: true, macs: ['3a:f1:9c:22:07:b4'], lease: lease(hostname) } });

  it('show a discovered device under the host name of its lease, the generated name below it', () => {
    const d = withHost({ origin: 'discovered', name: 'dev-3a-f1-9c-22-07-b4' }, 'esp32-ab12');
    expect(displayName(d)).toBe('esp32-ab12');
    expect(nameDetail(d)).toBe('dev-3a-f1-9c-22-07-b4');
    // without a host name (or only blanks), the generated name alone
    for (const h of [undefined, '', '  ']) {
      const plain = withHost({ origin: 'discovered', name: 'dev-3a-f1-9c-22-07-b4' }, h);
      expect(displayName(plain)).toBe('dev-3a-f1-9c-22-07-b4');
      expect(nameDetail(plain)).toBeUndefined();
    }
    expect(displayName(dev({ origin: 'discovered', name: 'dev-10-10-0-9', observed: { online: true } }))).toBe('dev-10-10-0-9');
  });

  it('show a configured device under its name, a differing host name below it', () => {
    const d = withHost({ name: 'Thermostat-A' }, 'esp32-ab12');
    expect(displayName(d)).toBe('Thermostat-A');
    expect(nameDetail(d)).toBe('host name esp32-ab12');
    // the same host name (in any case) is not repeated
    expect(nameDetail(withHost({ name: 'ESP32-AB12' }, 'esp32-ab12'))).toBeUndefined();
    expect(nameDetail(dev({}))).toBeUndefined();
    // a WireGuard client or a probe keeps its name too
    expect(displayName(withHost({ origin: 'wireguard_client', name: 'laptop' }, 'other'))).toBe('laptop');
  });

  it('sort by the displayed name', () => {
    const list = [withHost({ origin: 'discovered', name: 'dev-aa' }, 'zz-host'), dev({ name: 'mm' }), withHost({ origin: 'discovered', name: 'dev-bb' }, 'aa-host')].sort(compareDevices);
    expect(list.map(displayName)).toEqual(['aa-host', 'mm', 'zz-host']);
  });

  it('find the MAC a configured device id was derived from (adopted from discovery)', () => {
    // engine.DeviceID("02:00:00:00:00:3b")
    const id = '63a1e1ce-ed5a-5b1f-bf4e-87a24a5435ca';
    expect(idMac(id, ['02:00:00:00:00:3B'])).toBe('02:00:00:00:00:3B');
    // not the first of its MACs (a reservation put the lease's MAC first)
    expect(idMac(id, ['02:00:00:00:00:01', '02:00:00:00:00:3b'])).toBe('02:00:00:00:00:3b');
    expect(idMac(id.toUpperCase(), ['02:00:00:00:00:3b'])).toBe('02:00:00:00:00:3b');
    // configured with an id of its own, or without that MAC
    expect(idMac('a1b2c3d4-0000-4000-8000-000000000059', ['02:00:00:00:00:3b'])).toBeUndefined();
    expect(idMac(id, ['02:00:00:00:00:01'])).toBeUndefined();
    expect(idMac(id, undefined)).toBeUndefined();
  });

  it('list the configuration objects that name a device, by id or by name', () => {
    const id = 'a1b2c3d4-0000-4000-8000-000000000042';
    const rule = (name: string, device?: string): AccessRule => ({ name, source: device ? { device } : { global: true }, action: 'allow' }) as AccessRule;
    const refs = deviceReferences(
      { id, name: 'ESP32-42' },
      {
        rules: { r1: rule('no-dot', id.toUpperCase()), r2: rule('other', 'b'), r3: rule('by-name', 'esp32-42') },
        faults: { f1: { name: 'slow', source: { device: id } } as ConfigFault, f2: { name: 'global' } as ConfigFault },
        groups: { g1: { name: 'lab', members: ['x', id] }, g2: { name: 'empty' } },
        forwards: { p1: { protocol: 'tcp', entry: 'uplink', external_port: 8080, target: { device: id } } as PortForward, p2: { name: 'addr', protocol: 'tcp', entry: 'uplink', external_port: 1, target: { address: '10.10.0.5' } } as PortForward },
        scenarios: {
          s1: { name: 'reconnect', target: { network: 'n' }, steps: [{ id: 'a', at: '0s', target: { device: id } }] } as unknown as Scenario,
          s2: { name: 'whole', target: { device: id }, steps: [] } as unknown as Scenario,
          s3: { name: 'unrelated', target: { network: 'n' }, steps: [] } as unknown as Scenario,
        },
      },
    );
    expect(refs.map((r) => `${r.kind} ${r.name}`)).toEqual(['rule by-name', 'rule no-dot', 'fault slow', 'group lab', 'forward TCP 8080', 'scenario reconnect', 'scenario whole']);
    expect(refs.find((r) => r.kind === 'group')!.to).toBeUndefined();
    expect(refs.find((r) => r.kind === 'forward')!.to).toBe('/networks?forward=p1');
    expect(refs.find((r) => r.kind === 'scenario')!.to).toBe('/scenarios/s1');
    expect(deviceReferences({ id, name: 'ESP32-42' }, {})).toEqual([]);
  });

  it('do not count the normal profile as a fault', () => {
    expect(deviceFaults(dev({ effective: [{ family: 'impairment', layer: 'overlay', id: 'o', profile: { name: 'normal' } }] }))).toEqual([]);
    expect(deviceFaults(dev({ effective: [{ family: 'impairment', layer: 'overlay', id: 'o', profile: { name: 'lte' }, summary: '50 ms' }] }))).toEqual([
      'profile LTE',
    ]);
  });

  it('show the winners as the compact chips of the other screens, the summary only when the fault is not at hand', () => {
    const d = dev({
      effective: [
        { family: 'impairment', layer: 'overlay', id: 'o1', summary: 'latency 200ms jitter 50ms' },
        { family: 'mtu', layer: 'config', id: 'c1', summary: 'mtu 1280' },
        { family: 'dns', layer: 'config', id: 'c2', name: 'no-dns', summary: 'dns nxdomain' },
      ],
    });
    expect(deviceFaults(d)).toEqual(['latency 200ms jitter 50ms', 'mtu 1280', 'dns nxdomain']);
    const overlay = { id: 'o1', kind: 'fault', fault: { latency: '200ms', jitter: '50ms', loss: '5%' } } as Overlay;
    expect(
      deviceFaults(d, { overlay: (id) => (id === 'o1' ? overlay : undefined), config: (id) => (id === 'c1' ? { family: 'mtu', mtu: { size: 1280 } } : undefined) }),
    ).toEqual(['↑↓ 200 ms ±50', '5 % loss', 'MTU 1280', 'dns nxdomain']);
  });

  it('say which router and DNS servers a device is given', () => {
    const lan = { type: 'lan', name: 'IoT', interfaces: [], address: '10.10.0.1/24' } as Network;
    expect(deviceNetworkInfo(lan, 'd')).toEqual({ gateway: '10.10.0.1', dns: ['10.10.0.1'], proxy: true });
    const own = { ...lan, dhcp: { options: { router: '10.10.0.254', dns_servers: ['1.1.1.1', '9.9.9.9'] } } } as Network;
    expect(deviceNetworkInfo(own, 'd')).toEqual({ gateway: '10.10.0.254', dns: ['1.1.1.1', '9.9.9.9'], proxy: false });
    const hub = {
      type: 'wireguard',
      kind: 'hub',
      name: 'lab-hub',
      address: '10.30.0.1/24',
      listen_port: 51820,
      clients: { c1: { name: 'rA', address: '10.30.0.2' }, c2: { name: 'rB', address: '10.30.0.3', dns: { mode: 'custom', servers: ['1.1.1.1'] } }, c3: { name: 'rC', address: '10.30.0.4', dns: { mode: 'none' } } },
    } as unknown as Network;
    expect(deviceNetworkInfo(hub, 'c1')).toEqual({ dns: ['10.30.0.1'], proxy: true });
    expect(deviceNetworkInfo(hub, 'c2')).toEqual({ dns: ['1.1.1.1'], proxy: false });
    expect(deviceNetworkInfo(hub, 'c3')).toEqual({ dns: [], proxy: false });
    expect(deviceNetworkInfo(hub, 'other')).toBeUndefined();
    expect(deviceNetworkInfo(undefined, 'd')).toBeUndefined();
  });

  it('search by name, IP, MAC and host name, every word', () => {
    const d = dev({ observed: { online: true, macs: ['02:00:00:00:00:2a'], addresses: ['10.10.0.42'], lease: { ip: '10.10.0.42', mac: '02:00:00:00:00:2a', network: 'n', expires_at: '', hostname: 'kitchen' } } });
    expect(matchesSearch(d, 'esp32')).toBe(true);
    expect(matchesSearch(d, '10.10.0.42')).toBe(true);
    expect(matchesSearch(d, '00:2A')).toBe(true);
    expect(matchesSearch(d, 'kitchen esp')).toBe(true);
    expect(matchesSearch(d, 'kitchen phone')).toBe(false);
    expect(matchesSearch(d, '   ')).toBe(true);
  });

  it('give every status a label, not only a colour', () => {
    expect(deviceStatus(dev({}))).toEqual({ tone: 'normal', label: 'Online' });
    expect(deviceStatus(dev({ observed: { online: false } }))).toEqual({ tone: 'offline', label: 'Offline' });
    expect(deviceStatus(dev({ origin: 'probe' }))).toEqual({ tone: 'probe', label: 'Probe' });
  });

  it('sort online devices first, then by name with numbers in order', () => {
    const list = [dev({ name: 'b-10' }), dev({ name: 'b-9' }), dev({ name: 'a', observed: { online: false } })].sort(compareDevices);
    expect(list.map((d) => d.name)).toEqual(['b-9', 'b-10', 'a']);
  });

  it('name where a device was seen', () => {
    expect(sourcesLabel(dev({}))).toBe('DHCP, neighbor table');
  });
});
