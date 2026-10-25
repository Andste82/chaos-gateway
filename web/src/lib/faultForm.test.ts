import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import {
  durationChoices,
  emptyForm,
  lifetimeOf,
  overlayLifetime,
  overlayOnlyFamilies,
  overlayToForm,
  untilText,
  faultFormErrors,
  faultToForm,
  keepError,
  fieldOf,
  formToConfigFault,
  formToFault,
  formToOverlay,
  parseRate,
  presets,
  previewSentence,
  scopeOf,
  targetOf,
  type FaultForm,
} from './faultForm';
import type { Overlay } from '../api/generated/model';
import { CIDR_CHOICE } from './scopes';

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

  it('keeps the choice "a prefix inside a remote network" while its prefix is still empty', () => {
    expect(scopeOf(CIDR_CHOICE)).toEqual({ remote_network: { cidr: '' } });
    expect(targetOf(scopeOf(CIDR_CHOICE))).toBe(CIDR_CHOICE);
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
    const req = formToOverlay(form({ traffic: 'custom', protocol: 'udp', ports: '5683, 6000-6010', destKind: 'cidr', destination: '203.0.113.0/24', latency: '20' }));
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

  it("a configured MTU fault without a mode keeps leaving out the default mode", () => {
    const base = { name: 'small', source: { network: 'n1' }, family: 'mtu' as const, mtu: { size: 1280 } };
    const f = faultToForm(base, base.source, { name: base.name });
    expect(formToConfigFault(f, base)).toEqual(base);
    expect(formToConfigFault({ ...f, mtuMode: 'mss_clamp' }, base).mtu).toEqual({ size: 1280, mode: 'mss_clamp' });
    // a new one writes the mode
    expect(formToConfigFault(f).mtu).toEqual({ size: 1280, mode: 'icmp' });
  });

  it('an edited overlay keeps its remaining time unless another duration is chosen (P3-M13-06)', () => {
    const now = new Date('2026-10-14T12:00:00Z');
    const expiresAt = '2026-10-14T12:04:12.300Z';
    const f = faultToForm({ loss: '2%' }, { device: 'd1' }, { ttl: '5m', expiresAt });
    expect(f).toMatchObject({ duration: 'keep', keepUntil: expiresAt, keepTtl: '5m' });
    // the overlay's own TTL again, and the remaining time in whole seconds, rounded up, as expires_in
    expect(formToOverlay(f, now)).toMatchObject({ ttl: '5m', expires_in: '253s' });
    expect(CreateOverlayBody.safeParse(formToOverlay(f, now)).success).toBe(true);
    expect(durationChoices(f, '5m', now).map((d) => d.label)).toEqual(['Keep: 4:13 left of 5 min', '1 min', '5 min', '15 min', '1 hour', 'Until removed']);
    expect(previewSentence(f, 'ESP32-42')).toMatch(/· expires at \d\d:\d\d as before$/);
    // another choice starts a TTL of its own
    expect(formToOverlay({ ...f, duration: '15m' }, now)).toMatchObject({ ttl: '15m' });
    expect(formToOverlay({ ...f, duration: '15m' }, now).expires_in).toBeUndefined();
    expect(formToOverlay({ ...f, duration: 'perm' }, now).ttl).toBeUndefined();
    // an expired overlay is written with the shortest time rather than none (which would be until removed)
    expect(formToOverlay(f, new Date('2026-10-14T13:00:00Z')).expires_in).toBe('1s');
    // but the dialog does not send it: keeping an expiry that passed is an error, and Keep says it expired (Phase 3 audit)
    expect(keepError(f, now)).toBeUndefined();
    expect(keepError(f, new Date('2026-10-14T13:00:00Z'))).toMatch(/^The fault expired at \d\d:\d\d: choose how long it runs now\.$/);
    expect(keepError({ ...f, duration: '15m' }, new Date('2026-10-14T13:00:00Z'))).toBeUndefined();
    expect(durationChoices(f, '5m', new Date('2026-10-14T13:00:00Z'))[0]!.label).toMatch(/^Keep: expired at \d\d:\d\d$/);
    // a gateway clock read a little early never asks for more than the TTL
    expect(formToOverlay(f, new Date('2026-10-14T11:59:00Z')).expires_in).toBe('300s');
    // an expiry without a TTL (never sent by the gateway) is written as the TTL
    expect(formToOverlay({ ...f, keepTtl: '' }, now)).toEqual({ target: { device: 'd1' }, fault: { loss: '2%' }, ttl: '253s' });
  });

  it('a TTL that is not one of the choices is shown as such and kept (P3-M13-06)', () => {
    const now = new Date('2026-10-14T12:00:00Z');
    const f = faultToForm({ loss: '2%' }, { device: 'd1' }, { ttl: '30m', expiresAt: '2026-10-14T12:20:00Z' });
    const choices = durationChoices(f, '30m', now);
    expect(choices[0]).toEqual({ value: 'keep', label: 'Keep: 20:00 left of 30 min' });
    expect(choices.at(-1)).toEqual({ value: 'ttl:30m', label: '30 min (its own TTL)' });
    // kept: the TTL stays 30m, the expiry where it was
    expect(formToOverlay(f, now)).toMatchObject({ ttl: '30m', expires_in: '1200s' });
    expect(CreateOverlayBody.safeParse(formToOverlay(f, now)).success).toBe(true);
    // its own TTL again starts it over
    expect(formToOverlay({ ...f, duration: 'ttl:30m' }, now)).toEqual({ target: { device: 'd1' }, fault: { loss: '2%' }, ttl: '30m' });
    expect(previewSentence({ ...f, duration: 'ttl:30m' }, 'ESP32-42')).toMatch(/· expires after 30 min$/);
    // a TTL in seconds (set over the API) is shown in words
    expect(durationChoices(faultToForm({ loss: '2%' }, { device: 'd1' }, { ttl: '4530s', expiresAt: '2026-10-14T13:15:30Z' }), '4530s', now)).toContainEqual({
      value: 'ttl:4530s',
      label: '1 h 15 min 30 s (its own TTL)',
    });
    // without an expiry (an old answer) the own TTL is the choice, never 15 minutes
    expect(faultToForm({ loss: '2%' }, { device: 'd1' }, { ttl: '30m' }).duration).toBe('ttl:30m');
    // a new fault has no remaining time to keep
    expect(durationChoices(emptyForm(), undefined, now).map((d) => d.value)).toEqual(['1m', '5m', '15m', '1h', 'perm']);
  });

  it('reads an existing fault back into the form', () => {
    const f = faultToForm({ protocol: 'tcp', ports: [8883], latency: '200ms', jitter: '50ms', loss: '5%' }, { device: 'd1' }, { ttl: '5m' });
    expect(f).toMatchObject({ target: 'device:d1', traffic: 'mqtts', latency: '200', jitter: '50', loss: '5', duration: '5m', direction: 'both' });
    const g = faultToForm({ upload: { rate: '64kbit', blackout: true } }, { global: true });
    expect(g).toMatchObject({ target: 'global', direction: 'upload', rate: '64 kbit/s', loss: '100', duration: 'perm' });
    const h = faultToForm({ protocol: 'udp', ports: [5683], latency: '1s' }, { group: 'g1' });
    expect(h).toMatchObject({ traffic: 'custom', protocol: 'udp', ports: '5683', latency: '1000', target: 'group:g1' });
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
    expect(faultFormErrors(form({ traffic: 'custom', destKind: 'cidr', destination: '10.0' })).destination).toMatch(/IPv4 address or a prefix/);
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

describe('every fault kind in the dialog (P3-M13-04)', () => {
  const valid = (f: FaultForm) => CreateOverlayBody.safeParse(formToOverlay(f)).success;

  it('an MTU fault: its traffic, size and mode, read back and sent unchanged', () => {
    const f = form({ family: 'mtu', traffic: 'https', mtuSize: '1280', mtuMode: 'blackhole' });
    expect(faultFormErrors(f)).toEqual({});
    expect(formToOverlay(f)).toMatchObject({ target: { device: 'd1' }, fault: { family: 'mtu', protocol: 'tcp', ports: [443], mtu: { size: 1280, mode: 'blackhole' } } });
    expect(valid(f)).toBe(true);
    const back = faultToForm(formToFault(f), { device: 'd1' });
    expect(back).toMatchObject({ family: 'mtu', traffic: 'https', mtuSize: '1280', mtuMode: 'blackhole' });
    expect(formToFault(back)).toEqual(formToFault(f));
    expect(faultFormErrors(form({ family: 'mtu', mtuSize: '500' })).mtuSize).toBe('A packet size from 552 to 1500 bytes.');
    expect(faultFormErrors(form({ family: 'mtu', mtuSize: '1400.5' })).mtuSize).toBeTruthy();
    expect(previewSentence(form({ family: 'mtu', mtuSize: '1280', mtuMode: 'icmp', duration: 'perm' }), 'ESP32-42')).toBe(
      'ESP32-42 · All traffic → packets above 1280 bytes: ICMP · until removed',
    );
  });

  it('a tunnel fault: no target, no traffic, only the parameters a tunnel takes', () => {
    const f = form({ family: 'tunnel', tunnel: 'client:c1', latency: '100', jitter: '10', rate: '1 Mbit/s', reorder: '5', duration: '5m' });
    expect(faultFormErrors(f)).toEqual({});
    const req = formToOverlay(f);
    expect(req).toEqual({ fault: { family: 'tunnel', tunnel: { client: 'c1' }, latency: '100ms', jitter: '10ms' }, ttl: '5m' });
    expect(valid(f)).toBe(true);
    expect(formToConfigFault(f)).toEqual({ family: 'tunnel', tunnel: { client: 'c1' }, latency: '100ms', jitter: '10ms' });
    expect(faultFormErrors(form({ family: 'tunnel', latency: '10' })).tunnel).toMatch(/Choose the WireGuard client or link/);
    // a rate alone is no tunnel parameter: the fault would set nothing
    expect(faultFormErrors(form({ family: 'tunnel', tunnel: 'link:l1', rate: '1 Mbit/s' })).latency).toMatch(/Set at least one parameter/);
    const back = faultToForm({ family: 'tunnel', tunnel: { link: 'l1' }, upload: { flapping: { up: '20s', down: '10s' } } }, undefined);
    expect(back).toMatchObject({ family: 'tunnel', tunnel: 'link:l1', direction: 'upload', flapping: true, flapUp: '20', flapDown: '10' });
    expect(formToOverlay(back).fault).toEqual({ family: 'tunnel', tunnel: { link: 'l1' }, upload: { flapping: { up: '20s', down: '10s' } } });
    expect(previewSentence(back, 'Tunnel of link site-b')).toBe('Tunnel of link site-b · upload only → tunnel ↑ flapping 20s up / 10s down · until removed');
  });

  it('the advanced impairment parameters: burst loss, flapping, a distribution, loss correlation, queue limit', () => {
    const f = form({
      latency: '100',
      jitter: '20',
      distribution: 'pareto',
      lossModel: 'burst',
      burstP: '1',
      burstR: '30',
      burstK: '99',
      queueLimit: '50',
      flapping: true,
      flapUp: '20',
      flapDown: '0.5',
    });
    expect(faultFormErrors(f)).toEqual({});
    expect(formToFault(f)).toEqual({
      latency: '100ms',
      jitter: '20ms',
      distribution: 'pareto',
      burst_loss: { p: '1%', r: '30%', k: '99%' },
      queue_limit: 50,
      flapping: { up: '20s', down: '500ms' },
    });
    expect(valid(f)).toBe(true);
    const back = faultToForm(formToFault(f), { device: 'd1' });
    expect(formToFault(back)).toEqual(formToFault(f));
    expect(faultToForm({ loss: '2%', loss_correlation: '25%' }, { device: 'd1' })).toMatchObject({ lossModel: 'random', loss: '2', lossCorrelation: '25' });
    // the checks of the advanced fields
    expect(faultFormErrors(form({ lossModel: 'burst', burstR: '10' })).burstP).toBe('Needed for burst loss.');
    expect(faultFormErrors(form({ latency: '1', queueLimit: '0' })).queueLimit).toMatch(/from 1 to 1000000/);
    expect(faultFormErrors(form({ latency: '1', flapping: true, flapUp: '0', flapDown: '5' })).flapUp).toBe('A time in seconds above 0.');
    expect(faultFormErrors(form({ loss: '100', flapping: true, flapUp: '5', flapDown: '5' })).flapUp).toMatch(/timed blackout/);
  });

  it('different parameters per direction', () => {
    const f = form({ direction: 'each', latency: '50' });
    f.download.loss = '10';
    f.download.rate = '64kbit';
    expect(faultFormErrors(f)).toEqual({});
    expect(formToFault(f)).toEqual({ upload: { latency: '50ms' }, download: { loss: '10%', rate: '64kbit' } });
    expect(valid(f)).toBe(true);
    const back = faultToForm(formToFault(f), { device: 'd1' });
    expect(back.direction).toBe('each');
    expect(back.download).toMatchObject({ loss: '10', rate: '64 kbit/s' });
    expect(formToFault(back)).toEqual(formToFault(f));
    // the same parameters both ways are one set
    expect(faultToForm({ upload: { latency: '5ms' }, download: { latency: '5ms' } }, undefined).direction).toBe('both');
    // the download set's problems are its own
    f.download.jitter = '20';
    expect(faultFormErrors(f)).toEqual({ 'download.jitter': 'Jitter cannot be larger than the latency.' });
    expect(fieldOf('/fault/download/jitter', true)).toBe('download.jitter');
    expect(fieldOf('/fault/download/jitter')).toBe('jitter');
    expect(previewSentence(form({ direction: 'each', latency: '50', duration: 'perm' }), 'ESP32-42')).toBe(
      'ESP32-42 · All traffic · upload and download apart → ↑ 50 ms · until removed',
    );
  });

  it('every destination a fault can have', () => {
    for (const destination of [{ uplink: true as const }, { network: 'n1' }, { cidr: '203.0.113.0/24' }, { hostname: 'broker.example.com' }]) {
      const f = faultToForm({ destination, latency: '10ms' }, { device: 'd1' });
      expect(f.traffic).toBe('custom');
      expect(formToFault(f).destination).toEqual(destination);
      expect(faultFormErrors(f)).toEqual({});
    }
    expect(faultFormErrors(form({ traffic: 'custom', destKind: 'hostname', destination: 'not a name', latency: '1' })).destination).toMatch(/host name/);
    expect(previewSentence(form({ traffic: 'custom', destKind: 'uplink', latency: '10', duration: 'perm' }), 'ESP32-42')).toBe(
      'ESP32-42 · any protocol to uplink · upload & download → ↑↓ 10 ms · until removed',
    );
  });

  it("the gateway's pointers of the new fields", () => {
    expect(fieldOf('/fault/burst_loss/p')).toBe('burstP');
    expect(fieldOf('/fault/upload/flapping')).toBe('flapUp');
    expect(fieldOf('/fault/queue_limit')).toBe('queueLimit');
    expect(fieldOf('/fault/mtu/size')).toBe('mtuSize');
    expect(fieldOf('/fault/mtu')).toBe('mtuSize');
    expect(fieldOf('/fault/tunnel')).toBe('tunnel');
    expect(fieldOf('/faults/x/destination/network')).toBe('destination');
  });
});

// M23b: a DNS fault and a TLS case are kinds of the dialog; each is an overlay of its own kind, never a configured fault.
describe('the DNS and TLS kinds of the dialog', () => {
  const overlay = (over: Partial<Overlay>): Overlay => ({
    id: 'o1',
    kind: 'dns',
    owner: { type: 'user', id: 'admin', name: 'admin' },
    created_at: '2026-10-25T10:00:00Z',
    updated_at: '2026-10-25T10:00:00Z',
    generation: 1,
    ...over,
  });

  it('a DNS fault is the dns part of the overlay request, with the target and the duration', () => {
    const f = form({ family: 'dns', duration: '5m', dns: { action: 'nxdomain', names: 'broker.example.com', delay: '2s', answers: '', ttl: '5s' } });
    expect(faultFormErrors(f)).toEqual({});
    const req = formToOverlay(f);
    expect(req).toEqual({ target: { device: 'd1' }, dns: { action: 'nxdomain', names: ['broker.example.com'] }, ttl: '5m' });
    expect(CreateOverlayBody.safeParse(req).success).toBe(true);
    // the impairment fields of the form do not leak into it
    expect(req.fault).toBeUndefined();
    expect(formToOverlay({ ...f, duration: 'perm' }).ttl).toBeUndefined();
  });

  it('a TLS case is the tls part of the overlay request', () => {
    const f = form({ family: 'tls', duration: 'perm', tls: { ...emptyForm().tls, case: 'expired', sni: 'broker.example.com', cutExisting: true } });
    expect(faultFormErrors(f)).toEqual({});
    const req = formToOverlay(f);
    expect(req).toEqual({ target: { device: 'd1' }, tls: { case: 'expired', sni: ['broker.example.com'], cut_existing: true } });
    expect(CreateOverlayBody.safeParse(req).success).toBe(true);
    expect(req.fault).toBeUndefined();
  });

  it('the checks of the kind are the form\'s, with the target; the traffic of the other kinds is not checked', () => {
    const dns = form({ family: 'dns', dns: { action: 'delay', names: '', delay: '', answers: '', ttl: '' }, traffic: 'custom', ports: '99999' });
    expect(Object.keys(faultFormErrors(dns))).toEqual(['dnsDelay']);
    const tls = form({ family: 'tls', tls: { ...emptyForm().tls, sni: 'bad!name' }, latency: '' });
    expect(Object.keys(faultFormErrors(tls))).toEqual(['tlsSni']);
    expect(faultFormErrors(form({ family: 'dns', target: '' })).target).toBe('Choose a target.');
    expect(overlayOnlyFamilies).toEqual(['dns', 'tls']);
  });

  it('an overlay of either kind is opened with its fields, its target and its remaining time, and sent back unchanged', () => {
    const expires = new Date(Date.now() + 60_000).toISOString();
    const dns = overlay({ kind: 'dns', target: { network: 'n1' }, dns: { action: 'wrong_answer', answers: ['10.9.0.1'], names: ['broker.example.com'] }, ttl: '5m', expires_at: expires });
    const f = overlayToForm(dns);
    expect(f.family).toBe('dns');
    expect(f.target).toBe('network:n1');
    expect(f.duration).toBe('keep');
    expect(formToOverlay(f)).toMatchObject({ target: { network: 'n1' }, dns: dns.dns, ttl: '5m' });
    const tls = overlay({ kind: 'tls', target: { device: 'd1' }, tls: { case: 'handshake_reset', ports: [8883] } });
    const g = overlayToForm(tls);
    expect(g.family).toBe('tls');
    expect(g.duration).toBe('perm');
    expect(formToOverlay(g)).toEqual({ target: { device: 'd1' }, tls: tls.tls });
    // a fault overlay reads back as before
    expect(overlayToForm(overlay({ kind: 'fault', target: { device: 'd1' }, fault: { latency: '100ms' } })).family).toBe('impairment');
  });

  it('says what the fault does in the preview', () => {
    expect(previewSentence(form({ family: 'dns', duration: '5m', dns: { action: 'nxdomain', names: 'broker.example.com', delay: '', answers: '', ttl: '' } }), 'ESP32-42')).toBe(
      'ESP32-42 · DNS queries for broker.example.com → NXDOMAIN (no such name) · expires after 5 min',
    );
    expect(previewSentence(form({ family: 'dns', duration: 'perm', dns: { action: 'delay', names: '', delay: '2s', answers: '', ttl: '' } }), 'Network IoT')).toBe(
      'Network IoT · DNS queries for every name → answers delayed by 2s · until removed',
    );
    const tls = { ...emptyForm().tls, case: 'expired' as const, traffic: { protocol: 'any' as const, ports: '8883', destKind: 'cidr' as const, destination: '203.0.113.0/24' }, cutExisting: true };
    expect(previewSentence(form({ family: 'tls', duration: '1m', tls }), 'ESP32-42')).toBe(
      'ESP32-42 · TLS connections on TCP 8883 to 203.0.113.0/24, existing ones cut → an expired certificate · expires after 1 min',
    );
  });

  it("the gateway's pointers of the two kinds", () => {
    expect(fieldOf('/dns/delay')).toBe('dnsDelay');
    expect(fieldOf('/dns/names/1')).toBe('dnsNames');
    expect(fieldOf('/dns/answers/0')).toBe('dnsAnswers');
    expect(fieldOf('/dns/ttl')).toBe('dnsTtl');
    expect(fieldOf('/tls/ports/0')).toBe('ports');
    expect(fieldOf('/tls/sni/0')).toBe('tlsSni');
    expect(fieldOf('/tls/destination/cidr')).toBe('destination');
    expect(fieldOf('/tls/cut_existing')).toBe('tlsCut');
    expect(fieldOf('/target/device')).toBe('target');
  });

  it('the duration choice is shared: the lifetime of an overlay, its request and its words', () => {
    const l = overlayLifetime({ ttl: '5m' });
    expect(l).toEqual({ duration: '5m', keepUntil: '', keepTtl: '5m' });
    expect(lifetimeOf(l)).toEqual({ ttl: '5m' });
    expect(untilText(l)).toBe('expires after 5 min');
    expect(lifetimeOf({ duration: 'perm', keepUntil: '', keepTtl: '' })).toEqual({});
    const until = new Date(Date.now() + 90_000).toISOString();
    const kept = overlayLifetime({ ttl: '30m', expires_at: until });
    expect(kept.duration).toBe('keep');
    const req = lifetimeOf(kept, new Date());
    expect(req.ttl).toBe('30m');
    expect(req.expires_in).toMatch(/^\d+s$/);
  });
});
