import { describe, expect, it } from 'vitest';
import type { TrafficMatch } from '../api/generated/model';
import { GetScenarioResponse } from '../api/generated/zod/config/config';
import { emptyScenarioForm, formToScenario, scenarioFieldOf, scenarioFormErrors, scenarioToForm } from './scenarioForm';
import { parseParameters, runRequestCalls } from './runForm';
import { destinationOf, emptyTraffic, trafficErrors, trafficFieldOf, trafficFromMatch, trafficMatch } from './trafficForm';
import { ESP, mqttOutage } from './scenarioFixtures';

// The settings of a scenario, the parameters of a run and the selector of traffic (M19).

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';

describe('the settings of a scenario', () => {
  it('keep the steps and the checks, and the settings come back from the form', () => {
    const f = scenarioToForm(mqttOutage);
    expect(f).toEqual({ name: 'mqtt-outage', description: '', target: `device:${ESP}`, capture: true, zeroHit: 'warning', skip: [] });
    expect(formToScenario(f, mqttOutage)).toEqual(mqttOutage);
  });

  it('a default that was not there stays out; one that was stays', () => {
    const plain = { ...mqttOutage, capture: undefined };
    delete plain.capture;
    const f = scenarioToForm(plain);
    expect(formToScenario(f, plain)).toEqual(plain);
    const set = { ...plain, capture: false, zero_hit: 'warning' as const, preconditions: { skip: [] } };
    expect(formToScenario(scenarioToForm(set), set)).toEqual(set);
    f.zeroHit = 'error';
    f.skip = ['target_online'];
    f.description = '  the broker is cut  ';
    f.target = `network:${IOT}`;
    expect(formToScenario(f, plain)).toMatchObject({ zero_hit: 'error', preconditions: { skip: ['target_online'] }, description: 'the broker is cut', target: { network: IOT } });
  });

  it('a new scenario has no steps and is not valid until it has some', () => {
    const f = emptyScenarioForm(`device:${ESP}`);
    f.name = 'new-one';
    const sc = formToScenario(f);
    expect(sc).toEqual({ name: 'new-one', target: { device: ESP }, steps: [] });
    expect(GetScenarioResponse.shape.config.safeParse(sc).success).toBe(false);
  });

  it('the checks: the name (the pattern, unique), the target', () => {
    const f = emptyScenarioForm(`device:${ESP}`);
    expect(scenarioFormErrors(f, [])).toEqual({ name: 'Give the scenario a name.' });
    f.name = 'a b';
    expect(scenarioFormErrors(f, []).name).toMatch(/Letters, digits/);
    f.name = 'Taken';
    expect(scenarioFormErrors(f, ['taken'])).toEqual({ name: 'Another scenario has this name.' });
    f.name = 'fine';
    expect(scenarioFormErrors(f, [])).toEqual({});
    f.target = '';
    expect(scenarioFormErrors(f, []).target).toBeDefined();
    f.target = 'global';
    expect(scenarioFormErrors(f, []).target).toBeDefined();
    f.target = `device:${ESP}`;
    f.description = 'x'.repeat(501);
    expect(scenarioFormErrors(f, []).description).toBe('At most 500 characters.');
  });

  it("the gateway's messages find their field", () => {
    expect(scenarioFieldOf('name')).toBe('name');
    expect(scenarioFieldOf('target/device')).toBe('target');
    expect(scenarioFieldOf('zero_hit')).toBe('zeroHit');
    expect(scenarioFieldOf('preconditions/skip/0')).toBe('skip');
    expect(scenarioFieldOf('steps/0/at')).toBeUndefined();
  });
});

describe('the parameters of a run', () => {
  it('are path=value lines, empty lines ignored', () => {
    expect(parseParameters('')).toEqual({ parameters: {} });
    expect(parseParameters('target.device=esp32-42\n\n  steps.slow.fault.latency = 300ms \nsteps.0.at=5s\nkeep.eq=a=b')).toEqual({
      parameters: { 'target.device': 'esp32-42', 'steps.slow.fault.latency': '300ms', 'steps.0.at': '5s', 'keep.eq': 'a=b' },
    });
  });

  it('a line without a path, a path that is none, or one set twice is refused with its line', () => {
    expect(parseParameters('target.device').error).toBe('Line 1: write path=value, like target.device=esp32-42.');
    expect(parseParameters('=x').error).toMatch(/^Line 1/);
    expect(parseParameters('ok=1\nbad path=1').error).toBe('Line 2: bad path is no path: field names and step ids joined by dots.');
    expect(parseParameters('a..b=1').error).toMatch(/is no path/);
    expect(parseParameters('a=1\nb=2\na=3').error).toBe('Line 3: a is set twice.');
  });

  it('the request is the stored scenario run by its id, or the changed scenario inline; a busy target may refuse', () => {
    expect(runRequestCalls('c1', mqttOutage, false, {}, true)).toEqual([{ method: 'POST', path: '/scenarios/c1/runs' }]);
    expect(runRequestCalls('c1', mqttOutage, false, { parameters: { 'target.device': 'x' } }, false)).toEqual([
      { method: 'POST', path: '/scenarios/c1/runs?queue=false', body: { parameters: { 'target.device': 'x' } } },
    ]);
    const inline = runRequestCalls('c1', mqttOutage, true, { parameters: { a: '1' } }, true);
    expect(inline[0]).toMatchObject({ method: 'POST', path: '/runs', body: { inline: mqttOutage, parameters: { a: '1' } } });
  });
});

describe('the selector of traffic', () => {
  it('goes through the form and comes out the same', () => {
    const matches: TrafficMatch[] = [{}, { protocol: 'tcp' as const, ports: [80, 443], port_ranges: [{ from: 1000, to: 2000 }] }, { destination: { uplink: true } }, { destination: { network: IOT } }, { destination: { cidr: '203.0.113.0/24' } }, { destination: { hostname: '*.example.com' } }];
    for (const m of matches) {
      expect(trafficMatch(trafficFromMatch(m))).toEqual(m);
    }
    expect(destinationOf({ destKind: 'any', destination: 'x' })).toBeUndefined();
  });

  it('is checked under its fields', () => {
    const t = emptyTraffic();
    expect(trafficErrors(t)).toEqual({});
    t.ports = '70000';
    expect(trafficErrors(t).ports).toMatch(/no port/);
    t.ports = '80';
    expect(trafficErrors(t).ports).toBe('Ports need the protocol TCP or UDP.');
    t.protocol = 'udp';
    expect(trafficErrors(t)).toEqual({});
    t.destKind = 'network';
    expect(trafficErrors(t).destination).toBe('Choose a network.');
    t.destKind = 'hostname';
    t.destination = 'a b';
    expect(trafficErrors(t).destination).toMatch(/host name/);
  });

  it("finds the field of the gateway's message", () => {
    expect(trafficFieldOf('/ports/0')).toBe('ports');
    expect(trafficFieldOf('/port_ranges/0/to')).toBe('ports');
    expect(trafficFieldOf('/destination/cidr')).toBe('destination');
    expect(trafficFieldOf('/protocol')).toBe('ports');
    expect(trafficFieldOf('/name')).toBeUndefined();
  });
});
