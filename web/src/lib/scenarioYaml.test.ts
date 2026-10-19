import { describe, expect, it } from 'vitest';
import { reactive } from 'vue';
import type { Scenario } from '../api/generated/model';
import { mapRefs, parseScenarioYaml, scenarioToYaml, withIds, withNames, type RefIndex } from './scenarioYaml';
import { mqttOutage } from './scenarioFixtures';
import { readFileSync } from 'node:fs';

// The YAML view of a scenario (M19): the file of plan §2.10 in and out, with the names of the objects it
// refers to where the configuration has their UUIDs, and the problems it reports by line or by pointer.

// The file that the Go side decodes in `TestTheYamlOfTheScenarioUiIsAcceptedByTheGateway` (internal/domain);
// the tests run from web/.
const exported = readFileSync('../internal/domain/testdata/scenario-ui-export.yaml', 'utf8');

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const SENSORS = '5e0f0000-0000-4000-8000-000000000001';
const CUSTOM = 'c0000000-0000-4000-8000-000000000001';
const index: RefIndex = {
  devices: [
    { id: ESP, name: 'ESP32-42' },
    { id: SENSOR, name: 'Sensor-07' },
  ],
  groups: [{ id: SENSORS, name: 'sensors' }],
  networks: [{ id: IOT, name: 'IoT' }],
  profiles: [
    { id: 'normal', name: 'normal' },
    { id: CUSTOM, name: 'flaky-lte' },
  ],
};

const file = `name: mqtt-outage
target: { device: esp32-42 }          # a name, in any case
capture: true
steps:
  - { id: normal,  at: 0s,  profile: normal }
  - { id: slow,    at: 10s, fault: { latency: 200ms, jitter: 50ms } }
  - { id: lossy,   at: 20s, fault: { latency: 200ms, jitter: 50ms, loss: 10% } }
  - { id: cut,     at: 30s, rule: { action: drop, protocol: tcp, ports: [8883], cut_existing: true } }
  - { id: restore, at: 45s, restore: true }
checks:
  - { reconnected: { protocol: tcp, ports: [8883] }, window: { from: restore, within: 30s } }
  - { dns_query_seen: { name: broker.example.com }, window: { from: restore, within: 30s } }
`;

describe('the references of a scenario', () => {
  const sc: Scenario = {
    name: 'refs',
    target: { group: SENSORS },
    steps: [
      { id: 'a', at: '0s', target: { device: SENSOR }, profile: CUSTOM },
      { id: 'b', at: '1s', fault: { latency: '1s', destination: { network: IOT }, tunnel: { client: ESP } } },
      { id: 'c', at: '2s', rule: { action: 'drop', destination: { network: IOT } } },
      { id: 'd', at: '3s', wireguard: { link: IOT, action: 'disable' } },
      { id: 'e', at: '4s', wait: { for: 'connected', timeout: '5s', destination: { network: IOT } } },
    ],
    checks: [
      { reconnected: { destination: { network: IOT } }, window: { from: 'start', within: '1s' } },
      { traffic_not_seen: { except: [{ network: IOT }, { uplink: true }] }, window: { from: 'start', within: '1s' } },
    ],
  };

  it('are visited at the places the gateway visits', () => {
    const seen: string[] = [];
    mapRefs(sc, (kind, v, path) => {
      seen.push(`${path} ${kind}`);
      return v;
    });
    expect(seen).toEqual([
      '/target/group group',
      '/steps/0/target/device device',
      '/steps/0/profile profile',
      '/steps/1/fault/destination/network network',
      '/steps/1/fault/tunnel/client client',
      '/steps/2/rule/destination/network network',
      '/steps/3/wireguard/link link',
      '/steps/4/wait/destination/network network',
      '/checks/0/reconnected/destination/network network',
      '/checks/1/traffic_not_seen/except/0/network network',
    ]);
  });

  it('are shown as names and read back as UUIDs; the scenario itself is not changed', () => {
    const shown = withNames(sc, index);
    expect(shown.target).toEqual({ group: 'sensors' });
    expect(shown.steps[0]).toMatchObject({ target: { device: 'Sensor-07' }, profile: 'flaky-lte' });
    expect(shown.steps[1]!.fault!.destination).toEqual({ network: 'IoT' });
    expect(sc.target).toEqual({ group: SENSORS });
    const back = withIds(shown, index);
    expect(back.unknown).toEqual([]);
    expect(back.scenario).toEqual({ ...sc, steps: sc.steps.map((s) => (s.id === 'd' ? { ...s, wireguard: { link: IOT, action: 'disable' } } : s)) });
  });

  it('an object that is not known stays what it was, and an unknown name is reported at its pointer', () => {
    const shown = withNames({ ...sc, target: { device: 'a1b2c3d4-0000-4000-8000-0000000000ff' } }, index);
    expect(shown.target).toEqual({ device: 'a1b2c3d4-0000-4000-8000-0000000000ff' });
    const r = withIds({ name: 'x', target: { device: 'nobody' }, steps: [{ id: 'a', at: '0s', profile: 'nothing' }] }, index);
    expect(r.unknown).toEqual([
      { path: '/target/device', message: 'There is no device "nobody".' },
      { path: '/steps/0/profile', message: 'There is no profile "nothing".' },
    ]);
  });

  it('a name read back is found without regard to case', () => {
    expect((withIds({ target: { network: 'iot' } }, index).scenario as Scenario).target).toEqual({ network: IOT });
  });

  it('a name that two objects share is shown as the UUID', () => {
    const twice: RefIndex = { ...index, devices: [...index.devices, { id: 'a1b2c3d4-0000-4000-8000-0000000000aa', name: 'esp32-42' }] };
    expect(withNames({ ...sc, target: { device: ESP } }, twice).target).toEqual({ device: ESP });
  });
});

describe('the scenario as YAML', () => {
  it('has its members in the order of the spec, with the names of the objects', () => {
    const text = scenarioToYaml(mqttOutage, index);
    expect(text.startsWith('name: mqtt-outage\ntarget:\n  device: ESP32-42\ncapture: true\nsteps:\n  - id: normal\n    at: 0s\n    profile: normal\n')).toBe(true);
    // the check's name and type come before its window
    expect(text).toContain('  - reconnected:\n      protocol: tcp\n      ports:\n        - 8883\n    window:\n      from: restore\n      within: 30s\n');
    // a long line is not folded
    expect(scenarioToYaml({ ...mqttOutage, description: 'x'.repeat(200) }, index)).toContain(`description: ${'x'.repeat(200)}\n`);
  });

  it('is the file that the gateway decodes, byte for byte (the Go test of the same file keeps the two sides together)', () => {
    expect(scenarioToYaml(mqttOutage, index)).toBe(exported);
  });

  it("takes the draft's scenario, which is a reactive proxy", () => {
    const text = scenarioToYaml(reactive(structuredClone(mqttOutage)), index);
    expect(text).toContain('device: ESP32-42');
    expect(parseScenarioYaml(text, index).scenario).toEqual(mqttOutage);
  });

  it('reads back as the same scenario (and a scenario of the file reads as the same as the one of the forms)', () => {
    const r = parseScenarioYaml(scenarioToYaml(mqttOutage, index), index);
    expect(r.problems).toEqual([]);
    expect(r.scenario).toEqual(mqttOutage);
    const fromFile = parseScenarioYaml(file, index);
    expect(fromFile.problems).toEqual([]);
    expect(fromFile.scenario).toEqual(mqttOutage);
  });
});

describe('the problems of a YAML text', () => {
  const problems = (text: string, others: string[] = []) => parseScenarioYaml(text, index, others).problems;

  it('a syntax error has its line', () => {
    const p = problems('name: x\ntarget: { device: esp32-42\nsteps: []\n');
    expect(p.length).toBeGreaterThan(0);
    expect(p[0]!.line).toBeGreaterThan(0);
    expect(p[0]!.message).not.toMatch(/at line/);
    expect(problems('name: a\nname: b\n')[0]).toEqual({ message: 'Map keys must be unique', line: 2 });
  });

  it('the top is a mapping', () => {
    expect(problems('- a\n- b\n')).toEqual([{ message: 'A scenario is a mapping: name, target, steps, checks.' }]);
    expect(problems('')).toEqual([{ message: 'A scenario is a mapping: name, target, steps, checks.' }]);
  });

  it('an unknown name, a field the format does not have and a step without a type are named at their pointers', () => {
    const base = 'name: x\ntarget: { device: esp32-42 }\n';
    expect(problems(`${base}steps:\n  - { id: a, at: 0s, restore: true }\nchecks: []\nextra: 1\n`)).toEqual([{ path: '/extra', message: 'extra is not a field of the scenario format.' }]);
    expect(problems('name: x\ntarget: { device: nobody }\nsteps:\n  - { id: a, at: 0s, restore: true }\n')).toEqual([{ path: '/target/device', message: 'There is no device "nobody".' }]);
    expect(problems(`${base}steps:\n  - { id: a, at: 0s }\n`).map((x) => x.path)).toContain('/steps/0');
    expect(problems(`${base}steps:\n  - { id: a, at: 0s, restore: true, speed: 3 }\n`)).toEqual([{ path: '/steps/0/speed', message: 'speed is not a field of the scenario format.' }]);
  });

  it('the schema of the spec speaks for what the checks of the UI do not know', () => {
    const base = 'name: x\ntarget: { device: esp32-42 }\nsteps:\n';
    const p = problems(`${base}  - { id: a, at: 0s, fault: { latency: fast } }\n`);
    expect(p.map((x) => x.path)).toEqual(['/steps/0/fault/latency']);
    const q = problems(`${base}  - { id: a, at: 0s, fault: { loss: 200% } }\n`);
    expect(q.map((x) => x.path)).toEqual(['/steps/0/fault/loss']);
  });

  it('the rules of the scenario (a duplicate id, a name that is taken) are reported once, at the place', () => {
    const text = `${file}  - { id: normal, at: 50s, restore: true }\n`.replace('checks:\n', '').replace(/checks:[\s\S]*/, '');
    expect(parseScenarioYaml(file.replace('id: slow,', 'id: normal,'), index).problems.map((x) => x.path)).toContain('/steps/1/id');
    expect(text).toContain('normal');
    expect(problems(file, ['MQTT-OUTAGE'])).toEqual([{ path: '/name', message: 'Another scenario has this name.' }]);
  });
});
