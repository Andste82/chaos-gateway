import { expect, test } from '@playwright/test';
import { MockGateway } from './mock';

// M12: the overview with data, and empty.
test('the overview shows topology, active faults, uplink traffic and events', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');

  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await expect(page.getByTestId('context-line')).toContainText('Revision 42');
  await expect(page.getByTestId('context-line')).toContainText('kernel state in sync');
  await expect(page.getByTestId('health')).toHaveText('Healthy');
  // three active faults: two overlays and one configured fault
  await expect(page.getByTestId('faults-counter')).toHaveText('3 faults active');
  await expect(page.getByTestId('host-line')).toContainText('gw-lab-01');

  // topology: test networks → gateway → uplink, the fault badge on the path
  await expect(page.getByTestId('topology-network')).toHaveCount(2);
  await expect(page.getByTestId('topology-network').first()).toContainText('IoT');
  await expect(page.getByTestId('topology-network').first()).toContainText('10.10.0.0/24 · br-iot');
  await expect(page.getByTestId('topology-network').first()).toContainText('4 devices · DHCP');
  await expect(page.getByTestId('path-fault-badge')).toHaveText('↑↓ 3 faults on the path');
  await expect(page.getByTestId('topology-uplink')).toContainText('192.168.1.57/24 · eth0');

  // active faults: target, effect, affected packets, expiry; the device row links to it
  const rows = page.getByTestId('active-fault');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('ESP32-42');
  await expect(rows.nth(0)).toContainText('→ TCP 8883');
  await expect(rows.nth(0)).toContainText('↑↓ 200 ms ±50');
  await expect(rows.nth(0)).toContainText('5 % loss');
  await expect(rows.nth(0)).toContainText('18 342 pkts');
  await expect(rows.nth(0).getByTestId('fault-expiry')).toHaveText(/^4:\d\d$/);
  await expect(rows.nth(1)).toContainText('Network IoT');
  await expect(rows.nth(1)).toContainText('profile lte');
  await expect(rows.nth(1)).toContainText('partially overridden');
  await expect(rows.nth(2)).toContainText('Sensor-07');
  await expect(rows.nth(2)).toContainText('↓ 64 kbit/s');
  await expect(rows.nth(2).getByTestId('fault-expiry')).toHaveText('configured');
  await rows.nth(0).getByRole('link', { name: 'ESP32-42' }).click();
  await expect(page).toHaveURL(/\/devices\/a1b2c3d4-0000-4000-8000-000000000042$/);
  await page.goBack();

  // uplink traffic from two readings of the counters: 750 kB and 125 kB in the interval
  await expect(page.getByTestId('uplink-rates')).toContainText('Mbit/s', { timeout: 15_000 });
  await expect(page.getByRole('img', { name: /Uplink throughput\. Download/ })).toBeVisible();

  // recent events: the audit log before the stream delivers something
  await expect(page.getByTestId('recent-events')).toContainText('revision apply · revision 42 · by admin');
});

test('empty: no faults, no devices, no networks', async ({ page }) => {
  const gw = new MockGateway();
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('faults-counter')).toHaveText('No faults active');
  await expect(page.getByTestId('empty-state')).toHaveText(/No active faults: all traffic passes unimpaired\./);
  await expect(page.getByTestId('empty-state').getByRole('link', { name: 'Choose a device' })).toBeVisible();
  await expect(page.getByText('No test network')).toBeVisible();
  await expect(page.getByTestId('path-fault-badge')).toHaveCount(0);
  await expect(page.getByText('Nothing happened yet.')).toBeVisible();
});

test('an uplink without counters says so instead of drawing nothing', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.uplink = { config: { interface: { name: 'eth0' } }, status: { interface: 'eth0' } };
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('no-uplink-counters')).toBeVisible();
});
