import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import { emptyTls, testCaUrl, tlsBody, tlsCases, tlsEffect, tlsErrors, tlsFieldOf, tlsScope, tlsToForm, type TlsForm } from './tlsForm';

// The TLS form (M23b): the cases, the selection, the checks under the fields, the `tls` part of the request it becomes
// (checked against the spec), the interception settings it keeps and the field a message of the gateway belongs to.

const form = (over: Partial<TlsForm>): TlsForm => ({ ...emptyTls(), ...over });
const withTraffic = (t: Partial<TlsForm['traffic']>) => ({ ...emptyTls().traffic, ...t });
const spec = (tls: unknown) => CreateOverlayBody.safeParse({ target: { device: 'esp32-42' }, tls }).success;

describe('the TLS form', () => {
  it('offers the eight cases of the responder, the three that need the test CA marked', () => {
    expect(tlsCases.map((c) => c.value)).toEqual(['untrusted_ca', 'self_signed', 'expired', 'not_yet_valid', 'hostname_mismatch', 'handshake_reset', 'handshake_close', 'handshake_stall']);
    expect(tlsCases.filter((c) => c.testCa).map((c) => c.value)).toEqual(['expired', 'not_yet_valid', 'hostname_mismatch']);
  });

  it('a case without selectors is the case alone: the responder takes 443 and 8883', () => {
    expect(tlsBody(emptyTls())).toEqual({ case: 'untrusted_ca' });
    expect(tlsScope(emptyTls(), '')).toBe('TLS connections on TCP 443, 8883');
  });

  it('sends the ports, the destination, the server names and the cut, and no protocol of its own', () => {
    const f = form({
      case: 'expired',
      traffic: withTraffic({ ports: '8883, 8000-8100', destKind: 'cidr', destination: '203.0.113.0/24' }),
      sni: 'broker.example.com, *.cdn.example.net',
      cutExisting: true,
    });
    expect(tlsErrors(f)).toEqual({});
    const body = tlsBody(f);
    expect(body).toEqual({
      case: 'expired',
      ports: [8883],
      port_ranges: [{ from: 8000, to: 8100 }],
      destination: { cidr: '203.0.113.0/24' },
      sni: ['broker.example.com', '*.cdn.example.net'],
      cut_existing: true,
    });
    expect(spec(body)).toBe(true);
    expect(tlsScope(f, 'to 203.0.113.0/24')).toBe('TLS connections on TCP 8883, 8000-8100 to 203.0.113.0/24 for broker.example.com, *.cdn.example.net, existing ones cut');
  });

  it('every case the form offers is a case the spec accepts', () => {
    for (const c of tlsCases) expect(spec(tlsBody(form({ case: c.value, traffic: withTraffic({ ports: '443' }) })))).toBe(true);
    for (const d of [{ destKind: 'uplink' as const }, { destKind: 'network' as const, destination: 'IoT' }, { destKind: 'hostname' as const, destination: 'broker.example.com' }]) {
      const f = form({ traffic: withTraffic(d) });
      expect(tlsErrors(f)).toEqual({});
      expect(spec(tlsBody(f))).toBe(true);
    }
  });

  it('checks the ports, the destination and the server names; ports need no protocol', () => {
    expect(tlsErrors(form({ traffic: withTraffic({ ports: '443' }) }))).toEqual({});
    expect(tlsErrors(form({ traffic: withTraffic({ ports: '0' }) })).ports).toMatch(/is no port/);
    expect(tlsErrors(form({ traffic: withTraffic({ ports: '9-1' }) })).ports).toMatch(/is no range/);
    expect(tlsErrors(form({ traffic: withTraffic({ destKind: 'cidr', destination: 'nope' }) })).destination).toBeDefined();
    expect(tlsErrors(form({ traffic: withTraffic({ destKind: 'hostname', destination: 'a b' }) })).destination).toBeDefined();
    expect(tlsErrors(form({ traffic: withTraffic({ destKind: 'network', destination: '' }) })).destination).toBe('Choose a network.');
    expect(tlsErrors(form({ sni: 'broker.example.com, bad!name' })).tlsSni).toMatch(/is no server name/);
    expect(tlsErrors(form({ sni: 'a.example.com A.example.com' })).tlsSni).toBe('Each name once.');
  });

  it('reads a case back into the form and sends it unchanged, a protocol it was written with included', () => {
    for (const t of [
      { case: 'self_signed' as const },
      { case: 'handshake_stall' as const, ports: [443], sni: ['broker.example.com'] },
      { case: 'expired' as const, protocol: 'tcp' as const, ports: [8883], destination: { network: 'IoT' }, cut_existing: true },
    ]) {
      expect(tlsBody(tlsToForm(t))).toEqual(t);
    }
  });

  it('keeps the settings and the rules of an interception case as they are', () => {
    const t = { case: 'intercept' as const, ports: [443], intercept: { keylog: false, upstream: 'insecure' as const, http_rules: [{ action: 'status' as const, status: 503, match: { host: 'api.example.com' } }] } };
    const f = tlsToForm(t);
    expect(f.case).toBe('intercept');
    expect(tlsBody(f)).toEqual(t);
    expect(spec(tlsBody(f))).toBe(true);
    // another case has no interception settings (the gateway refuses them)
    f.case = 'expired';
    expect(tlsBody(f)).toEqual({ case: 'expired', ports: [443] });
  });

  it('says in words what the case does', () => {
    expect(tlsEffect('expired')).toBe('an expired certificate');
    expect(tlsEffect('handshake_reset')).toBe('a reset handshake');
    expect(tlsEffect('untrusted_ca')).toBe('a certificate of an unknown CA');
  });

  it('puts a problem of the gateway under its field', () => {
    expect(tlsFieldOf('/case')).toBe('tlsCase');
    expect(tlsFieldOf('/sni/0')).toBe('tlsSni');
    expect(tlsFieldOf('/ports/0')).toBe('ports');
    expect(tlsFieldOf('/port_ranges/0')).toBe('ports');
    expect(tlsFieldOf('/protocol')).toBe('ports');
    expect(tlsFieldOf('/destination/cidr')).toBe('destination');
    expect(tlsFieldOf('/cut_existing')).toBe('tlsCut');
    expect(tlsFieldOf('/intercept/http_rules/0')).toBeUndefined();
  });

  it('downloads the test CA from the API', () => {
    expect(testCaUrl).toBe('/api/v1/tls/ca');
  });
});
