import { describe, expect, it } from 'vitest';
import type { Run, RunStep } from '../api/generated/model';
import { artifactLabels, elapsedMs, isActive, lastRunByScenario, lateness, offsetText, progressPercent, resultText, signedMs, sortedArtifacts, stepMarks, stepsDone } from './runs';
import { mqttOutage } from './scenarioFixtures';

// The run in the UI (M19): its progress, the step in effect, the timing of a step against its schedule,
// the artifacts and the last result of each scenario.

const T0 = Date.parse('2026-10-19T12:00:00Z');
const at = (ms: number) => new Date(T0 + ms);

function run(over: Partial<Run> = {}): Run {
  return {
    id: '0a1b2c3d-0000-4000-8000-000000000001',
    scenario: { id: 'c1', name: 'mqtt-outage', inline: false },
    revision: 42,
    target: { device: 'a1b2c3d4-0000-4000-8000-000000000042' },
    owner: { type: 'user', id: 'admin' },
    status: 'running',
    created_at: at(-1000).toISOString(),
    started_at: at(0).toISOString(),
    snapshot: mqttOutage,
    steps: mqttOutage.steps.map((s) => ({ id: s.id, status: 'pending', scheduled_offset: s.at })) as RunStep[],
    ...over,
  } as Run;
}

const applied = (id: string, offset_ms: number, scheduled: string): RunStep => ({ id, status: 'applied', scheduled_offset: scheduled, offset_ms, generation: 1 });

describe('the state of a run', () => {
  it('a queued or running run is active', () => {
    expect(isActive({ status: 'queued' })).toBe(true);
    expect(isActive({ status: 'running' })).toBe(true);
    for (const status of ['passed', 'failed', 'error', 'aborted'] as const) expect(isActive({ status })).toBe(false);
  });

  it('the time of a run is on the gateway clock, up to its end', () => {
    const r = run();
    expect(elapsedMs(r, at(12_500))).toBe(12_500);
    expect(elapsedMs({ started_at: r.started_at, ended_at: at(30_000).toISOString() }, at(90_000))).toBe(30_000);
    expect(elapsedMs({ started_at: undefined, ended_at: undefined }, at(1000))).toBe(0);
    // a clock that is behind the run's start does not make a negative time
    expect(elapsedMs(r, at(-5000))).toBe(0);
  });
});

describe('the progress of a run', () => {
  it('is the time against the length of the timeline, below 100 while it runs', () => {
    const r = run();
    expect(progressPercent(r, at(0))).toBe(0);
    expect(progressPercent(r, at(37_500))).toBe(50); // 75 s long
    expect(progressPercent(r, at(500_000))).toBe(99);
  });

  it('a queued run has none; a finished one is through, or as far as it got', () => {
    expect(progressPercent(run({ status: 'queued', started_at: undefined }), at(5000))).toBe(0);
    expect(progressPercent(run({ status: 'passed', ended_at: at(75_000).toISOString() }), at(80_000))).toBe(100);
    expect(progressPercent(run({ status: 'failed', ended_at: at(75_000).toISOString() }), at(80_000))).toBe(100);
    // an aborted run shows how far it got
    expect(progressPercent(run({ status: 'aborted', ended_at: at(30_000).toISOString() }), at(80_000))).toBe(40);
  });

  it('counts the steps that are through', () => {
    const steps = [applied('normal', 3, '0s'), { id: 'slow', status: 'skipped' }, { id: 'lossy', status: 'pending' }] as RunStep[];
    expect(stepsDone({ steps })).toEqual({ done: 2, total: 3 });
    expect(stepsDone({})).toEqual({ done: 0, total: 0 });
  });
});

describe('the step in effect and the next one', () => {
  const steps = [applied('normal', 3, '0s'), applied('slow', 10_004, '10s'), { id: 'lossy', status: 'pending', scheduled_offset: '20s' }, { id: 'cut', status: 'pending', scheduled_offset: '30s' }] as RunStep[];

  it('the last applied step is in effect and the first pending one is next, with the time left', () => {
    expect(stepMarks(run({ steps }), at(14_000))).toEqual({ current: 'slow', next: { id: 'lossy', due: false, inMs: 6000 } });
  });

  it('a step whose time has come is being applied (or a wait holds the timeline)', () => {
    expect(stepMarks(run({ steps }), at(21_000))).toEqual({ current: 'slow', next: { id: 'lossy', due: true, inMs: 0 } });
  });

  it('only a running run has marks', () => {
    expect(stepMarks(run({ steps, status: 'queued' }), at(14_000))).toEqual({});
    expect(stepMarks(run({ steps, status: 'passed' }), at(14_000))).toEqual({});
    expect(stepMarks(run({ steps: [{ id: 'a', status: 'pending', scheduled_offset: '1s' }] as RunStep[] }), at(0))).toEqual({ next: { id: 'a', due: false, inMs: 1000 } });
  });
});

describe('the timing of a step', () => {
  it('is how far its offset is from its schedule, unknown after a wait', () => {
    const slow = applied('slow', 10_040, '10s');
    expect(lateness(slow, [applied('normal', 3, '0s')])).toBe(40);
    expect(lateness(applied('x', 9_990, '10s'), [])).toBe(-10);
    expect(lateness(slow, [{ id: 'w', type: 'wait', status: 'applied' } as RunStep])).toBeUndefined();
    expect(lateness({ id: 'p', status: 'pending', scheduled_offset: '1s' }, [])).toBeUndefined();
    expect(signedMs(40)).toBe('+40 ms');
    expect(signedMs(-10)).toBe('−10 ms');
    expect(offsetText(3)).toBe('3 ms');
    expect(offsetText(10_040)).toBe('10.0 s');
    expect(offsetText(10_000)).toBe('10 s');
    expect(offsetText(undefined)).toBe('—');
  });
});

describe('artifacts and results', () => {
  it('artifacts are listed capture first, then the reports', () => {
    const list = sortedArtifacts([
      { kind: 'events', url: '/api/v1/runs/1/events' },
      { kind: 'report_json', url: '/api/v1/runs/1/report.json' },
      { kind: 'keylog', url: '/api/v1/captures/9/download?part=keylog' },
      { kind: 'report_xml', url: '/api/v1/runs/1/report.xml' },
      { kind: 'capture', url: '/api/v1/captures/9/download' },
    ]);
    expect(list.map((a) => a.kind)).toEqual(['capture', 'keylog', 'report_xml', 'report_json', 'events']);
    expect(sortedArtifacts(undefined)).toEqual([]);
    expect(artifactLabels.report_xml.note).toBe('JUnit');
  });

  it('the newest run of a scenario is its last result, by id or by the name of an inline one', () => {
    const a = run({ id: 'a', status: 'passed' });
    const b = run({ id: 'b', status: 'failed' });
    const inline = run({ id: 'c', scenario: { name: 'try-it', inline: true }, status: 'error' });
    const m = lastRunByScenario([b, a, inline]);
    expect(m.get('c1')?.id).toBe('b');
    expect(m.get('try-it')?.id).toBe('c');
    expect(resultText(run({ status: 'failed', checks: [{ index: 0, name: 'x', type: 'reconnected', status: 'failed' }, { index: 1, name: 'y', type: 'reconnected', status: 'passed' }] }))).toBe('Failed · 1 check');
    expect(resultText(run({ status: 'passed' }))).toBe('Passed');
  });
});
