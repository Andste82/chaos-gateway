import { describe, expect, it } from 'vitest';
import type { Run, RunStep } from '../api/generated/model';
import { applyMs, artifactFileName, artifactLabels, elapsedMs, isActive, isRunOf, lastRunByScenario, lateness, mergeRuns, offsetText, progressPercent, resultText, signedMs, sortedArtifacts, stepMarks, stepsDone, waitShiftMs } from './runs';
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

describe('the timing of a step: the hand-over against the apply (P4-AUDIT-18)', () => {
  const slowApply: RunStep = { id: 'slow', type: 'fault', status: 'applied', scheduled_offset: '10s', start_offset_ms: 10_003, offset_ms: 13_400, generation: 5 };

  it('the figure against the schedule is the hand-over, not the apply: a slow apply handed over on time is not late', () => {
    expect(lateness(slowApply, [])).toBe(3);
    expect(applyMs(slowApply)).toBe(3397);
    // a hand-over over 100 ms late shows as it is, whatever the apply took
    expect(lateness({ ...slowApply, start_offset_ms: 10_250, offset_ms: 10_300 }, [])).toBe(250);
    expect(applyMs({ ...slowApply, start_offset_ms: 10_250, offset_ms: 10_300 })).toBe(50);
    expect(lateness({ ...slowApply, start_offset_ms: 9_990 }, [])).toBe(-10);
  });

  it('a step without a hand-over moment falls back to its apply; the apply time is then unknown', () => {
    const old: RunStep = { id: 'x', status: 'applied', scheduled_offset: '10s', offset_ms: 10_040 };
    expect(lateness(old, [])).toBe(40);
    expect(applyMs(old)).toBeUndefined();
    expect(applyMs({ id: 'p', status: 'pending', start_offset_ms: 1, offset_ms: 2 })).toBeUndefined();
  });

  it('no figure for a wait (its offset is when the condition held) and none after one', () => {
    const wait: RunStep = { id: 'w', type: 'wait', status: 'applied', scheduled_offset: '10s', offset_ms: 25_000 };
    expect(lateness(wait, [])).toBeUndefined();
    expect(lateness({ ...slowApply, scheduled_offset: '30s', start_offset_ms: 40_001, offset_ms: 40_100 }, [wait])).toBeUndefined();
    // the apply time is still known after a wait
    expect(applyMs({ ...slowApply, start_offset_ms: 40_001, offset_ms: 40_100 })).toBe(99);
  });
});

describe('the next step after and at a wait (P4-AUDIT-42)', () => {
  const wait = (over: Partial<RunStep>): RunStep => ({ id: 'w', type: 'wait', status: 'pending', scheduled_offset: '30s', ...over });
  const after = (over: Partial<RunStep> = {}): RunStep => ({ id: 'cut', type: 'rule', status: 'pending', scheduled_offset: '40s', ...over });
  const first: RunStep = { id: 'a', type: 'profile', status: 'applied', scheduled_offset: '0s', offset_ms: 3 };

  it('a pending wait counts down to its time, then waits for its condition instead of "applying"', () => {
    const steps = [first, wait({}), after()];
    expect(stepMarks(run({ steps }), at(20_000)).next).toEqual({ id: 'w', due: false, inMs: 10_000 });
    expect(stepMarks(run({ steps }), at(35_000)).next).toEqual({ id: 'w', due: true, inMs: 0, waiting: true });
    expect(stepMarks(run({ steps }), at(90_000)).next).toEqual({ id: 'w', due: true, inMs: 0, waiting: true });
  });

  it('a step that is not a wait is "applying" when due, never "waiting"', () => {
    expect(stepMarks(run({ steps: [first, after({ scheduled_offset: '5s' })] }), at(9_000)).next).toEqual({ id: 'cut', due: true, inMs: 0 });
  });

  it('the steps after a wait that took 20 s are due 20 s later than their time', () => {
    const steps = [first, wait({ status: 'applied', offset_ms: 50_000 }), after()];
    // 55 s: the unshifted schedule would say "applying" since 40 s; the engine runs the step at 60 s
    expect(stepMarks(run({ steps }), at(55_000))).toEqual({ current: 'w', next: { id: 'cut', due: false, inMs: 5_000 } });
    expect(stepMarks(run({ steps }), at(61_000)).next).toEqual({ id: 'cut', due: true, inMs: 0 });
  });

  it('a wait that ended on time moves nothing, and the largest shift counts', () => {
    expect(waitShiftMs([first, wait({ status: 'applied', offset_ms: 30_000 })])).toBe(0);
    expect(waitShiftMs([first, wait({ status: 'applied', offset_ms: 29_000 })])).toBe(0);
    expect(waitShiftMs([wait({ status: 'applied', offset_ms: 50_000 }), wait({ id: 'w2', status: 'applied', scheduled_offset: '60s', offset_ms: 75_000 })])).toBe(20_000);
    expect(waitShiftMs([wait({})])).toBe(0);
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

  it('a downloaded artifact is saved under the name of its kind and the start of the run id', () => {
    const a = run({ id: '0a1b2c3d-0000-4000-8000-000000000001' });
    const b = run({ id: '9f8e7d6c-0000-4000-8000-000000000002' });
    expect(artifactFileName(a, { kind: 'events' })).toBe('run-0a1b2c3d-events.jsonl');
    expect(artifactFileName(a, { kind: 'report_json' })).toBe('run-0a1b2c3d-report.json');
    expect(artifactFileName(a, { kind: 'report_xml' })).toBe('run-0a1b2c3d-report.xml');
    expect(artifactFileName(a, { kind: 'capture' })).toBe('run-0a1b2c3d-capture.pcapng');
    expect(artifactFileName(a, { kind: 'keylog' })).toBe('run-0a1b2c3d-keylog.txt');
    expect(artifactFileName(b, { kind: 'events' })).not.toBe(artifactFileName(a, { kind: 'events' }));
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

describe('the runs of a scenario', () => {
  it('a run is of a scenario by its id, a run of an inline scenario by its name', () => {
    const s = { id: 'c1', name: 'mqtt-outage' };
    expect(isRunOf(run(), s)).toBe(true);
    expect(isRunOf(run({ scenario: { id: 'c2', name: 'mqtt-outage', inline: false } }), s)).toBe(false);
    expect(isRunOf(run({ scenario: { name: 'mqtt-outage', inline: true } }), s)).toBe(true);
    expect(isRunOf(run({ scenario: { name: 'other', inline: true } }), s)).toBe(false);
  });

  it('lists asked for apart are one list, newest first, each run once', () => {
    const a = run({ id: 'a', created_at: at(-3000).toISOString() });
    const b = run({ id: 'b', created_at: at(-1000).toISOString() });
    const c = run({ id: 'c', created_at: at(-2000).toISOString() });
    expect(mergeRuns([a, b], [c, b]).map((r) => r.id)).toEqual(['b', 'c', 'a']);
    expect(mergeRuns([], [])).toEqual([]);
  });
});
