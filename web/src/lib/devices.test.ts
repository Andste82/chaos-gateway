import { describe, expect, it } from 'vitest';
import type { DeviceView } from '../api/generated/model';
import { compareDevices, deviceFaults, deviceStatus, matchesFilter, matchesSearch, sourcesLabel } from './devices';

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
      'profile lte: 50 ms',
    ]);
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
