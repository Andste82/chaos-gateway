import { expect, test, type Page } from '@playwright/test';
import type { Capture } from '../src/api/generated/model';
import { IOT, MockGateway, toast } from './mock';

// M23a (plan §2.11, §2.17 "Captures"): the Captures screen and the capture panel of the device detail, against the mocked
// API: start a capture of a device, see it running in the list and in the panel, stop it, download it; a capture the quota
// or a low disk ended is shown as such; deleting removes it from both places; the key log only where the scope allows it.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const KiB = 1024;
const MiB = 1024 * KiB;

function capture(over: Partial<Capture> & { id: string }): Capture {
  return {
    name: 'bug-12',
    scope: { device: ESP },
    format: 'pcapng',
    status: 'stopped',
    stop_reason: 'user',
    started_at: new Date(Date.now() - 600_000).toISOString(),
    stopped_at: new Date(Date.now() - 300_000).toISOString(),
    size_bytes: 130 * KiB,
    packets: 812,
    dropped: 0,
    ...over,
  };
}

const C1 = 'ca000000-0000-4000-8000-0000000000a1';
const C2 = 'ca000000-0000-4000-8000-0000000000a2';
const C3 = 'ca000000-0000-4000-8000-0000000000a3';
const C4 = 'ca000000-0000-4000-8000-0000000000a4';
const C5 = 'ca000000-0000-4000-8000-0000000000a5';
const RUN = '0a1b2c3d-0000-4000-8000-000000000007';

async function open(gw: MockGateway, page: Page, path: string) {
  await gw.install(page);
  await page.goto(path);
}

const rows = (page: Page) => page.getByTestId('capture-row');
const row = (page: Page, name: string) => rows(page).filter({ hasText: name });
const dialog = (page: Page) => page.getByRole('dialog', { name: 'New capture' });

test('the navigation entry Captures is a link to the screen', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/');
  const nav = page.getByRole('navigation', { name: 'Main' });
  await nav.getByRole('link', { name: 'Captures' }).click();
  await expect(page).toHaveURL(/\/captures$/);
  await expect(page.getByRole('heading', { name: 'Captures', level: 1 })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Captures' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByTestId('empty-state')).toContainText('No captures yet');
});

test('the list shows name, scope, state, size, packets, dropped, start and run, and the storage', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [
    capture({ id: C1, name: 'stopped-one', scope: { network: IOT }, size_bytes: 3 * MiB, packets: 18342, dropped: 12 }),
    capture({ id: C2, name: 'by-run', scope: { selector: { source: { device: ESP }, protocol: 'tcp', ports: [8883], destination: { uplink: true } } }, stop_reason: 'run_end', run: RUN }),
    capture({ id: C3, name: 'live', scope: { uplink: true }, status: 'running', stop_reason: undefined, stopped_at: undefined, size_bytes: 42 * KiB, packets: 77 }),
  ];
  await open(gw, page, '/captures');
  await expect(rows(page)).toHaveCount(3);

  const stopped = row(page, 'stopped-one');
  await expect(stopped.getByTestId('capture-scope')).toHaveText('Network IoT');
  await expect(stopped.getByTestId('capture-state')).toHaveText('Stopped');
  await expect(stopped.getByTestId('capture-size')).toHaveText('3.0 MiB');
  await expect(stopped.getByTestId('capture-packets')).toHaveText('18 342');
  await expect(stopped.getByTestId('capture-dropped')).toHaveText('12');
  await expect(stopped.getByTestId('capture-run')).toHaveText('—');

  const byRun = row(page, 'by-run');
  await expect(byRun.getByTestId('capture-scope')).toHaveText('Selector ESP32-42 → uplink TCP 8883');
  await expect(byRun.getByTestId('capture-state')).toHaveText('Ended with its run');
  await expect(byRun.getByTestId('capture-run').getByRole('link')).toHaveAttribute('href', `/runs/${RUN}`);

  const live = row(page, 'live');
  await expect(live.getByTestId('capture-scope')).toHaveText('Uplink');
  await expect(live.getByTestId('capture-state')).toHaveText('Running');
  // the newest first: the gateway's order
  await expect(rows(page).first()).toContainText('live');

  const storage = page.getByTestId('capture-storage');
  await expect(storage.getByTestId('capture-quota')).toHaveText('3.2 MiB of 5.0 GiB');
  await expect(storage.getByTestId('capture-free')).toHaveText('60 % free (60.0 GiB)');
  await expect(storage.getByTestId('capture-running')).toHaveText('1 of 8 running');
  await expect(storage).toContainText('below 10 % free');
  await expect(page.getByTestId('capture-disk-low')).toHaveCount(0);
});

test('the counters of a running capture follow the gateway', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [capture({ id: C3, name: 'live', scope: { uplink: true }, status: 'running', stop_reason: undefined, stopped_at: undefined, size_bytes: 1 * KiB, packets: 3 })];
  await open(gw, page, '/captures');
  await expect(row(page, 'live').getByTestId('capture-packets')).toHaveText('3');
  gw.captures[0]!.packets = 1500;
  gw.captures[0]!.size_bytes = 900 * KiB;
  // a running capture is read every 2 s
  await expect(row(page, 'live').getByTestId('capture-packets')).toHaveText('1 500', { timeout: 6_000 });
  await expect(row(page, 'live').getByTestId('capture-size')).toHaveText('900 KiB');
  await expect(page.getByTestId('capture-quota')).toHaveText('900 KiB of 5.0 GiB');
});

test('a capture of a device: start it on the device, see it running there and in the list, stop it and download it', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, `/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Captures' });
  await expect(page.getByTestId('device-captures-empty')).toHaveText('No capture of this device yet.');

  await page.getByRole('button', { name: 'Capture ESP32-42' }).click();
  const d = dialog(page);
  // the scope is the device's; the defaults are the gateway's
  await expect(d.getByTestId('capture-fixed-scope')).toContainText('Device ESP32-42');
  await d.getByLabel('Name').fill('bug-12');
  await d.getByTestId('capture-start').click();
  await expect(toast(page, 'bug-12 is recording')).toBeVisible();
  await expect(d).toBeHidden();
  const post = gw.requestsTo('POST', '/captures');
  expect(post).toHaveLength(1);
  expect(post[0]!.body).toEqual({ name: 'bug-12', scope: { device: ESP }, format: 'pcapng' });

  // running in the device's panel
  const mine = panel.getByTestId('capture-row');
  await expect(mine).toHaveCount(1);
  await expect(mine.getByTestId('capture-name')).toHaveText('bug-12');
  await expect(mine.getByTestId('capture-state')).toHaveText('Running');
  // and in the list
  await page.goto('/captures');
  await expect(row(page, 'bug-12').getByTestId('capture-state')).toHaveText('Running');
  await expect(row(page, 'bug-12').getByTestId('capture-scope')).toHaveText('Device ESP32-42');
  await expect(row(page, 'bug-12').getByTestId('capture-delete')).toHaveCount(0); // a capture that runs is stopped first
  await expect(page.getByTestId('capture-running')).toHaveText('1 of 8 running');

  // a running capture can be downloaded
  const running = page.waitForEvent('download');
  await row(page, 'bug-12').getByRole('link', { name: 'Download bug-12' }).click();
  expect((await running).suggestedFilename()).toBe('bug-12.pcapng');

  // back on the device: stop it there
  await page.goto(`/devices/${ESP}`);
  await panel.getByRole('button', { name: 'Stop bug-12' }).click();
  await expect(toast(page, 'bug-12 stopped')).toBeVisible();
  expect(gw.requestsTo('POST', `/captures/${gw.captures[0]!.id}/stop`)).toHaveLength(1);
  await expect(panel.getByTestId('capture-state')).toHaveText('Stopped');
  await expect(panel.getByRole('button', { name: 'Stop bug-12' })).toHaveCount(0);

  const download = page.waitForEvent('download');
  await panel.getByRole('link', { name: 'Download bug-12' }).click();
  const file = await download;
  expect(file.suggestedFilename()).toBe('bug-12.pcapng');
  expect(gw.requestsTo('GET', `/captures/${gw.captures[0]!.id}/download`).length).toBeGreaterThanOrEqual(2);
  // the list has it stopped, too
  await page.goto('/captures');
  await expect(row(page, 'bug-12').getByTestId('capture-state')).toHaveText('Stopped');
});

test("the device's panel lists only the captures of that device, also those of a selector with it as the source", async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [
    capture({ id: C1, name: 'own', scope: { device: ESP } }),
    capture({ id: C2, name: 'by-name', scope: { device: 'esp32-42' } }),
    capture({ id: C3, name: 'selector', scope: { selector: { source: { device: ESP }, protocol: 'udp' } } }),
    capture({ id: C4, name: 'other-device', scope: { device: SENSOR } }),
    capture({ id: C5, name: 'network', scope: { network: IOT } }),
  ];
  await open(gw, page, `/devices/${ESP}`);
  const mine = page.getByRole('region', { name: 'Captures' }).getByTestId('capture-row');
  await expect(mine).toHaveCount(3);
  // newest first: the gateway lists the last one it made first
  await expect(mine.getByTestId('capture-name')).toHaveText(['selector', 'by-name', 'own']);
  await expect(page.getByTestId('device-captures-all')).toHaveAttribute('href', '/captures');
  await page.goto(`/devices/${SENSOR}`);
  await expect(page.getByRole('region', { name: 'Captures' }).getByTestId('capture-row')).toHaveCount(1);
});

test('a capture the quota or a low disk ended is shown as such, one the size limit ended is not', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [
    capture({ id: C1, name: 'by-quota', scope: { network: IOT }, stop_reason: 'size', message: 'the captures take 5.0 GiB, more than their quota of 5 GiB', size_bytes: 200 * MiB }),
    capture({ id: C2, name: 'by-disk', scope: { uplink: true }, stop_reason: 'disk_low', message: 'the volume of the captures has less than 10 % free space' }),
    capture({ id: C3, name: 'by-limit', scope: { uplink: true }, stop_reason: 'size' }),
    capture({ id: C4, name: 'broken', scope: { uplink: true }, status: 'failed', stop_reason: 'error', message: 'the service restarted while the capture ran' }),
  ];
  gw.captureStorage = { disk_low: true, free_percent: 4, volume_free_bytes: 4 * 1024 ** 3 };
  await open(gw, page, '/captures');
  await expect(rows(page)).toHaveCount(4);

  const quota = row(page, 'by-quota');
  await expect(quota.getByTestId('capture-state')).toHaveText('Stopped · quota');
  await expect(quota.getByTestId('capture-reason')).toContainText('more than their quota of 5 GiB');
  await expect(quota.getByTestId('capture-state')).toHaveAttribute('data-tone', 'block');

  const disk = row(page, 'by-disk');
  await expect(disk.getByTestId('capture-state')).toHaveText('Stopped · disk low');
  await expect(disk.getByTestId('capture-reason')).toContainText('less than 10 % free space');
  // what it recorded stays: it can be downloaded
  await expect(disk.getByRole('link', { name: 'Download by-disk' })).toBeVisible();

  const limit = row(page, 'by-limit');
  await expect(limit.getByTestId('capture-state')).toHaveText('Ended · size limit');
  await expect(limit.getByTestId('capture-reason')).toHaveCount(0);
  await expect(limit.getByTestId('capture-state')).toHaveAttribute('data-tone', 'offline');

  await expect(row(page, 'broken').getByTestId('capture-state')).toHaveText('Failed');
  await expect(row(page, 'broken').getByTestId('capture-reason')).toContainText('the service restarted');

  // the storage says the volume is low
  await expect(page.getByTestId('capture-free')).toHaveText('4 % free (4.0 GiB)');
  await expect(page.getByTestId('capture-disk-low')).toContainText('less than 10 % free space: new captures are refused and running ones stop');
});

test('a capture that the quota or the disk stops while the screen is open shows up at once, with a toast', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [capture({ id: C3, name: 'live', scope: { uplink: true }, status: 'running', stop_reason: undefined, stopped_at: undefined, size_bytes: 900 * KiB })];
  await open(gw, page, '/captures');
  await expect(row(page, 'live').getByTestId('capture-state')).toHaveText('Running');
  await expect(page.getByTestId('capture-disk-low')).toHaveCount(0);

  gw.captures[0] = { ...gw.captures[0]!, status: 'stopped', stop_reason: 'disk_low', message: 'the volume of the captures has less than 10 % free space', stopped_at: new Date().toISOString() };
  gw.captureStorage = { disk_low: true, free_percent: 3, volume_free_bytes: 3 * 1024 ** 3 };
  await gw.emit(page, { type: 'capture_stopped', subject: { kind: 'capture', id: C3, name: 'live' }, data: { capture: C3, reason: 'disk_low' } });
  await gw.emit(page, { type: 'disk_low', data: { capture: C3, free_percent: 3 } });
  // the events read the list and the storage at once; the poll would be later
  await expect(row(page, 'live').getByTestId('capture-state')).toHaveText('Stopped · disk low', { timeout: 2_000 });
  await expect(page.getByTestId('capture-disk-low')).toBeVisible({ timeout: 2_000 });
  await expect(toast(page, 'The disk of the captures is low')).toBeVisible();

  // a capture of the quota: its own toast
  gw.captures.push(capture({ id: C1, name: 'quota-one', scope: { network: IOT }, stop_reason: 'size', message: 'the capture reached the space left in the quota' }));
  await gw.emit(page, { type: 'capture_stopped', subject: { kind: 'capture', id: C1, name: 'quota-one' }, data: { capture: C1, reason: 'size', message: 'the capture reached the space left in the quota' } });
  await expect(toast(page, 'quota-one stopped: quota reached')).toBeVisible();
  await expect(row(page, 'quota-one').getByTestId('capture-state')).toHaveText('Stopped · quota', { timeout: 2_000 });
});

test('a capture that starts elsewhere (an API client, a run) appears in the list and in the device panel without a reload', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, `/devices/${ESP}`);
  await expect(page.getByTestId('device-captures-empty')).toBeVisible();
  gw.captures.push(capture({ id: C1, name: 'from-ci', scope: { device: ESP }, status: 'running', stop_reason: undefined, stopped_at: undefined, size_bytes: 0, packets: 0 }));
  await gw.emit(page, { type: 'capture_started', subject: { kind: 'capture', id: C1, name: 'from-ci' }, data: { capture: C1 } });
  await expect(page.getByRole('region', { name: 'Captures' }).getByTestId('capture-row')).toHaveCount(1, { timeout: 2_000 });
  await page.getByTestId('device-captures-all').click();
  await expect(row(page, 'from-ci').getByTestId('capture-state')).toHaveText('Running');
});

test('deleting a capture removes it from the list and from the device, after a question', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [capture({ id: C1, name: 'old-one', scope: { device: ESP } }), capture({ id: C2, name: 'keep-one', scope: { device: ESP } }), capture({ id: C3, name: 'running-one', scope: { device: ESP }, status: 'running', stop_reason: undefined, stopped_at: undefined })];
  await open(gw, page, '/captures');
  await expect(rows(page)).toHaveCount(3);

  // deleting in the list: asked first, cancelling deletes nothing
  await row(page, 'old-one').getByRole('button', { name: 'Delete old-one' }).click();
  const ask = page.getByRole('dialog', { name: 'Delete capture' });
  await expect(ask.getByTestId('capture-delete-what')).toContainText('old-one and its files (130 KiB)');
  await ask.getByRole('button', { name: 'Cancel' }).click();
  await expect(ask).toBeHidden();
  expect(gw.requestsTo('DELETE', `/captures/${C1}`)).toHaveLength(0);
  await row(page, 'old-one').getByRole('button', { name: 'Delete old-one' }).click();
  await ask.getByTestId('capture-delete-confirm').click();
  await expect(toast(page, 'old-one deleted')).toBeVisible();
  expect(gw.requestsTo('DELETE', `/captures/${C1}`)).toHaveLength(1);
  await expect(rows(page)).toHaveCount(2);
  await expect(row(page, 'old-one')).toHaveCount(0);
  await expect(page.getByTestId('capture-quota')).toHaveText('260 KiB of 5.0 GiB');

  // gone from the device's panel, too
  await page.goto(`/devices/${ESP}`);
  const mine = page.getByRole('region', { name: 'Captures' }).getByTestId('capture-row');
  await expect(mine).toHaveCount(2);
  await expect(mine.getByTestId('capture-name')).toHaveText(['running-one', 'keep-one']);

  // deleting in the device's panel removes it from the list
  await mine.filter({ hasText: 'keep-one' }).getByRole('button', { name: 'Delete keep-one' }).click();
  await page.getByRole('dialog', { name: 'Delete capture' }).getByTestId('capture-delete-confirm').click();
  await expect(mine).toHaveCount(1);
  expect(gw.requestsTo('DELETE', `/captures/${C2}`)).toHaveLength(1);
  await page.getByTestId('device-captures-all').click();
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page)).toContainText('running-one');
});

test('a capture that is gone when it is stopped or deleted says so and leaves the list at once', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [
    capture({ id: C1, name: 'live', scope: { uplink: true }, status: 'running', stop_reason: undefined, stopped_at: undefined }),
    capture({ id: C2, name: 'old-one', scope: { uplink: true } }),
  ];
  await open(gw, page, '/captures');
  await expect(rows(page)).toHaveCount(2);
  // the quota deleted the stopped one at the gateway while the list was still the older one; nothing polls so soon
  gw.captures = gw.captures.filter((c) => c.id !== C2);
  await row(page, 'old-one').getByRole('button', { name: 'Delete old-one' }).click();
  await page.getByRole('dialog', { name: 'Delete capture' }).getByTestId('capture-delete-confirm').click();
  await expect(toast(page, 'old-one could not be deleted')).toBeVisible();
  await expect(rows(page)).toHaveCount(1, { timeout: 1_000 });

  // the same for a running capture that is gone when it is stopped (a running capture is read every 2 s: the limit
  // is shorter than that, so only the read after the refusal can have removed the row)
  gw.captures = [];
  await row(page, 'live').getByRole('button', { name: 'Stop live' }).click();
  await expect(toast(page, 'live could not be stopped')).toBeVisible();
  await expect(rows(page)).toHaveCount(0, { timeout: 1_000 });
  await expect(page.getByTestId('empty-state')).toContainText('No captures yet');
  expect(gw.requestsTo('POST', `/captures/${C1}/stop`)).toHaveLength(1);
});

test('the TLS key log is offered only for a capture that has one, and a sensitive capture is marked', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.captures = [
    capture({ id: C1, name: 'plain', scope: { device: ESP } }),
    capture({ id: C2, name: 'decryptable', scope: { device: ESP }, keylog: true, sensitive: true }),
  ];
  await open(gw, page, '/captures');
  await expect(row(page, 'plain').getByTestId('capture-keylog')).toHaveCount(0);
  await expect(row(page, 'plain').getByTestId('capture-sensitive')).toHaveCount(0);
  const secret = row(page, 'decryptable');
  await expect(secret.getByTestId('capture-sensitive')).toHaveText('SENSITIVE');
  const log = page.waitForEvent('download');
  await secret.getByRole('link', { name: 'Download the TLS key log of decryptable' }).click();
  expect((await log).suggestedFilename()).toBe('decryptable.keylog.txt');
  expect(gw.requests.filter((r) => r.path === `/captures/${C2}/download` && r.query.part === 'keylog')).toHaveLength(1);
});

test('without the scope full there is no key log and no download of a capture that holds secrets', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.sessionScope = 'overlays';
  gw.captures = [
    capture({ id: C1, name: 'plain', scope: { device: ESP } }),
    capture({ id: C2, name: 'decryptable', scope: { device: ESP }, keylog: true, sensitive: true }),
  ];
  await open(gw, page, '/captures');
  await expect(row(page, 'plain').getByRole('link', { name: 'Download plain' })).toBeVisible();
  const secret = row(page, 'decryptable');
  await expect(secret.getByTestId('capture-keylog')).toHaveCount(0);
  await expect(secret.getByRole('link', { name: 'Download decryptable' })).toHaveCount(0);
  await expect(secret.getByTestId('capture-download-denied')).toHaveText('Needs scope full');
});

test('New capture: a network, a selector and the uplink, with the limits, as the request has them', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/captures');
  await page.getByTestId('new-capture').click();
  const d = dialog(page);

  // the start waits for a device
  await expect(d.getByTestId('capture-start')).toBeDisabled();
  await expect(d.getByTestId('capture-scope-hint')).toContainText("The device's frames");
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Network' }).click();
  await d.getByLabel('Network', { exact: true }).selectOption({ label: 'IoT' });
  await d.getByLabel('Duration').fill('5m');
  await d.getByLabel('Size limit').fill('512MiB');
  await d.getByTestId('capture-start').click();
  await expect(toast(page, 'Capture is recording')).toBeVisible();
  expect(gw.requestsTo('POST', '/captures')[0]!.body).toEqual({ scope: { network: IOT }, format: 'pcapng', duration: '5m', max_size: '512MiB' });
  await expect(row(page, 'capture').first()).toBeVisible();

  // a selector: the source, the protocol, the ports and a destination
  await page.getByTestId('new-capture').click();
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Selector' }).click();
  await d.getByLabel('Source').selectOption({ label: 'Device · ESP32-42' });
  await d.getByLabel('Protocol').selectOption('tcp');
  await d.getByLabel('Ports').fill('8883, 1000-2000');
  await d.getByLabel('Destination', { exact: true }).selectOption('uplink');
  await d.getByLabel('Name').fill('mqtt');
  await d.getByTestId('capture-start').click();
  await expect(toast(page, 'mqtt is recording')).toBeVisible();
  expect(gw.requestsTo('POST', '/captures')[1]!.body).toEqual({
    name: 'mqtt',
    scope: { selector: { source: { device: ESP }, protocol: 'tcp', ports: [8883], port_ranges: [{ from: 1000, to: 2000 }], destination: { uplink: true } } },
    format: 'pcapng',
  });

  // the uplink with a ring buffer and a pcap file of short packets, under Advanced
  await page.getByTestId('new-capture').click();
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Uplink' }).click();
  await expect(d.getByTestId('capture-scope-hint')).toContainText('after NAT');
  await d.getByRole('button', { name: 'Advanced' }).click();
  await d.getByRole('group', { name: 'Format' }).getByRole('button', { name: 'pcap', exact: true }).click();
  await d.getByLabel('Packet length').fill('96');
  await d.getByRole('group', { name: 'Limit' }).getByRole('button', { name: 'Ring buffer' }).click();
  await expect(d.getByTestId('capture-ring-note')).toBeVisible();
  await d.getByLabel('Files').fill('10');
  await d.getByLabel('Size of a file').fill('16MiB');
  await d.getByTestId('capture-start').click();
  await expect(rows(page)).toHaveCount(3);
  expect(gw.requestsTo('POST', '/captures')[2]!.body).toEqual({ scope: { uplink: true }, format: 'pcap', snaplen: 96, ring_buffer: { files: 10, file_size: '16MiB' } });
  await expect(page.getByTestId('capture-running')).toHaveText('3 of 8 running');
});

test('New capture: each message stands under its field, and Start waits until the form is valid', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/captures');
  await page.getByTestId('new-capture').click();
  const d = dialog(page);
  const start = d.getByTestId('capture-start');
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Uplink' }).click();
  await expect(start).toBeEnabled();

  await d.getByLabel('Duration').fill('soon');
  await expect(d.getByText('A duration like 90s, 5m or 1h.')).toBeVisible();
  await expect(start).toBeDisabled();
  await d.getByLabel('Duration').fill('90s');
  await d.getByLabel('Size limit').fill('10KiB');
  await expect(d.getByText('At least 64KiB.')).toBeVisible();
  await expect(start).toBeDisabled();
  await d.getByLabel('Size limit').fill('64KiB');
  await expect(start).toBeEnabled();

  await d.getByRole('button', { name: 'Advanced' }).click();
  await d.getByLabel('Packet length').fill('10');
  await expect(d.getByText('A number of bytes from 64 to 262144.')).toBeVisible();
  await expect(start).toBeDisabled();
  await d.getByLabel('Packet length').fill('');
  await d.getByRole('group', { name: 'Limit' }).getByRole('button', { name: 'Ring buffer' }).click();
  await d.getByLabel('Files').fill('1');
  await expect(d.getByText('From 2 to 100 files.')).toBeVisible();
  await expect(start).toBeDisabled();
  await d.getByLabel('Files').fill('4');
  await expect(start).toBeEnabled();

  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Selector' }).click();
  await d.getByLabel('Protocol').selectOption('icmp');
  await d.getByLabel('Ports').fill('80');
  await expect(d.getByText('Ports need the protocol TCP or UDP.')).toBeVisible();
  await expect(start).toBeDisabled();
  expect(gw.requestsTo('POST', '/captures')).toHaveLength(0);
});

test('New capture: the gateway refuses a field, and says why a capture cannot start at all', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/captures');
  await page.getByTestId('new-capture').click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Selector' }).click();
  await d.getByLabel('Destination', { exact: true }).selectOption('hostname');
  await d.getByLabel('Host name').fill('broker.example.com');
  gw.rejectCapture = { errors: [{ path: '/scope/selector/destination/hostname', code: 'unresolved_hostname', message: 'no device has asked for broker.example.com yet' }] };
  await d.getByTestId('capture-start').click();
  await expect(d.getByText('no device has asked for broker.example.com yet')).toBeVisible();
  await expect(d.getByTestId('capture-error')).toContainText('see the marked fields');
  await expect(d).toBeVisible();
  // an edit takes the message away
  await d.getByLabel('Host name').fill('broker.example.org');
  await expect(d.getByText('no device has asked for broker.example.com yet')).toBeHidden();

  // a refusal of the ring buffer opens Advanced
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Uplink' }).click();
  await d.getByRole('button', { name: 'Advanced' }).click();
  await d.getByRole('group', { name: 'Limit' }).getByRole('button', { name: 'Ring buffer' }).click();
  await d.getByLabel('Size of a file').fill('99GiB');
  gw.rejectCapture = { errors: [{ path: '/ring_buffer', code: 'invalid_capture_limit', message: 'the ring buffer (4 files of 99GiB) is larger than the quota (5 GiB)' }] };
  await d.getByTestId('capture-start').click();
  await expect(d.getByText('larger than the quota (5 GiB)')).toBeVisible();

  // the disk is low: no field, the message below the form
  await d.getByLabel('Size of a file').fill('16MiB');
  gw.rejectCapture = { code: 'capacity_exceeded', detail: 'the volume of the captures has less than 10 % free space: delete captures, or free the disk' };
  await d.getByTestId('capture-start').click();
  await expect(d.getByTestId('capture-error')).toContainText('less than 10 % free space');
  await d.getByRole('button', { name: 'Cancel' }).click();
  await expect(d).toBeHidden();
  await expect(rows(page)).toHaveCount(0);
});

test('New capture shows the request in the API panel and in the dialog, and never offers the admin tunnel', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.networks.push({
    id: '7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d',
    config: { type: 'wireguard', kind: 'hub', name: 'admin-tunnel', role: 'management', address: '10.99.0.1/24', listen_port: 51821 } as never,
    status: { state: 'ok', interface: 'wg-admin' },
  });
  await open(gw, page, '/captures');
  await page.getByRole('button', { name: '</> API' }).click();
  await page.getByTestId('new-capture').click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Network' }).click();
  await expect(d.getByLabel('Network', { exact: true }).getByRole('option')).toHaveText(['Choose a network', 'IoT', 'Test']);
  await d.getByLabel('Network', { exact: true }).selectOption({ label: 'Test' });
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Selector' }).click();
  await expect(d.getByLabel('Source').getByRole('option')).not.toContainText(['admin-tunnel']);

  const panel = page.getByTestId('api-panel');
  await expect(panel).toContainText('This dialog: New capture');
  await expect(panel.getByTestId('api-call')).toContainText('POST /api/v1/captures');
  await d.getByText('</> API · what New capture sends').click();
  await expect(d.getByTestId('scenario-api').getByTestId('api-call')).toContainText('"selector"');
});

test('New capture never offers a client of the admin tunnel as a device or a source, and its device page says why it cannot be captured (P5-AUDIT-31)', async ({ page }) => {
  const gw = MockGateway.sample();
  const hub = '7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d';
  const laptop = 'b7c8d9e0-0000-4000-8000-0000000000a7';
  gw.networks.push({
    id: hub,
    config: {
      type: 'wireguard',
      kind: 'hub',
      name: 'admin-tunnel',
      role: 'management',
      address: '10.99.0.1/24',
      listen_port: 51821,
      clients: { [laptop]: { name: 'admin-laptop', public_key: 'FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=', address: '10.99.0.2' } },
    } as never,
    status: { state: 'ok', interface: 'wg-admin' },
  });
  gw.devices.push({ id: laptop, name: 'admin-laptop', origin: 'client', network: hub, observed: { online: true, addresses: ['10.99.0.2'], sources: ['wireguard'] } } as never);
  await open(gw, page, '/captures');
  await page.getByTestId('new-capture').click();
  const d = dialog(page);
  await expect(d.getByLabel('Device', { exact: true }).getByRole('option').filter({ hasText: 'ESP32-42' })).toHaveCount(1);
  await expect(d.getByLabel('Device', { exact: true }).getByRole('option').filter({ hasText: 'admin-laptop' })).toHaveCount(0);
  await d.getByRole('group', { name: 'Scope' }).getByRole('button', { name: 'Selector' }).click();
  await expect(d.getByLabel('Source').getByRole('option').filter({ hasText: 'Device · ESP32-42' })).toHaveCount(1);
  await expect(d.getByLabel('Source').getByRole('option').filter({ hasText: 'admin-laptop' })).toHaveCount(0);

  await page.goto(`/devices/${laptop}`);
  await expect(page.getByTestId('device-capture')).toBeDisabled();
  await expect(page.getByTestId('device-captures-management')).toHaveText(
    "This device is a client of a management WireGuard network: its packets are the admin's tunnel, which the gateway does not record.",
  );
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('device-capture')).toBeEnabled();
  await expect(page.getByTestId('device-captures-management')).toHaveCount(0);
});

test('a gateway without a capture volume says so on the screen and on the device', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.capabilities = { ...gw.capabilities, features: gw.capabilities.features.filter((f) => f !== 'captures') };
  await open(gw, page, '/captures');
  await expect(page.getByTestId('captures-unsupported')).toContainText('no capture volume');
  await expect(page.getByTestId('new-capture')).toBeDisabled();
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('device-captures-unsupported')).toContainText('no capture volume');
  await expect(page.getByRole('button', { name: 'Capture ESP32-42' })).toBeDisabled();
  expect(gw.requests.filter((r) => r.path.startsWith('/captures'))).toHaveLength(0);
});

test('the captures that cannot be read are said, not shown as none', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.failing['/captures'] = 500;
  await open(gw, page, '/captures');
  await expect(page.getByTestId('load-error').filter({ hasText: 'The captures are not loaded' })).toBeVisible();
  await expect(page.getByTestId('empty-state')).toHaveCount(0);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('load-error').filter({ hasText: 'The captures are not loaded' })).toBeVisible();
  await expect(page.getByTestId('device-captures-empty')).toHaveCount(0);
});

test('the API panel of the screen lists what it reads', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/captures');
  await page.getByRole('button', { name: '</> API' }).click();
  const calls = page.getByTestId('api-panel').getByTestId('api-call');
  await expect(calls.nth(0)).toContainText('GET /api/v1/captures');
  await expect(calls.nth(1)).toContainText('GET /api/v1/captures/storage');
});
