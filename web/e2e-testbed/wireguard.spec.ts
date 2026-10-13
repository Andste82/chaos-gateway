import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import jsQR from 'jsqr';
import { PNG } from 'pngjs';

// M14 (plan §5): create a WireGuard client in the UI against the real gateway in the namespace testbed,
// scan its QR code (the decoded text equals the download) and save the configuration, from which the Go
// test TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel brings the tunnel up in the remote client's
// namespace; the client list then shows it online. Then commit-confirm: a client of the management hub
// waits for confirmation, nobody confirms, and the UI shows the rollback after the timeout. Under software
// emulation an apply takes minutes.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
};
const applyTimeout = 10 * 60_000;

async function scanQr(page: Page): Promise<string | undefined> {
  const png = PNG.sync.read(await page.getByTestId('client-qr').screenshot());
  return jsQR(new Uint8ClampedArray(png.data), png.width, png.height)?.data;
}

test('a WireGuard client from the UI: QR code and download, its tunnel comes up; an unconfirmed change is rolled back', async ({ page }) => {
  await page.goto('/networks');
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel('Password').fill(env('CHAOSGW_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page.getByRole('heading', { name: 'Networks', exact: true })).toBeVisible();

  // 1. the hub lab-hub with its client rA; a new client gets the next free address
  await page.getByTestId('network-card').filter({ hasText: 'lab-hub' }).click();
  const rows = page.getByTestId('client-row');
  await expect(rows.filter({ hasText: 'rA' })).toContainText('10.99.0.2');
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to lab-hub' });
  await expect(dialog.getByLabel('Tunnel address')).toHaveValue('10.99.0.3');
  await dialog.getByLabel('Name').fill('rC');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: /^Preview revision \d+$/ });
  await expect(drawer.getByTestId('preview-changes')).toContainText('rC', { timeout: applyTimeout });
  await drawer.getByRole('button', { name: 'Show Linux changes' }).click();
  await expect(drawer.getByTestId('preview-linux')).toContainText('allowed 10.99.0.3/32');
  await drawer.getByRole('button', { name: /^Apply revision/ }).click();
  await expect(drawer).toBeHidden({ timeout: applyTimeout });
  const rc = rows.filter({ hasText: 'rC' });
  await expect(rc).toContainText('10.99.0.3');
  await expect(rc).not.toContainText('NEW');

  // 2. its configuration: the QR code decodes to the file the download saves
  await rc.getByRole('button', { name: 'Configuration and QR code of rC' }).click();
  const config = page.getByRole('dialog', { name: 'Configuration of rC' });
  await expect(config.getByRole('img', { name: 'QR code of the configuration of rC' })).toBeVisible();
  const [download] = await Promise.all([page.waitForEvent('download'), config.getByRole('button', { name: 'Download rC.conf' }).click()]);
  const saved = readFileSync((await download.path())!, 'utf8');
  expect(saved).toContain('PrivateKey = ');
  expect(saved).toContain('Address = 10.99.0.3/32');
  expect(await scanQr(page)).toBe(saved);
  // the Go test brings the tunnel up from it
  await download.saveAs(env('CHAOSGW_CONF_OUT'));
  await config.getByRole('button', { name: 'Close' }).first().click();

  // 3. the handshake: the client list shows rC online
  await expect(rc).toContainText('Online', { timeout: 5 * 60_000 });
  await expect(rc).toContainText(/handshake (just now|\d+ s ago|\d+ min ago)/);

  // 4. commit-confirm: a client of the management hub admin waits for confirmation; nobody confirms
  await page.getByTestId('network-card').filter({ hasText: 'admin' }).click();
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const adm = page.getByRole('dialog', { name: 'Add client to admin' });
  await expect(adm.getByLabel('Tunnel address')).toHaveValue('10.98.0.3');
  await adm.getByLabel('Name').fill('adm2');
  await adm.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer).toContainText('This change can cut you off from the gateway', { timeout: applyTimeout });
  await drawer.getByRole('button', { name: /^Apply revision/ }).click();
  await expect(drawer).toBeHidden({ timeout: applyTimeout });
  const banner = page.getByTestId('countdown-banner');
  await expect(banner).toContainText('waits for confirmation');
  await expect(banner.getByRole('button', { name: 'Roll back now' })).toBeVisible();
  const pending = (await banner.textContent())?.match(/Revision (\d+) is applied/)?.[1];
  expect(pending).toBeDefined();
  await expect(rows.filter({ hasText: 'adm2' })).toBeVisible();
  // the deadline passes: the gateway rolls back, the UI says so and the client is gone
  await expect(page.locator('[data-kind]').filter({ hasText: `Revision ${pending} rolled back` })).toContainText('It was not confirmed in time. Rolled back to revision', {
    timeout: applyTimeout,
  });
  await expect(banner).toBeHidden();
  await expect(rows.filter({ hasText: 'adm2' })).toHaveCount(0, { timeout: applyTimeout });
  await expect(rows.filter({ hasText: 'adm' })).toHaveCount(1);
});
