import { expect, test } from '@playwright/test';
import { MockGateway, toast } from './mock';

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';

// M12: the device detail with the data of Phase 2: identity, lease, active faults with parameters,
// affected packets and remaining TTL, counters; removing a fault.
test('the device detail shows identity, lease, faults and counters', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);

  await expect(page.getByRole('heading', { name: 'ESP32-42' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText('Devices / ESP32-42');
  await expect(page.getByTestId('device-identity')).toContainText('Online');
  await expect(page.getByTestId('device-identity')).toContainText('10.10.0.42 · 02:00:00:00:00:2a · IoT network');

  // the device's own fault with its parameters and TTL, and the network's profile it inherits
  const faults = page.getByTestId('active-fault');
  await expect(faults).toHaveCount(2);
  await expect(faults.nth(0)).toContainText('→ TCP 8883');
  await expect(faults.nth(0)).toContainText('↑↓ 200 ms ±50');
  await expect(faults.nth(0)).toContainText('18 342 pkts');
  const ttl = await faults.nth(0).getByTestId('fault-expiry').textContent();
  expect(ttl).toMatch(/^4:\d\d$/);
  await expect(faults.nth(0).getByTestId('fault-expiry')).not.toHaveText(ttl!, { timeout: 3_000 }); // it counts down
  await expect(faults.nth(1)).toContainText('via Network IoT');
  await expect(faults.nth(1)).toContainText('profile lte');

  await expect(page.getByTestId('device-lease')).toContainText('10.10.0.42 · active');
  await expect(page.getByTestId('device-lease')).toContainText(/5\d:\d\d left/);
  await expect(page.getByTestId('device-lease')).toContainText('host name esp32-42');
  await expect(page.getByTestId('device-lease')).toContainText('10.10.0.1 (proxy)');
  await expect(page.getByTestId('device-counters')).toContainText('3.1 KB/s');
  await expect(page.getByTestId('device-counters')).toContainText('2.4 KB/s');
  await expect(page.getByTestId('device-counters')).toContainText('Active connections3');

  // remove the device's fault
  await faults.nth(0).getByRole('button', { name: /^Remove / }).click();
  await expect(toast(page, 'Fault removed')).toBeVisible();
  await expect(page.getByTestId('active-fault')).toHaveCount(1);
  expect(gw.requestsTo('DELETE', '/overlays/f0000000-0000-4000-8000-000000000001')).toHaveLength(1);
});

test('a device without faults says its traffic is unimpaired', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.overlays = [];
  await gw.install(page);
  await page.goto('/devices/a1b2c3d4-0000-4000-8000-000000000015');
  await expect(page.getByTestId('empty-state')).toHaveText("No active faults: this device's traffic passes unimpaired.");
  await expect(page.getByTestId('device-lease')).toContainText('no DHCP lease');
});

test('an unknown device is reported as such', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices/ghost');
  await expect(page.getByRole('heading', { name: 'No such device' })).toBeVisible();
});
