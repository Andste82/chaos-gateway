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

  await expect(page.getByTestId('device-lease').getByRole('link', { name: 'IoT' })).toHaveAttribute('href', '/networks/0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21');
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

test("removing a network's fault from a device's page asks first, since it lifts it for every device", async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const faults = page.getByTestId('active-fault');
  await expect(faults.nth(1)).toContainText('via Network IoT');
  await faults.nth(1).getByRole('button', { name: /^Remove / }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove the fault for everyone?' });
  await expect(dialog).toContainText('It acts on Network IoT, not only on this device.');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toBeHidden();
  expect(gw.requestsTo('DELETE', '/overlays/f0000000-0000-4000-8000-000000000002')).toHaveLength(0);
  await faults.nth(1).getByRole('button', { name: /^Remove / }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Remove for everyone' }).click();
  await expect(toast(page, 'Fault removed')).toBeVisible();
  expect(gw.requestsTo('DELETE', '/overlays/f0000000-0000-4000-8000-000000000002')).toHaveLength(1);
});

test('faults cannot be removed while a revision waits for confirmation or the gateway is offline', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 45_000).toISOString() } };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const remove = page.getByTestId('active-fault').nth(0).getByRole('button', { name: /^Remove / });
  await expect(remove).toBeDisabled();
  // confirmed: removing works again, until the gateway stops answering
  await page.getByTestId('countdown-banner').getByRole('button', { name: 'Confirm' }).click();
  await expect(remove).toBeEnabled();
  gw.unreachable = true;
  await page.evaluate(() => (window as unknown as { __sse: { fail: () => void } }).__sse.fail());
  await expect(page.getByTestId('banner-offline')).toBeVisible();
  await expect(remove).toBeDisabled();
});

test('a device without faults says its traffic is unimpaired', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.overlays = [];
  await gw.install(page);
  await page.goto('/devices/a1b2c3d4-0000-4000-8000-000000000015');
  await expect(page.getByTestId('empty-state')).toContainText("No active faults: this device's traffic passes unimpaired.");
  await expect(page.getByTestId('empty-state').getByRole('button', { name: 'Add a fault' })).toBeVisible();
  await expect(page.getByTestId('device-lease')).toContainText('no DHCP lease');
});

test('an unknown device is reported as such', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices/ghost');
  await expect(page.getByRole('heading', { name: 'No such device' })).toBeVisible();
});
