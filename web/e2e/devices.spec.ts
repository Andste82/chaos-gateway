import { expect, test } from '@playwright/test';
import { MockGateway, manyDevices, toast } from './mock';

// M12: the device list with many devices (more than one page of the API), search, the filter
// chips, the empty state, and adopting a discovered device.
test('many devices: all pages are listed, search and filter chips narrow them', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.devices = manyDevices(620); // two pages of 500
  await gw.install(page);
  await page.goto('/devices');

  const filter = page.getByRole('group', { name: 'Filter' });
  await expect(filter.getByRole('button', { name: 'All 620' })).toHaveAttribute('aria-pressed', 'true');
  // online: i % 3 != 0 → 413; with faults: i % 7 == 0 → 89; not adopted: i % 10 == 9 → 62
  await expect(filter.getByRole('button', { name: 'Online 413' })).toBeVisible();
  await expect(filter.getByRole('button', { name: 'With faults 89' })).toBeVisible();
  await expect(filter.getByRole('button', { name: 'Not adopted 62' })).toBeVisible();
  await expect(page.getByTestId('device-row')).toHaveCount(620);
  expect(gw.requestsTo('GET', '/devices').length).toBeGreaterThanOrEqual(2);

  await filter.getByRole('button', { name: 'Not adopted 62' }).click();
  await expect(filter.getByRole('button', { name: 'Not adopted 62' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('device-row')).toHaveCount(62);
  await expect(page.getByTestId('device-row').first().getByRole('button', { name: /^Adopt / })).toBeVisible();

  await filter.getByRole('button', { name: 'All 620' }).click();
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('sensor-123');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await expect(page.getByTestId('device-row')).toContainText('10.20.0.125');
  // by MAC and by IP too
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('02:00:00:00:02:6b');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await page.getByRole('searchbox', { name: 'Search devices' }).fill(' 10.20.2.121 ');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('nothing-like-this');
  await expect(page.getByTestId('no-match')).toHaveText('No devices match “nothing-like-this”.');
});

test('the rows show identity, traffic, faults and status, and open the device', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');

  const esp = page.getByTestId('device-row').filter({ hasText: 'ESP32-42' });
  await expect(esp).toContainText('10.10.0.42');
  await expect(esp).toContainText('02:00:00:00:00:2a');
  await expect(esp).toContainText('IoT');
  await expect(esp).toContainText('3.1 KB/s / 2.4 KB/s');
  await expect(esp).toContainText('upload 200 ms ± 50 ms');
  await expect(esp).toContainText('Online');
  await expect(page.getByTestId('device-row').filter({ hasText: 'Sensor-09' })).toContainText('Offline');
  await expect(page.getByTestId('device-row').filter({ hasText: 'Test-Phone' })).toContainText('Test');

  await esp.click();
  await expect(page).toHaveURL(/\/devices\/a1b2c3d4-0000-4000-8000-000000000042$/);
  await expect(page.getByRole('heading', { name: 'ESP32-42' })).toBeVisible();
});

test('empty: no devices yet', async ({ page }) => {
  const gw = new MockGateway();
  await gw.install(page);
  await page.goto('/devices');
  await expect(page.getByTestId('empty-state')).toContainText('No devices yet.');
});

test('adopting a discovered device applies a revision and names it', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');

  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('adopt-preview')).toContainText('with MAC 3a:f1:9c:22:07:b4');
  await expect(dialog.getByTestId('adopt-preview')).toContainText('in network IoT');
  await dialog.getByLabel('Name').fill('bad name');
  await expect(dialog.getByRole('button', { name: 'Adopt and apply' })).toBeDisabled();
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Adopt and apply' }).click();

  await expect(dialog).toBeHidden();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('device-row').filter({ hasText: 'Thermostat-A' })).toBeVisible();

  const create = gw.requestsTo('POST', '/revisions').at(-1)!;
  expect(create.headers['if-match']).toBe('"42"');
  expect(create.headers['content-type']).toBe('application/merge-patch+json');
  expect(create.headers['x-csrf-token']).toBe('csrf-1');
  expect(create.body).toEqual({
    devices: { 'd15c0000-0000-4000-8000-3af19c2207b4': { name: 'Thermostat-A', identifiers: { macs: ['3a:f1:9c:22:07:b4'] }, network: '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21' } },
  });
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(1);
});

test('adopting after someone else changed the configuration is refused and says so', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  gw.conflictOnce = true; // another revision was applied through the API since the page read the state
  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await dialog.getByRole('button', { name: 'Adopt and apply' }).click();
  await expect(dialog.getByRole('alert')).toHaveText('The configuration was changed meanwhile. Close the dialog and adopt the device again.');
  // Escape closes the dialog (keyboard access)
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
});
