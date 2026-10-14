import { expect, test, type Page } from '@playwright/test';
import { MockGateway, toast } from './mock';

// M12: the first-start setup wizard against a mocked gateway: token, interfaces with the netplan
// hints of the preflight, test networks with validation, password, apply, confirm.
test('first start: the wizard sets up the gateway and confirms the revision', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);

  await page.goto('/devices');
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up Chaos Gateway' })).toBeVisible();

  // step 1: the token is required; the preflight shows its warning with the fix. Validation (plan
  // §2.17): the message under the field, the button disabled until the step is valid
  const next = page.getByRole('button', { name: 'Next' });
  await next.click();
  await expect(page.getByText('Enter the setup token from the container log.')).toBeVisible();
  await expect(next).toBeDisabled();
  await expect(page.getByTestId('preflight')).toContainText('sch_netem is not loaded yet');
  await expect(page.getByTestId('preflight')).toContainText('modules-load.d');
  // the checks of the interfaces are shown where the test interfaces are chosen, not here
  await expect(page.getByTestId('preflight')).not.toContainText('eth0 is configured');
  await page.getByLabel('Setup token').fill('wrong-token');
  await expect(page.getByText('Enter the setup token from the container log.')).toHaveCount(0);
  await next.click();

  // step 2: uplink suggested from the default route, management an addressed other interface
  await expect(page.getByLabel('Uplink (towards the Internet)')).toHaveValue('eth0');
  await expect(page.getByLabel('Management (where you reach the UI)')).toHaveValue('eth1');
  // the options name the interface and its address, the part that decides; the MAC stands below
  await expect(page.getByLabel('Uplink (towards the Internet)').locator('option:checked')).toHaveText('eth0 · 192.168.1.57/24');
  await expect(page.getByTestId('uplink-detail')).toHaveText('MAC 52:54:00:00:00:01');
  // the current step's number is readable on its tinted background (text-2, not text-3)
  await expect(page.locator('li[aria-current="step"] .mono')).toHaveClass(/text-text-2/);
  // an uplink without the host's default route needs its router, in its network
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth3');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('The default route of this host does not leave through eth3')).toBeVisible();
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth1');
  await page.getByLabel('Management (where you reach the UI)').selectOption('eth1');
  await page.getByLabel(/Router on the uplink/).fill('10.0.0.1');
  // once the step's button was pressed, the messages follow every change
  await expect(page.getByText("The router must be in the uplink's network (192.168.56.10/24).")).toBeVisible();
  await expect(next).toBeDisabled();
  await page.getByLabel('Uplink (towards the Internet)').selectOption('eth0');
  await page.getByLabel('Management (where you reach the UI)').selectOption('eth1');
  await page.getByLabel(/Router on the uplink/).fill('');
  await expect(page.getByText('Assign at least one interface to a test network.')).toBeVisible();
  await expect(next).toBeDisabled();
  await page.getByRole('checkbox', { name: /eth2/ }).check();
  await page.getByRole('checkbox', { name: /eth3/ }).check();
  // eth3 is configured by netplan and NetworkManager: the preflight's check names the file, the entry and the
  // connection, and says what to remove (P3-M12-07); the wizard shows its text, nothing of its own
  const hints = page.getByTestId('netplan-hints');
  await expect(hints).toContainText('eth3 is configured by netplan (/etc/netplan/60-lab.yaml, "lab" under "ethernets")');
  await expect(hints).toContainText('delete the entry "lab:" under "ethernets" in /etc/netplan/60-lab.yaml, then run "sudo netplan apply"');
  await expect(hints).toContainText('sudo nmcli connection delete "Wired connection 2"');
  // plan §3.4: a test interface has no entry there at all; nothing offers to only turn the addressing off
  await expect(hints).not.toContainText('dhcp4');
  // eth2 carries none, and the uplink's check is not one of a test interface
  await expect(hints).not.toContainText('eth2');
  await expect(hints).not.toContainText('eth0');
  // after the entry is gone, checking again reads the preflight anew
  const before = gw.requestsTo('GET', '/setup').length;
  await hints.getByRole('button', { name: 'Check again' }).click();
  await expect.poll(() => gw.requestsTo('GET', '/setup').length).toBeGreaterThan(before);
  await page.getByRole('checkbox', { name: /eth3/ }).uncheck();
  await expect(hints).toHaveCount(0);
  await page.getByRole('button', { name: 'Next' }).click();

  // step 3: validation under the field, the step does not advance until it is valid
  await page.getByLabel('Network name').fill('-bad');
  await page.getByLabel('Gateway address').fill('192.168.56.1/24');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Letters, digits, dot, dash and underscore')).toBeVisible();
  await expect(page.getByText('This prefix overlaps the uplink or the management network.')).toBeVisible();
  await expect(next).toBeDisabled();
  await page.getByLabel('Network name').fill('IoT');
  await page.getByLabel('Gateway address').fill('10.10.0.0/24');
  await expect(page.getByText('This is the network or broadcast address')).toBeVisible();
  await expect(page.getByText('Letters, digits, dot, dash and underscore')).toHaveCount(0);
  await expect(next).toBeDisabled();
  await page.getByLabel('Gateway address').fill('10.10.0.1/24');
  await expect(page.getByRole('switch', { name: 'DHCP on eth2' })).toHaveAttribute('aria-checked', 'true');
  await page.getByRole('button', { name: 'Next' }).click();

  // step 4: password rules
  await page.getByLabel('Admin password').fill('short');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'At least 12 characters.' })).toBeVisible();
  await page.getByLabel('Admin password').fill(gw.password);
  await page.getByLabel('Repeat the password').fill(gw.password + 'x');
  await expect(page.getByText('The passwords differ.')).toBeVisible();
  await expect(next).toBeDisabled();
  await page.getByLabel('Repeat the password').fill(gw.password);
  await page.getByRole('button', { name: 'Next' }).click();

  // step 5: review; a wrong token sends the user back to step 1
  await expect(page.getByTestId('setup-review')).toContainText('eth2 · 10.10.0.1/24 · DHCP on');
  await page.getByRole('button', { name: 'Apply setup' }).click();
  await expect(page.getByText('The setup token is not valid.')).toBeVisible();
  await expect(next).toBeDisabled();
  await page.getByLabel('Setup token').fill(gw.setupToken);
  await expect(page.getByText('The setup token is not valid.')).toHaveCount(0);
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

/** Runs the wizard with its proposals up to the commit-confirm of revision 1. */
async function applySetup(page: Page, gw: MockGateway) {
  await page.goto('/setup');
  await page.getByLabel('Setup token').fill(gw.setupToken);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /eth2/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByLabel('Admin password').fill(gw.password);
  await page.getByLabel('Repeat the password').fill(gw.password);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Apply setup' }).click();
  await expect(page.getByTestId('setup-confirm')).toBeVisible();
}

test("the countdown runs on the gateway's clock: a browser clock 90 s ahead does not end it", async ({ page }) => {
  // a Raspberry Pi without a real-time clock, before NTP: the gateway is 90 s behind the browser
  const gw = MockGateway.firstStart();
  gw.clockSkewMs = -90_000;
  await gw.install(page);
  await applySetup(page, gw);
  await expect(page.getByTestId('countdown-banner').getByRole('timer')).toHaveText(/^(0:5\d|1:00) left$/);
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions/1/confirm')).toHaveLength(1);
});

test('a countdown that ran out in the browser keeps Confirm until the gateway rolled back', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await page.clock.install();
  await gw.install(page);
  await applySetup(page, gw);
  // the browser's countdown ends, the gateway (which sends no Date header here) has not rolled back
  await page.clock.fastForward('01:05');
  await expect(page.getByTestId('countdown-over')).toContainText('The countdown has run out here.');
  await expect(page.getByTestId('countdown-banner').getByRole('timer')).toHaveText('0:00 left');
  await expect(page.getByRole('button', { name: 'Confirm setup' })).toBeVisible();
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
});

test('the wizard says the setup was rolled back once the gateway reopened it', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await page.clock.install();
  await gw.install(page);
  await applySetup(page, gw);
  await page.clock.fastForward('01:05');
  await expect(page.getByRole('button', { name: 'Confirm setup' })).toBeVisible();
  // the gateway rolls back; the wizard asks it every 2 s
  gw.rollBackSetup();
  await page.clock.fastForward('00:03');
  await expect(page.getByText('The setup was not confirmed in time and was rolled back.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Confirm setup' })).toHaveCount(0);
  await expect(page.getByTestId('countdown-banner')).toHaveCount(0);
});

test('a Confirm that comes too late learns from the gateway that it rolled back', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);
  await applySetup(page, gw);
  // the gateway rolled back before the browser's countdown ended (its clock or the network)
  gw.rollBackSetup();
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByText('The setup was not confirmed in time and was rolled back.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Confirm setup' })).toHaveCount(0);
});

test('after a rollback, Start again runs the wizard once more with a new token, and the choices kept', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);
  await applySetup(page, gw);
  gw.rollBackSetup();
  gw.setupToken = 'setup-token-5678';
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await page.getByRole('button', { name: 'Start again' }).click();

  // step 1 again, the old token gone; the interface choice stays
  await expect(page.getByRole('listitem').filter({ hasText: 'Setup token' })).toHaveAttribute('aria-current', 'step');
  await expect(page.getByLabel('Setup token')).toHaveValue('');
  await page.getByLabel('Setup token').fill(gw.setupToken);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('checkbox', { name: /eth2/ })).toBeChecked();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  // the password is asked again
  await expect(page.getByLabel('Admin password')).toHaveValue('');
  await page.getByLabel('Admin password').fill(gw.password);
  await page.getByLabel('Repeat the password').fill(gw.password);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Apply setup' }).click();
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  expect(gw.requestsTo('POST', '/setup').at(-1)!.headers['x-setup-token']).toBe('setup-token-5678');
});

test('the session store follows a rollback: the app sends the browser to the setup', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);
  await applySetup(page, gw);
  gw.rollBackSetup();
  await page.getByRole('button', { name: 'Confirm setup' }).click();
  await expect(page.getByRole('button', { name: 'Start again' })).toBeVisible();
  // a route the session would have opened goes to the setup instead
  await page.evaluate(() => window.history.pushState({}, '', '/devices'));
  await page.evaluate(() => window.dispatchEvent(new PopStateEvent('popstate')));
  await expect(page).toHaveURL(/\/setup$/);
});

test('a setup rolled back while the browser is in the shell sends it to the wizard', async ({ page }) => {
  // the wizard sent the browser to the management address; the login there opened the shell
  const gw = MockGateway.firstStart();
  gw.setup = { completed: true };
  gw.loggedIn = true;
  gw.state = { generation: 1, active_revision: 1, safe_mode: false, pending_confirm: { revision: 1, deadline: new Date(Date.now() + 30_000).toISOString() } };
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('countdown-banner')).toContainText('Revision 1');

  gw.rollBackSetup();
  await gw.emit(page, { type: 'revision_rolled_back', message: 'rolled back', data: { revision: 1, reason: 'confirmation timeout' } });
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up Chaos Gateway' })).toBeVisible();
  await expect(page.getByTestId('countdown-banner')).toHaveCount(0);
  await expect(toast(page, 'The setup was rolled back')).toBeVisible();
});

test('the wizard proposes a free name and prefix for each test network', async ({ page }) => {
  const gw = MockGateway.firstStart();
  gw.setup = {
    ...gw.setup,
    interfaces: [
      ...(gw.setup.interfaces ?? []),
      { name: 'lan0', mac: '52:54:00:00:01:00', link_up: true, os_managed: false },
      { name: 'lan1', mac: '52:54:00:00:01:01', link_up: true, os_managed: false },
      { name: 'lan2', mac: '52:54:00:00:01:02', link_up: true, os_managed: false },
    ],
  };
  await gw.install(page);
  await page.goto('/setup');
  await page.getByLabel('Setup token').fill(gw.setupToken);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /lan0/ }).check();
  await page.getByRole('checkbox', { name: /lan1/ }).check();
  await page.getByRole('checkbox', { name: /lan0/ }).uncheck();
  await page.getByRole('checkbox', { name: /lan2/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByLabel('Network name').nth(0)).toHaveValue('test-2');
  await expect(page.getByLabel('Gateway address').nth(0)).toHaveValue('10.20.0.1/24');
  await expect(page.getByLabel('Network name').nth(1)).toHaveValue('test');
  await expect(page.getByLabel('Gateway address').nth(1)).toHaveValue('10.10.0.1/24');
  // the proposal passes the wizard's own checks
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByLabel('Admin password')).toBeVisible();
});

test('a field error is tied to its field for screen readers, only while it is shown', async ({ page }) => {
  const gw = MockGateway.firstStart();
  await gw.install(page);
  await page.goto('/setup');
  const token = page.getByLabel('Setup token');
  await expect(token).toHaveAttribute('aria-describedby', 'token-help');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(token).toHaveAttribute('aria-describedby', 'token-help token-error');
  await expect(token).toHaveAccessibleDescription(/Enter the setup token from the container log./);
  await token.fill(gw.setupToken);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /eth2/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  const name = page.getByLabel('Network name');
  await expect(name).not.toHaveAttribute('aria-describedby');
  await name.fill('-bad');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(name).toHaveAccessibleDescription(/Letters, digits, dot, dash and underscore/);
});
