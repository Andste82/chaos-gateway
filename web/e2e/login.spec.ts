import { expect, test } from '@playwright/test';
import { MockGateway } from './mock';

// M12: the login; a deep link survives it; logging out ends the session.
test('login with the admin password, back to the page asked for', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.loggedIn = false;
  await gw.install(page);

  await page.goto('/devices');
  await expect(page).toHaveURL(/\/login\?next=(%2F|\/)devices$/);
  await page.getByLabel('Password').fill('wrong password!');
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page.getByRole('alert')).toHaveText('Wrong password.');
  await expect(page.getByLabel('Password')).toHaveAttribute('aria-invalid', 'true');

  await page.getByLabel('Password').fill(gw.password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page).toHaveURL(/\/devices$/);
  await expect(page.getByRole('heading', { name: 'Devices' })).toBeVisible();

  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login/);
  expect(gw.requestsTo('POST', '/auth/logout')).toHaveLength(1);
});

test('an ended session leads back to the login', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await expect(page.getByTestId('device-row').first()).toBeVisible();
  gw.loggedIn = false; // the session expired on the gateway
  await page.evaluate(() => (window as unknown as { __sse: { fail: () => void } }).__sse.fail());
  await expect(page).toHaveURL(/\/login\?next=/);
});
