import { expect, test } from '@playwright/test';

// M13 (plan §5): create a fault in the UI against the real gateway in the namespace testbed (the Go
// test TestAFaultCreatedInTheBrowserTakesEffectOnTheWire measures its effect afterwards); on the way,
// a validation error, a conflicting change made through the API (the conflict dialog) and the faults
// screen marking the overridden fault. Under software emulation an apply takes minutes.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
};
const applyTimeout = 10 * 60_000;

test('a configured fault through preview and apply, a conflict with an API change, a device fault, an overridden fault', async ({ page, request }) => {
  const deviceMAC = env('CHAOSGW_DEVICE_MAC');
  const token = env('CHAOSGW_TOKEN');

  await page.goto('/faults');
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel('Password').fill(env('CHAOSGW_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page.getByRole('heading', { name: 'Faults', exact: true })).toBeVisible();

  // 1. a configured fault on the network IoT goes into the draft
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add fault' });
  await dialog.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /Configuration/ }).click();
  await dialog.getByLabel('Target').selectOption({ label: 'Network · IoT' });
  await dialog.getByLabel('Name').fill('iot-delay');
  await dialog.getByLabel('Latency (ms)').fill('30');
  await dialog.getByLabel('Jitter (ms)').fill('');
  await dialog.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(page.getByTestId('unapplied-bar')).toContainText('1 change in 1 fault · not applied yet');

  // 2. meanwhile a test changes the configuration through the API with its token
  const auth = { Authorization: `Bearer ${token}` };
  const state = await (await request.get('/api/v1/state', { headers: auth })).json();
  const created = await request.post(`/api/v1/revisions?message=${encodeURIComponent('a group from a test')}`, {
    headers: { ...auth, 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${state.active_revision}"` },
    data: { groups: { 'c0a80001-0000-4000-8000-0000000000aa': { name: 'api-made' } } },
  });
  expect(created.status()).toBe(201);
  const rev = await created.json();
  const applied = await request.post(`/api/v1/revisions/${rev.id}/apply`, { headers: auth, timeout: applyTimeout });
  expect(applied.status()).toBe(200);

  // 3. Preview & apply meets the conflict: who changed what; reload and reapply mine
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict).toContainText(`Revision ${rev.id} was applied by token ci-runner`);
  await expect(conflict.getByTestId('conflict-theirs')).toContainText('api-made');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('iot-delay');
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  const drawer = page.getByRole('dialog', { name: /^Preview revision \d+$/ });
  await expect(drawer).toContainText(`Based on revision ${rev.id}.`, { timeout: applyTimeout });
  await expect(drawer.getByTestId('preview-changes')).toContainText('iot-delay');
  await drawer.getByRole('button', { name: 'Show Linux changes' }).click();
  await expect(drawer.getByTestId('preview-linux')).toContainText('netem');
  await drawer.getByRole('button', { name: /^Apply revision/ }).click();
  // the drawer closes when the gateway answered the apply (a failure stays in it); the toast is gone after 6 s,
  // which an emulated apply can outlast before the assertion looks, so the lasting state is asserted
  await expect(drawer).toBeHidden({ timeout: applyTimeout });
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  const network = page.getByTestId('fault-row').filter({ hasText: 'iot-delay' });
  await expect(network).not.toContainText('NEW');
  await expect(network).not.toContainText('not applied yet');

  // 4. a fault on device A, from its page; a validation error first
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Devices' }).click();
  const row = page.getByTestId('device-row').filter({ hasText: deviceMAC });
  await expect(row).toBeVisible({ timeout: 5 * 60_000 });
  await row.getByRole('link').click();
  await expect(page.getByTestId('device-identity')).toContainText(deviceMAC);
  const name = (await page.getByRole('heading', { level: 1 }).textContent())?.trim() ?? '';
  await page.getByRole('button', { name: 'Add fault' }).click();
  const add = page.getByRole('dialog', { name: 'Add fault' });
  await add.getByLabel('Latency (ms)').fill('20');
  await add.getByLabel('Jitter (ms)').fill('50');
  await expect(add.getByText('Jitter cannot be larger than the latency.')).toBeVisible();
  await expect(add.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await add.getByLabel('Latency (ms)').fill('100');
  await add.getByLabel('Jitter (ms)').fill('');
  await add.getByLabel('Loss (%)').fill('');
  await add.getByLabel('Duration').selectOption({ label: '1 hour' });
  await expect(add.getByTestId('fault-preview')).toContainText(`${name} · All traffic · upload & download → ↑↓ 100 ms · expires after 1 hour`);
  await add.getByRole('button', { name: 'Apply fault' }).click();
  await expect(add).toBeHidden({ timeout: applyTimeout });
  const fault = page.getByTestId('active-fault').filter({ hasText: '↑↓ 100 ms' });
  await expect(fault).toBeVisible();
  await expect(fault.getByTestId('fault-expiry')).toHaveText(/^(1:00:00|[45]\d:\d\d)$/); // the apply under emulation takes minutes of the hour

  // 5. the faults screen: the network's fault is overridden by A's for A
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Faults' }).click();
  await expect(network.getByTestId('fault-layer')).toHaveText('CONFIGURATION');
  await expect(network.getByTestId('overridden-by')).toContainText(`overridden by ${name} ↑↓ 100 ms (overlay)`, { timeout: 60_000 });
  await expect(network.getByTestId('fault-state')).toHaveText(/overridden/);
  const own = page.getByTestId('fault-row').filter({ hasText: name }).filter({ hasNotText: 'iot-delay' });
  await expect(own.getByTestId('fault-state')).toHaveText('effective');
});
