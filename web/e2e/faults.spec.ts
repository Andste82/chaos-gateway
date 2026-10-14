import { expect, test, type Page } from '@playwright/test';
import { CLIENT_RA, HUB, IOT, LINK, MockGateway, device, toast, withNetworks } from './mock';

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
  await expect(d.getByTestId('fault-preview')).toContainText('↓ 150 ms ±50, keep order, 3 % loss, ↓ 2 Mbit/s');

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

  // after an action, an open dialog still shows what it would send: in the panel and in the dialog
  await page.getByRole('button', { name: 'Add fault' }).click();
  await d.getByLabel('Latency (ms)').fill('120');
  await expect(panel).toContainText('This dialog: Add fault');
  await expect(panel.getByTestId('api-call')).toContainText('"latency": "120ms"');
  await d.getByText('</> API · what Apply sends').click();
  await expect(d.getByTestId('fault-api').getByTestId('api-call')).toContainText('POST /api/v1/overlays');
  await expect(d.getByTestId('fault-api').getByTestId('api-call')).toContainText('"latency": "120ms"');
  await page.keyboard.press('Escape');
  await expect(d).toBeHidden();
  await expect(panel).toContainText('Last action: Add fault');
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

  // a fault without any parameter changes nothing: the gateway would refuse it
  const saved = { latency: await d.getByLabel('Latency (ms)').inputValue(), rate: await d.getByLabel('Rate limit').inputValue() };
  for (const f of ['Latency (ms)', 'Jitter (ms)', 'Loss (%)', 'Rate limit']) await d.getByLabel(f).fill('');
  await expect(d.getByLabel('Latency (ms)')).toHaveAccessibleDescription('Set at least one parameter: latency, loss, a rate, …');
  await expect(apply).toBeDisabled();
  await d.getByLabel('Latency (ms)').fill(saved.latency);
  await d.getByLabel('Rate limit').fill(saved.rate);
  await expect(apply).toBeEnabled();

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
  // the edit keeps the overlay's remaining time (P3-M13-06)
  await expect(d.getByLabel('Duration')).toHaveValue('keep');
  await expect(d.getByLabel('Duration').locator('option:checked')).toHaveText(/^Keep: 4:\d\d left of 5 min$/);
  await d.getByLabel('Latency (ms)').fill('250');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(gw.overlays.filter((o) => o.id === ESP_FAULT)).toHaveLength(1);
  expect(gw.overlays.find((o) => o.id === ESP_FAULT)?.fault?.latency).toBe('250ms');
  expect(gw.requestsTo('DELETE', `/overlays/${ESP_FAULT}`)).toHaveLength(0);
  await expect(page.getByTestId('fault-row').filter({ hasText: 'ESP32-42' }).first()).toContainText('↑↓ 250 ms ±50');
});

test('editing a fault keeps its expiry; a TTL the dialog does not list is shown and kept (P3-M13-06)', async ({ page }) => {
  const gw = lab();
  const sensor = gw.devices[1]!;
  const LONG = 'f0000000-0000-4000-8000-0000000000c1';
  const expiresAt = new Date(Date.now() + 20 * 60_000);
  gw.overlays = [
    ...gw.overlays,
    {
      id: LONG,
      kind: 'fault',
      owner: { type: 'user', id: 'admin', name: 'admin' },
      target: { device: sensor.id },
      fault: { loss: '2%' },
      // set over the API with a TTL the dialog has no choice for
      ttl: '30m',
      expires_at: expiresAt.toISOString(),
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      generation: 42,
      state: 'effective',
    },
  ];
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: '2 % loss' }).getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  const duration = d.getByLabel('Duration');
  await expect(duration).toHaveValue('keep');
  await expect(duration.locator('option:checked')).toHaveText(/^Keep: (20:00|19:\d\d) left of 30 min$/);
  await expect(duration.locator('option')).toHaveText([/^Keep: /, '1 min', '5 min', '15 min', '1 hour', 'Until removed', '30 min (its own TTL)']);
  await expect(d.getByTestId('fault-preview')).toContainText(/expires at \d\d:\d\d as before/);
  await d.getByLabel('Loss (%)').fill('3');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, `Fault active on ${sensor.name}`)).toContainText(/Expires at \d\d:\d\d as before\./);

  // the TTL was sent again and the remaining time as expires_in, so the expiry stays where it was
  const sent = gw.requestsTo('POST', '/overlays')[0]!.body as { ttl: string; expires_in: string };
  expect(sent.ttl).toBe('30m');
  expect(Number(/^(\d+)s$/.exec(sent.expires_in)?.[1])).toBeGreaterThan(19 * 60);
  expect(Number(/^(\d+)s$/.exec(sent.expires_in)?.[1])).toBeLessThanOrEqual(20 * 60);
  const kept = gw.overlays.find((o) => o.id === LONG)!;
  expect(kept.fault).toEqual({ loss: '3%' });
  expect(kept.ttl).toBe('30m');
  expect(Math.abs(new Date(kept.expires_at!).getTime() - expiresAt.getTime())).toBeLessThan(5_000);

  // the next edit still keeps the same expiry, and the overlay's own TTL is still 30 minutes
  await page.getByTestId('fault-row').filter({ hasText: '3 % loss' }).getByRole('button', { name: /^Edit / }).click();
  await expect(duration).toHaveValue('keep');
  await expect(duration.locator('option:checked')).toHaveText(/^Keep: (20:00|19:\d\d) left of 30 min$/);
  await expect(duration.locator('option').last()).toHaveText('30 min (its own TTL)');
});

/** The sample lab with its WireGuard networks; the client rA is a device of its own too (plan §2.2.1). */
function remoteLab(): MockGateway {
  const gw = withNetworks(lab());
  gw.devices = [...gw.devices, device({ id: CLIENT_RA, name: 'rA', origin: 'wireguard_client', network: HUB })];
  return gw;
}

test("a fault on a WireGuard client's networks: the target offers the remote networks and the request carries the scope (P3-M13-08)", async ({ page }) => {
  const gw = remoteLab();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = dialog(page);
  const target = d.getByLabel('Target');
  await expect(target.locator('option')).toContainText([
    'Client networks · rA (10.50.0.0/24)',
    'Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)',
    'Remote network · a prefix inside them…',
  ]);
  await target.selectOption({ label: 'Client networks · rA (10.50.0.0/24)' });
  await expect(d.getByTestId('fault-preview')).toContainText('Client networks of rA ·');
  await d.getByLabel('Latency (ms)').fill('120');
  await d.getByLabel('Jitter (ms)').fill('');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Client networks of rA')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({ target: { remote_network: { client: CLIENT_RA } }, fault: { latency: '120ms' }, ttl: '15m' });
  await expect(page.getByTestId('fault-row').filter({ hasText: 'Client networks of rA' })).toContainText('↑↓ 120 ms');
});

test("a fault on a prefix inside a remote network: the prefix is checked under the target, the gateway's refusal stands there too (P3-M13-08)", async ({ page }) => {
  const gw = remoteLab();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = dialog(page);
  await d.getByLabel('Target').selectOption({ label: 'Remote network · a prefix inside them…' });
  await expect(d.getByText('Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.')).toBeVisible();
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Prefix inside a remote network').fill('10.80.0.0/24');
  // the gateway knows no remote network there
  gw.rejectOverlay = [{ path: '/target/remote_network/cidr', code: 'unknown_remote_network', message: '10.80.0.0/24 is not inside a client network or a route via a link' }];
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(d.getByText('10.80.0.0/24 is not inside a client network or a route via a link')).toBeVisible();
  await expect(d.getByLabel('Prefix inside a remote network')).toHaveAttribute('aria-invalid', 'true');
  await d.getByLabel('Prefix inside a remote network').fill('10.60.0.128/25');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Remote network 10.60.0.128/25')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[1]!.body).toMatchObject({ target: { remote_network: { cidr: '10.60.0.128/25' } } });
  // a configured fault on a link's prefixes goes through the draft with the same scope
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: 'Configuration · after preview' }).click();
  await d.getByLabel('Target').selectOption({ label: 'Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)' });
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: /^Preview revision/ })).toBeVisible();
  const faults = (gw.requestsTo('POST', '/revisions')[0]!.body as { faults: Record<string, { source: unknown }> }).faults;
  expect(Object.values(faults)[0]!.source).toEqual({ remote_network: { link: LINK } });
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
  // no parameter: it cannot go into the draft
  await d.getByLabel('Latency (ms)').fill('');
  await d.getByLabel('Jitter (ms)').fill('');
  await expect(d.getByText('Set at least one parameter: latency, loss, a rate, …')).toBeVisible();
  await expect(d.getByRole('button', { name: 'Add to the changes' })).toBeDisabled();
  await d.getByLabel('Latency (ms)').fill('30');
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
  const slow = page.getByTestId('fault-row').filter({ hasText: 'iot-slow' });
  await slow.getByRole('button', { name: /^Remove / }).click();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 fault/);
  // the deleted fault stays in the list, marked, until the draft is applied; Undo brings it back
  await expect(slow).toContainText('DELETED');
  await expect(slow).toContainText('removed when applied');
  await expect(slow.getByRole('button', { name: /^Remove |^Edit / })).toHaveCount(0);
  await slow.getByRole('button', { name: /^Undo the removal of / }).click();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  await expect(slow).not.toContainText('DELETED');
  await expect(slow.getByTestId('fault-state')).toHaveText('partially overridden');
  // removed and applied: it is gone
  await slow.getByRole('button', { name: /^Remove / }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 43' }).getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(slow).toHaveCount(0);
  expect((gw.requestsTo('POST', '/revisions')[0]!.body as { faults: Record<string, unknown> }).faults).toEqual({ [IOT_SLOW]: null });
});

test("edit a fault overlay of an API token: the admin's fault replaces it, and the dialog says so", async ({ page }) => {
  const gw = lab();
  const sensor = gw.devices[1]!;
  const TOKEN_FAULT = 'f0000000-0000-4000-8000-0000000000b1';
  gw.overlays = [
    ...gw.overlays,
    {
      id: TOKEN_FAULT,
      kind: 'fault',
      owner: { type: 'token', id: 't1', name: 'ci-runner' },
      target: { device: sensor.id },
      fault: { loss: '2%' },
      ttl: '1h',
      expires_at: new Date(Date.now() + 50 * 60_000).toISOString(),
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      generation: 42,
      state: 'effective',
    },
  ];
  await gw.install(page);
  await page.goto('/faults');
  const row = page.getByTestId('fault-row').filter({ hasText: '2 % loss' });
  await expect(row.getByTestId('fault-layer')).toHaveText('OVERLAY');
  await row.getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  await expect(d.getByTestId('fault-owner')).toHaveText('Set by the token ci-runner. Applying removes it and puts a fault of yours in its place.');
  await expect(d.getByLabel('Loss (%)')).toHaveValue('2');
  await d.getByLabel('Loss (%)').fill('4');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, `Fault active on ${sensor.name}`)).toBeVisible();

  // the gateway wrote a new overlay for the admin (the key includes the owner), and the token's went
  expect(gw.requestsTo('POST', '/overlays')).toHaveLength(1);
  expect(gw.requestsTo('DELETE', `/overlays/${TOKEN_FAULT}`)).toHaveLength(1);
  const onSensor = gw.overlays.filter((o) => o.target?.device === sensor.id);
  expect(onSensor).toHaveLength(1);
  // the token's overlay had 50 of its 60 minutes left: the admin's keeps them and its TTL of 1 hour (P3-M13-06)
  expect(onSensor[0]).toMatchObject({ owner: { type: 'user' }, fault: { loss: '4%' }, ttl: '1h' });
  expect(onSensor[0]!.expires_in).toBeUndefined();
  expect(Math.abs(new Date(onSensor[0]!.expires_at!).getTime() - (Date.now() + 50 * 60_000))).toBeLessThan(30_000);
  await expect(page.getByTestId('fault-row').filter({ hasText: '4 % loss' })).toHaveCount(1);
  await expect(page.getByTestId('fault-row').filter({ hasText: '2 % loss' })).toHaveCount(0);
  // the admin's own fault: no note
  await page.getByTestId('fault-row').filter({ hasText: '4 % loss' }).getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByTestId('fault-owner')).toHaveCount(0);
});

test('an edit whose old overlay cannot be removed closes with the new fault active and says the old one is left', async ({ page }) => {
  const gw = lab();
  const sensor = gw.devices[1]!;
  const TOKEN_FAULT = 'f0000000-0000-4000-8000-0000000000b2';
  gw.overlays = [
    ...gw.overlays,
    {
      id: TOKEN_FAULT,
      kind: 'fault',
      owner: { type: 'token', id: 't1', name: 'ci-runner' },
      target: { device: sensor.id },
      fault: { loss: '2%' },
      ttl: '1h',
      expires_at: new Date(Date.now() + 50 * 60_000).toISOString(),
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      generation: 42,
      state: 'effective',
    },
  ];
  await gw.install(page);
  // the removal of the token's overlay fails (the gateway is busy)
  await page.route(`**/api/v1/overlays/${TOKEN_FAULT}`, (route, request) =>
    request.method() === 'DELETE'
      ? route.fulfill({ status: 503, contentType: 'application/problem+json', body: JSON.stringify({ type: 'about:blank', title: 'busy', status: 503, code: 'busy', detail: 'the engine is busy' }) })
      : route.fallback(),
  );
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: '2 % loss' }).getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  await d.getByLabel('Loss (%)').fill('4');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  // the dialog closes: applying again would write a third overlay
  await expect(d).toBeHidden();
  const t = toast(page, 'The fault before the edit is still active');
  await expect(t).toContainText(`The edited fault is active on ${sensor.name}, but removing the old one failed`);
  await expect(t).toContainText('Remove it on the faults screen.');
  expect(gw.requestsTo('POST', '/overlays')).toHaveLength(1);
  // both are listed, so the old one can be removed from here
  await expect(page.getByTestId('fault-row').filter({ hasText: '4 % loss' })).toHaveCount(1);
  await expect(page.getByTestId('fault-row').filter({ hasText: '2 % loss' })).toHaveCount(1);
});

test("a configured fault the gateway refused in a candidate is marked, and its dialog shows why", async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/faults');
  const slow = page.getByTestId('fault-row').filter({ hasText: 'iot-slow' });
  await slow.getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  await d.getByLabel('Jitter (ms)').fill('40');
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  gw.rejectRevision = [
    { path: `/faults/${IOT_SLOW}/jitter`, code: 'invalid_jitter', message: 'the jitter needs more queue than this network has' },
    { path: `/faults/${IOT_SLOW}`, code: 'capacity_exceeded', message: 'no fault id is left on eth1' },
  ];
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview' });
  await expect(drawer.getByTestId('preview-invalid')).toContainText('Fault iot-slow · jitter: the jitter needs more queue than this network has');
  await drawer.getByRole('button', { name: 'Back to editing' }).click();

  await expect(slow).toContainText('NEEDS FIXING');
  await expect(slow.getByTestId('fault-problem')).toHaveText(['the jitter needs more queue than this network has', 'no fault id is left on eth1']);
  await slow.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByLabel('Jitter (ms)')).toHaveAttribute('aria-invalid', 'true');
  await expect(d.getByLabel('Jitter (ms)')).toHaveAccessibleDescription('the jitter needs more queue than this network has');
  await expect(d.getByRole('alert')).toHaveText('no fault id is left on eth1');
  // the edit takes the message away, and the fault is no longer marked
  await d.getByLabel('Jitter (ms)').fill('20');
  await expect(d.getByText('the jitter needs more queue than this network has')).toBeHidden();
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(slow).not.toContainText('NEEDS FIXING');
  await expect(slow.getByTestId('fault-problem')).toHaveCount(0);
});

test('closing the drawer while the candidate is created discards it when it arrives', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: 'iot-slow' }).getByRole('button', { name: /^Remove / }).click();
  let release!: () => void;
  gw.revisionGate = new Promise((r) => (release = r));
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview' });
  await expect(drawer).toBeVisible();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(1);
  await page.keyboard.press('Escape');
  await expect(drawer).toBeHidden();
  release();
  // revision 43 came for the closed drawer: it is discarded, and nothing previews it
  await expect.poll(() => gw.requestsTo('DELETE', '/revisions/43').length).toBe(1);
  expect(gw.requestsTo('POST', '/revisions/43/preview')).toHaveLength(0);
  gw.revisionGate = undefined;
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' })).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' }).getByTestId('preview-changes')).toContainText('iot-slow');
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
  const reqs = gw.requestsTo('GET', '/explain');
  expect(reqs).toHaveLength(1);
  expect(reqs[0]!.query).toEqual({ device: ESP, dst: '203.0.113.10', protocol: 'tcp', port: '8883' });
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
