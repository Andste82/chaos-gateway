import { expect, test, type Page } from '@playwright/test';
import type { LanNetwork } from '../src/api/generated/model';
import { IOT, MockGateway, TEST, toast, withNetworks } from './mock';

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const MAC = '02:00:00:00:00:2a';
const WIFI = '02:00:00:00:00:2b';

// #73: a configured device is edited from its detail page (name, network, fixed IP), through the
// preview-and-apply drawer; the dialog checks what the gateway checks, notes what it allows, and shows
// what the gateway refused under the field.
function lab(): MockGateway {
  const gw = withNetworks(MockGateway.sample());
  gw.config = {
    ...gw.config,
    devices: {
      // the ESP has a second MAC; its lease is on the first one
      [ESP]: { name: 'ESP32-42', identifiers: { macs: [WIFI, MAC] }, network: IOT },
      [SENSOR]: { name: 'Sensor-07', identifiers: { macs: ['02:00:00:00:00:39'] }, network: IOT, fixed_ip: '10.10.0.57' },
    },
  };
  gw.devices = gw.devices.map((d) => (gw.config.devices?.[d.id] ? { ...d, config: gw.config.devices[d.id] } : d));
  return gw;
}

async function openEdit(page: Page) {
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  return page.getByRole('dialog', { name: 'Edit ESP32-42' });
}

test('a fixed IP is set, changed and removed from the device detail, via preview and apply', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('device-fixed-ip-value')).toHaveText('none');

  // set: the checks of the gateway, then the notes that do not stop it
  await page.getByRole('button', { name: 'Set a fixed IP for ESP32-42' }).click();
  let dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  const ip = dialog.getByLabel('Fixed IP (DHCP reservation)');
  await ip.fill('10.10.0');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText('Not an IPv4 address, such as 10.10.0.50.');
  await ip.fill('10.10.0.1');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText("10.10.0.1 is the gateway's own address in IoT.");
  await ip.fill('10.20.0.5');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText('10.20.0.5 is not a host address of IoT (10.10.0.0/24).');
  await ip.fill('10.10.0.57');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText('10.10.0.57 is already reserved for device Sensor-07.');
  await expect(ip).toHaveAttribute('aria-invalid', 'true');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  // in the pool and leased to another client: allowed, with notes
  await ip.fill('10.10.0.133');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveCount(0);
  await expect(dialog.locator('[data-hint="pool"]')).toContainText('10.10.0.133 lies in the default DHCP pool 10.10.0.128–10.10.0.254 of IoT. The gateway allows it');
  await expect(dialog.locator('[data-hint="leased"]')).toContainText('10.10.0.133 is leased to 3a:f1:9c:22:07:b4 now');
  await ip.fill('10.10.0.50');
  await expect(dialog.locator('[data-hint="pool"]')).toHaveCount(0);
  await expect(dialog.locator('[data-hint="leased"]')).toHaveCount(0);
  await expect(dialog.locator('[data-hint="renewal"]')).toHaveText(
    'The device gets 10.10.0.50 when it renews its lease, which takes up to half the lease time (30 min).',
  );
  await expect(dialog.locator('[data-hint="first_mac"]')).toHaveText(
    `The reservation is for the device's first MAC; ${MAC}, the MAC of its current lease, is put first.`,
  );
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();

  let drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device ESP32-42 changed');
  const sent = gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: Record<string, unknown> };
  expect(sent.devices).toEqual({ [ESP]: { fixed_ip: '10.10.0.50', identifiers: { macs: [MAC, WIFI] } } });
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('device-fixed-ip-value')).toHaveText('10.10.0.50');
  await expect(page.getByTestId('device-fixed-ip-mark')).toHaveCount(0);

  // change it, and rename the device
  await page.getByRole('button', { name: 'Edit the fixed IP of ESP32-42' }).click();
  dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.50');
  await expect(dialog.locator('[data-hint="renewal"]')).toHaveCount(0);
  await dialog.getByLabel('Name').fill('Sensor-07');
  await expect(dialog.getByText('The name Sensor-07 is already used by another device.')).toBeVisible();
  await dialog.getByLabel('Name').fill('esp-kitchen');
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.10.0.60');
  await expect(dialog.locator('[data-hint="first_mac"]')).toHaveText(`The reservation is for the device's first MAC, ${MAC}, not for its other one.`);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  drawer = page.getByRole('dialog', { name: 'Preview revision 44' });
  await expect(drawer).toBeVisible();
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({ [ESP]: { name: 'esp-kitchen', fixed_ip: '10.10.0.60' } });
  // backing out keeps the change in the draft, shown as not applied yet
  await drawer.getByRole('button', { name: 'Close' }).click();
  await expect(page.getByTestId('device-fixed-ip-value')).toHaveText('10.10.0.60');
  await expect(page.getByTestId('device-fixed-ip-mark')).toHaveText('not applied yet');
  await expect(page.getByTestId('unapplied-bar')).toContainText('2 changes in 1 device · not applied yet');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: /^Apply revision/ }).click();
  await expect(page.getByRole('heading', { name: 'esp-kitchen' })).toBeVisible();
  await expect(page.getByTestId('device-fixed-ip-value')).toHaveText('10.10.0.60');

  // remove it
  await page.getByRole('button', { name: 'Edit the fixed IP of esp-kitchen' }).click();
  dialog = page.getByRole('dialog', { name: 'Edit esp-kitchen' });
  await dialog.getByRole('button', { name: 'Remove the fixed IP 10.10.0.60' }).click();
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('');
  await expect(dialog.locator('[data-hint="removed"]')).toHaveText(
    'Without the reservation the device keeps 10.10.0.60 until its lease runs out; then it gets an address from the pool.',
  );
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  drawer = page.getByRole('dialog', { name: /^Preview revision/ });
  await expect(drawer).toBeVisible();
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({ [ESP]: { fixed_ip: null } });
  await drawer.getByRole('button', { name: /^Apply revision/ }).click();
  await expect(page.getByTestId('device-fixed-ip-value')).toHaveText('none');
  expect(gw.config.devices?.[ESP]).toEqual({ name: 'esp-kitchen', identifiers: { macs: [MAC, WIFI] }, network: IOT });
});

test("a fixed IP the gateway refuses shows the gateway's message under the field", async ({ page }) => {
  const gw = lab();
  gw.rejectRevision = [{ path: `/devices/${ESP}/fixed_ip`, code: 'outside_subnet', message: '10.20.0.50 is not a host address of Test (10.20.0.0/24)' }];
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  let dialog = await openEdit(page);
  // another network: the checks follow it
  await dialog.getByLabel('Network').selectOption({ label: 'Test · 10.20.0.0/24' });
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.20.0.50');
  // DHCP is on by default in Test: the default pool is the second half
  await expect(dialog.locator('[data-hint="pool"]')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-invalid')).toContainText('10.20.0.50 is not a host address of Test (10.20.0.0/24)');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({
    [ESP]: { network: TEST, fixed_ip: '10.20.0.50', identifiers: { macs: [MAC, WIFI] } },
  });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(page.getByTestId('device-refused')).toContainText('The gateway refused the change: 10.20.0.50 is not a host address of Test');
  await page.getByRole('button', { name: 'Fix ESP32-42' }).click();
  dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText('10.20.0.50 is not a host address of Test (10.20.0.0/24)');
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveAttribute('aria-invalid', 'true');
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  // changed, the gateway's message gives way
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.20.0.51');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeEnabled();
});

test('a device without a MAC cannot have a fixed IP; DHCP off in its network is noted; a discovered device is adopted, not edited', async ({ page }) => {
  const gw = lab();
  gw.config.devices![ESP] = { name: 'ESP32-42', identifiers: { ipv4: ['10.10.0.42'] }, network: IOT };
  gw.config.devices![SENSOR] = { ...gw.config.devices![SENSOR]!, network: TEST, fixed_ip: undefined };
  gw.config.networks![TEST] = { ...(gw.config.networks![TEST] as LanNetwork), dhcp: { enabled: false } };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  let dialog = await openEdit(page);
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.10.0.50');
  await expect(dialog.getByTestId('fixed-ip-error')).toHaveText("The reservation is for the device's first MAC address: the device has none.");
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  await page.goto(`/devices/${SENSOR}`);
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  dialog = page.getByRole('dialog', { name: 'Edit Sensor-07' });
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.20.0.200');
  await expect(dialog.locator('[data-hint]')).toHaveText([
    'DHCP is off in Test: the reservation has no effect until it is turned on.',
    "The reservation is for the device's MAC 02:00:00:00:00:39.",
  ]);
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  await page.goto('/devices/d15c0000-0000-4000-8000-3af19c2207b4');
  await expect(page.getByRole('button', { name: 'Adopt' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
});
