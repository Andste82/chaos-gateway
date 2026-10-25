import { expect, test, type Page } from '@playwright/test';
import type { Run, Scenario } from '../src/api/generated/model';
import { ESP32, IOT, LTE_ID, MockGateway, SCN_BOOT, SCN_MQTT, artifactsOf, mqttOutage, runOf, toast, withScenarios } from './mock';

// M19: the scenario list and its timeline, the editors of the settings, the steps and the checks (changes of the
// draft that go through the unapplied-changes bar and the preview), the YAML view and the import, running a
// scenario (from the configuration or from the draft) and following the run: the step in effect, the checks that
// resolve, the artifacts, abort — on the scenario's page, on the page of the run and on the overview.

function lab(): MockGateway {
  return withScenarios(MockGateway.sample());
}

const detail = (page: Page) => page.getByTestId('scenario-detail');
const step = (page: Page, id: string) => page.locator(`[data-testid="timeline-step"][data-step="${id}"]`);
const item = (page: Page, name: string) => page.getByTestId('scenario-item').filter({ has: page.locator('.mono', { hasText: new RegExp(`^${name}$`) }) });
const dialog = (page: Page, name: RegExp | string) => page.getByRole('dialog', { name });

/** Moves a run of the mock on and tells the page, as the engine's events do. */
async function advance(gw: MockGateway, page: Page, id: string, change: (r: Run) => void, type: 'run_step' | 'run_check' | 'run_finished' = 'run_step') {
  change(gw.runs.find((r) => r.id === id)!);
  await gw.emit(page, { type, message: `run ${id}`, data: { run: id } });
}

test('the list: target, steps, length, last result; the timeline with the type chips and the checks', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r1', status: 'failed', ended_at: new Date(Date.now() - 120_000).toISOString(), checks: [{ index: 0, name: 'reconnected 1', type: 'reconnected', status: 'failed' }] })];
  await gw.install(page);
  await page.goto('/');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Scenarios' }).click();
  await expect(page).toHaveURL(/\/scenarios$/);
  await expect(page.getByTestId('scenario-item')).toHaveCount(2);
  const mqtt = item(page, 'mqtt-outage');
  await expect(mqtt.getByTestId('scenario-meta')).toHaveText('ESP32-42 · 5 steps · 1 min 15 s · 2 checks');
  await expect(mqtt.getByTestId('scenario-last')).toHaveText(/Failed · 2 min ago/);
  await expect(item(page, 'boot-loop').getByTestId('scenario-meta')).toHaveText('Network IoT · 2 steps · 20 s · 0 checks');
  await expect(item(page, 'boot-loop').getByTestId('scenario-last')).toHaveText('Never run');

  // the list is in the order of the names and the first scenario is shown; then the timeline of the other, with a chip for the type of each step
  await expect(page.getByTestId('scenario-item').locator('.mono').first()).toHaveText('boot-loop');
  await expect(detail(page).getByTestId('scenario-name')).toHaveText('boot-loop');
  await mqtt.click();
  await expect(page).toHaveURL(new RegExp(`/scenarios/${SCN_MQTT}$`));
  await expect(detail(page).getByTestId('scenario-name')).toHaveText('mqtt-outage');
  await expect(detail(page).getByTestId('scenario-context')).toContainText('Target ESP32-42 · 1 min 15 s · capture on');
  await expect(page.getByTestId('timeline-step')).toHaveCount(5);
  await expect(step(page, 'normal')).toContainText('Profile LTE');
  await expect(step(page, 'normal').getByTestId('step-type')).toHaveText('Profile');
  await expect(step(page, 'slow')).toContainText('↑↓ 200 ms ±50');
  await expect(step(page, 'lossy')).toContainText('10 % loss');
  await expect(step(page, 'cut').getByTestId('step-type')).toHaveText('Access rule');
  await expect(step(page, 'cut')).toContainText('Drop · cut existing → TCP 8883');
  await expect(step(page, 'restore')).toContainText('Restore: remove all overlays of the run');
  await expect(step(page, 'slow')).toContainText('10s');
  const checks = page.getByTestId('checks');
  await expect(checks.getByTestId('check')).toHaveCount(2);
  await expect(checks.getByTestId('check').first()).toContainText('Reconnected: TCP 8883 · within 30 s of step restore');
  await expect(checks.getByTestId('check').nth(1)).toContainText('DNS query seen: a query for broker.example.com');
  await expect(page.getByTestId('artifacts-empty')).toContainText('A run leaves a JUnit and a JSON report');

  // another scenario, by its link; the address says which
  await item(page, 'boot-loop').click();
  await expect(page).toHaveURL(new RegExp(`/scenarios/${SCN_BOOT}$`));
  await expect(detail(page).getByTestId('scenario-name')).toHaveText('boot-loop');
  await expect(page.getByTestId('timeline-step')).toHaveCount(2);
  await expect(page.getByTestId('checks-empty')).toBeVisible();
});

test('no scenarios: the empty state offers the first one; a scenario that does not exist says so', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/scenarios');
  await expect(page.getByTestId('empty-state')).toContainText('No scenarios yet');
  await page.goto('/scenarios/nothing-here');
  await expect(page.getByTestId('empty-state')).toBeVisible();
  const gw2 = lab();
  await gw2.install(page);
  await page.goto('/scenarios/nothing-here');
  await expect(page.getByTestId('scenario-not-found')).toContainText('There is no scenario "nothing-here"');
});

test('edit a step, add a step and a check, delete a step: one change of the draft, applied through the preview', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);

  // edit the second step: the fault dialog's fields, here without target and duration
  await page.getByRole('button', { name: 'Edit step slow' }).click();
  const d = dialog(page, 'Edit step');
  await expect(d.getByLabel('Id', { exact: true })).toHaveValue('slow');
  await expect(d.getByLabel('At', { exact: true })).toHaveValue('10s');
  await expect(d.getByLabel('Latency (ms)', { exact: true })).toHaveValue('200');
  await d.getByLabel('Latency (ms)', { exact: true }).fill('300');
  await d.getByLabel('At', { exact: true }).fill('12s');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await expect(d).toBeHidden();
  await expect(step(page, 'slow')).toContainText('12s');
  await expect(step(page, 'slow')).toContainText('↑↓ 300 ms ±50');
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 scenario · not applied yet/);
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-mark')).toHaveText('CHANGED');

  // a step in front of the check windows' step cannot be deleted while a check uses it
  await expect(page.getByRole('button', { name: 'Delete step restore' })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Delete step restore' })).toHaveAttribute('title', /Used by the check reconnected 1 and the check dns_query_seen 2/);

  // add a wait step: the editor offers what this build runs
  await page.getByTestId('add-step').click();
  const a = dialog(page, 'Add step');
  await expect(a.getByLabel('Type', { exact: true }).locator('option')).toHaveText(['Profile', 'Fault', 'DNS fault', 'TLS case', 'DHCP action', 'Access rule', 'WireGuard action', 'Capture', 'Wait', 'Remove', 'Restore']);
  await a.getByLabel('Type', { exact: true }).selectOption('wait');
  await a.getByLabel('Id', { exact: true }).fill('online');
  await a.getByLabel('At', { exact: true }).fill('50s');
  await a.getByLabel('Wait until').selectOption('dns_query');
  await a.getByLabel('Host name').fill('not a name');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a.getByText('A host name like broker.example.com.')).toBeVisible();
  await a.getByLabel('Host name').fill('broker.example.com');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a).toBeHidden();
  await expect(step(page, 'online')).toContainText('Wait for a DNS query for broker.example.com · timeout 60 s');

  // a remove step names a step that comes before it
  await page.getByTestId('add-step').click();
  const r = dialog(page, 'Add step');
  await r.getByLabel('Type', { exact: true }).selectOption('remove');
  await r.getByLabel('Id', { exact: true }).fill('drop-cut');
  await r.getByLabel('At', { exact: true }).fill('40s');
  await expect(r.getByLabel('Remove the overlay of step').locator('option')).toHaveText(['Choose a step', 'normal · at 0s', 'slow · at 12s', 'lossy · at 20s', 'cut · at 30s']);
  await r.getByLabel('Remove the overlay of step').selectOption('cut');
  await r.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'drop-cut')).toContainText('Remove the overlay of step cut');
  // the timeline is in the order of the times
  expect(await page.getByTestId('timeline-step').evaluateAll((els) => els.map((e) => e.getAttribute('data-step')))).toEqual(['normal', 'slow', 'lossy', 'cut', 'drop-cut', 'restore', 'online']);

  // a check: traffic not seen, with an allowed destination, in a window up to a step
  await page.getByTestId('add-check').click();
  const c = dialog(page, 'Add check');
  await c.getByLabel('Check', { exact: true }).selectOption('traffic_not_seen');
  await c.getByLabel('Opens when', { exact: true }).selectOption('cut');
  await c.getByLabel('Closes', { exact: true }).selectOption('until');
  await c.getByLabel('Step', { exact: true }).selectOption('restore');
  await c.getByRole('button', { name: 'Allow a destination' }).click();
  await c.getByLabel('Allowed destination 1', { exact: true }).selectOption('network');
  await c.getByLabel('Network of allowed destination 1').selectOption(IOT);
  await c.getByLabel('Name in the report').fill('quiet while cut');
  await c.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(c).toBeHidden();
  await expect(page.getByTestId('check')).toHaveCount(3);
  await expect(page.getByTestId('check').nth(2)).toContainText('quiet while cut');
  await expect(page.getByTestId('check').nth(2)).toContainText('Traffic not seen: any traffic except network IoT · from step cut until step restore');

  // delete the lossy step: it is not used
  await page.getByRole('button', { name: 'Delete step lossy' }).click();
  await expect(step(page, 'lossy')).toHaveCount(0);

  // the bar counts one scenario; the preview sends one merge patch with the steps and checks as lists
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/\d+ changes in 1 scenario · not applied yet/);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = dialog(page, 'Preview revision 43');
  await expect(drawer.getByTestId('preview-changes')).toContainText('scenario mqtt-outage changed');
  const patch = gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Partial<Scenario>> };
  expect(Object.keys(patch)).toEqual(['scenarios']);
  const sent = patch.scenarios[SCN_MQTT]!;
  expect(Object.keys(sent).sort()).toEqual(['checks', 'steps']);
  expect(sent.steps!.map((s) => s.id)).toEqual(['normal', 'slow', 'cut', 'restore', 'online', 'drop-cut']);
  expect(sent.steps!.find((s) => s.id === 'slow')).toEqual({ id: 'slow', at: '12s', fault: { latency: '300ms', jitter: '50ms' } });
  expect(sent.steps!.find((s) => s.id === 'online')).toEqual({ id: 'online', at: '50s', wait: { for: 'dns_query', name: 'broker.example.com', timeout: '60s' } });
  expect(sent.checks![2]).toEqual({ name: 'quiet while cut', traffic_not_seen: { except: [{ network: IOT }] }, window: { from: 'cut', until: 'restore' } });
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-mark')).toHaveCount(0);
  await expect(step(page, 'slow')).toContainText('↑↓ 300 ms ±50');
  expect(gw.config.scenarios?.[SCN_MQTT]?.checks).toHaveLength(3);
});

test('a step that is renamed takes the references to it along; a step a check uses cannot be deleted', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByRole('button', { name: 'Edit step restore' }).click();
  const d = dialog(page, 'Edit step');
  await d.getByLabel('Id', { exact: true }).fill('put-back');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await expect(step(page, 'put-back')).toBeVisible();
  await expect(page.getByTestId('check').first()).toContainText('within 30 s of step put-back');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  expect(sent.checks!.map((c) => c.window.from)).toEqual(['put-back', 'put-back']);
});

test('a step id that another step has and a time that is none are refused under the fields', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('add-step').click();
  const d = dialog(page, 'Add step');
  await d.getByLabel('Type', { exact: true }).selectOption('capture');
  await d.getByLabel('Id', { exact: true }).fill('slow');
  await d.getByLabel('At', { exact: true }).fill('10');
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(d.getByLabel('Id', { exact: true })).toHaveAccessibleDescription('Another step has this id.');
  await expect(d.getByLabel('At', { exact: true })).toHaveAccessibleDescription('A time like 0s, 10s or 1m30s.');
  await expect(d.getByRole('button', { name: 'Add to the changes' })).toBeDisabled();
  await d.getByLabel('Id', { exact: true }).fill('rec');
  await d.getByLabel('At', { exact: true }).fill('1s');
  await expect(d.getByRole('button', { name: 'Add to the changes' })).toBeEnabled();
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'rec')).toContainText('Start a capture of the target');
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
});

test('a new scenario: settings, steps and a check, then the preview and the apply', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/scenarios');
  await page.getByRole('button', { name: 'New scenario' }).click();
  const s = dialog(page, 'New scenario');
  const add = s.getByRole('button', { name: 'Add to the changes' });
  await expect(s.locator('.field-error')).toHaveCount(0);
  await s.getByLabel('Name', { exact: true }).fill('MQTT-Outage');
  await expect(s.getByLabel('Name', { exact: true })).toHaveAccessibleDescription('Another scenario has this name.');
  await add.click();
  await expect(s).toBeVisible();
  await s.getByLabel('Name', { exact: true }).fill('sensor-flap');
  await s.getByLabel('Description', { exact: true }).fill('The network goes away for a while');
  await s.getByLabel('Target', { exact: true }).selectOption({ label: 'Device · Sensor-07' });
  await s.getByLabel('Capture the target\'s traffic for the whole run').check();
  await s.getByLabel('A fault, rule or profile that matched no packet').selectOption('error');
  await s.getByLabel('The target is online').check();
  await s.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(s.getByTestId('api-call').first()).toContainText('"zero_hit": "error"');
  await add.click();
  await expect(s).toBeHidden();

  // it is in the draft, marked NEW; without a step the gateway would refuse it, so the apply waits
  await expect(page).toHaveURL(/\/scenarios\/[0-9a-f-]{36}$/);
  await expect(item(page, 'sensor-flap').getByTestId('scenario-mark')).toHaveText('NEW');
  await expect(detail(page).getByTestId('scenario-problems')).toContainText('A scenario needs at least one step.');
  await expect(page.getByTestId('timeline-empty')).toBeVisible();
  await expect(page.getByTestId('unapplied-bar')).toContainText('a scenario needs fixing first');
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await expect(page.getByTestId('run-scenario')).toBeDisabled();

  // the first step: a fault on the scenario's target
  await page.getByTestId('add-step').click();
  const st = dialog(page, 'Add step');
  await st.getByLabel('Type', { exact: true }).selectOption('fault');
  await st.getByLabel('Id', { exact: true }).fill('lag');
  await st.getByLabel('At', { exact: true }).fill('0s');
  await st.getByLabel('Traffic').selectOption('mqtts');
  await st.getByLabel('Latency (ms)', { exact: true }).fill('500');
  await st.getByLabel('Jitter (ms)', { exact: true }).fill('');
  await expect(st.getByLabel('Acts on')).toContainText('The scenario\'s target (Sensor-07)');
  await st.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'lag')).toContainText('↑↓ 500 ms → TCP 8883');
  // the second: a rule on a device of the scenario's narrower target is not offered for a tunnel; this one reject
  await page.getByTestId('add-step').click();
  const rule = dialog(page, 'Add step');
  await rule.getByLabel('Type', { exact: true }).selectOption('rule');
  await rule.getByLabel('Id', { exact: true }).fill('refuse');
  await rule.getByLabel('At', { exact: true }).fill('30s');
  await rule.getByLabel('Access', { exact: true }).selectOption('reject');
  await rule.getByLabel('Reject with').selectOption('admin_prohibited');
  await rule.getByLabel('Protocol', { exact: true }).selectOption('tcp');
  await rule.getByLabel('Ports', { exact: true }).fill('443');
  await rule.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'refuse')).toContainText('Reject → TCP 443');
  await page.getByTestId('add-step').click();
  const end = dialog(page, 'Add step');
  await end.getByLabel('Type', { exact: true }).selectOption('restore');
  await end.getByLabel('Id', { exact: true }).fill('end');
  await end.getByLabel('At', { exact: true }).fill('1m');
  await end.getByRole('button', { name: 'Add to the changes' }).click();
  await page.getByTestId('add-check').click();
  const ck = dialog(page, 'Add check');
  await ck.getByLabel('Opens when', { exact: true }).selectOption('end');
  await ck.getByLabel('Protocol', { exact: true }).selectOption('tcp');
  await ck.getByLabel('Ports', { exact: true }).fill('8883');
  await ck.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(page.getByTestId('check')).toHaveCount(1);

  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 scenario · not applied yet/);
  await expect(page.getByTestId('unapplied-bar')).not.toContainText('needs fixing');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = dialog(page, 'Preview revision 43');
  await expect(drawer.getByTestId('preview-changes')).toContainText('scenario sensor-flap added');
  const sent = Object.values((gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, unknown> }).scenarios);
  expect(sent).toEqual([
    {
      name: 'sensor-flap',
      description: 'The network goes away for a while',
      target: { device: 'a1b2c3d4-0000-4000-8000-000000000057' },
      capture: true,
      zero_hit: 'error',
      preconditions: { skip: ['target_online'] },
      steps: [
        { id: 'lag', at: '0s', fault: { protocol: 'tcp', ports: [8883], latency: '500ms' } },
        { id: 'refuse', at: '30s', rule: { action: 'reject', reject_with: 'admin_prohibited', protocol: 'tcp', ports: [443] } },
        { id: 'end', at: '1m', restore: true },
      ],
      checks: [{ reconnected: { protocol: 'tcp', ports: [8883] }, window: { from: 'end', within: '30s' } }],
    },
  ]);
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(item(page, 'sensor-flap').getByTestId('scenario-mark')).toHaveCount(0);
  await expect(page.getByTestId('run-scenario')).toBeEnabled();
});

test('delete a scenario and undo it; a refused scenario shows what the gateway said', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await page.getByRole('button', { name: 'Delete boot-loop' }).click();
  await expect(item(page, 'boot-loop').getByTestId('scenario-mark')).toHaveText('DELETED');
  await expect(detail(page)).toContainText('Removed when the changes are applied');
  await expect(page.getByTestId('run-scenario')).toHaveCount(0);
  await page.getByRole('button', { name: 'Undo the deletion of boot-loop' }).click();
  await expect(item(page, 'boot-loop').getByTestId('scenario-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();

  // the gateway refuses a change: the pointer's words stand on the scenario, and the step's dialog opens with the message
  await page.getByRole('button', { name: 'Edit step back' }).click();
  const d = dialog(page, 'Edit step');
  await d.getByLabel('At', { exact: true }).fill('25s');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  gw.rejectRevision = [{ path: `/scenarios/${SCN_BOOT}/steps/1/at`, code: 'invalid', message: 'the step comes after the end of the run' }];
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog(page, 'Preview revision 43')).toHaveCount(0);
  const drawer = page.getByRole('dialog').filter({ hasText: 'the step comes after the end of the run' });
  await expect(drawer).toContainText('Scenario boot-loop');
  await drawer.getByRole('button', { name: 'Back to editing' }).click();
  await expect(detail(page).getByTestId('scenario-refused')).toContainText('steps/1/at the step comes after the end of the run');
  await page.getByRole('button', { name: 'Edit step back' }).click();
  await expect(dialog(page, 'Edit step').getByLabel('At', { exact: true })).toHaveAccessibleDescription('the step comes after the end of the run');
});

test('the YAML view: the scenario with names, checked while it is typed, applied to the draft; a download', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByRole('tab', { name: 'YAML' }).click();
  await expect(page).toHaveURL(/tab=yaml/);
  const text = page.getByTestId('yaml-text');
  await expect(text).toHaveValue(/^name: mqtt-outage\n/);
  // names where the configuration has UUIDs
  await expect(text).toHaveValue(/target:\n {2}device: ESP32-42\n/);
  await expect(text).toHaveValue(/profile: lte\n/);
  await expect(text).not.toHaveValue(new RegExp(ESP32));
  await expect(page.getByTestId('yaml-status')).toContainText('The text is the scenario as it is.');
  await expect(page.getByTestId('yaml-apply')).toBeDisabled();

  // a syntax error has its line; a field the format does not have, its pointer
  await text.fill('name: mqtt-outage\ntarget: { device: ESP32-42\nsteps: []\n');
  await expect(page.getByTestId('yaml-problem').first()).toContainText(/line \d+ /);
  await expect(page.getByTestId('yaml-apply')).toBeDisabled();
  await text.fill('name: mqtt-outage\ntarget: { device: nobody }\nsteps:\n  - { id: a, at: 0s, restore: true, speed: 3 }\n');
  await expect(page.getByTestId('yaml-problems')).toContainText('There is no device "nobody".');
  await expect(page.getByTestId('yaml-problems')).toContainText('speed is not a field of the scenario format.');
  await text.fill('name: mqtt-outage\ntarget: { device: ESP32-42 }\nsteps:\n  - { id: a, at: 10, restore: true }\n');
  await expect(page.getByTestId('yaml-problems')).toContainText('A time like 0s, 10s or 1m30s.');

  // a valid file: applied to the draft with the UUIDs the configuration stores
  await text.fill(
    [
      'name: mqtt-outage',
      'target: { device: esp32-42 }',
      'steps:',
      '  - { id: cut, at: 0s, rule: { action: drop, protocol: tcp, ports: [8883] } }',
      '  - { id: back, at: 20s, restore: true }',
      'checks:',
      '  - { reconnected: { protocol: tcp, ports: [8883] }, window: { from: back, within: 1m } }',
      '',
    ].join('\n'),
  );
  await expect(page.getByTestId('yaml-valid')).toBeVisible();
  await page.getByTestId('yaml-apply').click();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/\d+ changes in 1 scenario/);
  await page.getByRole('tab', { name: 'Timeline' }).click();
  await expect(page.getByTestId('timeline-step')).toHaveCount(2);
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-meta')).toHaveText('ESP32-42 · 2 steps · 1 min 20 s · 1 check');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  expect(sent.target).toBeUndefined(); // unchanged members are not in the patch
  expect(sent.steps!.map((s) => s.id)).toEqual(['cut', 'back']);
  await page.getByRole('button', { name: 'Back to editing' }).click();

  // the text follows the scenario and downloads as a file
  await page.getByRole('tab', { name: 'YAML' }).click();
  await expect(text).toHaveValue(/- id: cut\n/);
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download' }).click();
  expect((await download).suggestedFilename()).toBe('mqtt-outage.yaml');
});

test('import a scenario from YAML: a file of the repository with names becomes a new scenario of the draft', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/scenarios');
  await page.getByRole('button', { name: 'Import YAML' }).click();
  const d = dialog(page, 'Import a scenario');
  const yaml = [
    'name: sensor-offline',
    'target: { device: Sensor-07 }',
    'steps:',
    '  - { id: gone, at: 0s, profile: lte }',
    '  - { id: back, at: 30s, restore: true }',
    '',
  ].join('\n');
  await d.getByLabel('YAML').fill('name: boot-loop\ntarget: { device: Sensor-07 }\nsteps: [{ id: a, at: 0s, restore: true }]\n');
  await expect(d.getByTestId('import-problems')).toContainText('Another scenario has this name.');
  await expect(d.getByRole('button', { name: 'Add to the changes' })).toBeDisabled();
  await d.getByLabel('YAML').fill(yaml);
  await expect(d.getByTestId('import-problems')).toHaveCount(0);
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(d).toBeHidden();
  await expect(item(page, 'sensor-offline').getByTestId('scenario-mark')).toHaveText('NEW');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = Object.values((gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios);
  expect(sent).toEqual([
    {
      name: 'sensor-offline',
      target: { device: 'a1b2c3d4-0000-4000-8000-000000000057' },
      steps: [
        { id: 'gone', at: '0s', profile: LTE_ID },
        { id: 'back', at: '30s', restore: true },
      ],
    },
  ]);
});

test('run a scenario and follow it: the step in effect, the checks that resolve, the artifacts', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('run-scenario').click();
  const d = dialog(page, 'Run scenario');
  await expect(d.getByTestId('run-dialog-what')).toContainText('The run executes the scenario as the active revision has it.');
  await d.getByLabel('Parameters', { exact: true }).fill('steps.slow.fault.latency=400ms\ntarget.device=esp32-42');
  await d.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(d.getByTestId('api-call').first()).toContainText(`POST /api/v1/scenarios/${SCN_MQTT}/runs`);
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  await expect(toast(page, 'mqtt-outage is running')).toBeVisible();
  expect(gw.requestsTo('POST', `/scenarios/${SCN_MQTT}/runs`)[0]!.body).toEqual({ parameters: { 'steps.slow.fault.latency': '400ms', 'target.device': 'esp32-42' } });

  // the page follows the run it started: running, nothing applied yet, the buttons say what is possible
  const id = gw.runs[0]!.id;
  await expect(page).toHaveURL(new RegExp(`run=${id}`));
  await expect(page.getByTestId('run-status')).toHaveText('Running');
  await expect(page.getByTestId('run-progress')).toContainText('0 of 5 steps through');
  await expect(page.getByTestId('abort-run')).toBeVisible();
  await expect(page.getByTestId('run-scenario')).toHaveCount(0);
  await expect(page.getByTestId('step-status').first()).toHaveText('pending');
  await expect(page.getByTestId('timeline-step').filter({ has: page.getByTestId('step-now') })).toHaveCount(0);
  await expect(step(page, 'normal').getByTestId('step-next')).toContainText(/applying…|next in/);
  await expect(page.getByTestId('checks').getByTestId('check-state').first()).toHaveText('PENDING');
  await expect(page.getByTestId('artifacts-empty')).toHaveText('The reports appear when the run has ended.');

  // two steps are applied: the second is in effect, with its timing against the schedule and its generation
  await advance(gw, page, id, (r) => {
    r.steps![0] = { id: 'normal', type: 'profile', status: 'applied', scheduled_offset: '0s', offset_ms: 4, generation: 50, queue_wait_ms: 0, applied_at: new Date().toISOString() };
    r.steps![1] = { id: 'slow', type: 'fault', status: 'applied', scheduled_offset: '10s', offset_ms: 10_037, generation: 51, queue_wait_ms: 130, applied_at: new Date().toISOString() };
    r.warnings = [{ code: 'step_late', step: 'slow', message: 'step slow waited 130 ms for an apply' }];
  });
  await expect(page.getByTestId('run-progress')).toContainText('2 of 5 steps through');
  await expect(step(page, 'slow').getByTestId('step-now')).toHaveText('now');
  await expect(step(page, 'normal').getByTestId('step-now')).toHaveCount(0);
  await expect(step(page, 'slow').getByTestId('step-status')).toHaveText('applied');
  await expect(step(page, 'slow').getByTestId('step-timing')).toHaveText(/^\s*at 10\.0 s \(\+37 ms\) · generation 51$/);
  await expect(step(page, 'slow').getByTestId('step-queue-wait')).toHaveText('waited 130 ms');
  await expect(step(page, 'lossy').getByTestId('step-next')).toContainText('next in');
  await expect(page.getByTestId('run-warning')).toContainText('step slow waited 130 ms for an apply');
  await expect(page.getByRole('progressbar', { name: 'Run progress' })).toHaveAttribute('aria-valuenow', /^\d+$/);

  // the run ends: the checks resolve, the artifacts appear
  gw.reports[id] = { counters: [{ step: 'slow', overlay: 'f0000000-0000-4000-8000-0000000000e1', counters: { packets: 118, bytes: 9000, epoch: 1 } }, { step: 'cut', counters: { packets: 0, bytes: 0, epoch: 1 } }] };
  await advance(
    gw,
    page,
    id,
    (r) => {
      r.status = 'failed';
      r.ended_at = new Date().toISOString();
      r.reason = 'check reconnected 1 failed';
      r.steps = r.steps!.map((s, i) => (s.status === 'pending' ? { ...s, status: 'applied', offset_ms: [0, 10_000, 20_000, 30_000, 45_000][i]!, generation: 52 + i } : s));
      r.checks = [
        { index: 0, name: 'reconnected 1', type: 'reconnected', status: 'failed', window_start: new Date(Date.now() - 30_000).toISOString(), window_end: new Date().toISOString(), message: 'no new connection to tcp/8883 inside the window' },
        {
          index: 1,
          name: 'dns_query_seen 2',
          type: 'dns_query_seen',
          status: 'passed',
          message: 'esp32-42 asked for broker.example.com',
          evidence: [{ device: 'ESP32-42', name: 'broker.example.com', at: new Date().toISOString() }],
        },
      ];
      r.artifacts = artifactsOf(r.id, { id: 'ca000000-0000-4000-8000-000000000001', size: 1_800_000 });
    },
    'run_finished',
  );
  await expect(page.getByTestId('run-status')).toHaveText('Failed');
  await expect(page.getByTestId('run-reason')).toHaveText('check reconnected 1 failed');
  await expect(page.getByRole('progressbar', { name: 'Run progress' })).toHaveAttribute('aria-valuenow', '100');
  await expect(page.getByTestId('abort-run')).toHaveCount(0);
  await expect(page.getByTestId('run-scenario')).toBeVisible();
  const [failed, passed] = await page.getByTestId('check').all();
  await expect(failed!.getByTestId('check-state')).toHaveText('FAILED');
  await expect(failed!.getByTestId('check-message')).toHaveText('no new connection to tcp/8883 inside the window');
  await expect(passed!.getByTestId('check-state')).toHaveText('PASSED');
  await expect(passed!.getByTestId('check-evidence')).toBeHidden();
  await passed!.getByText('1 observation').click();
  await expect(passed!.getByTestId('check-evidence')).toContainText('device ESP32-42 · name broker.example.com');
  const artifacts = page.getByTestId('artifact');
  await expect(artifacts).toHaveText(['Capture1.8 MB', 'report.xml · JUnitDownload', 'report.jsonDownload', 'events.jsonlDownload']);
  await expect(artifacts.nth(0).getByRole('link')).toHaveAttribute('href', '/api/v1/captures/ca000000-0000-4000-8000-000000000001/download');
  await expect(artifacts.nth(1).getByRole('link')).toHaveAttribute('href', `/api/v1/runs/${id}/report.xml`);
  await expect(artifacts.nth(2).getByRole('link')).toHaveAttribute('href', `/api/v1/runs/${id}/report.json`);

  // the list and the runs tab show the result
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-last')).toHaveText(/^Failed · /);
  await page.getByRole('tab', { name: 'Runs' }).click();
  await expect(page.getByTestId('run-row')).toHaveCount(1);
  await expect(page.getByTestId('run-row')).toContainText('Failed');
  await page.getByRole('link', { name: /^Show the run from/ }).click();
  await expect(page.getByTestId('run-status')).toHaveText('Failed');

  // the page of the run has the same, and the counters of the overlays (a rule that matched no packet stands out)
  await page.getByTestId('run-page-link').click();
  await expect(page).toHaveURL(new RegExp(`/runs/${id}$`));
  await expect(page.getByTestId('run-title')).toHaveText('Run of mqtt-outage');
  await expect(page.getByTestId('timeline-step')).toHaveCount(5);
  await expect(page.getByTestId('run-counter')).toHaveText([/slow118 packets/, /cut0 packets/]);
  await expect(page.getByTestId('artifact')).toHaveCount(4);
  await expect(page.getByTestId('run-scenario-link')).toHaveAttribute('href', new RegExp(`/scenarios/${SCN_MQTT}\\?run=${id}`));
});

test('a run that passed: PASSED checks, the report links, and the scenario can be run again', async ({ page }) => {
  const gw = lab();
  gw.runs = [
    runOf(SCN_BOOT, {
      id: 'r-ok',
      scenario: lab().config.scenarios![SCN_BOOT]!,
      status: 'passed',
      started_at: new Date(Date.now() - 30_000).toISOString(),
      ended_at: new Date(Date.now() - 5_000).toISOString(),
      steps: [
        { id: 'offline', status: 'applied', scheduled_offset: '0s', offset_ms: 2, generation: 60 },
        { id: 'back', status: 'applied', scheduled_offset: '20s', offset_ms: 20_011, generation: 61 },
      ],
      checks: [],
      artifacts: artifactsOf('r-ok'),
    }),
  ];
  await gw.install(page);
  await page.goto(`/runs/r-ok`);
  await expect(page.getByTestId('run-status')).toHaveText('Passed');
  await expect(page.getByTestId('run-meta')).toContainText('Network IoT · started by admin · revision 42');
  await expect(step(page, 'back').getByTestId('step-timing')).toHaveText(/^\s*at 20\.0 s \(\+11 ms\) · generation 61$/);
  await expect(page.getByTestId('checks-empty')).toBeVisible();
  await expect(page.getByTestId('artifact')).toHaveCount(3);
  await expect(page.getByTestId('abort-run')).toHaveCount(0);
  await page.goto(`/runs/nothing`);
  await expect(page.getByTestId('run-not-found')).toContainText('There is no run nothing');
});

test('abort a run: the run is aborted with its reason, the page stops following it and the scenario can be edited again', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r-go', status: 'running' })];
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  // an active run of the scenario is followed without being asked for
  await expect(page.getByTestId('run-status')).toHaveText('Running');
  await page.getByTestId('abort-run').click();
  await expect(toast(page, 'mqtt-outage aborted')).toBeVisible();
  expect(gw.requestsTo('POST', '/runs/r-go/abort')).toHaveLength(1);
  await expect(page.getByTestId('run-status')).toHaveText('Aborted');
  await expect(page.getByTestId('run-reason')).toHaveText('aborted by admin');
  await expect(page.getByTestId('abort-run')).toHaveCount(0);
  // back to the scenario: no run is shown, and it is not followed again
  await page.getByTestId('hide-run').click();
  await expect(page.getByTestId('run-progress')).toHaveCount(0);
  await expect(page.getByTestId('add-step')).toBeVisible();
  await expect(page.getByTestId('run-scenario')).toBeVisible();
});

test('a queued run waits for the target; the run dialog can refuse a busy target', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_BOOT, { id: 'busy', status: 'running', scenario: { ...lab().config.scenarios![SCN_BOOT]!, target: { device: ESP32 } } })];
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('run-scenario').click();
  const d = dialog(page, 'Run scenario');
  await d.getByLabel('Wait in the queue when the target is busy').uncheck();
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  await expect(d.getByTestId('run-error')).toContainText('the target is busy: run busy holds it');
  expect(gw.requestsTo('POST', `/scenarios/${SCN_MQTT}/runs`)[0]!.query).toEqual({ queue: 'false' });
  await d.getByLabel('Wait in the queue when the target is busy').check();
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  await expect(toast(page, 'mqtt-outage is queued')).toBeVisible();
  await expect(page.getByTestId('run-status')).toHaveText('Queued');
  await expect(page.getByTestId('run-queued')).toContainText('Waits for the run on the same target to end');
  await expect(page.getByTestId('abort-run')).toBeVisible();
});

test('the gateway refuses the parameters of a run at their path; a line without = is refused before', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('run-scenario').click();
  const d = dialog(page, 'Run scenario');
  await d.getByLabel('Parameters', { exact: true }).fill('target.device');
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  await expect(d.getByTestId('run-param-error')).toHaveText('Line 1: write path=value, like target.device=esp32-42.');
  expect(gw.requestsTo('POST', `/scenarios/${SCN_MQTT}/runs`)).toHaveLength(0);
  gw.rejectRun = [{ path: '/parameters/steps.nope.at', code: 'unknown_field', message: 'there is no step nope' }];
  await d.getByLabel('Parameters', { exact: true }).fill('steps.nope.at=5s');
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  await expect(d.getByTestId('run-param-error')).toHaveText('steps.nope.at: there is no step nope');
  await expect(d).toBeVisible();
});

test('a scenario with changes that are not applied yet runs from the draft, sent with the request', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await page.getByRole('button', { name: 'Edit step back' }).click();
  const e = dialog(page, 'Edit step');
  await e.getByLabel('At', { exact: true }).fill('15s');
  await e.getByRole('button', { name: 'Save to the changes' }).click();
  await page.getByTestId('run-scenario').click();
  const d = dialog(page, 'Run scenario');
  await expect(d.getByTestId('run-dialog-what')).toContainText('It has changes that are not applied yet');
  await d.getByRole('button', { name: 'Run', exact: true }).click();
  const body = gw.requestsTo('POST', '/runs')[0]!.body as { inline: Scenario };
  expect(body.inline.name).toBe('boot-loop');
  expect(body.inline.steps.find((s) => s.id === 'back')!.at).toBe('15s');
  // no revision was made; the run is of an inline scenario, followed on the scenario's page
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
  await expect(page.getByTestId('run-status')).toHaveText('Running');
  await expect(page.getByTestId('run-meta')).toContainText('inline scenario, not stored');
  await expect(page.getByTestId('unapplied-bar')).toBeVisible();
});

test('the overview shows the running scenarios with their progress and links to them', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r-live', status: 'running' }), runOf(SCN_BOOT, { id: 'r-old', status: 'passed', ended_at: new Date().toISOString() })];
  await gw.install(page);
  await page.goto('/');
  const card = page.getByTestId('running-scenarios');
  await expect(card.getByTestId('running-scenario')).toHaveCount(1);
  await expect(card).toContainText('mqtt-outage');
  await expect(card).toContainText('ESP32-42 · 0 of 5 steps');
  await expect(card.getByRole('progressbar', { name: 'Progress of mqtt-outage' })).toBeVisible();
  await advance(gw, page, 'r-live', (r) => (r.steps![0] = { id: 'normal', type: 'profile', status: 'applied', scheduled_offset: '0s', offset_ms: 2, generation: 7 }));
  await expect(card).toContainText('1 of 5 steps · step normal in effect');
  await card.getByRole('link', { name: 'mqtt-outage' }).click();
  await expect(page).toHaveURL(new RegExp(`/scenarios/${SCN_MQTT}\\?run=r-live`));
  await expect(page.getByTestId('run-status')).toHaveText('Running');

  // nothing running: it says so
  await advance(gw, page, 'r-live', (r) => ((r.status = 'passed'), (r.ended_at = new Date().toISOString())), 'run_finished');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Overview' }).click();
  await expect(page.getByTestId('no-running-scenario')).toHaveText('No scenario is running.');
});

test('a DNS step is edited with the DNS fields (M23b); a type the gateway does not list is not offered; the check types follow the capabilities', async ({ page }) => {
  const gw = lab();
  gw.config = {
    ...gw.config,
    scenarios: {
      ...gw.config.scenarios,
      [SCN_BOOT]: { name: 'boot-loop', target: { network: IOT }, steps: [{ id: 'dns', at: '0s', dns: { action: 'nxdomain', names: ['broker.example.com'] } }, { id: 'back', at: '20s', restore: true }] },
    },
  };
  // a gateway of an older build knows no DHCP actions
  gw.capabilities.step_types = gw.capabilities.step_types.filter((t) => t !== 'dhcp');
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await expect(step(page, 'dns').getByTestId('step-type')).toHaveText('DNS fault');
  await expect(step(page, 'dns')).toContainText('DNS nxdomain · broker.example.com');
  await page.getByRole('button', { name: 'Edit step dns' }).click();
  const d = dialog(page, 'Edit step');
  await expect(d).not.toContainText('This step is edited in the YAML view.');
  await expect(d.getByLabel('Type', { exact: true })).toBeEnabled();
  await expect(d.getByLabel('Type', { exact: true }).locator('option')).toHaveText(['Profile', 'Fault', 'DNS fault', 'TLS case', 'Access rule', 'WireGuard action', 'Capture', 'Wait', 'Remove', 'Restore']);
  await expect(d.getByLabel('Host names')).toHaveValue('broker.example.com');
  await d.getByLabel('At', { exact: true }).fill('2s');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_BOOT]!;
  expect(sent.steps![0]).toEqual({ id: 'dns', at: '2s', dns: { action: 'nxdomain', names: ['broker.example.com'] } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await page.getByTestId('add-check').click();
  await expect(dialog(page, 'Add check').getByLabel('Check', { exact: true }).locator('option')).toHaveText(['Reconnected', 'No connection', 'Traffic seen', 'Traffic not seen', 'DNS query seen', 'TLS rejected', 'TLS accepted']);
});

// M21: the two TLS checks are in the editor once the gateway lists them; they take the connection the device meant (a selector
// like the traffic checks) and are written as `tls_rejected` and `tls_accepted`.
test('a TLS check is added with the connection the device meant and sent as its own check type', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('add-check').click();
  const c = dialog(page, 'Add check');
  await c.getByLabel('Check', { exact: true }).selectOption('tls_rejected');
  await expect(c).toContainText('aborts a TLS handshake with the gateway');
  await c.getByLabel('Opens when', { exact: true }).selectOption('restore');
  await c.getByLabel('Protocol', { exact: true }).selectOption('tcp');
  await c.getByLabel('Ports', { exact: true }).fill('8883');
  await c.getByLabel('Name in the report').fill('rejects the broken certificate');
  await c.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(c).toBeHidden();
  await expect(page.getByTestId('check').last()).toContainText('rejects the broken certificate');
  await expect(page.getByTestId('check').last()).toContainText('TLS rejected: TCP 8883');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Partial<Scenario>> }).scenarios[SCN_MQTT]!;
  expect(sent.checks!.at(-1)).toMatchObject({ name: 'rejects the broken certificate', tls_rejected: { protocol: 'tcp', ports: [8883] }, window: { from: 'restore' } });
});

test('while a revision waits for confirmation, a scenario cannot be run', async ({ page }) => {
  const gw = lab();
  gw.state = { ...gw.state, pending_confirm: { revision: 43, deadline: new Date(Date.now() + 60_000).toISOString() } };
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await expect(page.getByTestId('run-scenario')).toBeDisabled();
});

// P4-AUDIT-36: the step editor offers what the gateway accepts and shows the rules it always refuses under the field.

test('the step editor offers only the targets inside the scenario and the steps whose overlay can be removed', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  // boot-loop acts on the IoT network: its devices, not those of the Test network
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await page.getByTestId('add-step').click();
  const d = dialog(page, 'Add step');
  const acts = d.getByLabel('Acts on');
  await expect(acts.locator('option')).toHaveText(["The scenario's target (Network IoT)", 'Device · ESP32-42', 'Device · Sensor-07', 'Device · Sensor-09', 'Network · IoT']);
  // a remove names a step that made an overlay (the restore did not)
  await d.getByLabel('Type', { exact: true }).selectOption('remove');
  await d.getByLabel('At', { exact: true }).fill('30s');
  await expect(d.getByLabel('Remove the overlay of step').locator('option')).toHaveText(['Choose a step', 'offline · at 0s']);
  await d.getByRole('button', { name: 'Cancel' }).click();

  // a scenario about one device offers nothing wider
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('add-step').click();
  await expect(dialog(page, 'Add step').getByLabel('Acts on').locator('option')).toHaveText(["The scenario's target (ESP32-42)", 'Device · ESP32-42']);
});

test('the rules the gateway always refuses are shown under the fields of the step', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await page.getByTestId('add-step').click();
  const d = dialog(page, 'Add step');
  const save = d.getByRole('button', { name: 'Add to the changes' });

  // the id start is reserved for the beginning of the run
  await d.getByLabel('Type', { exact: true }).selectOption('restore');
  await d.getByLabel('Id', { exact: true }).fill('start');
  await expect(d.getByLabel('Id', { exact: true })).toHaveAccessibleDescription('start is reserved for the beginning of the run.');
  await d.getByLabel('Id', { exact: true }).fill('end');

  // a TCP reset needs the protocol TCP
  await d.getByLabel('Type', { exact: true }).selectOption('rule');
  await d.getByLabel('Access', { exact: true }).selectOption('reset');
  await expect(d.getByLabel('Access', { exact: true })).toHaveAccessibleDescription('A TCP reset needs the protocol TCP.');
  await d.getByLabel('Protocol', { exact: true }).selectOption('tcp');
  await expect(d.locator('.field-error')).toHaveCount(0);

  // a capture stop needs the start of a capture of the same target before it
  await d.getByLabel('Type', { exact: true }).selectOption('capture');
  await d.getByLabel('Stop').check();
  await expect(d.locator('.field-error')).toHaveText('No step before this one starts a capture of the same target.');
  await save.click();
  await expect(d).toBeVisible();

  // a wait that never times out
  await d.getByLabel('Type', { exact: true }).selectOption('wait');
  await d.getByLabel('Timeout').fill('0s');
  await expect(d.getByLabel('Timeout')).toHaveAccessibleDescription('The timeout must be longer than zero.');
  await d.getByLabel('Timeout').fill('10s');
  await expect(d.locator('.field-error')).toHaveCount(0);
  await save.click();
  await expect(d).toBeHidden();
  await expect(step(page, 'end')).toContainText('Wait until the target is online · timeout 10 s');

  // a check window of zero length
  await page.getByTestId('add-check').click();
  const c = dialog(page, 'Add check');
  await c.getByLabel('Within', { exact: true }).fill('0s');
  await c.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(c.getByLabel('Within', { exact: true })).toHaveAccessibleDescription('The window must be longer than zero.');
});

// P4-AUDIT-37: the keyboard focus never falls to <body>, and the tabs are the tab pattern.

test('the focus stays on the page when a scenario, a step or a check is deleted and when the deletion is undone', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  const focusedIsPage = () => page.evaluate(() => document.activeElement !== document.body);
  await page.goto(`/scenarios/${SCN_MQTT}`);

  // Delete scenario, then Undo: the focus moves to the button that took its place, and back
  const del = page.getByRole('button', { name: 'Delete mqtt-outage' });
  await del.focus();
  await page.keyboard.press('Enter');
  const undo = page.getByRole('button', { name: 'Undo the deletion of mqtt-outage' });
  await expect(undo).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(del).toBeFocused();

  // Delete step: the step that took its place, else the one before it; the last one leaves "Add step"
  await page.getByRole('button', { name: 'Delete step lossy' }).focus();
  await page.keyboard.press('Enter');
  await expect(step(page, 'lossy')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Edit step cut' })).toBeFocused();

  // Delete check: the check that took its place, else the one before it, else "Add check"
  const checks = page.getByTestId('checks').getByTestId('check');
  await expect(checks).toHaveCount(2);
  await checks.first().getByRole('button', { name: /^Delete check/ }).focus();
  await page.keyboard.press('Enter');
  await expect(checks).toHaveCount(1);
  await expect(checks.first().getByRole('button', { name: /^Edit check/ })).toBeFocused();
  await checks.first().getByRole('button', { name: /^Delete check/ }).focus();
  await page.keyboard.press('Enter');
  await expect(checks).toHaveCount(0);
  await expect(page.getByTestId('add-check')).toBeFocused();
  expect(await focusedIsPage()).toBe(true);

  // the only step of a scenario: its deletion leaves "Add step"
  await page.goto(`/scenarios/${SCN_BOOT}`);
  await page.getByRole('button', { name: 'Delete step back' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Edit step offline' })).toBeFocused();
  await page.getByRole('button', { name: 'Delete step offline' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('add-step')).toBeFocused();
});

test('the Timeline, YAML and Runs tabs are tabs with their panels, one tab stop and the arrow keys', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  const tabs = page.getByRole('tablist', { name: 'Scenario views' });
  const [timeline, yaml, runs] = ['Timeline', 'YAML', 'Runs'].map((n) => tabs.getByRole('tab', { name: n }));
  await expect(timeline).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tabpanel')).toHaveAccessibleName('Timeline');
  await expect(timeline).toHaveAttribute('aria-controls', /.+/);
  // one tab stop: the tab that has the focus; the others are reached with the arrow keys
  await timeline!.focus();
  await expect(timeline).toHaveAttribute('tabindex', '0');
  await expect(yaml).toHaveAttribute('tabindex', '-1');
  await expect(runs).toHaveAttribute('tabindex', '-1');
  await page.keyboard.press('ArrowRight');
  await expect(yaml).toBeFocused();
  await expect(yaml).toHaveAttribute('aria-selected', 'true');
  await expect(page).toHaveURL(/tab=yaml/);
  await expect(page.getByRole('tabpanel')).toHaveAccessibleName('YAML');
  await page.keyboard.press('ArrowRight');
  await expect(runs).toBeFocused();
  await expect(page.getByTestId('runs-tab')).toBeVisible();
  await page.keyboard.press('Home');
  await expect(timeline).toBeFocused();
  await expect(page).not.toHaveURL(/tab=/);
  await expect(page.getByTestId('timeline')).toBeVisible();
});

// P4-AUDIT-18: the timeline shows when a step was handed over (the accuracy of the timeline) and when it was applied.

test('a slow apply handed over on time shows both moments, and only the hand-over is measured against the schedule', async ({ page }) => {
  const gw = lab();
  gw.runs = [
    runOf(SCN_MQTT, {
      id: 'r-slow',
      status: 'passed',
      ended_at: ended(10_000),
      steps: [
        { id: 'normal', type: 'profile', status: 'applied', scheduled_offset: '0s', start_offset_ms: 1, offset_ms: 2_100, generation: 50 },
        { id: 'slow', type: 'fault', status: 'applied', scheduled_offset: '10s', start_offset_ms: 10_003, offset_ms: 13_400, generation: 51, queue_wait_ms: 0 },
        { id: 'lossy', type: 'fault', status: 'applied', scheduled_offset: '20s', start_offset_ms: 20_250, offset_ms: 20_300, generation: 52 },
        { id: 'cut', type: 'rule', status: 'applied', scheduled_offset: '30s', offset_ms: 30_040, generation: 53 },
        { id: 'restore', type: 'restore', status: 'applied', scheduled_offset: '50s', start_offset_ms: 50_001, offset_ms: 50_120, generation: 54 },
      ],
    }),
  ];
  await gw.install(page);
  await page.goto('/runs/r-slow');
  await expect(step(page, 'slow').getByTestId('step-timing')).toHaveText('handed over at 10.0 s (+3 ms), applied at 13.4 s (apply 3.4 s) · generation 51');
  await expect(step(page, 'normal').getByTestId('step-timing')).toHaveText('handed over at 1 ms (+1 ms), applied at 2.1 s (apply 2.1 s) · generation 50');
  // a hand-over over 100 ms late shows as it is
  await expect(step(page, 'lossy').getByTestId('step-timing')).toHaveText('handed over at 20.3 s (+250 ms), applied at 20.3 s (apply 50 ms) · generation 52');
  // a run without the hand-over moment keeps the apply as its only moment
  await expect(step(page, 'cut').getByTestId('step-timing')).toHaveText('at 30.0 s (+40 ms) · generation 53');
});

// P4-AUDIT-42: a wait holds the timeline and moves every later step; the countdown knows it.

test('a pending wait says what it waits for, and the step after a wait is counted against the shifted schedule', async ({ page }) => {
  const gw = lab();
  const scenario: Scenario = {
    name: 'wait-it',
    target: { network: IOT },
    steps: [
      { id: 'go', at: '0s', profile: LTE_ID },
      { id: 'hold', at: '30s', wait: { for: 'online', timeout: '60s' } },
      { id: 'back', at: '40s', restore: true },
    ],
  };
  gw.runs = [
    runOf(SCN_BOOT, {
      id: 'r-w',
      scenario,
      status: 'running',
      started_at: ended(35_000),
      steps: [
        { id: 'go', type: 'profile', status: 'applied', scheduled_offset: '0s', start_offset_ms: 1, offset_ms: 4, generation: 5 },
        { id: 'hold', type: 'wait', status: 'pending', scheduled_offset: '30s' },
        { id: 'back', type: 'restore', status: 'pending', scheduled_offset: '40s' },
      ],
    }),
  ];
  await gw.install(page);
  await page.goto('/runs/r-w');
  await expect(step(page, 'hold').getByTestId('step-next')).toHaveText('waiting for the condition (timeout 60 s)');
  await expect(step(page, 'back').getByTestId('step-next')).toHaveCount(0);

  // the wait took 20 s: 50 s into the run, the step scheduled at 40 s is due at 60 s, not "applying"
  await advance(gw, page, 'r-w', (r) => {
    r.started_at = ended(50_000);
    r.steps![1] = { id: 'hold', type: 'wait', status: 'applied', scheduled_offset: '30s', offset_ms: 50_000 };
  });
  await expect(step(page, 'hold').getByTestId('step-timing')).toHaveText('at 50 s');
  await expect(step(page, 'back').getByTestId('step-next')).toHaveText(/^next in \d/);
});

// P4-AUDIT-41: the artifacts of two runs are not saved under the same name.

test('the artifacts of a run are saved under the name of the kind and the run', async ({ page }) => {
  const gw = lab();
  const ids = ['0a1b2c3d-0000-4000-8000-0000000000aa', '9f8e7d6c-0000-4000-8000-0000000000bb'];
  gw.runs = ids.map((id) => runOf(SCN_MQTT, { id, status: 'passed', ended_at: ended(60_000), artifacts: artifactsOf(id) }));
  await gw.install(page);
  const saved: string[] = [];
  for (const id of ids) {
    await page.goto(`/runs/${id}`);
    for (const label of ['events.jsonl', 'report.json', 'report.xml']) {
      const link = page.getByRole('link', { name: `Download ${label}` });
      const download = page.waitForEvent('download');
      await link.click();
      saved.push((await download).suggestedFilename());
    }
  }
  expect(saved).toEqual([
    'run-0a1b2c3d-events.jsonl', 'run-0a1b2c3d-report.json', 'run-0a1b2c3d-report.xml',
    'run-9f8e7d6c-events.jsonl', 'run-9f8e7d6c-report.json', 'run-9f8e7d6c-report.xml',
  ]);
});

// P4-AUDIT-40: a run outlives the deletion of its scenario; nothing links into a scenario page that is not there.

const GONE = 'dead0000-0000-4000-8000-00000000d00d';

test('a run of a deleted scenario is reached by its own page, not by a dead end of the scenario page', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(GONE, { id: 'r-orphan', status: 'running' })];
  await gw.install(page);

  // the overview links to the run page, the run page does not offer the scenario
  await page.goto('/');
  const row = page.getByTestId('running-scenarios').getByRole('link', { name: 'mqtt-outage' });
  await expect(row).toHaveAttribute('href', '/runs/r-orphan');
  await row.click();
  await expect(page).toHaveURL(/\/runs\/r-orphan$/);
  await expect(page.getByTestId('run-status')).toHaveText('Running');
  await expect(page.getByTestId('run-scenario-link')).toHaveCount(0);

  // the old link into the scenario still ends at the run
  await page.goto(`/scenarios/${GONE}?run=r-orphan`);
  await expect(page.getByTestId('scenario-not-found')).toContainText(`There is no scenario "${GONE}"`);
  await page.getByTestId('not-found-run-link').click();
  await expect(page).toHaveURL(/\/runs\/r-orphan$/);

  // a stored scenario keeps the links to its page
  gw.runs = [runOf(SCN_MQTT, { id: 'r-live', status: 'running' })];
  await page.goto('/');
  await expect(page.getByTestId('running-scenarios').getByRole('link', { name: 'mqtt-outage' })).toHaveAttribute('href', new RegExp(`/scenarios/${SCN_MQTT}\\?run=r-live`));
  await page.goto('/runs/r-live');
  await expect(page.getByTestId('run-scenario-link')).toBeVisible();
});

test('deleting a scenario says that its runs go on', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r-live', status: 'running' })];
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByRole('button', { name: 'Delete mqtt-outage' }).click();
  await expect(detail(page)).toContainText('A run of it that is queued or running is not stopped by that: it keeps going on its own copy of the scenario');
  await expect(detail(page)).not.toContainText('listed by the preview');
});

// P4-AUDIT-39: the lists of runs are windows; what must not be missed is asked for by the gateway's filters.

const ended = (ms: number) => new Date(Date.now() - ms).toISOString();
const passedRuns = (n: number, scenario: string, prefix: string) =>
  Array.from({ length: n }, (_, i) =>
    runOf(scenario, { id: `${prefix}-${String(i).padStart(3, '0')}`, status: 'passed', ended_at: ended(60_000), ...(scenario === SCN_BOOT ? { scenario: lab().config.scenarios![SCN_BOOT]! } : {}) }),
  );

test('the overview finds a queued run behind more than 50 newer runs, by asking for the status', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r-queued', status: 'queued' }), ...passedRuns(60, SCN_BOOT, 'r-new')];
  await gw.install(page);
  await page.goto('/');
  const card = page.getByTestId('running-scenarios');
  await expect(card.getByTestId('running-scenario')).toHaveCount(1);
  await expect(card).toContainText('mqtt-outage');
  const asked = gw.requestsTo('GET', '/runs').map((r) => r.query.status);
  expect(asked).toEqual(expect.arrayContaining(['running', 'queued']));
});

test('the last result of a scenario older than the newest 200 runs is asked for, not read as "Never run"', async ({ page }) => {
  const gw = lab();
  gw.runs = [
    runOf(SCN_BOOT, { id: 'r-old', scenario: lab().config.scenarios![SCN_BOOT]!, status: 'failed', ended_at: ended(3_600_000) }),
    ...passedRuns(210, SCN_MQTT, 'r-new'),
  ];
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-last')).toHaveText(/^Passed · /);
  await expect(item(page, 'boot-loop').getByTestId('scenario-last')).toHaveText(/^Failed · /);
  await expect(page.getByText('Never run')).toHaveCount(0);
  expect(gw.requestsTo('GET', '/runs').some((r) => r.query.scenario === SCN_BOOT && r.query.limit === '1')).toBe(true);
});

test('the Runs tab asks for the runs of the scenario and pages them', async ({ page }) => {
  const gw = lab();
  gw.runs = [
    runOf(SCN_BOOT, { id: 'r-boot', scenario: lab().config.scenarios![SCN_BOOT]!, status: 'passed', ended_at: ended(3_600_000) }),
    ...passedRuns(130, SCN_MQTT, 'r-mqtt'),
    ...passedRuns(210, SCN_BOOT, 'r-other'),
  ];
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_BOOT}?tab=runs`);
  // all 211 runs of boot-loop are older than 50 newer ones of its own: 50 are shown, the rest on request
  await expect(page.getByTestId('run-row')).toHaveCount(50);
  expect(gw.requestsTo('GET', '/runs').some((r) => r.query.scenario === SCN_BOOT && r.query.limit === '50')).toBe(true);
  await page.getByTestId('runs-more').click();
  await expect(page.getByTestId('run-row')).toHaveCount(150);
  await page.getByTestId('runs-more').click();
  await expect(page.getByTestId('run-row')).toHaveCount(211);
  await expect(page.getByTestId('runs-more')).toHaveCount(0);
  // the other scenario's runs do not appear
  await page.goto(`/scenarios/${SCN_MQTT}?tab=runs`);
  await expect(page.getByTestId('run-row')).toHaveCount(50);
  await page.getByTestId('runs-more').click();
  await expect(page.getByTestId('run-row')).toHaveCount(130);
});

// P4-AUDIT-12: a runs query that loads or failed is not an answer ("Never run", "has not been run yet", "not there any more").

test('the runs are slow: the list and the Runs tab say they load, and never "Never run"', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r1', status: 'passed', ended_at: new Date(Date.now() - 120_000).toISOString() })];
  let release!: () => void;
  gw.holding['/runs'] = new Promise<void>((r) => (release = r));
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}?tab=runs`);
  await expect(page.getByTestId('scenario-item')).toHaveCount(2);
  await expect(page.getByTestId('scenario-last')).toHaveText(['Loading…', 'Loading…']);
  await expect(page.getByTestId('runs-loading')).toHaveText('Loading the runs…');
  await expect(page.getByText('Never run')).toHaveCount(0);
  await expect(page.getByTestId('runs-empty')).toHaveCount(0);
  release();
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-last')).toHaveText(/^Passed · /);
  await expect(item(page, 'boot-loop').getByTestId('scenario-last')).toHaveText('Never run');
  await expect(page.getByTestId('run-row')).toHaveCount(1);
});

test('the runs failed: the list says the last result is unknown, the Runs tab shows the error, Try again reads them', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r1', status: 'passed', ended_at: new Date(Date.now() - 120_000).toISOString() })];
  gw.failing['/runs'] = 500;
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}?tab=runs`);
  await expect(page.getByTestId('scenario-last')).toHaveText(['Last result unknown', 'Last result unknown']);
  await expect(page.getByTestId('scenario-list').getByTestId('load-error')).toContainText('The last results are not loaded');
  await expect(page.getByText('Never run')).toHaveCount(0);
  const tab = page.getByTestId('runs-tab');
  await expect(tab.getByTestId('load-error')).toContainText('The runs are not loaded');
  await expect(page.getByTestId('runs-empty')).toHaveCount(0);
  delete gw.failing['/runs'];
  await tab.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByTestId('run-row')).toHaveCount(1);
  await expect(item(page, 'mqtt-outage').getByTestId('scenario-last')).toHaveText(/^Passed · /);
  await expect(page.getByTestId('load-error')).toHaveCount(0);
});

test('a run that cannot be read is not "gone": only a 404 says so; slow, it loads', async ({ page }) => {
  const gw = lab();
  gw.runs = [runOf(SCN_MQTT, { id: 'r-x', status: 'passed', ended_at: new Date(Date.now() - 60_000).toISOString(), artifacts: artifactsOf('r-x') })];
  gw.failing['/runs/r-x'] = 500;
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}?run=r-x`);
  await expect(page.getByTestId('run-failed').getByTestId('load-error')).toContainText('The run is not loaded');
  await expect(page.getByTestId('run-gone')).toHaveCount(0);
  delete gw.failing['/runs/r-x'];
  await page.getByTestId('run-failed').getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByTestId('run-status')).toHaveText('Passed');
  await expect(page.getByTestId('run-failed')).toHaveCount(0);

  // a run the gateway does not have (any more) is gone
  await page.goto(`/scenarios/${SCN_MQTT}?run=nothing`);
  await expect(page.getByTestId('run-gone')).toContainText('This run is not there any more');
  await expect(page.getByTestId('run-failed')).toHaveCount(0);

  // slow: neither
  let release!: () => void;
  gw.holding['/runs/r-x'] = new Promise<void>((r) => (release = r));
  await page.goto(`/scenarios/${SCN_MQTT}?run=r-x`);
  await expect(page.getByTestId('run-loading')).toBeVisible();
  await expect(page.getByTestId('run-gone')).toHaveCount(0);
  release();
  await expect(page.getByTestId('run-status')).toHaveText('Passed');
});
