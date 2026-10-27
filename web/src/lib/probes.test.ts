import { describe, expect, it } from 'vitest';
import type { ProbeView } from '../api/generated/model';
import { findProbe, measurableProfiles, probeTone } from './probes';

const probe = (name: string, addresses: string[], online: boolean): ProbeView => ({
  id: name === 'probe-a' ? '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8' : '9d2e4b61-0c3f-4a75-8e1b-2f6a7c8d9e01',
  config: { name, network: 'n' },
  observed: { addresses, online },
});

describe('probes', () => {
  it('say in words whether they have their address and are there', () => {
    expect(probeTone(probe('probe-a', [], false))).toEqual({ tone: 'unknown', label: 'No address yet' });
    expect(probeTone(probe('probe-a', ['10.10.0.77'], true))).toEqual({ tone: 'normal', label: 'Online' });
    expect(probeTone(probe('probe-a', ['10.10.0.77'], false))).toEqual({ tone: 'offline', label: 'Offline' });
  });

  it('are found by id or by name, ignoring case', () => {
    const list = [probe('probe-a', [], false), probe('Probe-B', [], false)];
    expect(findProbe(list, 'PROBE-A')?.config.name).toBe('probe-a');
    expect(findProbe(list, '9D2E4B61-0c3f-4a75-8e1b-2f6a7c8d9e01')?.config.name).toBe('Probe-B');
    expect(findProbe(list, 'probe-c')).toBeUndefined();
    expect(findProbe(list, '')).toBeUndefined();
  });

  it('measure the profiles that have an impairment part', () => {
    expect(
      measurableProfiles([
        { id: '1', config: { name: 'lte', parts: { impairment: {} } } },
        { id: '2', config: { name: 'mtu', parts: {} } },
        { id: '3', config: { name: 'tls', parts: {} } },
      ]),
    ).toEqual([{ id: '1', name: 'lte' }]);
  });
});
