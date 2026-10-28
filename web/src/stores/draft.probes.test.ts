import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import type { Configuration, Probe, RevisionWithConfiguration } from '../api/generated/model';
import { deviceReferences } from '../lib/devices';
import { useDraftStore } from './draft';

// The probes in the configuration draft (M26a): their changes in words, their merge patch, the gateway's messages about them,
// the removal together with the objects that name a probe and its Undo, and the concurrent-change dialog's overlaps and reapply.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const TEST = '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7';
const A = '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8';
const NEW = '9d2e4b61-0c3f-4a75-8e1b-2f6a7c8d9e01';
const RULE = 'a1000000-0000-4000-8000-000000000001';
const GROUP = 'a2000000-0000-4000-8000-000000000001';
const FAULT = 'c0f00000-0000-4000-8000-000000000007';
const FWD = 'e1000000-0000-4000-8000-000000000001';
const names = { network: (id: string) => ({ [IOT]: 'IoT', [TEST]: 'Test' })[id] };

const probeA: Probe = { name: 'probe-a', network: IOT };

function config(probes: Record<string, Probe> = { [A]: probeA }): Configuration {
  return { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, probes };
}

/** A configuration in which every kind of object names the probe. */
function named(): Configuration {
  return {
    ...config(),
    access_rules: { [RULE]: { name: 'probe-no-dns', source: { device: A }, protocol: 'udp', ports: [53], action: 'drop' } },
    access_rule_order: [RULE],
    faults: { [FAULT]: { name: 'probe-slow', source: { device: A }, latency: '100ms' } },
    groups: { [GROUP]: { name: 'clients', members: [A, 'a1b2c3d4-0000-4000-8000-000000000042'] } },
    port_forwards: { [FWD]: { protocol: 'tcp', entry: 'uplink', external_port: 8080, target: { device: A } } },
  };
}

function revision(id: number, configuration: Configuration = config()): RevisionWithConfiguration {
  return { id, status: 'active', schema_version: 1, created_at: '', created_by: { type: 'user', id: 'admin' }, configuration };
}

beforeEach(() => setActivePinia(createPinia()));

describe('the probes in the draft (M26a)', () => {
  it('a new probe is a change in words and the whole probe in the patch; taken back it is none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    const added: Probe = { name: 'probe-b', network: TEST, trusts_test_ca: true };
    d.setProbe(NEW, added);
    expect(d.changes({ network: names.network })).toEqual([
      { kind: 'probe', id: NEW, name: 'probe-b', field: 'probe', from: '—', to: 'added (on Test, trusts the test CA)' },
    ]);
    expect(d.summary({ network: names.network })).toBe('1 change in 1 probe');
    expect(d.patch()).toEqual({ probes: { [NEW]: added } });
    expect(d.effectiveProbes[NEW]).toEqual(added);
    expect(d.dirty).toBe(true);
    d.setProbe(NEW, null);
    expect(d.dirty).toBe(false);
    expect(d.patch()).toEqual({});
  });

  it('a probe moved to another network is the member that changed; one back at its base is no change', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(A, { ...probeA, network: TEST, trusts_test_ca: true });
    expect(d.changes({ network: names.network })).toEqual([
      { kind: 'probe', id: A, name: 'probe-a', field: 'network', from: 'IoT', to: 'Test' },
      { kind: 'probe', id: A, name: 'probe-a', field: 'trusts the test CA', from: 'no', to: 'yes' },
    ]);
    expect(d.summary({ network: names.network })).toBe('2 changes in 1 probe');
    expect(d.patch()).toEqual({ probes: { [A]: { network: TEST, trusts_test_ca: true } } });
    d.revertProbe(A);
    expect(d.dirty).toBe(false);
    d.setProbe(A, { ...probeA, network: TEST });
    d.setProbe(A, { ...probeA });
    expect(d.dirty).toBe(false);
  });

  it('a deleted probe is null in the patch and a change in words; Undo brings it back', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(A, null);
    expect(d.changes()).toEqual([{ kind: 'probe', id: A, name: 'probe-a', field: 'probe', from: 'present', to: 'deleted' }]);
    expect(d.patch()).toEqual({ probes: { [A]: null } });
    expect(d.effectiveProbes[A]).toBeUndefined();
    d.revertProbe(A);
    expect(d.dirty).toBe(false);
    expect(d.effectiveProbes[A]).toEqual(probeA);
  });

  it("the gateway's messages are kept by probe with the pointer below it, and an edit of the probe clears them", () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(NEW, { name: 'probe-b', network: TEST });
    d.setServerErrors([
      { path: `/probes/${NEW}/network`, message: 'a probe is a port of a local test network' },
      { path: `/probes/${A}`, message: 'not valid' },
    ]);
    expect(d.serverErrors[NEW]).toEqual({ network: 'a probe is a port of a local test network' });
    expect(d.serverErrors[A]).toEqual({ probe: 'not valid' });
    d.setProbe(NEW, { name: 'probe-b', network: IOT });
    expect(d.serverErrors[NEW]).toBeUndefined();
  });

  it('discard drops the probe edits', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(NEW, { name: 'probe-b', network: TEST });
    d.setProbe(A, null);
    d.discard();
    expect(d.dirty).toBe(false);
    expect(d.probes).toEqual({});
  });
});

describe('removing a probe together with the objects that name it (M26a)', () => {
  it('deletes the rule, the fault and the forward, takes the probe out of the group and deletes the probe', () => {
    const d = useDraftStore();
    d.track(revision(42, named()));
    d.removeProbe({ id: A, name: 'probe-a' });
    expect(d.patch()).toEqual({
      probes: { [A]: null },
      access_rules: { [RULE]: null },
      access_rule_order: [],
      faults: { [FAULT]: null },
      port_forwards: { [FWD]: null },
      groups: { [GROUP]: { members: ['a1b2c3d4-0000-4000-8000-000000000042'] } },
    });
    expect(d.summary()).toBe('5 changes in 1 rule, 1 fault, 1 group, 1 port forward and 1 probe');
  });

  it('a scenario that targets the probe, or a step of it, blocks the removal and is not changed by it', () => {
    const SCN = '5c000000-0000-4000-8000-000000000001';
    const STEPS = '5c000000-0000-4000-8000-000000000002';
    const d = useDraftStore();
    d.track(
      revision(42, {
        ...config(),
        scenarios: {
          [SCN]: { name: 'probe-outage', target: { device: A }, steps: [{ id: 'cut', at: '0s', fault: { latency: '100ms' } }] },
          [STEPS]: { name: 'probe-step', target: { network: IOT }, steps: [{ id: 'cut', at: '0s', target: { device: 'probe-a' }, fault: { latency: '100ms' } }] },
        },
      }),
    );
    // the dialog asks for the references with the scenarios of the draft; the ones that block keep the removal waiting
    const refs = deviceReferences({ id: A, name: 'probe-a' }, { scenarios: d.effectiveScenarios });
    expect(refs.map((r) => `${r.kind} ${r.name} ${r.effect}`)).toEqual(['scenario probe-outage blocks', 'scenario probe-step blocks']);
    expect(refs.map((r) => r.to)).toEqual([`/scenarios/${SCN}`, `/scenarios/${STEPS}`]);
    // a scenario is a file of its own: the store neither deletes nor changes it
    d.removeProbe({ id: A, name: 'probe-a' });
    expect(d.patch()).toEqual({ probes: { [A]: null } });
  });

  it('Undo brings back the probe and everything that went with it, and the draft is clean', () => {
    const d = useDraftStore();
    d.track(revision(42, named()));
    d.removeProbe({ id: A, name: 'probe-a' });
    d.restoreProbe(A);
    expect(d.dirty).toBe(false);
    expect(d.effectiveProbes[A]).toEqual(probeA);
    expect(d.effectiveRules[RULE]).toBeDefined();
    expect(d.effectiveGroups[GROUP]!.members).toContain(A);
  });

  it('Undo without the record (the draft was reapplied on another revision) goes back to the base', () => {
    const d = useDraftStore();
    d.track(revision(42, named()));
    d.removeProbe({ id: A, name: 'probe-a' });
    d.rebase(revision(43, named()));
    expect(d.probes[A]).toBeNull();
    d.restoreProbe(A);
    expect(d.dirty).toBe(false);
  });

  it('a removal of a device is not changed by it: the same Undo, the device and its objects', () => {
    const d = useDraftStore();
    const DEV = 'a1b2c3d4-0000-4000-8000-000000000042';
    d.track(
      revision(42, {
        ...named(),
        devices: { [DEV]: { name: 'esp', network: IOT } },
        access_rules: { [RULE]: { name: 'esp-no-dns', source: { device: DEV }, protocol: 'udp', ports: [53], action: 'drop' } },
      }),
    );
    d.removeDevice({ id: DEV, name: 'esp' });
    expect(d.patch().access_rules).toEqual({ [RULE]: null });
    expect(d.patch().devices).toEqual({ [DEV]: null });
    expect(d.patch().probes).toBeUndefined();
    d.restoreDevice(DEV);
    expect(d.dirty).toBe(false);
  });
});

describe('the probes meet another revision (M26a)', () => {
  it('a probe both changed is named, theirs → mine, with the networks by name; reapplied, the fields only they changed stay theirs', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(A, { ...probeA, network: TEST });
    // they moved it too, and it trusts the test CA now
    const theirs = config({ [A]: { ...probeA, network: IOT, name: 'probe-x', trusts_test_ca: true } });
    expect(d.overlaps(theirs, { network: names.network })).toEqual([]);
    const both = config({ [A]: { ...probeA, network: '5d5d5d5d-0000-4000-8000-000000000001', trusts_test_ca: true } });
    expect(d.overlaps(both, { network: names.network })).toEqual([{ kind: 'probe', id: A, name: 'probe-a', field: 'network', from: '5d5d5d5d-0000-4000-8000-000000000001', to: 'Test' }]);
    d.rebase(revision(43, theirs));
    expect(d.effectiveProbes[A]).toEqual({ name: 'probe-x', network: TEST, trusts_test_ca: true });
    expect(d.patch()).toEqual({ probes: { [A]: { network: TEST } } });
  });

  it('a probe they removed that I changed comes back as mine; one I removed that they removed too drops out', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProbe(A, { ...probeA, network: TEST });
    d.setProbe(NEW, { name: 'probe-b', network: IOT });
    d.rebase(revision(43, config({})));
    expect(d.effectiveProbes[A]).toEqual({ ...probeA, network: TEST });
    expect(d.effectiveProbes[NEW]).toEqual({ name: 'probe-b', network: IOT });
    d.setProbe(A, null);
    d.rebase(revision(44, config({})));
    expect(d.probes[A]).toBeUndefined();
  });
});
