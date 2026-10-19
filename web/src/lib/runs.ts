// Runs in the UI (plan §2.10, §2.17 "Run states"; M19): the states of a run in words and tones, its progress
// (the steps that are through, the current and the next one, the time against the length of the timeline), the
// timing of a step against its schedule, the results of the checks, the artifacts, and the run of each
// scenario that the list shows as its last result.
import type { Artifact, CheckResult, Run, RunStatus, RunStep } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { durationMs } from './format';
import { scenarioLength } from './scenarios';

export const statusLabels: Record<RunStatus, string> = {
  queued: 'Queued',
  running: 'Running',
  passed: 'Passed',
  failed: 'Failed',
  error: 'Error',
  aborted: 'Aborted',
};

/** What each state means, for the tooltip and the screen reader (plan §2.17 "Run states"). */
export const statusHints: Record<RunStatus, string> = {
  queued: 'Waits for the run on the same target to end.',
  running: 'The timeline is executing.',
  passed: 'Every check passed.',
  failed: 'A check failed.',
  error: 'A step or a precondition could not be executed.',
  aborted: 'Stopped; the overlays of the run were removed.',
};

/** Blue for what is as configured or good, orange while traffic is impaired, red for a failure. */
export const statusTone: Record<RunStatus, DotTone> = { queued: 'unknown', running: 'fault', passed: 'normal', failed: 'block', error: 'block', aborted: 'offline' };

/** The run is queued or running: it has a progress, can be aborted, and its page is polled. */
export function isActive(r: Pick<Run, 'status'>): boolean {
  return r.status === 'queued' || r.status === 'running';
}

/** The tone and text of a check's result; a check that is not decided yet is "pending". */
export const checkStatusText: Record<CheckResult['status'], string> = { pending: 'PENDING', passed: 'PASSED', failed: 'FAILED', error: 'ERROR' };

/** How long a run has been going (or went) in milliseconds, on the gateway's clock; 0 before it started. */
export function elapsedMs(r: Pick<Run, 'started_at' | 'ended_at'>, now: Date): number {
  if (!r.started_at) return 0;
  const end = r.ended_at ? new Date(r.ended_at).getTime() : now.getTime();
  return Math.max(0, end - new Date(r.started_at).getTime());
}

/** The steps of a run that are through (applied, skipped or failed), of all. */
export function stepsDone(r: Pick<Run, 'steps'>): { done: number; total: number } {
  const steps = r.steps ?? [];
  return { done: steps.filter((s) => s.status !== 'pending').length, total: steps.length };
}

/**
 * The progress of a run in percent. A finished run is through; a running one is the time against the
 * length of the timeline (a wait or a check window that reaches past the last step makes it longer than
 * the time says, so it stays below 100 until the run ends); a queued run has none yet.
 */
export function progressPercent(r: Run, now: Date): number {
  if (r.status === 'queued') return 0;
  if (!isActive(r)) return r.status === 'passed' || r.status === 'failed' ? 100 : Math.round(fraction(r, now) * 100);
  return Math.min(99, Math.round(fraction(r, now) * 100));
}

function fraction(r: Run, now: Date): number {
  const length = r.snapshot ? scenarioLength(r.snapshot) : 0;
  const { done, total } = stepsDone(r);
  const bySteps = total ? done / total : 0;
  if (!length) return bySteps;
  return Math.min(1, Math.max(elapsedMs(r, now) / length, 0));
}

export interface StepMarks {
  /** The step in effect: the last one that was applied. */
  current?: string;
  /** The first one that is not through; `due` once its time has come (a wait that holds the timeline, or a step being applied). */
  next?: { id: string; due: boolean; inMs: number };
}

/** Which step is current and which comes next while the run runs. */
export function stepMarks(r: Run, now: Date): StepMarks {
  if (r.status !== 'running') return {};
  const steps = r.steps ?? [];
  const applied = steps.filter((s) => s.status === 'applied');
  const pending = steps.find((s) => s.status === 'pending');
  const elapsed = elapsedMs(r, now);
  const due = pending ? (durationMs(pending.scheduled_offset) ?? 0) : 0;
  return {
    ...(applied.length ? { current: applied[applied.length - 1]!.id } : {}),
    ...(pending ? { next: { id: pending.id, due: due <= elapsed, inMs: Math.max(0, due - elapsed) } } : {}),
  };
}

/**
 * How late a step was against its schedule in milliseconds (negative: early). Unknown after a `wait` step,
 * which moves every later step by the time it waited.
 */
export function lateness(step: RunStep, earlier: RunStep[]): number | undefined {
  if (step.offset_ms === undefined || step.scheduled_offset === undefined || step.status !== 'applied') return undefined;
  if (earlier.some((s) => s.type === 'wait')) return undefined;
  return Math.round(step.offset_ms - (durationMs(step.scheduled_offset) ?? 0));
}

/** "+12 ms", "−3 ms". */
export function signedMs(ms: number): string {
  return `${ms < 0 ? '−' : '+'}${Math.abs(ms)} ms`;
}

/** The timing of a step as the run view shows it: "+3 ms after 10s", or the offset the wait ended at. */
export function offsetText(ms: number | undefined): string {
  if (ms === undefined) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms % 1000 === 0 ? 0 : 1)} s`;
}

export const artifactLabels: Record<Artifact['kind'], { title: string; file: string; note?: string }> = {
  capture: { title: 'Capture', file: 'capture.pcapng' },
  report_xml: { title: 'report.xml', file: 'report.xml', note: 'JUnit' },
  report_json: { title: 'report.json', file: 'report.json' },
  events: { title: 'events.jsonl', file: 'events.jsonl' },
  keylog: { title: 'TLS key log', file: 'keylog.txt', note: 'decrypts the capture' },
};

/** The artifacts of a run in the order of the prototype: capture, JUnit, JSON, events (the key log with the capture). */
export function sortedArtifacts(list: Artifact[] | undefined): Artifact[] {
  const rank: Record<Artifact['kind'], number> = { capture: 0, keylog: 1, report_xml: 2, report_json: 3, events: 4 };
  return [...(list ?? [])].sort((a, b) => rank[a.kind] - rank[b.kind]);
}

/** The newest run of each scenario, by the scenario's id or, for a run of an inline scenario, its name. */
export function lastRunByScenario(runs: Run[]): Map<string, Run> {
  const out = new Map<string, Run>();
  // the list is newest first; the first run seen of a scenario is its last
  for (const r of runs) {
    for (const key of [r.scenario.id, r.scenario.name].filter((k): k is string => !!k)) if (!out.has(key)) out.set(key, r);
  }
  return out;
}

/** A run's one-line result for a list: "Passed · 2 min ago" is built by the caller from this and the time. */
export function resultText(r: Run): string {
  if (r.status === 'failed') {
    const failed = (r.checks ?? []).filter((c) => c.status === 'failed').length;
    return failed ? `Failed · ${failed} ${failed === 1 ? 'check' : 'checks'}` : 'Failed';
  }
  return statusLabels[r.status];
}
