import { describe, expect, it } from 'vitest';
import type { Probe, ProbeView } from '../api/generated/model';
import { formFromProbe, probeChanges, probeFieldOf, probeFormErrors, probeFromForm, probeRows, rowTone, type ProbeContext } from './probes';

// M26a: the form of a probe, its checks, the probe as the configuration stores it, its changes in words and the rows of the list.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const TEST = '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7';
const ctx: ProbeContext = { taken: ['ESP32-42', 'probe-a'], networks: [{ id: IOT, name: 'IoT' }, { id: TEST, name: 'Test' }] };

describe('the probe form', () => {
  it('needs a name that nothing else of the namespace has, and a local test network', () => {
    expect(probeFormErrors({ name: 'probe-b', network: IOT, trustsTestCa: false }, ctx)).toEqual({});
    expect(probeFormErrors({ name: '', network: '', trustsTestCa: false }, ctx)).toEqual({
      name: 'The name is required.',
      network: 'Choose the test network the probe is a port of.',
    });
    expect(probeFormErrors({ name: 'PROBE-A', network: IOT, trustsTestCa: false }, ctx).name).toMatch(/already the name of a device/);
    expect(probeFormErrors({ name: 'esp32-42', network: IOT, trustsTestCa: false }, ctx).name).toMatch(/already the name/);
    expect(probeFormErrors({ name: '-x', network: IOT, trustsTestCa: false }, ctx).name).toMatch(/Letters, digits/);
    expect(probeFormErrors({ name: 'a'.repeat(64), network: IOT, trustsTestCa: false }, ctx).name).toMatch(/at most 63/);
    expect(probeFormErrors({ name: '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8', network: IOT, trustsTestCa: false }, ctx).name).toMatch(/UUID/);
    // a network that is no local test network (a WireGuard network, one that is gone)
    expect(probeFormErrors({ name: 'probe-b', network: 'hub', trustsTestCa: false }, ctx).network).toMatch(/local test network/);
  });

  it('is read back from a probe and writes the same probe, keeping what it does not edit', () => {
    const p: Probe = { name: 'probe-a', network: IOT, trusts_test_ca: true, description: 'on the IoT bridge', labels: ['ci'] };
    expect(formFromProbe(p)).toEqual({ name: 'probe-a', network: IOT, trustsTestCa: true });
    expect(probeFromForm(formFromProbe(p), p)).toEqual(p);
    expect(probeFromForm({ name: ' probe-b ', network: TEST, trustsTestCa: false }, p)).toEqual({ name: 'probe-b', network: TEST, description: 'on the IoT bridge', labels: ['ci'] });
  });

  it('writes the switch when it is on, and does not write the default', () => {
    expect(probeFromForm({ name: 'probe-b', network: IOT, trustsTestCa: false })).toEqual({ name: 'probe-b', network: IOT });
    expect(probeFromForm({ name: 'probe-b', network: IOT, trustsTestCa: true })).toEqual({ name: 'probe-b', network: IOT, trusts_test_ca: true });
    // a base that has it off explicitly keeps it, so an unchanged form is no change
    const off: Probe = { name: 'probe-a', network: IOT, trusts_test_ca: false };
    expect(probeFromForm(formFromProbe(off), off)).toEqual(off);
  });

  it("maps the gateway's pointers to the fields of the form", () => {
    expect(probeFieldOf('name')).toBe('name');
    expect(probeFieldOf('network')).toBe('network');
    expect(probeFieldOf('trusts_test_ca')).toBeUndefined();
    expect(probeFieldOf('')).toBeUndefined();
  });
});

describe('the changes of a probe in words', () => {
  const name = (id: string) => ({ [IOT]: 'IoT', [TEST]: 'Test' })[id] ?? id;
  const a: Probe = { name: 'probe-a', network: IOT };

  it('say that a probe is added, deleted or what field changed', () => {
    expect(probeChanges(undefined, { name: 'probe-b', network: TEST, trusts_test_ca: true }, name)).toEqual([{ field: 'probe', from: '—', to: 'added (on Test, trusts the test CA)' }]);
    expect(probeChanges(undefined, { name: 'probe-b', network: TEST }, name)).toEqual([{ field: 'probe', from: '—', to: 'added (on Test)' }]);
    expect(probeChanges(a, null, name)).toEqual([{ field: 'probe', from: 'present', to: 'deleted' }]);
    expect(probeChanges(undefined, null, name)).toEqual([]);
    expect(probeChanges(a, { ...a, name: 'probe-x', network: TEST, trusts_test_ca: true }, name)).toEqual([
      { field: 'name', from: 'probe-a', to: 'probe-x' },
      { field: 'network', from: 'IoT', to: 'Test' },
      { field: 'trusts the test CA', from: 'no', to: 'yes' },
    ]);
    // a member the words do not show
    expect(probeChanges(a, { ...a, labels: ['ci'] }, name)).toEqual([{ field: 'settings', from: 'as before', to: 'changed' }]);
  });
});

describe('the rows of the list', () => {
  const view = (id: string, name: string, addresses: string[], online = true): ProbeView => ({ id, config: { name, network: IOT }, observed: { addresses, online } });
  const base: Record<string, Probe> = { a: { name: 'probe-a', network: IOT }, c: { name: 'probe-c', network: IOT } };

  it('are the base with the draft: new, changed and deleted probes marked, each with what the gateway observes', () => {
    const rows = probeRows(base, { a: { name: 'probe-a', network: TEST }, b: { name: 'probe-b', network: IOT }, c: null }, [view('a', 'probe-a', ['10.10.0.77']), view('c', 'probe-c', [], false)]);
    expect(rows.map((r) => [r.config.name, r.mark])).toEqual([
      ['probe-a', 'CHANGED'],
      ['probe-b', 'NEW'],
      ['probe-c', 'DELETED'],
    ]);
    expect(rows[0]!.view?.observed.addresses).toEqual(['10.10.0.77']);
    expect(rows[0]!.config.network).toBe(TEST);
    expect(rows[1]!.view).toBeUndefined();
    expect(rows[2]!.config.network).toBe(IOT);
  });

  it('list a probe the gateway has while the base is not known, and an edit equal to the base is no mark', () => {
    expect(probeRows({}, {}, [view('a', 'probe-a', [])]).map((r) => [r.config.name, r.mark])).toEqual([['probe-a', undefined]]);
    expect(probeRows(base, { a: { name: 'probe-a', network: IOT } }, [])[0]!.mark).toBeUndefined();
    // sorted by name, numbers as numbers
    const many: Record<string, Probe> = { x: { name: 'probe-10', network: IOT }, y: { name: 'probe-9', network: IOT } };
    expect(probeRows(many, {}, []).map((r) => r.config.name)).toEqual(['probe-9', 'probe-10']);
  });

  it('say in words whether the probe has an address, and that a change or a new probe is not applied yet', () => {
    const [a, b, c] = probeRows(base, { a: { name: 'probe-a', network: TEST }, b: { name: 'probe-b', network: IOT } }, [view('a', 'probe-a', ['10.10.0.77']), view('c', 'probe-c', [])]);
    expect(rowTone(a!)).toEqual({ tone: 'normal', label: 'Online · change not applied yet' });
    expect(rowTone(b!)).toEqual({ tone: 'offline', label: 'not applied yet' });
    expect(rowTone(c!)).toEqual({ tone: 'unknown', label: 'No address yet' });
    expect(rowTone(probeRows(base, {}, [])[0]!)).toEqual({ tone: 'unknown', label: 'status unknown' });
  });
});
