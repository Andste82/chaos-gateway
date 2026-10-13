import { expect, test } from '@playwright/test';

// M12 (plan §5): first start with the setup token → assign interfaces → see a discovered device,
// in the browser against the real gateway in the namespace testbed. The Go test that runs this
// passes the gateway's address, its setup token and the device that sends traffic.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
};

test('first start with the setup token, assign the interfaces, see a discovered device', async ({ page }) => {
  const token = env('CHAOSGW_SETUP_TOKEN');
  const password = 'correct horse battery staple';
  const deviceMAC = env('CHAOSGW_DEVICE_MAC');
  const deviceIP = env('CHAOSGW_DEVICE_IP');

  // an unconfigured gateway sends every page to the setup
  await page.goto('/devices');
  await expect(page).toHaveURL(/\/setup$/);

  await page.getByLabel('Setup token').fill(token);
  await page.getByRole('button', { name: 'Next' }).click();

  // the interfaces of the gateway's namespace: uplink wan0, management mgmt0, test network on lan0
  await page.getByLabel('Uplink (towards the Internet)').selectOption('wan0');
  await page.getByLabel('Management (where you reach the UI)').selectOption('mgmt0');
  // the gateway's default route leaves through mgmt0, so the router on the uplink is named
  await page.getByLabel(/Router on the uplink/).fill(env('CHAOSGW_UPLINK_GATEWAY'));
  await page.getByRole('checkbox', { name: /lan0/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();

  await page.getByLabel('Network name').fill('IoT');
  await page.getByLabel('Gateway address').fill(env('CHAOSGW_LAN_ADDRESS'));
  await page.getByRole('button', { name: 'Next' }).click();

  await page.getByLabel('Admin password').fill(password);
  await page.getByLabel('Repeat the password').fill(password);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByTestId('setup-review')).toContainText('lan0');

  // the gateway applies revision 1 (minutes under emulation) and waits for the confirmation
  await page.getByRole('button', { name: 'Apply setup' }).click();
  await expect(page.getByTestId('setup-confirm')).toBeVisible({ timeout: 10 * 60_000 });
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await expect(page.getByTestId('topology-network')).toContainText('IoT');

  // the device that sends traffic through the gateway appears in the list, not adopted yet
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Devices' }).click();
  const row = page.getByTestId('device-row').filter({ hasText: deviceMAC });
  await expect(row).toBeVisible({ timeout: 5 * 60_000 });
  await expect(row).toContainText(deviceIP);
  await expect(row).toContainText('IoT');
  await expect(row.getByRole('button', { name: /^Adopt / })).toBeVisible();
  await page.getByRole('group', { name: 'Filter' }).getByRole('button', { name: /^Not adopted/ }).click();
  await expect(row).toBeVisible();

  // and its detail page shows where the gateway saw it
  await row.getByRole('link').click();
  await expect(page.getByTestId('device-identity')).toContainText(deviceIP);
  await expect(page.getByText('discovered, not adopted')).toBeVisible();
});
