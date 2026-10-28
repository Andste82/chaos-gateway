import { expect, test, type Page } from '@playwright/test';
import type { CalibrationRequest, DiagnosticRequest, ProfileView } from '../src/api/generated/model';
import { defaultCalibration, MockGateway, sampleProbe } from './mock';

// M25 (plan §2.12, §2.17 "Probes"): the probes on the Diagnostics screen, a diagnostic from a probe, the self-test "measure this
// profile" in its dialog and on the Profiles screen, against the mocked API: the list with the address, the choice *From*, the
// request each sends, the verdict and the checks, the refusals under the fields, and a gateway without probes.

const PROBE = '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8';
const LTE = 'b0000000-0000-5000-8000-0000000000e1';
const BAD_LTE = 'b0000000-0000-5000-8000-0000000000e2';
const TLS = 'b0000000-0000-5000-8000-0000000000e4';

function lab(opts: { probes?: boolean; noAddress?: boolean } = {}): MockGateway {
  const gw = MockGateway.sample();
  const profiles: ProfileView[] = [
    { id: LTE, builtin: true, available: true, config: { name: 'lte', parts: { impairment: { latency: '50ms', jitter: '10ms', loss: '0.1%' } } } },
    { id: BAD_LTE, builtin: true, available: true, config: { name: 'bad-lte', parts: { impairment: { latency: '150ms', jitter: '50ms', loss: '3%', rate: '2Mbit' } } } },
    { id: TLS, builtin: true, available: true, config: { name: 'tls-broken', parts: { tls: { case: 'handshake_reset' } } } },
  ];
  gw.profiles = profiles;
  if (opts.probes !== false) {
    gw.capabilities.features = [...gw.capabilities.features, 'probes'];
    gw.probes = [sampleProbe({ address: opts.noAddress ? '' : '10.10.0.77' })];
  }
  return gw;
}

async function open(gw: MockGateway, page: Page, path = '/diagnostics') {
  await gw.install(page);
  await page.goto(path);
}

const run = (page: Page) => page.getByTestId('run-diagnostic');
const diagnostics = (gw: MockGateway) => gw.requestsTo('POST', '/diagnostics').map((r) => r.body as DiagnosticRequest);
const calibrations = (gw: MockGateway) => gw.requestsTo('POST', `/probes/${PROBE}/calibrate`).map((r) => r.body as CalibrationRequest);
const dialog = (page: Page) => page.getByRole('dialog', { name: 'Measure with a probe' });

test('the probes are listed with their network, address and state', async ({ page }) => {
  await open(lab(), page);
  const row = page.locator('[data-probe="probe-a"]');
  await expect(row).toBeVisible();
  await expect(row).toContainText('IoT');
  await expect(row).toContainText('10.10.0.77');
  await expect(row).toContainText('02:28:b4:ce:f6:08');
  await expect(row).toContainText('Online');
  await expect(row.getByRole('link', { name: 'probe-a' })).toHaveAttribute('href', `/devices/${PROBE}`);
});

test('a probe that has no address yet says so', async ({ page }) => {
  await open(lab({ noAddress: true }), page);
  await expect(page.locator('[data-probe="probe-a"]')).toContainText('No address yet');
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  await expect(dialog(page).getByTestId('calibrate-no-address')).toContainText('DHCP');
  await expect(dialog(page).getByTestId('calibrate-run')).toBeDisabled();
});

test('a gateway with the feature and no probe explains what a probe is', async ({ page }) => {
  const gw = lab();
  gw.probes = [];
  await open(gw, page);
  await expect(page.getByTestId('probes-empty')).toContainText('virtual client');
  await expect(page.getByTestId('diagnostic-from')).toHaveCount(0);
});

test('a gateway without probes shows neither the list nor the choice', async ({ page }) => {
  await open(lab({ probes: false }), page);
  await expect(page.getByRole('heading', { name: 'Probes' })).toHaveCount(0);
  await expect(page.getByTestId('diagnostic-from')).toHaveCount(0);
  await open(lab({ probes: false }), page, '/profiles');
  await expect(page.getByTestId('profile-measure')).toHaveCount(0);
});

test('Diagnose from here chooses the probe, and the request names it', async ({ page }) => {
  const gw = lab();
  await open(gw, page);
  await expect(page.getByTestId('diagnostic-from')).toHaveValue('');
  await expect(page.getByTestId('diagnostic-from-hint')).toContainText('faults of a device do not apply');
  await page.getByRole('button', { name: 'Diagnose from probe-a' }).click();
  await expect(page.getByTestId('diagnostic-from')).toHaveValue(PROBE);
  await expect(page.getByTestId('diagnostic-from-hint')).toContainText('faults, rules and profiles that target the probe');
  await page.getByRole('group', { name: 'Diagnostic' }).getByRole('button', { name: 'Ping' }).click();
  await expect(page.getByTestId('diagnostic-from')).toHaveValue(PROBE); // the choice stays when the type changes
  await page.getByLabel('Host').fill('203.0.113.10');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Succeeded');
  expect(diagnostics(gw)).toEqual([{ type: 'ping', host: '203.0.113.10', from: { probe: PROBE } }]);
  // the name, not the id the request holds, in the result and in the session list (P6-AUDIT-44)
  await expect(page.getByTestId('diagnostic-result')).toContainText('from the probe probe-a');
  await expect(page.getByTestId('diagnostic-result')).not.toContainText(PROBE);
  await page.getByTestId('diagnostic-from').selectOption('');
  await run(page).click();
  const rows = page.getByTestId('diagnostic-history').getByRole('button');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('203.0.113.10');
  await expect(rows.nth(0)).toContainText('from the gateway');
  await expect(rows.nth(1)).toContainText('203.0.113.10');
  await expect(rows.nth(1)).toContainText('from probe-a');
  await expect(rows.nth(1)).not.toContainText(PROBE);
});

test('the choice From is filled in from the address, by name', async ({ page }) => {
  const gw = lab();
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=8883&from=probe-a');
  await expect(page.getByTestId('diagnostic-from')).toHaveValue(PROBE);
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Succeeded');
  expect(diagnostics(gw)).toEqual([{ type: 'tcp', host: '203.0.113.10', port: 8883, from: { probe: PROBE } }]);
  // back to the gateway: the request has no from
  await page.getByTestId('diagnostic-from').selectOption('');
  await run(page).click();
  await expect.poll(() => diagnostics(gw).length).toBe(2);
  expect(diagnostics(gw)[1]).toEqual({ type: 'tcp', host: '203.0.113.10', port: 8883 });
});

test('a probe that is not ready is the gateway’s refusal, shown as it says it', async ({ page }) => {
  const gw = lab();
  gw.diagnose = () => ({ refuse: { status: 503, code: 'unavailable', detail: 'the probe "probe-a" has no address yet: it gets one by DHCP from its network, which needs DHCP switched on' } });
  await open(gw, page, '/diagnostics?type=ping&host=203.0.113.10&from=probe-a');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-refused')).toContainText('has no address yet');
});

test('a profile is measured on a probe: the request, the verdict, the checks and the values', async ({ page }) => {
  const gw = lab();
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await expect(d.getByTestId('calibrate-probe')).toHaveValue(PROBE);
  await d.getByRole('group', { name: 'Measure' }).getByRole('button', { name: 'A profile' }).click();
  // the profiles with an impairment part are on offer, the TLS one is not
  await expect(d.getByTestId('calibrate-profile').locator('option')).toHaveText(['Choose a profile', 'lte', 'bad-lte']);
  await d.getByTestId('calibrate-profile').selectOption(LTE);
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-verdict')).toHaveText('Within tolerance');
  await expect(d.getByTestId('calibrate-sentence')).toContainText('matches the parameters within the tolerances');
  await expect(d.locator('[data-check="latency"]')).toContainText('Delay');
  await expect(d.locator('[data-check="latency"]')).toHaveAttribute('data-ok', 'true');
  await expect(d.getByTestId('calibrate-rows')).toContainText('Round trip (median)');
  await expect(d.getByTestId('calibrate-rows')).toContainText('102 ms');
  await expect(d.getByTestId('calibrate-rows')).toContainText('99 %');
  await expect(d).toContainText('2000 of 2000 datagrams came back');
  expect(calibrations(gw)).toEqual([{ profile: LTE, host: '203.0.113.10' }]);
});

test('a measurement that does not match says which check failed', async ({ page }) => {
  const gw = lab();
  gw.calibrate = () => ({
    ...defaultCalibration({ host: '203.0.113.10', profile: LTE }),
    within_tolerance: false,
    measured: { baseline_rtt_ms: 1, rtt_median_ms: 52.1, loss_percent: 0 },
    checks: [
      { name: 'latency', ok: false, detail: 'the median round trip is 52.10 ms against 1.00 ms unimpaired: 51.10 ms added, 100.00 ms expected (tolerance ±7.00 ms)' },
      { name: 'loss', ok: true, detail: '0 of 2000 datagrams were lost' },
    ],
  });
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Measure' }).getByRole('button', { name: 'A profile' }).click();
  await d.getByTestId('calibrate-profile').selectOption(LTE);
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByLabel('Port').fill('9001');
  await d.getByLabel('Duration').fill('20s');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-verdict')).toHaveText('Out of tolerance');
  await expect(d.getByTestId('calibrate-sentence')).toHaveText('What the probe measured does not match the parameters: delay.');
  await expect(d.locator('[data-check="latency"]')).toHaveAttribute('data-ok', 'false');
  await expect(d.locator('[data-check="latency"]')).toContainText('does not match');
  await expect(d.locator('[data-check="loss"]')).toHaveAttribute('data-ok', 'true');
  expect(calibrations(gw)).toEqual([{ profile: LTE, host: '203.0.113.10', port: 9001, duration: '20s' }]);
});

test('the probe’s faults now are measured without a profile', async ({ page }) => {
  const gw = lab();
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await expect(d).toContainText('No overlay is made');
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-verdict')).toHaveText('Within tolerance');
  expect(calibrations(gw)).toEqual([{ host: '203.0.113.10' }]);
});

test('the form checks itself and the gateway’s refusal stands under its field', async ({ page }) => {
  const gw = lab();
  gw.calibrate = () => ({ refuse: { status: 422, code: 'validation_failed', detail: 'the request is not valid', errors: [{ path: '/duration', code: 'invalid_duration', message: 'the duration must be 5s to 5m0s' }] } });
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Measure' }).getByRole('button', { name: 'A profile' }).click();
  await d.getByTestId('calibrate-run').click();
  await expect(d).toContainText('Choose the profile to measure.');
  await expect(d).toContainText('The echo server is needed.');
  expect(calibrations(gw)).toEqual([]);
  await d.getByTestId('calibrate-profile').selectOption(LTE);
  await d.getByLabel('Echo server').fill('broker.example.com');
  await expect(d).toContainText('An IPv4 address.');
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByLabel('Duration').fill('1s');
  await expect(d).toContainText('From 5s to 5m.');
  await d.getByLabel('Duration').fill('30s');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-refused')).toBeVisible();
  await expect(d).toContainText('the duration must be 5s to 5m0s');
});

test('the call waits for the whole duration and says so', async ({ page }) => {
  const gw = lab();
  gw.calibrationDelay = 1500;
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByLabel('Duration').fill('45s');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-running')).toHaveText('Measuring for 45 s…');
  await expect(d.getByTestId('calibrate-run')).toBeDisabled();
  await expect(d.getByTestId('calibrate-verdict')).toBeVisible();
  await expect(d.getByTestId('calibrate-running')).toHaveCount(0);
});

test('the measuring dialog cannot be closed while it measures, and the next opening starts clean (P6-AUDIT-41)', async ({ page }) => {
  const gw = lab();
  gw.probes = [...gw.probes, sampleProbe({ id: '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b9', name: 'probe-b', address: '10.10.0.78' })];
  gw.calibrationDelay = 1500;
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-running')).toBeVisible();
  // neither Escape, nor a click outside, nor the buttons close it
  await page.keyboard.press('Escape');
  await page.mouse.click(5, 5);
  await expect(d).toBeVisible();
  await expect(d.getByRole('button', { name: 'Close', exact: true }).first()).toBeDisabled();
  await expect(d.getByRole('button', { name: 'Close', exact: true }).last()).toBeDisabled();
  // the answer arrives in the dialog that asked
  await expect(d.getByTestId('calibrate-verdict')).toBeVisible();
  await expect(d.getByTestId('calibrate-running')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(d).toBeHidden();
  // another opening shows neither the measuring nor the old result
  await page.getByRole('button', { name: 'Measure with probe-b' }).click();
  await expect(d).toBeVisible();
  await expect(d.getByTestId('calibrate-running')).toHaveCount(0);
  await expect(d.getByTestId('calibrate-result')).toHaveCount(0);
  await expect(d.getByLabel('Echo server')).toHaveValue('');
  await d.getByLabel('Echo server').fill('203.0.113.11');
  await expect(d.getByTestId('calibrate-run')).toBeEnabled();
});

test('the end of a measurement is announced by a status that was there before (P6-AUDIT-49)', async ({ page }) => {
  const gw = lab();
  await open(gw, page);
  await page.getByRole('button', { name: 'Measure with probe-a' }).click();
  const d = dialog(page);
  const status = d.getByTestId('calibrate-announcement');
  await expect(status).toHaveAttribute('role', 'status');
  await expect(status).toHaveText('');
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByTestId('calibrate-run').click();
  await expect(status).toHaveText(/^Within tolerance: /);
});

test('a list of probes the gateway did not deliver says so and does not claim there are none (P6-AUDIT-47)', async ({ page }) => {
  const gw = lab();
  gw.failing = { '/probes': 500 };
  await open(gw, page);
  const card = page.getByRole('region', { name: 'Probes' });
  await expect(card.getByTestId('load-error')).toContainText('The probes are not loaded: the gateway failed to read /probes.');
  await expect(page.getByTestId('probes-empty')).toHaveCount(0);
  gw.failing = {};
  await card.getByRole('button', { name: 'Try again' }).click();
  await expect(page.locator('[data-probe="probe-a"]')).toContainText('10.10.0.77');
  await expect(card.getByTestId('load-error')).toHaveCount(0);
  // a later read that fails keeps the rows and says so
  gw.failing = { '/probes': 500 };
  await expect(card.getByTestId('load-error')).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('[data-probe="probe-a"]')).toBeVisible();
});

test('a profile card has Measure with probe, which opens the dialog with the profile chosen', async ({ page }) => {
  const gw = lab();
  await open(gw, page, '/profiles');
  await expect(page.getByTestId('profile-measure')).toHaveCount(2); // lte and bad-lte, not the TLS profile
  await page.getByRole('button', { name: 'Measure Bad LTE with a probe' }).click();
  const d = dialog(page);
  await expect(d.getByTestId('calibrate-profile')).toHaveValue(BAD_LTE);
  await d.getByLabel('Echo server').fill('203.0.113.10');
  await d.getByTestId('calibrate-run').click();
  await expect(d.getByTestId('calibrate-verdict')).toBeVisible();
  expect(calibrations(gw)).toEqual([{ profile: BAD_LTE, host: '203.0.113.10' }]);
});

test('Measure with probe waits for a probe', async ({ page }) => {
  const gw = lab();
  gw.probes = [];
  await open(gw, page, '/profiles');
  const b = page.getByRole('button', { name: 'Measure LTE with a probe' });
  await expect(b).toBeDisabled();
  await expect(b).toHaveAttribute('title', /No probe/);
});
