import { describe, expect, it } from 'vitest';
import { discoveredDeviceId, newId } from './ids';

describe('ids', () => {
  it('derive the UUID of a discovered device from its MAC as the gateway does (engine.DeviceID)', () => {
    // the values engine.DeviceID gives for these MACs
    expect(discoveredDeviceId('02:00:00:00:00:3b')).toBe('63a1e1ce-ed5a-5b1f-bf4e-87a24a5435ca');
    expect(discoveredDeviceId('3a:f1:9c:22:07:b4')).toBe('4a572e83-a883-5ef4-bdc8-3381cee45f75');
    expect(discoveredDeviceId('02:00:00:00:00:2a')).toBe('38789eb8-c569-5a2d-963a-cc790b97bf59');
    // the MAC in any case
    expect(discoveredDeviceId('0A:0B:0C:0D:0E:0F')).toBe('f4ee0cd8-ce82-5ea1-aabf-2f514b568348');
    expect(discoveredDeviceId('0a:0b:0c:0d:0e:0f')).toBe('f4ee0cd8-ce82-5ea1-aabf-2f514b568348');
  });

  it('make a random version 4 UUID', () => {
    expect(newId()).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });
});
