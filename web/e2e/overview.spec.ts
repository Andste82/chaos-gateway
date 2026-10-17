import { expect, test } from '@playwright/test';
import { IOT, MockGateway, withNetworks } from './mock';

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
  await expect(rows.nth(1)).toContainText('profile LTE');
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

test('every item of the overview links to its detail', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');
  const rows = page.getByTestId('active-fault');
  // a network's fault leads to the network, a device's to the device
  await expect(rows.nth(1).getByRole('link', { name: 'Network IoT' })).toHaveAttribute('href', `/networks/${IOT}`);
  await expect(rows.nth(2).getByRole('link', { name: 'Sensor-07' })).toHaveAttribute('href', '/devices/a1b2c3d4-0000-4000-8000-000000000057');
  // the uplink box leads to the networks, the gateway to its technical view
  await expect(page.getByTestId('topology-uplink').getByRole('link', { name: 'Uplink' })).toHaveAttribute('href', '/networks');
  await expect(page.getByTestId('topology-gateway').getByRole('link', { name: 'Chaos Gateway' })).toHaveAttribute('href', '/linux');
  // an event about a device leads to it, one about a network to the network, an overlay's to the faults
  await gw.emit(page, { type: 'device_online', message: 'device ESP32-42 online', data: { device: 'a1b2c3d4-0000-4000-8000-000000000042' } });
  await gw.emit(page, { type: 'network_degraded', message: 'network IoT degraded', data: { network: IOT } });
  await gw.emit(page, { type: 'overlay_created', message: 'overlay created', subject: { kind: 'overlay', id: 'f0000000-0000-4000-8000-000000000001' } });
  const events = page.getByTestId('recent-events');
  await expect(events.getByRole('link', { name: 'device ESP32-42 online' })).toHaveAttribute('href', '/devices/a1b2c3d4-0000-4000-8000-000000000042');
  await expect(events.getByRole('link', { name: 'network IoT degraded' })).toHaveAttribute('href', `/networks/${IOT}`);
  await expect(events.getByRole('link', { name: 'overlay created' })).toHaveAttribute('href', '/faults');
  // a revision has no page of its own: plain text
  await expect(events.getByRole('link', { name: /revision apply/ })).toHaveCount(0);
});

test('while the networks and the events load, the overview says so instead of "none"', async ({ page }) => {
  const gw = new MockGateway();
  let release = () => {};
  const gate = new Promise<void>((r) => (release = r));
  gw.holding['/networks'] = gate;
  gw.holding['/audit'] = gate;
  gw.holding['/uplink'] = gate;
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('topology-networks-loading')).toHaveText('Loading the networks…');
  await expect(page.getByText('No test network')).toHaveCount(0);
  await expect(page.getByTestId('topology-gateway')).toContainText('networks loading');
  await expect(page.getByTestId('topology-uplink')).toContainText('Loading…');
  await expect(page.getByText('Loading the events…')).toBeVisible();
  await expect(page.getByText('Nothing happened yet.')).toHaveCount(0);
  release();
  await expect(page.getByText('No test network')).toBeVisible();
  await expect(page.getByText('Nothing happened yet.')).toBeVisible();
});

test('the uplink is not claimed "up" while it is not known, and a failed read says so', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.failing['/uplink'] = 500;
  gw.failing['/uplink/traffic'] = 500;
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('topology-uplink')).toContainText('Uplink not loaded');
  await expect(page.getByTestId('topology-uplink')).not.toContainText(/\bup\b/);
  await expect(page.getByTestId('load-error').filter({ hasText: 'The uplink counters are not loaded' })).toBeVisible();
  await expect(page.getByTestId('uplink-collecting')).toHaveCount(0);
});

test('the uplink chart says visibly that it collects the first readings, until it has them', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('uplink-collecting')).toHaveText('Collecting the first readings…');
  await expect(page.getByTestId('uplink-rates')).toContainText('Mbit/s', { timeout: 15_000 });
  await expect(page.getByTestId('uplink-collecting')).toHaveCount(0);
});

// Issue #63: the gateway keeps the history, so a page load or a reload draws the last minutes at once;
// the live samples are appended to it, none missing and none twice.
test('the uplink chart draws the gateway history at once after a reload, then appends the live samples', async ({ page }) => {
  const gw = MockGateway.sample();
  const t0 = gw.now() - 300_000;
  // five minutes of readings every 5 s, the newest just now
  for (let i = 1; i <= 60; i++) {
    gw.uplinkTraffic.push({ time: new Date(t0 + i * 5_000).toISOString(), down_bps: i * 50_000, up_bps: 100_000 });
  }
  const newest = gw.uplinkTraffic.at(-1)!.time;
  await gw.install(page);
  await page.goto('/');
  const chart = page.getByTestId('uplink-chart');
  await expect(chart).toHaveAttribute('data-samples', '60', { timeout: 4_000 });
  await expect(page.getByTestId('uplink-collecting')).toHaveCount(0);
  await expect(page.getByTestId('uplink-rates')).toContainText('↓ 3.0 Mbit/s');
  const traffic = () => gw.requests.filter((r) => r.path === '/uplink/traffic');
  expect(traffic()[0]!.query.since).toBeUndefined();

  // the next request asks for what came after the newest sample, and the chart has exactly the gateway's samples
  await expect.poll(() => traffic().length, { timeout: 15_000 }).toBeGreaterThan(1);
  expect(traffic()[1]!.query.since).toBe(newest);
  await expect.poll(async () => gw.uplinkTraffic.length > 60 && (await chart.getAttribute('data-samples')) === String(gw.uplinkTraffic.length), { timeout: 15_000 }).toBe(true);
  await expect(page.getByTestId('uplink-rates')).not.toContainText('↓ 3.0 Mbit/s');

  // a reload loses nothing: the whole history again, before the next live sample
  const before = gw.uplinkTraffic.length;
  const requests = traffic().length;
  await page.reload();
  await expect.poll(() => traffic().length, { timeout: 4_000 }).toBeGreaterThan(requests);
  expect(traffic()[requests]!.query.since).toBeUndefined();
  await expect.poll(async () => Number(await chart.getAttribute('data-samples')), { timeout: 4_000 }).toBeGreaterThanOrEqual(before);
  await expect(page.getByTestId('uplink-collecting')).toHaveCount(0);
});

test('empty: no faults, no devices, no networks', async ({ page }) => {
  const gw = new MockGateway();
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('faults-counter')).toHaveText('No faults active');
  await expect(page.getByTestId('empty-state')).toHaveText(/No active faults: all traffic passes unimpaired\./);
  await expect(page.getByTestId('empty-state').getByRole('link', { name: 'Choose a device' })).toBeVisible();
  await expect(page.getByText('No test network')).toBeVisible();
  await expect(page.getByTestId('topology').getByRole('link', { name: 'Add a test network' })).toHaveAttribute('href', '/networks');
  await expect(page.getByTestId('path-fault-badge')).toHaveCount(0);
  await expect(page.getByText('Nothing happened yet.')).toBeVisible();
});

test('a management network is drawn apart from the test networks, off the faulted path (P3-AUDIT-38)', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await page.goto('/');

  // the test networks stay on the path with the fault badge; the management hub is not among them
  const testNetworks = page.getByTestId('topology-network');
  await expect(testNetworks.filter({ hasText: 'lab-hub' })).toHaveCount(1);
  await expect(testNetworks.filter({ hasText: 'admin' })).toHaveCount(0);
  await expect(page.getByTestId('path-fault-badge')).toHaveText('↑↓ 3 faults on the path');

  // it has a row of its own below the gateway, without a fault badge, and links to its network
  const management = page.getByTestId('topology-management');
  await expect(management).toContainText('Management access · no faults apply');
  const admin = management.getByTestId('topology-management-network');
  await expect(admin).toHaveCount(1);
  await expect(admin).toContainText('admin');
  await expect(admin).toContainText('10.98.0.0/24');
  await expect(admin).toContainText('WireGuard · management');
  await expect(admin).not.toContainText('device');
  await expect(management.getByTestId('path-fault-badge')).toHaveCount(0);
  await expect(management.getByRole('link', { name: 'admin' })).toHaveAttribute('href', /^\/networks\//);

  // the gateway counts the test networks and the management networks apart
  const count = await testNetworks.count();
  await expect(page.getByTestId('topology-gateway')).toContainText(`${count} test networks · 1 management`);
});

test('an uplink without counters says so instead of drawing nothing', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.uplink = { config: { interface: { name: 'eth0' } }, status: { interface: 'eth0' } };
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('no-uplink-counters')).toBeVisible();
});
