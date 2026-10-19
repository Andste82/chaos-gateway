// Runs of a scenario from a file: with the generated client, and with chaosctl, which is the shorter way in a CI job.
import { mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parse } from 'yaml';
import { createRun, getRun, listOverlays } from '../../typescript/src';
import type { Overlay, Run, Scenario } from '../../typescript/src/model';
import { chaosctl, dut, ok, sleep } from './helpers';

const scenarioFile = join(__dirname, '..', 'scenarios', 'link-outage.yaml');
const ended = ['passed', 'failed', 'error', 'aborted'];

test('a scenario from a file runs to the end with the client', async () => {
  let run = ok(
    await createRun({
      inline: parse(readFileSync(scenarioFile, 'utf8')) as Scenario,
      parameters: { 'target.device': dut },
      // the gateway aborts the run when the job dies and stops renewing it, and removes its faults
      lease: '30s',
    }),
  ) as Run;

  const deadline = Date.now() + 60_000;
  while (!ended.includes(run.status)) {
    if (Date.now() > deadline) throw new Error(`run ${run.id} is still ${run.status}`);
    await sleep(500);
    run = ok(await getRun(run.id)) as Run;
  }

  expect(run.reason).toBeUndefined();
  expect(run.status).toBe('passed');
  expect(run.steps?.map((s) => s.id)).toEqual(['slow', 'cut', 'back']);
  expect(run.steps?.every((s) => s.status === 'applied')).toBe(true);

  // the generated client reads every answer as JSON, so the JUnit report comes from fetch() (setup.ts adds the host
  // and the token)
  const junit = await fetch(`/runs/${run.id}/report.xml`);
  expect(junit.status).toBe(200);
  expect(await junit.text()).toContain('<testsuite');
});

test('chaosctl runs a scenario and the exit code is the result', () => {
  const dir = mkdtempSync(join(tmpdir(), 'chaosctl-'));
  const result = chaosctl('run', '-f', scenarioFile, '--set', `target.device=${dut}`, '--wait', '--junit', join(dir, 'junit.xml'), '--artifacts', join(dir, 'out'));

  expect(result.status).toBe(0); // 0 passed, 1 a check failed, 2 error or aborted
  expect(result.stdout).toContain('PASSED');
  expect(readFileSync(join(dir, 'junit.xml'), 'utf8')).toContain('<testsuite');
  expect(readdirSync(join(dir, 'out')).sort()).toEqual(expect.arrayContaining(['events.jsonl', 'report.json', 'report.xml']));
});

test('chaosctl returns 2 for a scenario the gateway refuses', () => {
  const result = chaosctl('run', '-f', scenarioFile, '--set', 'target.device=no-such-device', '--wait');
  expect(result.status).toBe(2);
  expect(result.stderr).toMatch(/no-such-device|not_found|validation_failed/);
});

test('chaosctl with cleans up when the command fails', async () => {
  // the command sees the ids of the overlays and fails; chaosctl returns its exit code and removes the fault
  const result = chaosctl('with', '--device', dut, '--latency', '300ms', '--ttl', '5m', '--', 'node', '-e', 'console.log(process.env.CHAOSCTL_OVERLAYS); process.exit(3)');

  expect(result.status).toBe(3);
  const id = result.stdout.trim();
  expect(id).not.toBe('');
  const left = (ok(await listOverlays()) as { items: Overlay[] }).items.map((o) => o.id);
  expect(left).not.toContain(id);
});
