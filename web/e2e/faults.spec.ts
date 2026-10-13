import { expect, test, type Page } from '@playwright/test';
import { IOT, MockGateway, toast } from './mock';

// M13: the add/edit fault dialog with its preview, validation errors, the faults screen (by family
// and scope, overlays and configuration marked, "overridden by", explain), the TTL display and the
// `</> API` panel.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const ESP_FAULT = 'f0000000-0000-4000-8000-000000000001';
const LTE_ON_IOT = 'f0000000-0000-4000-8000-000000000002';
const IOT_SLOW = 'c0f00000-0000-4000-8000-000000000009';

/** The sample lab with a configured fault on network IoT that ESP32-42's own fault overrides for it. */
function lab(): MockGateway {
  const gw = MockGateway.sample();
  gw.config = { ...gw.config, faults: { ...gw.config.faults, [IOT_SLOW]: { name: 'iot-slow', source: { network: IOT }, latency: '80ms' } } };
  gw.faultViews[IOT_SLOW] = {
    state: 'partially_overridden',
    counters: { packets: 512, bytes: 64_000, epoch: 1 },
    overridden_by: [{ family: 'impairment', layer: 'overlay', id: ESP_FAULT, level: 2, summary: 'upload 200 ms ± 50 ms' }],
  };
  gw.overlays = gw.overlays.map((o) =>
    o.id === LTE_ON_IOT ? { ...o, overridden_by: [{ family: 'impairment', layer: 'overlay', id: ESP_FAULT, level: 2 }] } : o,
  );
  return gw;
}

const dialog = (page: Page) => page.getByRole('dialog', { name: /^(Add|Edit) fault$/ });

test('add a fault on the device: presets, the live preview, Apply → the fault with its TTL, a toast and the API panel', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).click();
  const d = dialog(page);
  await expect(d.getByLabel('Target')).toBeDisabled();
  await expect(d.getByLabel('Target')).toHaveValue(`device:${ESP}`);
  await d.getByLabel('Traffic').selectOption({ label: 'HTTPS · TCP 443' });
  await d.getByRole('group', { name: 'Start from' }).getByRole('button', { name: 'LTE', exact: true }).click();
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('50');
  await expect(d.getByLabel('Jitter (ms)')).toHaveValue('10');
  await expect(d.getByLabel('Loss (%)')).toHaveValue('0.1');
  await d.getByRole('group', { name: 'Direction' }).getByRole('button', { name: 'Download' }).click();
  await expect(d.getByRole('group', { name: 'Direction' }).getByRole('button', { name: 'Download' })).toHaveAttribute('aria-pressed', 'true');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await expect(d.getByTestId('fault-preview')).toContainText('ESP32-42 · HTTPS · TCP 443 · download only → ↓ 50 ms ±10, 0.1 % loss · expires after 5 min');
  // Advanced: Bad LTE brings a rate and opens it
  await d.getByRole('button', { name: 'Bad LTE' }).click();
  await expect(d.getByRole('button', { name: 'Hide advanced' })).toHaveAttribute('aria-expanded', 'true');
  await expect(d.getByLabel('Rate limit')).toHaveValue('2 Mbit/s');
  await d.getByLabel('Keep packet order').check();
  await expect(d.getByTestId('fault-preview')).toContainText('↓ 150 ms ±50, 3 % loss, ↓ 2 Mbit/s');

  // the panel shows what the dialog would send, before anything is sent
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Add fault' }).click();
  await d.getByLabel('Traffic').selectOption({ label: 'MQTT · TCP 8883' });
  await d.getByLabel('Latency (ms)').fill('300');
  await d.getByLabel('Jitter (ms)').fill('');
  await d.getByLabel('Loss (%)').fill('');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  await expect(d).toBeHidden();
  const post = gw.requestsTo('POST', '/overlays');
  expect(post).toHaveLength(1);
  expect(post[0]!.body).toEqual({ target: { device: ESP }, fault: { protocol: 'tcp', ports: [8883], latency: '300ms' }, ttl: '5m' });
  // the same key (device, TCP 8883) replaced the fault that was there: it keeps its id, with the new TTL
  const row = page.getByTestId('active-fault').filter({ hasText: '↑↓ 300 ms' });
  await expect(row).toBeVisible();
  await expect(row.getByTestId('fault-expiry')).toHaveText(/^[45]:\d\d$/);

  await page.getByRole('button', { name: '</> API' }).click();
  await expect(page.getByRole('button', { name: '</> API' })).toHaveAttribute('aria-pressed', 'true');
  const panel = page.getByTestId('api-panel');
  await expect(panel).toContainText('Last action: Add fault');
  await expect(panel.getByTestId('api-call')).toContainText('POST /api/v1/overlays');
  await expect(panel.getByTestId('api-call')).toContainText('"latency": "300ms"');
});

test('validation errors are shown under the fields, and Apply waits until the form is valid', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).click();
  const d = dialog(page);
  const apply = d.getByRole('button', { name: 'Apply fault' });
  await d.getByLabel('Latency (ms)').fill('20');
  await d.getByLabel('Jitter (ms)').fill('50');
  await expect(d.getByText('Jitter cannot be larger than the latency.')).toBeVisible();
  await expect(d.getByLabel('Jitter (ms)')).toHaveAttribute('aria-invalid', 'true');
  await expect(d.getByLabel('Jitter (ms)')).toHaveAccessibleDescription('Jitter cannot be larger than the latency.');
  await expect(apply).toBeDisabled();
  await d.getByLabel('Loss (%)').fill('120');
  await expect(d.getByText('At most 100.')).toBeVisible();
  await d.getByRole('button', { name: /^Advanced/ }).click();
  await d.getByLabel('Rate limit').fill('fast');
  await expect(d.getByText('A rate like 2 Mbit/s, 500 kbit/s or 64kbit.')).toBeVisible();
  await d.getByLabel('Traffic').selectOption({ label: 'Other…' });
  await d.getByLabel('Ports').fill('80');
  await expect(d.getByText('Ports need the protocol TCP or UDP.')).toBeVisible();
  // fixed one by one: Apply is enabled again
  await d.getByLabel('Protocol').selectOption('tcp');
  await d.getByLabel('Rate limit').fill('500 kbit/s');
  await d.getByLabel('Loss (%)').fill('1');
  await d.getByLabel('Jitter (ms)').fill('5');
  await expect(apply).toBeEnabled();
  expect(gw.requestsTo('POST', '/overlays')).toHaveLength(0);

  // what the gateway refuses (422) stands under the field it names
  gw.rejectOverlay = [{ path: '/fault/rate', code: 'invalid_rate', message: 'the rate is below what the queue can shape' }];
  await apply.click();
  await expect(d.getByText('the rate is below what the queue can shape')).toBeVisible();
  await expect(d.getByLabel('Rate limit')).toHaveAttribute('aria-invalid', 'true');
  await expect(d).toBeVisible();
  // an edit of the form takes the gateway's message away
  await d.getByLabel('Rate limit').fill('1 Mbit/s');
  await expect(d.getByText('the rate is below what the queue can shape')).toBeHidden();
});

test('the faults screen groups by family, sorts by scope, marks overlays and configuration, and marks an overridden fault', async ({ page }) => {
  await lab().install(page);
  await page.goto('/faults');
  await expect(page.getByRole('heading', { name: 'Faults', exact: true })).toBeVisible();

  const impairment = page.getByTestId('fault-family-impairment');
  const rows = impairment.getByTestId('fault-row');
  await expect(impairment.getByRole('heading', { name: 'Impairment' })).toBeVisible();
  await expect(rows).toHaveCount(3);
  // device → network; at the same scope the overlay first
  await expect(rows.nth(0)).toContainText('ESP32-42');
  await expect(rows.nth(0).getByTestId('fault-layer')).toHaveText('OVERLAY');
  await expect(rows.nth(0)).toContainText('↑↓ 200 ms ±50');
  await expect(rows.nth(0)).toContainText('18 342 pkts');
  await expect(rows.nth(0).getByTestId('fault-expiry')).toHaveText(/^4:\d\d$/);
  await expect(rows.nth(1)).toContainText('Sensor-07');
  await expect(rows.nth(1).getByTestId('fault-layer')).toHaveText('CONFIGURATION');
  await expect(rows.nth(1).getByTestId('fault-expiry')).toHaveText('configured');
  await expect(rows.nth(2)).toContainText('Network IoT');
  await expect(rows.nth(2)).toContainText('iot-slow');
  await expect(rows.nth(2).getByTestId('fault-state')).toHaveText('partially overridden');
  await expect(rows.nth(2).getByTestId('overridden-by')).toHaveText('overridden by ESP32-42 ↑↓ 200 ms ±50, 5 % loss (overlay)');
  await expect(rows.nth(0).getByTestId('overridden-by')).toHaveCount(0);

  // the profile activation is a family of its own, and names what overrides it too
  const profiles = page.getByTestId('fault-family-profile').getByTestId('fault-row');
  await expect(profiles).toHaveCount(1);
  await expect(profiles.nth(0)).toContainText('profile LTE');
  await expect(profiles.nth(0).getByTestId('overridden-by')).toContainText('overridden by ESP32-42');
});

test('edit a fault overlay on the faults screen: the same key is written again and keeps its id', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/faults');
  const row = page.getByTestId('fault-row').filter({ hasText: 'ESP32-42' }).first();
  await row.getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  await expect(d).toBeVisible();
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('200');
  await expect(d.getByLabel('Jitter (ms)')).toHaveValue('50');
  await expect(d.getByLabel('Loss (%)')).toHaveValue('5');
  await expect(d.getByLabel('Traffic')).toHaveValue('mqtts');
  await expect(d.getByLabel('Duration')).toHaveValue('5m');
  await d.getByLabel('Latency (ms)').fill('250');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(gw.overlays.filter((o) => o.id === ESP_FAULT)).toHaveLength(1);
  expect(gw.overlays.find((o) => o.id === ESP_FAULT)?.fault?.latency).toBe('250ms');
  expect(gw.requestsTo('DELETE', `/overlays/${ESP_FAULT}`)).toHaveLength(0);
  await expect(page.getByTestId('fault-row').filter({ hasText: 'ESP32-42' }).first()).toContainText('↑↓ 250 ms ±50');
});

test('a configured fault goes through the unapplied-changes bar, the preview and the apply', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /Configuration/ }).click();
  await expect(d.getByLabel('Duration')).toHaveCount(0);
  await d.getByLabel('Target').selectOption({ label: 'Network · IoT' });
  await d.getByLabel('Name').fill('iot-delay');
  await d.getByLabel('Latency (ms)').fill('30');
  await d.getByLabel('Jitter (ms)').fill('');
  await expect(d.getByTestId('fault-preview')).toContainText('Network IoT · All traffic · upload & download → ↑↓ 30 ms · configured until removed');
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(d).toBeHidden();
  expect(gw.requestsTo('POST', '/overlays')).toHaveLength(0);

  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 fault · not applied yet/);
  const row = page.getByTestId('fault-row').filter({ hasText: 'iot-delay' });
  await expect(row).toContainText('Network IoT');
  await expect(row).toContainText('NEW');
  await expect(row).toContainText('not applied yet');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('fault iot-delay added');
  const body = gw.requestsTo('POST', '/revisions')[0]!.body as { faults: Record<string, unknown> };
  expect(Object.values(body.faults)).toEqual([{ name: 'iot-delay', source: { network: IOT }, latency: '30ms' }]);
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  await expect(row.getByTestId('fault-layer')).toHaveText('CONFIGURATION');
  await expect(row).not.toContainText('NEW');
});

test('removing: an overlay at once, a configured fault through the draft', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: 'profile LTE' }).getByRole('button', { name: /^Remove / }).click();
  await expect(toast(page, 'Fault removed')).toBeVisible();
  expect(gw.requestsTo('DELETE', `/overlays/${LTE_ON_IOT}`)).toHaveLength(1);
  await expect(page.getByTestId('fault-family-profile')).toHaveCount(0);
  await page.getByTestId('fault-row').filter({ hasText: 'iot-slow' }).getByRole('button', { name: /^Remove / }).click();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 fault/);
});

test('Explain names the access verdict and the winning fault per family with the ones that lost', async ({ page }) => {
  const gw = lab();
  gw.explanation = {
    generation: 42,
    source: { ip: '10.10.0.42', device: { id: ESP, name: 'ESP32-42' } },
    destination: { ip: '203.0.113.10' },
    access: { verdict: 'allow', layer: 'access_matrix', reason: 'IoT → uplink: allow (default)' },
    faults: [
      {
        family: 'impairment',
        winner: { family: 'impairment', layer: 'overlay', id: ESP_FAULT, level: 2, summary: 'upload 200 ms ± 50 ms' },
        overridden: [
          { family: 'impairment', layer: 'config', id: IOT_SLOW, name: 'iot-slow', level: 8, reason: 'level 2 beats level 8' },
          { family: 'impairment', layer: 'overlay', id: LTE_ON_IOT, profile: { name: 'lte' }, level: 8, reason: 'level 2 beats level 8' },
        ],
      },
    ],
  };
  await gw.install(page);
  await page.goto('/faults');
  const explain = page.getByRole('region', { name: 'Explain' });
  await expect(explain.getByRole('button', { name: 'Explain' })).toBeDisabled();
  await explain.getByLabel('Device').selectOption({ label: 'ESP32-42' });
  await explain.getByLabel('Destination').fill('203.0.113.10');
  await explain.getByLabel('Protocol').selectOption('tcp');
  await explain.getByLabel('Port').fill('8883');
  await explain.getByRole('button', { name: 'Explain' }).click();
  const result = page.getByTestId('explain-result');
  await expect(result).toContainText('Allow');
  await expect(result).toContainText('by the access matrix — IoT → uplink: allow (default)');
  await expect(result.getByTestId('explain-family')).toContainText('Impairment: an overlay (upload 200 ms ± 50 ms) wins (overlay, level 2)');
  await expect(result.getByTestId('explain-family')).toContainText('fault iot-slow lost: level 2 beats level 8');
  await expect(result.getByTestId('explain-family')).toContainText('profile LTE lost: level 2 beats level 8');
  const req = gw.requestsTo('GET', '/explain')[0]!;
  expect(req).toBeDefined();
});

test('empty: no faults, the dashed box offers Add fault', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.overlays = [];
  gw.config = { ...gw.config, faults: {} };
  await gw.install(page);
  await page.goto('/faults');
  await expect(page.getByTestId('empty-state')).toContainText('No faults: all traffic passes unimpaired.');
  await page.getByTestId('empty-state').getByRole('button', { name: 'Add fault' }).click();
  await expect(dialog(page)).toBeVisible();
});

test('an expiring fault counts down on the faults screen and says so when it is gone', async ({ page }) => {
  const gw = lab();
  gw.overlays = gw.overlays.map((o) => (o.id === ESP_FAULT ? { ...o, expires_at: new Date(Date.now() + 3_000).toISOString() } : o));
  await gw.install(page);
  await page.goto('/faults');
  const row = page.getByTestId('fault-row').filter({ hasText: 'ESP32-42' }).first();
  await expect(row.getByTestId('fault-expiry')).toHaveText(/^0:0[0-3]$/);
  await expect(row.getByTestId('fault-expiry')).toHaveText('0:00', { timeout: 5_000 });
  gw.overlays = gw.overlays.filter((o) => o.id !== ESP_FAULT);
  await gw.emit(page, { type: 'overlay_expired', subject: { kind: 'overlay', id: ESP_FAULT, name: 'ESP32-42 fault' } });
  await expect(toast(page, 'ESP32-42 fault expired')).toBeVisible();
  await expect(page.getByTestId('fault-row').filter({ hasText: 'ESP32-42' })).toHaveCount(0);
});
