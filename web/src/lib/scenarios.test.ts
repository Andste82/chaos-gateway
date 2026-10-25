import { describe, expect, it } from 'vitest';
import type { Scenario, Step } from '../api/generated/model';
import { GetScenarioResponse } from '../api/generated/zod/config/config';
import { ESP, mqttOutage } from './scenarioFixtures';
import {
  checkName,
  checkText,
  checkType,
  freeStepId,
  lengthText,
  newScenario,
  scenarioChanges,
  scenarioLength,
  scenarioMeta,
  scenarioProblems,
  stepReferences,
  stepText,
  stepType,
  stepsBefore,
  timelineOrder,
  windowText,
  withCheck,
  withStep,
  withoutCheck,
  withoutStep,
} from './scenarios';

// The scenario in the user's words and the checks the UI makes on it (M19): the example of plan §2.10, the
// problems the gateway would refuse (the same pointers), the edits of the timeline and the changes in words.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const names = { device: (id: string) => ({ [ESP]: 'ESP32-42' })[id], network: (id: string) => ({ [IOT]: 'IoT' })[id], profile: (r: string) => r };

const problems = (sc: Scenario, others: string[] = []) => scenarioProblems(sc, others).map((p) => `${p.path}`);

describe('step and check types', () => {
  it('a step has the one type it has members for', () => {
    expect(mqttOutage.steps.map(stepType)).toEqual(['profile', 'fault', 'fault', 'rule', 'restore']);
    expect(stepType({ id: 'x', at: '0s', capture: 'start' })).toBe('capture');
    expect(stepType({ id: 'x', at: '0s' })).toBeUndefined();
    expect(mqttOutage.checks!.map(checkType)).toEqual(['reconnected', 'dns_query_seen']);
  });

  it('the steps run by time, and in the list order at the same time', () => {
    const steps: Step[] = [
      { id: 'b', at: '10s', restore: true },
      { id: 'a', at: '0s', restore: true },
      { id: 'c', at: '10s', restore: true },
      { id: 'd', at: '1m', restore: true },
      { id: 'e', at: '500ms', restore: true },
    ];
    expect(timelineOrder(steps).map((s) => s.id)).toEqual(['a', 'e', 'b', 'c', 'd']);
  });
});

describe('the length of the timeline', () => {
  it('is the last step or the end of the last window', () => {
    expect(scenarioLength(mqttOutage)).toBe(75_000); // restore at 45 s + 30 s window
    expect(scenarioLength({ steps: [{ id: 'a', at: '5s', restore: true }] })).toBe(5000);
    // a window up to a step ends when that step is applied
    expect(
      scenarioLength({
        steps: [
          { id: 'a', at: '5s', restore: true },
          { id: 'b', at: '40s', restore: true },
        ],
        checks: [{ traffic_seen: {}, window: { from: 'start', until: 'b' } }],
      }),
    ).toBe(40_000);
  });

  it('is told in words', () => {
    expect(lengthText(0)).toBe('0 s');
    expect(lengthText(45_000)).toBe('45 s');
    expect(lengthText(150_000)).toBe('2 min 30 s');
    expect(lengthText(3_900_000)).toBe('1 h 5 min');
    expect(lengthText(250)).toBe('250 ms');
    expect(scenarioMeta(mqttOutage)).toBe('5 steps · 1 min 15 s · 2 checks');
    expect(scenarioMeta({ name: 'x', target: { device: ESP }, steps: [{ id: 'a', at: '0s', restore: true }] })).toBe('1 step · 0 s · 0 checks');
  });
});

describe('steps and checks in words', () => {
  it('says what each step does', () => {
    const t = (i: number) => stepText(mqttOutage.steps[i]!, names);
    expect(t(0)).toBe('Profile Normal');
    expect(t(1)).toBe('↑↓ 200 ms ±50');
    expect(t(2)).toBe('↑↓ 200 ms ±50, 10 % loss');
    expect(t(3)).toBe('Drop · cut existing → TCP 8883');
    expect(t(4)).toBe('Restore: remove all overlays of the run');
    expect(stepText({ id: 'a', at: '0s', fault: { latency: '1s', destination: { network: IOT } } }, names)).toContain('→ network IoT');
    expect(stepText({ id: 'a', at: '0s', remove: 'slow' }, names)).toBe('Remove the overlay of step slow');
    expect(stepText({ id: 'a', at: '0s', capture: 'start' }, names)).toBe('Start a capture of the target');
    expect(stepText({ id: 'a', at: '0s', wait: { for: 'online', timeout: '2m' } }, names)).toBe('Wait until the target is online · timeout 2 min');
    expect(stepText({ id: 'a', at: '0s', wait: { for: 'dns_query', name: 'broker.example.com', timeout: '30s' } }, names)).toBe('Wait for a DNS query for broker.example.com · timeout 30 s');
    expect(stepText({ id: 'a', at: '0s', wait: { for: 'connected', protocol: 'tcp', ports: [8883], timeout: '30s' } }, names)).toBe('Wait for a new connection → TCP 8883 · timeout 30 s');
    expect(stepText({ id: 'a', at: '0s', wireguard: { client: ESP, action: 'key_mismatch' } }, names)).toBe('WireGuard key mismatch · client ESP32-42');
    expect(stepText({ id: 'a', at: '0s', dns: { action: 'nxdomain', names: ['a.example.com'] } }, names)).toBe('DNS nxdomain · a.example.com');
    expect(stepText({ id: 'a', at: '0s', dhcp: { action: 'delete_lease' } }, names)).toBe('DHCP delete lease');
    // what the step carries: the delay, the addresses, the TTL, the ports and the cut, the lease time and the address of a new IP
    expect(stepText({ id: 'a', at: '0s', dns: { action: 'delay', delay: '2s' } }, names)).toBe('DNS delay 2s');
    expect(stepText({ id: 'a', at: '0s', dns: { action: 'wrong_answer', answers: ['10.9.0.1'], names: ['broker.example.com'] } }, names)).toBe('DNS answer 10.9.0.1 · broker.example.com');
    expect(stepText({ id: 'a', at: '0s', dns: { action: 'short_ttl', ttl: '0s' } }, names)).toBe('DNS TTL 0s');
    expect(stepText({ id: 'a', at: '0s', tls: { case: 'not_yet_valid', ports: [8883], cut_existing: true } }, names)).toBe('TLS not yet valid · 8883 · cuts existing');
    expect(stepText({ id: 'a', at: '0s', tls: { case: 'self_signed' } }, names)).toBe('TLS self signed');
    expect(stepText({ id: 'a', at: '0s', dhcp: { action: 'short_lease', lease_time: '7s' } }, names)).toBe('DHCP short lease 7s');
    expect(stepText({ id: 'a', at: '0s', dhcp: { action: 'force_new_ip', ip: '10.10.0.254' } }, names)).toBe('DHCP new IP 10.10.0.254');
  });

  it('says what each check observes and in which window', () => {
    expect(checkText(mqttOutage.checks![0]!, names)).toBe('TCP 8883');
    expect(checkText(mqttOutage.checks![1]!, names)).toBe('a query for broker.example.com');
    expect(checkText({ traffic_not_seen: { except: [{ network: IOT }, { uplink: true }] }, window: { from: 'start', within: '1m' } }, names)).toBe('any traffic except network IoT, uplink');
    expect(windowText({ from: 'restore', within: '30s' })).toBe('within 30 s of step restore');
    expect(windowText({ from: 'start', until: 'cut' })).toBe('from the start until step cut');
    expect(checkName(mqttOutage.checks![0]!, 0)).toBe('reconnected 1');
    expect(checkName({ name: 'mqtt-back', window: { from: 'start', within: '1s' } }, 3)).toBe('mqtt-back');
  });
});

describe('the problems of a scenario', () => {
  it('the example of the plan has none, and the spec takes it', () => {
    expect(scenarioProblems(mqttOutage)).toEqual([]);
    expect(GetScenarioResponse.shape.config.safeParse(mqttOutage).success).toBe(true);
  });

  it('the name: required, the pattern, not a UUID, unique case-insensitively', () => {
    expect(problems({ ...mqttOutage, name: '' })).toContain('/name');
    expect(problems({ ...mqttOutage, name: '-x' })).toContain('/name');
    expect(problems({ ...mqttOutage, name: IOT })).toContain('/name');
    expect(scenarioProblems(mqttOutage, ['MQTT-outage'])).toEqual([{ path: '/name', message: 'Another scenario has this name.' }]);
  });

  it('the target: required, a device, a group or a network', () => {
    expect(problems({ ...mqttOutage, target: {} })).toEqual(['/target']);
    expect(problems({ ...mqttOutage, target: { global: true } })).toEqual(['/target']);
    expect(problems({ ...mqttOutage, target: { remote_network: { cidr: '10.1.0.0/24' } } })).toEqual(['/target']);
    expect(problems({ ...mqttOutage, target: { network: IOT } })).toEqual([]);
  });

  it('the steps: at least one, ids that are free and well formed, a time, exactly one type', () => {
    expect(problems({ ...mqttOutage, steps: [], checks: [] })).toEqual(['/steps']);
    const steps = (s: Step[]): Scenario => ({ ...mqttOutage, checks: [], steps: s });
    expect(problems(steps([{ id: 'A', at: '0s', restore: true }]))).toEqual(['/steps/0/id']);
    expect(problems(steps([{ id: 'a', at: '0s', restore: true }, { id: 'a', at: '1s', restore: true }]))).toEqual(['/steps/1/id']);
    expect(problems(steps([{ id: 'a', at: '10', restore: true }]))).toEqual(['/steps/0/at']);
    expect(problems(steps([{ id: 'a', at: '0s' }]))).toEqual(['/steps/0']);
    expect(problems(steps([{ id: 'a', at: '0s', restore: true, capture: 'stop' }]))).toEqual(['/steps/0']);
  });

  it('a scenario from a text that is wrong in its types is a list of problems, not an error', () => {
    const wrong = (o: unknown) => scenarioProblems(o as Scenario).map((p) => p.path);
    expect(wrong({ name: 'x', target: { device: ESP }, steps: 5 })).toEqual(['/steps']);
    expect(wrong({ name: 'x', target: { device: ESP }, steps: ['text', null, { id: 'a', at: 10, restore: true }] })).toEqual(['/steps/0', '/steps/1', '/steps/2/at']);
    expect(wrong({ name: 7, target: 'x', steps: [{ id: 'a', at: '0s', restore: true }], checks: 'no' })).toEqual(['/name', '/target']);
    expect(wrong({ name: 'x', target: { device: ESP }, steps: [{ id: 'a', at: '0s', restore: true }], checks: ['x', { traffic_seen: {}, window: 'now' }, { traffic_seen: {}, window: { from: 4, within: 5 } }] })).toEqual([
      '/checks/0',
      '/checks/1/window',
      '/checks/2/window/from',
      '/checks/2/window/within',
    ]);
    expect(wrong(null)).toEqual(['/name', '/target', '/steps']);
  });

  it('a remove names a step that comes before it', () => {
    const steps = (s: Step[]): Scenario => ({ ...mqttOutage, checks: [], steps: s });
    const f: Step = { id: 'slow', at: '10s', fault: { latency: '1s' } };
    expect(problems(steps([f, { id: 'r', at: '20s', remove: 'slow' }]))).toEqual([]);
    expect(problems(steps([f, { id: 'r', at: '5s', remove: 'slow' }]))).toEqual(['/steps/1/remove']);
    expect(problems(steps([f, { id: 'r', at: '20s', remove: 'gone' }]))).toEqual(['/steps/1/remove']);
    expect(problems(steps([{ id: 'r', at: '20s', remove: 'r' }]))).toEqual(['/steps/0/remove']);
    // at the same time the list order decides
    expect(problems(steps([f, { id: 'r', at: '10s', remove: 'slow' }]))).toEqual([]);
    expect(problems(steps([{ id: 'r', at: '10s', remove: 'slow' }, f]))).toEqual(['/steps/0/remove']);
  });

  it('a check has one type and a window that names a step and ends once', () => {
    const checks = (c: Scenario['checks']): Scenario => ({ ...mqttOutage, checks: c });
    expect(problems(checks([{ window: { from: 'start', within: '1s' } }]))).toEqual(['/checks/0']);
    expect(problems(checks([{ traffic_seen: {}, reconnected: {}, window: { from: 'start', within: '1s' } }]))).toEqual(['/checks/0']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'nope', within: '1s' } }]))).toEqual(['/checks/0/window/from']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'start' } }]))).toEqual(['/checks/0/window']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'start', within: '1s', until: 'cut' } }]))).toEqual(['/checks/0/window']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'start', within: '1' } }]))).toEqual(['/checks/0/window/within']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'start', until: 'gone' } }]))).toEqual(['/checks/0/window/until']);
    // the window closes after it opens
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'cut', until: 'slow' } }]))).toEqual(['/checks/0/window/until']);
    expect(problems(checks([{ traffic_seen: {}, window: { from: 'slow', until: 'cut' } }]))).toEqual([]);
    expect(problems(checks([{ name: 'x'.repeat(101), traffic_seen: {}, window: { from: 'start', within: '1s' } }]))).toEqual(['/checks/0/name']);
  });
});

describe('editing the steps and the checks', () => {
  it('a new step goes to the end; a replaced one stays where it was', () => {
    const added = withStep(mqttOutage, { id: 'more', at: '50s', restore: true });
    expect(added.steps.map((s) => s.id)).toEqual(['normal', 'slow', 'lossy', 'cut', 'restore', 'more']);
    const replaced = withStep(mqttOutage, { id: 'slow', at: '12s', fault: { latency: '300ms' } }, 'slow');
    expect(replaced.steps[1]).toEqual({ id: 'slow', at: '12s', fault: { latency: '300ms' } });
    expect(replaced.steps).toHaveLength(5);
    expect(mqttOutage.steps).toHaveLength(5);
  });

  it('a step that changes its id takes the references to it along', () => {
    const sc: Scenario = {
      ...mqttOutage,
      steps: [...mqttOutage.steps, { id: 'rm', at: '46s', remove: 'cut' }],
      checks: [{ traffic_seen: {}, window: { from: 'cut', until: 'restore' } }, { traffic_seen: {}, window: { from: 'start', within: '1s' } }],
    };
    expect(stepReferences(sc, 'cut')).toEqual({ steps: ['rm'], checks: ['traffic_seen 1'] });
    const renamed = withStep(sc, { ...sc.steps[3]!, id: 'block' }, 'cut');
    expect(renamed.steps.find((s) => s.id === 'rm')?.remove).toBe('block');
    expect(renamed.checks![0]!.window).toEqual({ from: 'block', until: 'restore' });
    expect(renamed.checks![1]!.window).toEqual({ from: 'start', within: '1s' });
    expect(scenarioProblems(renamed)).toEqual([]);
    expect(withoutStep(sc, 'rm').steps.map((s) => s.id)).not.toContain('rm');
  });

  it('the ids of new steps are free ones', () => {
    expect(freeStepId({ steps: [] })).toBe('step-1');
    expect(freeStepId({ steps: [{ id: 'step-1', at: '0s', restore: true }, { id: 'step-2', at: '0s', restore: true }] })).toBe('step-3');
  });

  it('the steps before a step', () => {
    expect(stepsBefore(mqttOutage, '30s', 'cut')).toEqual(['normal', 'slow', 'lossy']);
    expect(stepsBefore(mqttOutage, '30s')).toEqual(['normal', 'slow', 'lossy', 'cut']);
    expect(stepsBefore(mqttOutage, '0s')).toEqual(['normal']);
    expect(stepsBefore(mqttOutage, '0s', 'normal')).toEqual([]);
  });

  it('checks are added, replaced and deleted; the last one takes the list with it', () => {
    const c = { traffic_seen: {}, window: { from: 'start', within: '5s' } };
    expect(withCheck(mqttOutage, c).checks).toHaveLength(3);
    expect(withCheck(mqttOutage, c, 0).checks![0]).toEqual(c);
    expect(withoutCheck(mqttOutage, 0).checks).toHaveLength(1);
    expect(withoutCheck(withoutCheck(mqttOutage, 0), 0).checks).toBeUndefined();
    expect(newScenario('x', { device: ESP })).toEqual({ name: 'x', target: { device: ESP }, steps: [] });
  });
});

describe('what changed', () => {
  it('a scenario added or deleted', () => {
    expect(scenarioChanges(undefined, mqttOutage, names)).toEqual([{ field: 'scenario', from: '—', to: 'added (5 steps · 1 min 15 s · 2 checks)' }]);
    expect(scenarioChanges(mqttOutage, null, names)).toEqual([{ field: 'scenario', from: 'present', to: 'deleted' }]);
  });

  it('the settings, then the steps by id, then the checks by position', () => {
    const after: Scenario = {
      ...mqttOutage,
      name: 'mqtt-down',
      capture: false,
      zero_hit: 'error',
      steps: [
        ...mqttOutage.steps.filter((s) => s.id !== 'lossy').map((s) => (s.id === 'slow' ? { ...s, at: '12s' } : s)),
        { id: 'late', at: '60s', capture: 'stop' },
      ],
      checks: [mqttOutage.checks![0]!],
    };
    expect(scenarioChanges(mqttOutage, after, names)).toEqual([
      { field: 'name', from: 'mqtt-outage', to: 'mqtt-down' },
      { field: 'capture', from: 'yes', to: 'no' },
      { field: 'zero hit', from: 'warning', to: 'error' },
      { field: 'step slow', from: '10s ↑↓ 200 ms ±50', to: '12s ↑↓ 200 ms ±50' },
      { field: 'step late', from: '—', to: 'added: 60s Stop the captures of the target' },
      { field: 'step lossy', from: '20s ↑↓ 200 ms ±50, 10 % loss', to: 'deleted' },
      { field: 'check 2', from: 'DNS query seen a query for broker.example.com within 30 s of step restore', to: 'deleted' },
    ]);
    expect(scenarioChanges(mqttOutage, mqttOutage, names)).toEqual([]);
  });

  it('a change the words do not show is named as a change of settings', () => {
    const after: Scenario = { ...mqttOutage, steps: mqttOutage.steps.map((s) => (s.id === 'cut' ? { ...s, rule: { ...s.rule!, reject_with: 'no_route' as const } } : s)) };
    // the line of the rule is the same (the action is still Drop)
    expect(scenarioChanges(mqttOutage, after, names)).toEqual([{ field: 'step cut', from: 'as before', to: 'settings changed' }]);
    expect(scenarioChanges(mqttOutage, { ...mqttOutage, preconditions: { skip: ['target_online'] } }, names)).toEqual([
      { field: 'skipped preconditions', from: '—', to: 'target_online' },
    ]);
  });
});
