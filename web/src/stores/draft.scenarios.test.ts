import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import type { Configuration, RevisionWithConfiguration, Scenario } from '../api/generated/model';
import { ESP, mqttOutage } from '../lib/scenarioFixtures';
import { newScenario, withCheck, withStep, withoutStep } from '../lib/scenarios';
import { useDraftStore } from './draft';

// The scenarios in the configuration draft (M19): their changes in words, their merge patch (the steps and
// the checks are lists, which a merge patch replaces whole), the problems that hold back Preview & apply, the
// gateway's messages about them, and the concurrent-change dialog's overlaps and reapply.

const SC = 'c5000000-0000-4000-8000-000000000001';
const NEW = 'c5000000-0000-4000-8000-0000000000ff';
const names = { device: (id: string) => ({ [ESP]: 'ESP32-42' })[id] };

function config(scenarios: Record<string, Scenario> = { [SC]: mqttOutage }): Configuration {
  return { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, scenarios };
}

function revision(id: number, configuration: Configuration = config()): RevisionWithConfiguration {
  return { id, status: 'active', schema_version: 1, created_at: '', created_by: { type: 'user', id: 'admin' }, configuration };
}

beforeEach(() => setActivePinia(createPinia()));

describe('the scenarios in the draft', () => {
  it('a new scenario is a change in words and the whole scenario in the patch; taken back it is none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(NEW, { ...mqttOutage, name: 'second' });
    expect(d.changes(names)).toEqual([{ kind: 'scenario', id: NEW, name: 'second', field: 'scenario', from: '—', to: 'added (5 steps · 1 min 15 s · 2 checks)' }]);
    expect(d.summary(names)).toBe('1 change in 1 scenario');
    expect(d.patch()).toEqual({ scenarios: { [NEW]: { ...mqttOutage, name: 'second' } } });
    expect(d.effectiveScenarios[NEW]?.name).toBe('second');
    d.setScenario(NEW, null);
    expect(d.dirty).toBe(false);
  });

  it('a step edited replaces the list of steps; one edit back to the base is no change', () => {
    const d = useDraftStore();
    d.track(revision(42));
    const slower = withStep(mqttOutage, { id: 'slow', at: '12s', fault: { latency: '300ms' } }, 'slow');
    d.setScenario(SC, slower);
    expect(d.changes(names)).toEqual([{ kind: 'scenario', id: SC, name: 'mqtt-outage', field: 'step slow', from: '10s ↑↓ 200 ms ±50', to: '12s ↑↓ 300 ms' }]);
    expect(d.patch()).toEqual({ scenarios: { [SC]: { steps: slower.steps } } });
    d.setScenario(SC, mqttOutage);
    expect(d.dirty).toBe(false);
    expect(d.patch()).toEqual({});
  });

  it('a check added is the whole list of checks in the patch', () => {
    const d = useDraftStore();
    d.track(revision(42));
    const more = withCheck(mqttOutage, { traffic_seen: {}, window: { from: 'start', within: '5s' } });
    d.setScenario(SC, more);
    expect(d.patch()).toEqual({ scenarios: { [SC]: { checks: more.checks } } });
    expect(d.changes(names)).toEqual([{ kind: 'scenario', id: SC, name: 'mqtt-outage', field: 'check 3', from: '—', to: 'Traffic seen any connection within 5 s of the start' }]);
  });

  it('deleted it is null; Undo brings it back', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, null);
    expect(d.patch()).toEqual({ scenarios: { [SC]: null } });
    expect(d.changes(names)).toEqual([{ kind: 'scenario', id: SC, name: 'mqtt-outage', field: 'scenario', from: 'present', to: 'deleted' }]);
    expect(d.effectiveScenarios[SC]).toBeUndefined();
    d.restoreScenario(SC);
    expect(d.dirty).toBe(false);
    expect(d.effectiveScenarios[SC]).toBeDefined();
  });

  it('is counted with the other objects of the bar', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(NEW, { ...mqttOutage, name: 'second' });
    d.setScenario(SC, null);
    expect(d.summary(names)).toBe('2 changes in 2 scenarios');
  });

  it('a scenario the gateway would refuse holds back the apply until it is fixed', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(NEW, newScenario('empty', { device: ESP }));
    expect(Object.keys(d.invalidScenarios)).toEqual([NEW]);
    expect(d.invalidScenarios[NEW]![0]!.path).toBe('/steps');
    d.setScenario(NEW, withStep(newScenario('empty', { device: ESP }), { id: 'a', at: '0s', restore: true }));
    expect(d.invalidScenarios).toEqual({});
    // a name another scenario has
    d.setScenario(NEW, withStep(newScenario('MQTT-outage', { device: ESP }), { id: 'a', at: '0s', restore: true }));
    expect(d.invalidScenarios[NEW]![0]!.message).toBe('Another scenario has this name.');
    // deleted: nothing to fix
    d.setScenario(NEW, null);
    expect(d.invalidScenarios).toEqual({});
  });

  it("the gateway's messages are kept by scenario with the pointer below it, and an edit of the scenario clears them", () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, withoutStep(mqttOutage, 'cut'));
    d.setServerErrors([
      { path: `/scenarios/${SC}/steps/2/at`, message: 'not a duration' },
      { path: `/scenarios/${SC}/checks/0/window/from`, message: 'there is no step restore' },
      { path: `/scenarios/${SC}`, message: 'the scenario is not valid' },
    ]);
    expect(d.serverErrors[SC]).toEqual({ 'steps/2/at': 'not a duration', 'checks/0/window/from': 'there is no step restore', scenario: 'the scenario is not valid' });
    d.setScenario(SC, mqttOutage);
    expect(d.serverErrors[SC]).toBeUndefined();
  });
});

describe('the scenarios of the draft against a revision applied meanwhile', () => {
  it('a field they changed and a list I changed both stay; both changed to different things is named', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, { ...mqttOutage, steps: mqttOutage.steps.filter((s) => s.id !== 'lossy') });
    const theirs: Configuration = config({ [SC]: { ...mqttOutage, description: 'theirs', steps: mqttOutage.steps.map((s) => (s.id === 'cut' ? { ...s, at: '31s' } : s)) } });
    expect(d.overlaps(theirs, names)).toEqual([{ kind: 'scenario', id: SC, name: 'mqtt-outage', field: 'steps', from: 'normal, slow, lossy, cut, restore', to: 'normal, slow, cut, restore' }]);
    d.rebase(revision(43, theirs));
    // the list of steps is mine as a whole (it is one value), the description is theirs
    expect(d.effectiveScenarios[SC]).toMatchObject({ description: 'theirs' });
    expect(d.effectiveScenarios[SC]!.steps.map((s) => s.id)).toEqual(['normal', 'slow', 'cut', 'restore']);
    expect(d.effectiveScenarios[SC]!.steps.find((s) => s.id === 'cut')!.at).toBe('30s');
  });

  it('edits to different members merge, and their change of the list stays when I changed none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, { ...mqttOutage, capture: false });
    const theirs: Configuration = config({ [SC]: { ...mqttOutage, steps: mqttOutage.steps.slice(0, 4) } });
    expect(d.overlaps(theirs, names)).toEqual([]);
    d.rebase(revision(43, theirs));
    expect(d.effectiveScenarios[SC]).toMatchObject({ capture: false });
    expect(d.effectiveScenarios[SC]!.steps).toHaveLength(4);
  });

  it('a scenario they deleted comes back as mine, and one I delete that is gone drops out', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, { ...mqttOutage, description: 'mine' });
    expect(d.overlaps(config({}), names)).toEqual([{ kind: 'scenario', id: SC, name: 'mqtt-outage', field: 'scenario', from: 'deleted', to: 'kept with my change' }]);
    d.rebase(revision(43, config({})));
    expect(d.effectiveScenarios[SC]).toEqual({ ...mqttOutage, description: 'mine' });
    const e = useDraftStore();
    e.discard();
    e.setScenario(SC, null);
    e.rebase(revision(44, config({})));
    expect(e.dirty).toBe(false);
  });

  it('a draft their revision already holds is none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setScenario(SC, { ...mqttOutage, description: 'same' });
    d.rebase(revision(43, config({ [SC]: { ...mqttOutage, description: 'same' } })));
    expect(d.dirty).toBe(false);
  });
});
