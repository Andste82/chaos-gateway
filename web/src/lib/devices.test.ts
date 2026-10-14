import { describe, expect, it } from 'vitest';
import type { DeviceView, Network, Overlay } from '../api/generated/model';
import { compareDevices, deviceFaults, deviceNetworkInfo,deviceStatus, matchesFilter, matchesSearch, sourcesLabel } from './devices';

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
