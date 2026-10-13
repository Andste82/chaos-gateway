import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import {
  emptyForm,
  faultFormErrors,
  faultToForm,
  fieldOf,
  formToConfigFault,
  formToOverlay,
  hiddenParameters,
  parseRate,
  presets,
  previewSentence,
  scopeOf,
  targetOf,
  type FaultForm,
} from './faultForm';

const form = (over: Partial<FaultForm>): FaultForm => ({ ...emptyForm('device:d1'), ...over });

describe('the fault dialog', () => {
  it('keeps a remote network target when a fault is read back and sent again unchanged', () => {
    for (const target of [{ remote_network: { client: 'c1' } }, { remote_network: { link: 'l1' } }, { remote_network: { cidr: '10.9.0.0/24' } }]) {
      expect(scopeOf(targetOf(target))).toEqual(target);
      const f = faultToForm({ latency: '100ms' }, target, { ttl: '5m' });
      expect(f.target).not.toBe('global');
      const req = formToOverlay(f);
      expect(req.target).toEqual(target);
      expect(CreateOverlayBody.safeParse(req).success).toBe(true);
      expect(formToConfigFault(f).source).toEqual(target);
    }
    expect(targetOf({ remote_network: { cidr: '10.9.0.0/24' } })).not.toBe(targetOf({ global: true }));
  });

  it('turns the form into the overlay request, checked against the spec', () => {
    const f = form({ traffic: 'mqtts', latency: '200', jitter: '50', loss: '5', duration: '5m' });
    const req = formToOverlay(f);
    expect(req).toEqual({ target: { device: 'd1' }, fault: { protocol: 'tcp', ports: [8883], latency: '200ms', jitter: '50ms', loss: '5%' }, ttl: '5m' });
    expect(CreateOverlayBody.safeParse(req).success).toBe(true);
  });

  it('one direction only, the advanced parameters, a blackout, until removed', () => {
    const req = formToOverlay(
      form({ direction: 'download', latency: '150', jitter: '50', loss: '3', rate: '2 Mbit/s', reorder: '1', duplicate: '0.5', corrupt: '0.1', keepOrder: true, duration: 'perm' }),
    );
    expect(req).toEqual({
      target: { device: 'd1' },
      fault: { download: { latency: '150ms', jitter: '50ms', loss: '3%', rate: '2Mbit', reorder: '1%', duplicate: '0.5%', corrupt: '0.1%', keep_order: true } },
    });
    expect(CreateOverlayBody.safeParse(req).success).toBe(true);
    expect(formToOverlay(form({ loss: '100', latency: '10' })).fault).toEqual({ blackout: true });
  });

  it('custom traffic: protocol, ports and a destination', () => {
    const req = formToOverlay(form({ traffic: 'custom', protocol: 'udp', ports: '5683, 6000-6010', destination: '203.0.113.0/24', latency: '20' }));
    expect(req.fault).toEqual({ protocol: 'udp', ports: [5683], port_ranges: [{ from: 6000, to: 6010 }], destination: { cidr: '203.0.113.0/24' }, latency: '20ms' });
    expect(CreateOverlayBody.safeParse(req).success).toBe(true);
  });

  it('a configured fault keeps what the form does not show', () => {
    const base = { name: 'old', description: 'kept', enabled: false, source: { network: 'n1' }, latency: '10ms' };
    expect(formToConfigFault(form({ target: 'network:n1', name: 'iot-delay', latency: '30' }), base)).toEqual({
      description: 'kept',
      enabled: false,
      name: 'iot-delay',
      source: { network: 'n1' },
      latency: '30ms',
    });
  });

  it('reads an existing fault back into the form', () => {
    const f = faultToForm({ protocol: 'tcp', ports: [8883], latency: '200ms', jitter: '50ms', loss: '5%' }, { device: 'd1' }, { ttl: '5m' });
    expect(f).toMatchObject({ target: 'device:d1', traffic: 'mqtts', latency: '200', jitter: '50', loss: '5', duration: '5m', direction: 'both' });
    const g = faultToForm({ upload: { rate: '64kbit', blackout: true } }, { global: true });
    expect(g).toMatchObject({ target: 'global', direction: 'upload', rate: '64 kbit/s', loss: '100', duration: 'perm' });
    const h = faultToForm({ protocol: 'udp', ports: [5683], latency: '1s' }, { group: 'g1' });
    expect(h).toMatchObject({ traffic: 'custom', protocol: 'udp', ports: '5683', latency: '1000', target: 'group:g1' });
    expect(hiddenParameters({ upload: { latency: '1ms' }, download: { latency: '2ms' } })).toEqual(['different upload and download parameters']);
    expect(hiddenParameters({ flapping: { up: '1s', down: '1s' } })).toEqual(['flapping']);
  });

  it('checks the fields: jitter, loss, rate, reorder, ports, destination, name', () => {
    expect(faultFormErrors(form({ latency: '200', jitter: '50' }))).toEqual({});
    expect(faultFormErrors(form({ latency: '20', jitter: '50' })).jitter).toBe('Jitter cannot be larger than the latency.');
    expect(faultFormErrors(form({ loss: '120' })).loss).toBe('At most 100.');
    expect(faultFormErrors(form({ loss: '-1' })).loss).toBe('A number of 0 or more.');
    expect(faultFormErrors(form({ rate: 'fast' })).rate).toMatch(/A rate like/);
    expect(faultFormErrors(form({ rate: '4 bit/s' })).rate).toBe('At least 8 bit/s.');
    expect(faultFormErrors(form({ reorder: '5' })).reorder).toBe('Reordering needs a latency.');
    expect(faultFormErrors(form({ traffic: 'custom', ports: '80' })).ports).toBe('Ports need the protocol TCP or UDP.');
    expect(faultFormErrors(form({ traffic: 'custom', destination: '10.0' })).destination).toMatch(/IPv4 address or a prefix/);
    expect(faultFormErrors(form({ name: '-x' }), 'config').name).toBeTruthy();
    expect(faultFormErrors(form({ name: '-x' }), 'overlay').name).toBeUndefined();
  });

  it('the live preview sentence', () => {
    expect(previewSentence(form({ traffic: 'mqtts', latency: '200', jitter: '50', loss: '5', duration: '5m' }), 'ESP32-42')).toBe(
      'ESP32-42 · MQTT · TCP 8883 · upload & download → ↑↓ 200 ms ±50, 5 % loss · expires after 5 min',
    );
    expect(previewSentence(form({ direction: 'upload', duration: 'perm' }), 'ESP32-42')).toBe('ESP32-42 · All traffic · upload only → no impairment · until removed');
    expect(previewSentence(form({ latency: '30' }), 'Network IoT', 'config')).toMatch(/configured until removed/);
  });

  it('the presets are the built-in profiles', () => {
    expect(presets.map((p) => p.label)).toEqual(['LTE', 'Bad LTE', 'Satellite', 'Offline']);
    expect(formToOverlay(form({ ...presets[1]!.values })).fault).toEqual({ latency: '150ms', jitter: '50ms', loss: '3%', rate: '2Mbit' });
    expect(parseRate('64kbit')).toBe('64kbit');
    expect(parseRate('1.5 gbit/s')).toBe('1.5Gbit');
  });

  it('a fault that sets no parameter cannot be applied', () => {
    const none = 'Set at least one parameter: latency, loss, a rate, …';
    expect(faultFormErrors(form({})).latency).toBe(none);
    expect(faultFormErrors(form({ direction: 'download' }), 'config').latency).toBe(none);
    expect(faultFormErrors(form({ keepOrder: true })).latency).toBe(none);
    expect(faultFormErrors(form({ latency: '0', loss: '0' })).latency).toBe(none);
    expect(faultFormErrors(form({ loss: '100' }))).toEqual({});
    expect(faultFormErrors(form({ rate: '64kbit' }))).toEqual({});
    expect(faultFormErrors(form({ corrupt: '1' }))).toEqual({});
    // a broken latency keeps its own message
    expect(faultFormErrors(form({ latency: '-5' })).latency).toBe('A number of 0 or more.');
  });

  it('the gateway names a field by its JSON pointer', () => {
    expect(fieldOf('/fault/jitter')).toBe('jitter');
    expect(fieldOf('/fault/upload/latency')).toBe('latency');
    expect(fieldOf('/faults/c0f0/source/device')).toBe('target');
    expect(fieldOf('/faults/c0f0/port_ranges/0')).toBe('ports');
    expect(fieldOf('/fault')).toBeUndefined();
    expect(fieldOf('/faults/c0f0')).toBeUndefined();
  });
});
