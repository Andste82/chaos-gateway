import { expect, test, type Page } from '@playwright/test';
import type { Overlay } from '../src/api/generated/model';
import { CLIENT_RA, HUB, LINK, MockGateway, device, toast, withNetworks } from './mock';

// P3-M13-04: the fault dialog creates and edits every fault kind the API offers — MTU/PMTUD and
// tunnel faults (M10) and the advanced impairment parameters (burst loss, flapping, a delay
// distribution, a queue limit, different parameters per direction) — and never drops a parameter
// it does not show.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';

const dialog = (page: Page) => page.getByRole('dialog', { name: /^(Add|Edit) fault$/ });

function overlay(id: string, over: Partial<Overlay>): Overlay {
  return {
    id,
    kind: 'fault',
    owner: { type: 'user', id: 'admin', name: 'admin' },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    generation: 42,
    state: 'effective',
    counters: { packets: 12, bytes: 1200, epoch: 1 },
    ...over,
  };
}

test('an MTU fault: created with the kind MTU / PMTUD, then edited on the faults screen', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).click();
  const d = dialog(page);
  await d.getByRole('group', { name: 'Kind' }).getByRole('button', { name: 'MTU / PMTUD' }).click();
  // an MTU fault has no impairment parameters and no direction
  await expect(d.getByLabel('Latency (ms)')).toHaveCount(0);
  await expect(d.getByRole('group', { name: 'Direction' })).toHaveCount(0);
  await d.getByLabel('Target').selectOption({ label: 'Device · ESP32-42' });
  await d.getByLabel('Traffic').selectOption({ label: 'HTTPS · TCP 443' });
  await d.getByLabel('Packet size (bytes)').fill('400');
  await expect(d.getByLabel('Packet size (bytes)')).toHaveAccessibleDescription('A packet size from 552 to 1500 bytes.');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Packet size (bytes)').fill('1280');
  await d.getByLabel('Mode').selectOption('blackhole');
  await expect(d.getByTestId('mtu-hint')).toContainText('TCP transfers stall');
  await d.getByLabel('Duration').selectOption({ label: 'Until removed' });
  await expect(d.getByTestId('fault-preview')).toHaveText(/ESP32-42 · HTTPS · TCP 443 → packets above 1280 bytes: Black hole · until removed/);
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({
    target: { device: ESP },
    fault: { family: 'mtu', protocol: 'tcp', ports: [443], mtu: { size: 1280, mode: 'blackhole' } },
  });

  const row = page.getByTestId('fault-family-mtu').getByTestId('fault-row');
  await expect(row).toContainText('MTU 1280 blackhole');
  await row.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByRole('group', { name: 'Kind' })).toHaveCount(0);
  await expect(d.getByLabel('Packet size (bytes)')).toHaveValue('1280');
  await expect(d.getByLabel('Mode')).toHaveValue('blackhole');
  await expect(d.getByLabel('Traffic')).toHaveValue('https');
  await d.getByLabel('Packet size (bytes)').fill('1400');
  await d.getByLabel('Mode').selectOption('mss_clamp');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42').last()).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[1]!.body).toEqual({
    target: { device: ESP },
    fault: { family: 'mtu', protocol: 'tcp', ports: [443], mtu: { size: 1400, mode: 'mss_clamp' } },
  });
  // the same key: the overlay kept its id, nothing was removed
  expect(gw.overlays.filter((o) => o.fault?.family === 'mtu')).toHaveLength(1);
  expect(gw.requests.filter((r) => r.method === 'DELETE')).toHaveLength(0);
  await expect(row).toContainText('MTU 1400 mss_clamp');
});

test('a tunnel fault: edited with the parameters a tunnel takes, and one configured on a link through the draft', async ({ page }) => {
  const TUN = 'f0000000-0000-4000-8000-0000000000d1';
  const gw = withNetworks(MockGateway.sample());
  gw.devices = [...gw.devices, device({ id: CLIENT_RA, name: 'rA', origin: 'wireguard_client', network: HUB })];
  gw.overlays = [...gw.overlays, overlay(TUN, { fault: { family: 'tunnel', tunnel: { client: CLIENT_RA }, latency: '100ms' } })];
  await gw.install(page);
  await page.goto('/faults');
  const row = page.getByTestId('fault-family-tunnel').getByTestId('fault-row');
  await expect(row).toContainText('Tunnel of rA');
  await expect(row).toContainText('tunnel ↑↓ 100 ms');
  await row.getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  await expect(d.getByLabel('Tunnel')).toHaveValue(`client:${CLIENT_RA}`);
  await expect(d.getByLabel('Tunnel')).toBeDisabled();
  // no target, no traffic: the whole tunnel
  await expect(d.getByLabel('Target')).toHaveCount(0);
  await expect(d.getByLabel('Traffic')).toHaveCount(0);
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('100');
  await d.getByRole('group', { name: 'Direction' }).getByRole('button', { name: 'Upload · from the peer' }).click();
  await d.getByLabel('Loss (%)').fill('5');
  await d.getByRole('button', { name: 'Advanced: burst loss, flapping' }).click();
  // a tunnel takes no rate, reorder, … and no delay distribution
  await expect(d.getByLabel('Rate limit')).toHaveCount(0);
  await expect(d.getByLabel('Delay distribution')).toHaveCount(0);
  await d.getByLabel('Flapping').check();
  await d.getByLabel('Up (s)').fill('20');
  await d.getByLabel('Down (s)').fill('10');
  await expect(d.getByTestId('fault-preview')).toHaveText(/Tunnel of rA · upload only → tunnel ↑ 100 ms, tunnel 5 % loss, tunnel ↑ flapping 20s up \/ 10s down · until removed/);
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Tunnel of rA')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({
    fault: { family: 'tunnel', tunnel: { client: CLIENT_RA }, upload: { latency: '100ms', loss: '5%', flapping: { up: '20s', down: '10s' } } },
  });
  expect(gw.overlays.filter((o) => o.fault?.family === 'tunnel').map((o) => o.id)).toEqual([TUN]);

  // a configured tunnel fault on the link: it has no source
  await page.getByRole('button', { name: 'Add fault' }).click();
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /Configuration/ }).click();
  await d.getByRole('group', { name: 'Kind' }).getByRole('button', { name: 'Tunnel' }).click();
  await expect(d.getByLabel('Tunnel')).toHaveAccessibleDescription('Choose the WireGuard client or link whose tunnel is impaired.');
  await d.getByLabel('Tunnel').selectOption({ label: 'Link · site-b' });
  await d.getByLabel('Name').fill('site-b-slow');
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 fault · not applied yet/);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 43' })).toBeVisible();
  const body = gw.requestsTo('POST', '/revisions')[0]!.body as { faults: Record<string, unknown> };
  expect(Object.values(body.faults)).toEqual([{ name: 'site-b-slow', family: 'tunnel', tunnel: { link: LINK }, latency: '200ms', jitter: '50ms' }]);
});

test('the advanced impairment parameters: burst loss, flapping, a distribution, a queue limit and each direction its own', async ({ page }) => {
  const ADV = 'f0000000-0000-4000-8000-0000000000d2';
  const gw = MockGateway.sample();
  gw.overlays = [
    ...gw.overlays,
    overlay(ADV, {
      target: { device: SENSOR },
      fault: {
        upload: { latency: '100ms', jitter: '20ms', distribution: 'normal', burst_loss: { p: '1%', r: '30%' } },
        download: { rate: '64kbit', queue_limit: 20, flapping: { up: '20s', down: '10s' } },
      },
    }),
  ];
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: 'burst loss' }).getByRole('button', { name: /^Edit / }).click();
  const d = dialog(page);
  // every parameter is shown: nothing would be replaced on save
  await expect(d.getByText(/does not show/)).toHaveCount(0);
  await expect(d.getByRole('group', { name: 'Direction' }).getByRole('button', { name: 'Each its own' })).toHaveAttribute('aria-pressed', 'true');
  await expect(d.getByRole('button', { name: 'Hide advanced' })).toHaveAttribute('aria-expanded', 'true');
  const side = d.getByRole('group', { name: 'Parameters of' });
  await expect(side.getByRole('button', { name: 'Upload' })).toHaveAttribute('aria-pressed', 'true');
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('100');
  await expect(d.getByLabel('Delay distribution')).toHaveValue('normal');
  await expect(d.getByRole('group', { name: 'Loss model' }).getByRole('button', { name: 'Burst (Gilbert-Elliott)' })).toHaveAttribute('aria-pressed', 'true');
  await expect(d.getByLabel('Good → bad (%)')).toHaveValue('1');
  await expect(d.getByLabel('Bad → good (%)')).toHaveValue('30');
  await d.getByLabel('Delivered when good (%)').fill('99');

  await side.getByRole('button', { name: 'Download' }).click();
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('');
  await expect(d.getByLabel('Rate limit')).toHaveValue('64 kbit/s');
  await expect(d.getByLabel('Queue limit (packets)')).toHaveValue('20');
  await expect(d.getByLabel('Flapping')).toBeChecked();
  await expect(d.getByLabel('Up (s)')).toHaveValue('20');
  await d.getByLabel('Down (s)').fill('5');
  await d.getByLabel('Loss (%)').fill('2');
  // a problem of the download set is named from the upload side too
  await d.getByLabel('Jitter (ms)').fill('10');
  await expect(d.getByLabel('Jitter (ms)')).toHaveAccessibleDescription('Jitter cannot be larger than the latency.');
  await side.getByRole('button', { name: 'Upload' }).click();
  await expect(d.getByText('The download parameters need fixing.')).toBeVisible();
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await side.getByRole('button', { name: 'Download' }).click();
  await d.getByLabel('Jitter (ms)').fill('');

  await expect(d.getByTestId('fault-preview')).toContainText('upload and download apart → ↑ 100 ms ±20, normal delay, burst loss, 2 % loss, ↓ 64 kbit/s, queue 20 pkts, ↓ flapping 20s up / 5s down');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Sensor-07')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({
    target: { device: SENSOR },
    fault: {
      upload: { latency: '100ms', jitter: '20ms', distribution: 'normal', burst_loss: { p: '1%', r: '30%', k: '99%' } },
      download: { loss: '2%', rate: '64kbit', queue_limit: 20, flapping: { up: '20s', down: '5s' } },
    },
  });
  expect(gw.overlays.filter((o) => o.target?.device === SENSOR).map((o) => o.id)).toEqual([ADV]);
});
