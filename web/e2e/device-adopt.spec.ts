import { expect, test, type Page } from '@playwright/test';
import type { DeviceView, LanNetwork } from '../src/api/generated/model';
import { HUB, IOT, MockGateway, toast, withNetworks } from './mock';

const DEV = 'd15c0000-0000-4000-8000-3af19c2207b4';
const MAC = '3a:f1:9c:22:07:b4';
const RANDOM = '3e:00:00:00:00:01';

// #74: a discovered device can keep the address of its current lease when it is adopted, as a DHCP
// reservation (fixed IP) for its first MAC; the MAC of the lease is put first (decision on #77).
function lab(over: (d: DeviceView) => DeviceView = (d) => d): MockGateway {
  const gw = withNetworks(MockGateway.sample());
  gw.devices = gw.devices.map((d) =>
    d.id === DEV
      ? over({
          ...d,
          // a randomized MAC seen first; the lease is on the other one
          observed: {
            ...d.observed,
            macs: [RANDOM, MAC],
            lease: { ip: '10.10.0.133', mac: MAC, network: IOT, expires_at: new Date(Date.now() + 5 * 60_000).toISOString(), state: 'active' },
          },
        })
      : d,
  );
  return gw;
}

async function openAdopt(page: Page) {
  await page.goto('/devices');
  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  return page.getByRole('dialog', { name: 'Adopt device' });
}

test('adopting with "Keep this address": the fixed IP of the lease, its MAC first, via preview and apply', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  const dialog = await openAdopt(page);
  const keep = dialog.getByRole('checkbox', { name: 'Keep this address (DHCP reservation)' });
  await expect(keep).not.toBeChecked();
  await expect(dialog.getByTestId('adopt-reservation')).toContainText('10.10.0.133');
  await keep.check();

  const ip = dialog.getByLabel('Fixed IP (DHCP reservation)');
  await expect(ip).toHaveValue('10.10.0.133');
  // the checks of the device dialog: the gateway's own address is refused
  await ip.fill('10.10.0.1');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText("10.10.0.1 is the gateway's own address in IoT.");
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await ip.fill('10.10.0.133');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveCount(0);
  // in the pool: allowed, a note (decision on #76); the device keeps its address; the lease's MAC first
  await expect(dialog.locator('[data-hint]')).toHaveText([
    '10.10.0.133 lies in the DHCP pool 10.10.0.128–10.10.0.254 of IoT. The gateway allows it, but the pool may hand it to another client first; an address outside the pool is safer.',
    'The device has 10.10.0.133 from its current lease and keeps it; from now on the address is reserved for it.',
    `The reservation is for the device's first MAC; ${MAC}, the MAC of its current lease, is put first.`,
  ]);
  const preview = dialog.getByTestId('adopt-preview');
  await expect(preview).toContainText(`with MAC ${MAC}, ${RANDOM} in network IoT and always gets 10.10.0.133 from DHCP in a new revision based on revision 42.`);
  await expect(preview).not.toContainText('Traffic is not changed');
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device Thermostat-A added');
  expect(gw.requestsTo('POST', '/revisions').at(-1)!.body).toEqual({
    devices: { [DEV]: { name: 'Thermostat-A', identifiers: { macs: [MAC, RANDOM] }, network: IOT, fixed_ip: '10.10.0.133' } },
  });
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.config.devices?.[DEV]).toMatchObject({ fixed_ip: '10.10.0.133', identifiers: { macs: [MAC, RANDOM] } });
});

test('adopting without the box adds no fixed IP and keeps the MACs in the order they were seen', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  const dialog = await openAdopt(page);
  const keep = dialog.getByRole('checkbox', { name: 'Keep this address (DHCP reservation)' });
  // checked and unchecked again: as if it never was
  await keep.check();
  await expect(dialog.getByTestId('adopt-preview')).toContainText('always gets 10.10.0.133 from DHCP');
  await keep.uncheck();
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveCount(0);
  await expect(dialog.getByTestId('adopt-preview')).toContainText(`with MAC ${RANDOM}, ${MAC} in network IoT in a new revision based on revision 42. Traffic is not changed.`);
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions').at(-1)!.body).toEqual({
    devices: { [DEV]: { name: 'Thermostat-A', identifiers: { macs: [RANDOM, MAC] }, network: IOT } },
  });
});

const notOffered: [string, () => MockGateway][] = [
  ['without a MAC', () => lab((d) => ({ ...d, observed: { ...d.observed, macs: [] } }))],
  ['without a network', () => lab((d) => ({ ...d, network: undefined }))],
  ['in a WireGuard network', () => lab((d) => ({ ...d, network: HUB }))],
  [
    'in a network with DHCP off',
    () => {
      const gw = lab();
      gw.config.networks![IOT] = { ...(gw.config.networks![IOT] as LanNetwork), dhcp: { enabled: false } };
      return gw;
    },
  ],
];
for (const [what, make] of notOffered) {
  test(`the box is not offered for a device ${what}`, async ({ page }) => {
    const gw = make();
    await gw.install(page);
    // the configuration has come and is rendered (the networks the box depends on), so the box's
    // absence is not just the dialog before the networks are known
    const config = page.waitForResponse((r) => r.url().endsWith('/revisions/active'));
    const dialog = await openAdopt(page);
    await config;
    await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
    await expect(dialog.getByTestId('adopt-preview')).toContainText('Traffic is not changed.');
    await expect(dialog.getByTestId('adopt-reservation')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Preview & apply' }).click();
    await expect(page.getByRole('dialog', { name: 'Preview revision 43' })).toBeVisible();
    const body = gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: Record<string, object> };
    expect(body.devices[DEV]).not.toHaveProperty('fixed_ip');
  });
}
