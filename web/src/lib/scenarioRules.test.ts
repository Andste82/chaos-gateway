import { describe, expect, it } from 'vitest';
import type { Scenario, Scope, Step } from '../api/generated/model';
import { checkFormErrors, emptyCheckForm } from './checkForm';
import { createsOverlay, scopeKey, scopeWithin, startsCapture, tunnelOutsideTarget, type Topology } from './scenarioRules';
import { scenarioProblems } from './scenarios';
import { emptyStepForm, stepFormErrors, type StepForm } from './stepForm';

// The rules of `domain.ValidateScenario` (internal/domain/validate_scenario.go) that the UI sees before the draft
// is sent (P4-AUDIT-36). Each case below is a case of the Go tests (validate_scenario_test.go) with the pointer
// the gateway reports it at, so the two stay one rule.

const DEV = 'a1b2c3d4-0000-4000-8000-000000000042';
const OTHER_DEV = 'a1b2c3d4-0000-4000-8000-000000000043';
const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const LAB = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f22';
const GROUP = 'c1000000-0000-4000-8000-000000000001';
const HUB = 'b2000000-0000-4000-8000-000000000001';
const CLIENT = 'd3000000-0000-4000-8000-000000000001';
const LINK = 'b2000000-0000-4000-8000-000000000002';

const topology: Topology = {
  networkOf: (id) => ({ [DEV]: IOT, [OTHER_DEV]: LAB, [CLIENT]: HUB })[id],
  membersOf: (id) => (id === GROUP ? [DEV, OTHER_DEV] : undefined),
};

const base = (steps: Step[], target: Scope = { device: DEV }, checks?: Scenario['checks']): Scenario => ({ name: 'x', target, steps, ...(checks ? { checks } : {}) });
const at = (sc: Scenario, topo: Topology = topology) => scenarioProblems(sc, [], topo).map((p) => p.path);

describe('the target of a DHCP action (M23b)', () => {
  it('a device or a network; a forced new IP only a device; a step may narrow a group to a device', () => {
    const silence: Step = { id: 'a', at: '0s', dhcp: { action: 'silence' } };
    const force: Step = { id: 'a', at: '0s', dhcp: { action: 'force_new_ip' } };
    expect(at(base([silence]))).toEqual([]);
    expect(at(base([silence], { network: IOT }))).toEqual([]);
    expect(at(base([silence], { group: GROUP }))).toEqual(['/steps/0/dhcp']);
    expect(at(base([{ ...silence, target: { device: DEV } }], { group: GROUP }))).toEqual([]);
    expect(at(base([force]))).toEqual([]);
    expect(at(base([force], { network: IOT }))).toEqual(['/steps/0/dhcp']);
    expect(at(base([{ ...force, target: { device: DEV } }], { network: IOT }))).toEqual([]);
    expect(scenarioProblems(base([force], { network: IOT }), [], topology)[0]!.message).toMatch(/force_new_ip moves a device's reservation/);
    expect(scenarioProblems(base([silence], { group: GROUP }), [], topology)[0]!.message).toMatch(/not a group/);
  });
});

describe('the rules of the gateway in a scenario', () => {
  it('the step id start is reserved', () => {
    expect(at(base([{ id: 'start', at: '0s', restore: true }]))).toEqual(['/steps/0/id']);
    expect(at(base([{ id: 'begin', at: '0s', restore: true }]))).toEqual([]);
  });

  it('a step may narrow the target, never widen it', () => {
    const fault = { blackout: true };
    // the target is a device: only that device
    expect(at(base([{ id: 'a', at: '0s', target: { network: IOT }, fault }]))).toEqual(['/steps/0/target']);
    expect(at(base([{ id: 'a', at: '0s', target: { device: OTHER_DEV }, fault }]))).toEqual(['/steps/0/target']);
    expect(at(base([{ id: 'a', at: '0s', target: { device: DEV }, fault }]))).toEqual([]);
    // a group: its members, not other devices (as far as the configuration says)
    expect(at(base([{ id: 'a', at: '0s', target: { device: DEV }, fault }], { group: GROUP }))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', target: { device: 'ghost' }, fault }], { group: GROUP }))).toEqual(['/steps/0/target']);
    expect(at(base([{ id: 'a', at: '0s', target: { network: IOT }, fault }], { group: GROUP }))).toEqual(['/steps/0/target']);
    // a network: its devices, not those of another
    expect(at(base([{ id: 'a', at: '0s', target: { device: DEV }, fault }], { network: IOT }))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', target: { device: OTHER_DEV }, fault }], { network: IOT }))).toEqual(['/steps/0/target']);
    expect(at(base([{ id: 'a', at: '0s', target: { group: GROUP }, fault }], { network: IOT }))).toEqual(['/steps/0/target']);
    expect(at(base([{ id: 'a', at: '0s', target: { remote_network: { client: CLIENT } }, fault }], { network: HUB }))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', target: { remote_network: { cidr: '10.0.0.0/24' } }, fault }], { network: IOT }))).toEqual(['/steps/0/target']);
  });

  it('what the configuration does not tell is not a problem here: the gateway decides', () => {
    const fault = { blackout: true };
    expect(at(base([{ id: 'a', at: '0s', target: { device: OTHER_DEV }, fault }], { network: IOT }), {})).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', target: { device: OTHER_DEV }, fault }], { group: GROUP }), {})).toEqual([]);
    // what is certain without it still is a problem
    expect(at(base([{ id: 'a', at: '0s', target: { network: IOT }, fault }]), {})).toEqual(['/steps/0/target']);
  });

  it('a remove needs a step that creates an overlay, before it', () => {
    expect(at(base([{ id: 'a', at: '0s', capture: 'start' }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual(['/steps/1/remove']);
    expect(at(base([{ id: 'a', at: '0s', wait: { for: 'online', timeout: '10s' } }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual(['/steps/1/remove']);
    expect(at(base([{ id: 'a', at: '0s', restore: true }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual(['/steps/1/remove']);
    expect(at(base([{ id: 'a', at: '0s', fault: { blackout: true } }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'drop' } }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', profile: 'normal' }, { id: 'b', at: '1s', remove: 'a' }]))).toEqual([]);
    expect(at(base([{ id: 'a', at: '5s', remove: 'later' }, { id: 'later', at: '10s', fault: { blackout: true } }]))).toEqual(['/steps/0/remove']);
    expect(at(base([{ id: 'a', at: '0s', remove: 'a' }]))).toEqual(['/steps/0/remove']);
    expect(createsOverlay({ id: 'x', at: '0s', dns: { action: 'nxdomain' } })).toBe(true);
    expect(createsOverlay({ id: 'x', at: '0s', capture: 'stop' })).toBe(false);
  });

  it('a capture stop needs the start of a capture of the same target before it', () => {
    expect(at(base([{ id: 'a', at: '0s', capture: 'stop' }]))).toEqual(['/steps/0/capture']);
    expect(at(base([{ id: 'a', at: '0s', capture: 'stop' }, { id: 'b', at: '5s', capture: 'start' }]))).toEqual(['/steps/0/capture']);
    expect(at(base([{ id: 'a', at: '0s', capture: 'start' }, { id: 'b', at: '5s', capture: 'stop' }]))).toEqual([]);
    // the same time: the list order decides
    expect(at(base([{ id: 'a', at: '5s', capture: 'start' }, { id: 'b', at: '5s', capture: 'stop' }]))).toEqual([]);
    expect(at(base([{ id: 'b', at: '5s', capture: 'stop' }, { id: 'a', at: '5s', capture: 'start' }]))).toEqual(['/steps/0/capture']);
    // another target than the start
    expect(at(base([{ id: 'a', at: '0s', capture: 'start' }, { id: 'b', at: '5s', capture: 'stop', target: { device: DEV } }], { network: IOT }))).toEqual(['/steps/1/capture']);
    expect(at(base([{ id: 'a', at: '0s', capture: 'start', target: { device: DEV } }, { id: 'b', at: '5s', capture: 'stop', target: { device: DEV } }], { network: IOT }))).toEqual([]);
    expect(startsCapture([{ id: 'a', at: '0s', capture: 'start' }], { network: IOT }, { network: IOT.toUpperCase() })).toBe(true);
    expect(scopeKey({ remote_network: { client: 'A' } })).toBe(scopeKey({ remote_network: { client: 'a' } }));
  });

  it('a tunnel fault and a WireGuard action stay inside the target', () => {
    const tunnelFault = (tunnel: { client?: string; link?: string }): Step => ({ id: 'a', at: '0s', fault: { family: 'tunnel', tunnel, latency: '50ms' } });
    expect(at(base([tunnelFault({ link: LINK })]))).toEqual(['/steps/0/fault/tunnel']);
    expect(at(base([{ id: 'a', at: '0s', wireguard: { link: LINK, action: 'disable' } }]))).toEqual(['/steps/0/wireguard']);
    // a link: the target is that WireGuard network
    expect(at(base([{ id: 'a', at: '0s', wireguard: { link: LINK, action: 'disable' } }], { network: LINK }))).toEqual([]);
    // a client: the target is the client or its network
    expect(at(base([tunnelFault({ client: CLIENT })], { device: CLIENT }))).toEqual([]);
    expect(at(base([tunnelFault({ client: CLIENT })], { network: HUB }))).toEqual([]);
    expect(at(base([tunnelFault({ client: CLIENT })], { network: IOT }))).toEqual(['/steps/0/fault/tunnel']);
    expect(at(base([tunnelFault({ client: CLIENT })], { group: GROUP }))).toEqual(['/steps/0/fault/tunnel']);
    // the network of the client is not known: not a problem here
    expect(at(base([tunnelFault({ client: CLIENT })], { network: IOT }), {})).toEqual([]);
    expect(tunnelOutsideTarget(undefined, { device: DEV })).toBe(false);
  });

  it('a TCP reset needs the protocol tcp, not any', () => {
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'reset', protocol: 'udp' } }]))).toEqual(['/steps/0/rule/action']);
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'reset' } }]))).toEqual(['/steps/0/rule/action']);
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'reset', protocol: 'any' } }]))).toEqual(['/steps/0/rule/action']);
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'reset', protocol: 'tcp', ports: [8883] } }]))).toEqual([]);
    expect(at(base([{ id: 'a', at: '0s', rule: { action: 'drop', protocol: 'udp' } }]))).toEqual([]);
  });

  it('a wait timeout and a check window are longer than zero', () => {
    expect(at(base([{ id: 'a', at: '0s', wait: { for: 'online', timeout: '0s' } }]))).toEqual(['/steps/0/wait/timeout']);
    expect(at(base([{ id: 'a', at: '0s', wait: { for: 'online', timeout: '0ms' } }]))).toEqual(['/steps/0/wait/timeout']);
    expect(at(base([{ id: 'a', at: '0s', wait: { for: 'online', timeout: '1s' } }]))).toEqual([]);
    const step: Step = { id: 'a', at: '0s', fault: { blackout: true } };
    expect(at(base([step], { device: DEV }, [{ reconnected: {}, window: { from: 'a', within: '0s' } }]))).toEqual(['/checks/0/window/within']);
    expect(at(base([step], { device: DEV }, [{ reconnected: {}, window: { from: 'a', within: '5s' } }]))).toEqual([]);
  });

  it('a step with two types or none gets the one problem about that, not those about its types', () => {
    expect(at(base([{ id: 'a', at: '0s', restore: true, remove: 'a', rule: { action: 'reset' } } as Step]))).toEqual(['/steps/0']);
  });
});

describe('the same rules under the fields of the step form', () => {
  const form = (patch: Partial<StepForm>, type: StepForm['type'] = 'restore'): StepForm => ({ ...emptyStepForm(type, 'a'), ...patch });
  const ctx = (steps: Step[], earlier: string[], target: Scope = { device: DEV }) => ({ others: steps.map((s) => s.id).filter((i) => i !== 'a'), earlier, steps, target, topology });

  it('start is not an id', () => {
    expect(stepFormErrors(form({ id: 'start' }), ctx([], []))).toEqual({ id: 'start is reserved for the beginning of the run.' });
  });

  it('"Acts on" outside the target', () => {
    const f = (narrow: string) => form({ narrow }, 'restore');
    expect(stepFormErrors(f(`network:${IOT}`), ctx([], []))).toEqual({ narrow: "A step may narrow the scenario's target, never widen it." });
    expect(stepFormErrors(f(`device:${DEV}`), ctx([], []))).toEqual({});
    expect(stepFormErrors(f(`device:${DEV}`), ctx([], [], { network: IOT }))).toEqual({});
    expect(stepFormErrors(f(`device:${OTHER_DEV}`), ctx([], [], { network: IOT }))).toEqual({ narrow: "A step may narrow the scenario's target, never widen it." });
    // without the context of the scenario the rule is not checked here
    expect(stepFormErrors(f(`network:${IOT}`), { others: [], earlier: [] })).toEqual({});
  });

  it('a remove of a step that creates no overlay', () => {
    const steps: Step[] = [{ id: 'cap', at: '0s', capture: 'start' }, { id: 'cut', at: '1s', rule: { action: 'drop' } }];
    expect(stepFormErrors(form({ remove: 'cap' }, 'remove'), ctx(steps, ['cap', 'cut']))).toEqual({ remove: 'Step cap creates no overlay that could be removed.' });
    expect(stepFormErrors(form({ remove: 'cut' }, 'remove'), ctx(steps, ['cap', 'cut']))).toEqual({});
  });

  it('a capture stop without the start of the same target before it', () => {
    const start: Step = { id: 'cap', at: '0s', capture: 'start' };
    const stop = (narrow = '') => form({ capture: 'stop', narrow }, 'capture');
    expect(stepFormErrors(stop(), ctx([start], []))).toEqual({ capture: 'No step before this one starts a capture of the same target.' });
    expect(stepFormErrors(stop(), ctx([start], ['cap']))).toEqual({});
    expect(stepFormErrors(stop(`device:${DEV}`), ctx([start], ['cap'], { network: IOT }))).toEqual({ capture: 'No step before this one starts a capture of the same target.' });
    expect(stepFormErrors(form({ capture: 'start' }, 'capture'), ctx([], []))).toEqual({});
  });

  it('a tunnel outside the target', () => {
    const wg = (who: string) => form({ who }, 'wireguard');
    expect(stepFormErrors(wg(`link:${LINK}`), ctx([], []))).toEqual({ who: expect.stringContaining('the target must be that client') });
    expect(stepFormErrors(wg(`link:${LINK}`), ctx([], [], { network: LINK }))).toEqual({});
    expect(stepFormErrors(wg(`client:${CLIENT}`), ctx([], [], { network: HUB }))).toEqual({});
    const fault = form({}, 'fault');
    fault.fault = { ...fault.fault, family: 'tunnel', tunnel: `client:${CLIENT}` };
    expect(stepFormErrors(fault, ctx([], []))).toEqual({ tunnel: expect.stringContaining('the target must be that client') });
    expect(stepFormErrors(fault, ctx([], [], { network: HUB }))).toEqual({});
  });

  it('a reset of another protocol than TCP', () => {
    const rule = (action: StepForm['action'], protocol: StepForm['traffic']['protocol']) => form({ action, traffic: { ...emptyStepForm('rule', 'a').traffic, protocol } }, 'rule');
    expect(stepFormErrors(rule('reset', 'any'), ctx([], []))).toEqual({ action: 'A TCP reset needs the protocol TCP.' });
    expect(stepFormErrors(rule('reset', 'udp'), ctx([], []))).toEqual({ action: 'A TCP reset needs the protocol TCP.' });
    expect(stepFormErrors(rule('reset', 'tcp'), ctx([], []))).toEqual({});
    expect(stepFormErrors(rule('drop', 'udp'), ctx([], []))).toEqual({});
  });

  it('a wait that never times out', () => {
    expect(stepFormErrors(form({ timeout: '0s' }, 'wait'), ctx([], []))).toEqual({ timeout: 'The timeout must be longer than zero.' });
    expect(stepFormErrors(form({ timeout: '1m' }, 'wait'), ctx([], []))).toEqual({});
  });
});

describe('the same rule under the fields of the check form', () => {
  it('a window of zero length', () => {
    const f = { ...emptyCheckForm(), within: '0s' };
    expect(checkFormErrors(f, { steps: [] })).toEqual({ within: 'The window must be longer than zero.' });
    expect(checkFormErrors({ ...f, within: '30s' }, { steps: [] })).toEqual({});
  });
});

describe('the scope within a scope', () => {
  it('follows the gateway: a device is inside its group and its network, a group inside a network of all its members', () => {
    expect(scopeWithin({ device: DEV }, { group: GROUP }, topology)).toBe(true);
    expect(scopeWithin({ group: GROUP }, { network: IOT }, topology)).toBe(false);
    expect(scopeWithin({ group: GROUP }, { group: GROUP.toUpperCase() }, topology)).toBe(true);
    expect(scopeWithin({ network: IOT }, { device: DEV }, topology)).toBe(false);
    expect(scopeWithin({ device: DEV }, { global: true }, topology)).toBe(true);
  });
});
