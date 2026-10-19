import { describe, expect, it } from 'vitest';
import type { Scenario, Step } from '../api/generated/model';
import { GetScenarioResponse } from '../api/generated/zod/config/config';
import { editableTypes, emptyStepForm, formToStep, stepFieldOf, stepFormErrors, stepToForm, type StepForm } from './stepForm';
import { ESP, mqttOutage } from './scenarioFixtures';

// The step form (M19): every step of the example of plan §2.10 goes through the form and comes out the
// same, each type has its checks under its fields, and the step it builds is one the spec accepts.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const ctx = { others: [] as string[], earlier: [] as string[] };
const spec = (step: Step): boolean => GetScenarioResponse.shape.config.safeParse({ name: 'x', target: { device: ESP }, steps: [step] }).success;

describe('the step form', () => {
  it('gives back every step of the example as it was', () => {
    for (const s of mqttOutage.steps) expect(formToStep(stepToForm(s))).toEqual(s);
  });

  it('gives back the other types too, and the step the editor does not make as it was', () => {
    const steps: Step[] = [
      { id: 'a', at: '1s', target: { device: ESP }, capture: 'start' },
      { id: 'b', at: '2s', wait: { for: 'online', timeout: '2m' } },
      { id: 'c', at: '3s', wait: { for: 'connected', timeout: '30s', protocol: 'tcp', ports: [8883], destination: { network: IOT } } },
      { id: 'd', at: '4s', wait: { for: 'dns_query', name: 'broker.example.com', timeout: '30s' } },
      { id: 'e', at: '5s', remove: 'a' },
      { id: 'f', at: '6s', rule: { action: 'reject', reject_with: 'host_unreachable', protocol: 'udp', ports: [53], destination: { uplink: true } } },
      { id: 'g', at: '7s', wireguard: { link: IOT, action: 'block_endpoint' } },
      { id: 'h', at: '8s', fault: { family: 'mtu', mtu: { size: 1280, mode: 'blackhole' }, protocol: 'tcp' } },
      { id: 'i', at: '9s', fault: { family: 'tunnel', tunnel: { client: ESP }, latency: '300ms' } },
      { id: 'j', at: '10s', dns: { action: 'nxdomain' } },
      { id: 'k', at: '11s', dhcp: { action: 'silence' } },
    ];
    for (const s of steps) {
      expect(formToStep(stepToForm(s))).toEqual(s);
      expect(spec(s)).toBe(true);
    }
    expect(stepToForm(steps[9]!).kept).toEqual(steps[9]);
    expect(editableTypes.has('dns')).toBe(false);
  });

  it('a step that is edited keeps what the form leaves out; a tunnel step and a WireGuard action never narrow', () => {
    const f = stepToForm({ id: 'j', at: '10s', dns: { action: 'nxdomain', names: ['a.example.com'] } });
    f.at = '12s';
    expect(formToStep(f)).toEqual({ id: 'j', at: '12s', dns: { action: 'nxdomain', names: ['a.example.com'] } });
    const w = stepToForm({ id: 'w', at: '1s', wireguard: { client: ESP, action: 'disable' } });
    w.narrow = `device:${ESP}`;
    expect(formToStep(w).target).toBeUndefined();
    const t = stepToForm({ id: 't', at: '1s', fault: { family: 'tunnel', tunnel: { client: ESP }, latency: '1s' } });
    t.narrow = `device:${ESP}`;
    expect(formToStep(t).target).toBeUndefined();
    const f2 = emptyStepForm('fault', 'x');
    f2.narrow = `device:${ESP}`;
    expect(formToStep(f2).target).toEqual({ device: ESP });
  });

  it('a new step of each type is a step the spec accepts once its fields are filled', () => {
    const fill = (type: Parameters<typeof emptyStepForm>[0], set: (f: StepForm) => void): Step => {
      const f = emptyStepForm(type, 'x');
      set(f);
      expect(stepFormErrors(f, { others: [], earlier: ['a'] })).toEqual({});
      return formToStep(f);
    };
    const steps = [
      fill('profile', (f) => (f.profile = 'lte')),
      fill('fault', () => undefined),
      fill('rule', (f) => (f.action = 'drop')),
      fill('wireguard', (f) => (f.who = `client:${ESP}`)),
      fill('capture', () => undefined),
      fill('wait', () => undefined),
      fill('remove', (f) => (f.remove = 'a')),
      fill('restore', () => undefined),
    ];
    for (const s of steps) expect(spec(s)).toBe(true);
    expect(steps[1]).toEqual({ id: 'x', at: '0s', fault: { latency: '200ms', jitter: '50ms' } });
    expect(steps[2]).toEqual({ id: 'x', at: '0s', rule: { action: 'drop' } });
    expect(steps[5]).toEqual({ id: 'x', at: '0s', wait: { for: 'online', timeout: '60s' } });
  });
});

describe('the checks under the fields', () => {
  const e = (f: StepForm, c = ctx) => stepFormErrors(f, c);

  it('the id and the time', () => {
    const f = emptyStepForm('restore', 'ok');
    expect(e(f)).toEqual({});
    f.id = '';
    expect(e(f).id).toBe('Give the step an id.');
    f.id = 'Bad Id';
    expect(e(f).id).toMatch(/lower-case letter/);
    f.id = 'taken';
    expect(e(f, { others: ['taken'], earlier: [] }).id).toBe('Another step has this id.');
    f.id = 'ok';
    f.at = '10';
    expect(e(f).at).toMatch(/like 0s/);
    f.at = '1m30s';
    expect(e(f)).toEqual({});
  });

  it('a profile is chosen; a wire guard action names its client or link', () => {
    expect(e(emptyStepForm('profile', 'a')).profile).toBe('Choose a profile.');
    expect(e(emptyStepForm('wireguard', 'a')).who).toBe('Choose the WireGuard client or link.');
  });

  it('a fault has the checks of the fault dialog, without its target and its name', () => {
    const f = emptyStepForm('fault', 'a');
    f.fault.latency = '';
    f.fault.jitter = '';
    expect(Object.keys(e(f))).toEqual(['latency']);
    f.fault.latency = 'fast';
    expect(e(f).latency).toBeDefined();
    const tunnel = emptyStepForm('fault', 'a');
    tunnel.fault.family = 'tunnel';
    expect(e(tunnel).tunnel).toBeDefined();
    const mtu = emptyStepForm('fault', 'a');
    mtu.fault.family = 'mtu';
    mtu.fault.mtuSize = '100';
    expect(e(mtu).mtuSize).toBeDefined();
  });

  it('a rule cuts only TCP connections and none with allow; its traffic is checked', () => {
    const f = emptyStepForm('rule', 'a');
    f.cutExisting = true;
    expect(e(f)).toEqual({});
    f.action = 'allow';
    expect(e(f).cutExisting).toBe('An allow rule cuts nothing.');
    f.action = 'drop';
    f.traffic.protocol = 'udp';
    expect(e(f).cutExisting).toMatch(/TCP or any/);
    f.cutExisting = false;
    f.traffic.protocol = 'any';
    f.traffic.ports = '53';
    expect(e(f).ports).toBe('Ports need the protocol TCP or UDP.');
    f.traffic.ports = '';
    f.traffic.destKind = 'cidr';
    f.traffic.destination = 'nope';
    expect(e(f).destination).toBeDefined();
  });

  it('a wait has a timeout and what it waits for', () => {
    const f = emptyStepForm('wait', 'a');
    f.timeout = 'soon';
    expect(e(f).timeout).toBeDefined();
    f.timeout = '30s';
    f.waitFor = 'dns_query';
    expect(e(f).name).toBe('A host name like broker.example.com.');
    f.name = 'broker.example.com';
    expect(e(f)).toEqual({});
    f.waitFor = 'connected';
    f.traffic.destKind = 'hostname';
    f.traffic.destination = 'not a name';
    expect(e(f).destination).toBeDefined();
  });

  it('a remove names a step that comes before', () => {
    const f = emptyStepForm('remove', 'a');
    expect(e(f).remove).toBe('Choose the step whose overlay is removed.');
    f.remove = 'later';
    expect(e(f, { others: [], earlier: ['first'] }).remove).toBe('Choose a step that comes before this one.');
    f.remove = 'first';
    expect(e(f, { others: [], earlier: ['first'] })).toEqual({});
  });

  it('a narrowing to a remote network needs its prefix', () => {
    const f = emptyStepForm('capture', 'a');
    f.narrow = 'remote_network:cidr:';
    expect(e(f).narrow).toBeDefined();
  });
});

describe('the gateway\'s messages', () => {
  it('are put under the field their pointer names', () => {
    expect(stepFieldOf('at')).toBe('at');
    expect(stepFieldOf('id')).toBe('id');
    expect(stepFieldOf('target/device')).toBe('narrow');
    expect(stepFieldOf('profile')).toBe('profile');
    expect(stepFieldOf('fault/jitter')).toBe('jitter');
    expect(stepFieldOf('fault/upload/latency', true)).toBe('latency');
    expect(stepFieldOf('fault/download/latency', true)).toBe('download.latency');
    expect(stepFieldOf('fault/mtu/size')).toBe('mtuSize');
    expect(stepFieldOf('fault/tunnel/client')).toBe('tunnel');
    expect(stepFieldOf('rule/ports')).toBe('ports');
    expect(stepFieldOf('rule/ports/0')).toBe('ports');
    expect(stepFieldOf('rule/destination/cidr')).toBe('destination');
    expect(stepFieldOf('rule/cut_existing')).toBe('cutExisting');
    expect(stepFieldOf('rule/reject_with')).toBe('rejectWith');
    expect(stepFieldOf('rule/action')).toBe('action');
    expect(stepFieldOf('wireguard/client')).toBe('who');
    expect(stepFieldOf('wireguard/action')).toBe('wgAction');
    expect(stepFieldOf('wait/timeout')).toBe('timeout');
    expect(stepFieldOf('wait/name')).toBe('name');
    expect(stepFieldOf('wait/destination/network')).toBe('destination');
    expect(stepFieldOf('remove')).toBe('remove');
    expect(stepFieldOf('capture')).toBe('capture');
    expect(stepFieldOf('')).toBeUndefined();
  });
});

// a scenario made of the example's steps, to be sure the forms leave it valid
describe('the example through the forms', () => {
  it('is the same scenario', () => {
    const sc: Scenario = { ...mqttOutage, steps: mqttOutage.steps.map((s) => formToStep(stepToForm(s))) };
    expect(sc).toEqual(mqttOutage);
  });
});
