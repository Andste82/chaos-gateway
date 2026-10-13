import { expect, test } from '@playwright/test';
import { MockGateway, toast } from './mock';

// M12: the first-start setup wizard against a mocked gateway: token, interfaces with the netplan
// hints of the preflight, test networks with validation, password, apply, confirm.
test('first start: the wizard sets up the gateway and confirms the revision', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);

  await page.goto('/devices');
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up Chaos Gateway' })).toBeVisible();

  // step 1: the token is required; the preflight shows its warning with the fix
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Enter the setup token from the container log.')).toBeVisible();
  await expect(page.getByTestId('preflight')).toContainText('sch_netem is not loaded yet');
  await expect(page.getByTestId('preflight')).toContainText('modules-load.d');
  await page.getByLabel('Setup token').fill('wrong-token');
  await page.getByRole('button', { name: 'Next' }).click();

  // step 2: uplink suggested from the default route, management an addressed other interface
  await expect(page.getByLabel('Uplink (towards the Internet)')).toHaveValue('eth0');
  await expect(page.getByLabel('Management (where you reach the UI)')).toHaveValue('eth1');
  // an uplink without the host's default route needs its router, in its network
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth3');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('The default route of this host does not leave through eth3')).toBeVisible();
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth1');
  await page.getByLabel('Management (where you reach the UI)').selectOption('eth1');
  await page.getByLabel(/Router on the uplink/).fill('10.0.0.1');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText("The router must be in the uplink's network (192.168.56.10/24).")).toBeVisible();
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth0');
  await page.getByLabel('Management (where you reach the UI)').selectOption('eth1');
  await page.getByLabel(/Router on the uplink/).fill('');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Assign at least one interface to a test network.')).toBeVisible();
  await page.getByRole('checkbox', { name: /eth2/ }).check();
  await page.getByRole('checkbox', { name: /eth3/ }).check();
  // eth3 is configured by netplan: the hint says how to free it
  await expect(page.getByTestId('netplan-hints')).toContainText('eth3 is configured by the operating system');
  await expect(page.getByTestId('netplan-hints')).toContainText('sudo netplan apply');
  await page.getByRole('checkbox', { name: /eth3/ }).uncheck();
  await expect(page.getByTestId('netplan-hints')).toHaveCount(0);
  await page.getByRole('button', { name: 'Next' }).click();

  // step 3: validation under the field, the step does not advance until it is valid
  await page.getByLabel('Network name').fill('-bad');
  await page.getByLabel('Gateway address').fill('192.168.56.1/24');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Letters, digits, dot, dash and underscore')).toBeVisible();
  await expect(page.getByText('This prefix overlaps the uplink or the management network.')).toBeVisible();
  await page.getByLabel('Network name').fill('IoT');
  await page.getByLabel('Gateway address').fill('10.10.0.0/24');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('This is the network or broadcast address')).toBeVisible();
  await page.getByLabel('Gateway address').fill('10.10.0.1/24');
  await expect(page.getByRole('switch', { name: 'DHCP on eth2' })).toHaveAttribute('aria-checked', 'true');
  await page.getByRole('button', { name: 'Next' }).click();

  // step 4: password rules
  await page.getByLabel('Admin password').fill('short');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'At least 12 characters.' })).toBeVisible();
  await page.getByLabel('Admin password').fill(gw.password);
  await page.getByLabel('Repeat the password').fill(gw.password + 'x');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('The passwords differ.')).toBeVisible();
  await page.getByLabel('Repeat the password').fill(gw.password);
  await page.getByRole('button', { name: 'Next' }).click();

  // step 5: review; a wrong token sends the user back to step 1
  await expect(page.getByTestId('setup-review')).toContainText('eth2 · 10.10.0.1/24 · DHCP on');
  await page.getByRole('button', { name: 'Apply setup' }).click();
  await expect(page.getByText('The setup token is not valid.')).toBeVisible();
  await page.getByLabel('Setup token').fill(gw.setupToken);
  for (let i = 0; i < 4; i++) await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Apply setup' }).click();

  // the request: revision 1 with uplink, management and one test network, the token in its header
  await expect(page.getByTestId('setup-confirm')).toBeVisible();
  const sent = gw.requestsTo('POST', '/setup').at(-1)!;
  expect(sent.headers['x-setup-token']).toBe(gw.setupToken);
  const body = sent.body as { admin_password: string; configuration: { uplink: unknown; management: unknown; networks: Record<string, unknown> } };
  expect(body.admin_password).toBe(gw.password);
  expect(body.configuration.uplink).toEqual({ interface: { name: 'eth0', mac: '52:54:00:00:00:01' } });
  expect(body.configuration.management).toEqual({ interface: { name: 'eth1', mac: '52:54:00:00:00:02' } });
  expect(Object.values(body.configuration.networks)).toEqual([
    { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth2', mac: '52:54:00:00:00:03' }], address: '10.10.0.1/24', dhcp: { enabled: true } },
  ]);

  // commit-confirm: the countdown runs, Confirm finishes the setup and opens the overview
  await expect(page.getByTestId('countdown-banner')).toContainText(/0:5\d left|1:00 left/);
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await expect(toast(page, 'Setup complete')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions/1/confirm')).toHaveLength(1);
});

test('after the setup the wizard is not reachable any more', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/setup');
  await expect(page).toHaveURL(/\/$/);
});
