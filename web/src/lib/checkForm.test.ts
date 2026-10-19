import { describe, expect, it } from 'vitest';
import type { Check } from '../api/generated/model';
import { GetScenarioResponse } from '../api/generated/zod/config/config';
import { checkFieldOf, checkFormErrors, checkToForm, emptyCheckForm, formToCheck, observesTraffic } from './checkForm';
import { ESP, mqttOutage } from './scenarioFixtures';

// The check form (M19): the checks of the example go through it unchanged, every type has its checks under
// its fields, the window is checked against the steps, and the gateway's messages find their field.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const steps = ['normal', 'slow', 'cut', 'restore'];
const spec = (c: Check): boolean =>
  GetScenarioResponse.shape.config.safeParse({ name: 'x', target: { device: ESP }, steps: [{ id: 'cut', at: '0s', restore: true }], checks: [c] }).success;

describe('the check form', () => {
  it('gives back every check as it was', () => {
    const checks: Check[] = [
      ...mqttOutage.checks!,
      { name: 'no leaks', traffic_not_seen: { protocol: 'tcp', except: [{ uplink: true }, { network: IOT }, { cidr: '203.0.113.0/24' }, { hostname: 'broker.example.com' }] }, window: { from: 'start', until: 'cut' } },
      { no_connection: { destination: { network: IOT } }, window: { from: 'cut', within: '5s' } },
      { traffic_seen: {}, window: { from: 'start', within: '1s' } },
      { tls_rejected: { protocol: 'tcp', ports: [8883] }, window: { from: 'start', within: '1m' } },
      { traffic_not_seen: {}, window: { from: 'start', within: '1m' } },
    ];
    for (const c of checks) {
      expect(formToCheck(checkToForm(c))).toEqual(c);
      expect(spec(c)).toBe(true);
    }
  });

  it('a new check is a reconnected check in a window of 30 s from the start', () => {
    expect(formToCheck(emptyCheckForm())).toEqual({ reconnected: {}, window: { from: 'start', within: '30s' } });
  });

  it('knows which types observe traffic', () => {
    expect(observesTraffic('reconnected')).toBe(true);
    expect(observesTraffic('traffic_not_seen')).toBe(true);
    expect(observesTraffic('dns_query_seen')).toBe(false);
  });
});

describe('the checks under the fields', () => {
  const errors = (f = emptyCheckForm()) => checkFormErrors(f, { steps });

  it('a new check is complete', () => {
    expect(errors()).toEqual({});
  });

  it('the traffic, the name of the DNS query, the allowed destinations', () => {
    const f = emptyCheckForm();
    f.traffic.ports = '8883';
    expect(errors(f).ports).toBe('Ports need the protocol TCP or UDP.');
    f.traffic.protocol = 'tcp';
    expect(errors(f)).toEqual({});
    const d = emptyCheckForm('dns_query_seen');
    expect(errors(d).dnsName).toBeDefined();
    d.dnsName = 'broker.example.com';
    expect(errors(d)).toEqual({});
    const n = emptyCheckForm('traffic_not_seen');
    n.except.push({ destKind: 'cidr', destination: 'nope' });
    expect(errors(n).except).toBeDefined();
    n.except[0] = { destKind: 'cidr', destination: '203.0.113.0/24' };
    n.except.push({ destKind: 'uplink', destination: '' });
    expect(errors(n)).toEqual({});
    n.except.push({ destKind: 'network', destination: '' });
    expect(errors(n).except).toBeDefined();
  });

  it('the window opens at a step or the start and closes after a time or at a step', () => {
    const f = emptyCheckForm();
    f.from = 'gone';
    expect(errors(f).from).toBeDefined();
    f.from = 'restore';
    f.within = '30';
    expect(errors(f).within).toBeDefined();
    f.within = '30s';
    f.windowKind = 'until';
    expect(errors(f).until).toBeDefined();
    f.until = 'cut';
    expect(errors(f)).toEqual({});
    f.name = 'x'.repeat(101);
    expect(errors(f).name).toBe('At most 100 characters.');
  });
});

describe("the gateway's messages", () => {
  it('are put under the field their pointer names', () => {
    expect(checkFieldOf('name')).toBe('name');
    expect(checkFieldOf('window/from')).toBe('from');
    expect(checkFieldOf('window/within')).toBe('within');
    expect(checkFieldOf('window/until')).toBe('until');
    expect(checkFieldOf('window')).toBe('window');
    expect(checkFieldOf('dns_query_seen/name')).toBe('dnsName');
    expect(checkFieldOf('reconnected/ports/0')).toBe('ports');
    expect(checkFieldOf('reconnected/destination/cidr')).toBe('destination');
    expect(checkFieldOf('traffic_not_seen/except/1/network')).toBe('except');
    expect(checkFieldOf('')).toBeUndefined();
  });
});
