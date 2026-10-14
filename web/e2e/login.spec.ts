import { expect, test } from '@playwright/test';
import { MockGateway, withRules } from './mock';

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

// Phase 3 audit: a logout leaves nothing of the session in the page: the next login in the same tab has no
// unapplied edits of the last one and reads the configuration again.
test('logging out drops the draft and the data the session read', async ({ page }) => {
  const gw = withRules(MockGateway.sample());
  await gw.install(page);
  await page.goto('/rules');
  const web = page.getByTestId('rule-row').filter({ hasText: 'test-web' });
  await web.getByRole('switch', { name: 'Enable rule test-web' }).click();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 rule · not applied yet/);
  const reads = gw.requestsTo('GET', '/revisions/active').length;

  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel('Password').fill(gw.password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page).toHaveURL(/\/$/);
  // within the page (no reload): the Access rules link of the navigation
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Access rules' }).click();
  await expect(web.getByRole('switch', { name: 'Enable rule test-web' })).toHaveAttribute('aria-checked', 'true');
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  expect(gw.requestsTo('GET', '/revisions/active').length).toBeGreaterThan(reads);
});

// Phase 3 audit: after failed logins the gateway refuses more for a while (5 s, doubling up to 15 min); the
// form says until when, from the answer's Retry-After, and offers no new attempt until then. The alert is
// announced once: its text names a time of day and stays the same, while the countdown beside it ticks
// outside any live region.
test('a locked-out login says how long to wait', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.loggedIn = false;
  gw.loginLockedFor = 320;
  await gw.install(page);
  await page.goto('/login');
  await page.getByLabel('Password').fill(gw.password);
  await page.getByRole('button', { name: 'Log in' }).click();
  const alert = page.getByRole('alert');
  await expect(alert).toHaveText(/^Too many failed attempts\. You can try again at \d\d:\d\d:\d\d\.$/);
  const countdown = page.getByTestId('login-countdown');
  await expect(countdown).toHaveText(/^5:(20|19) left$/);
  await expect(page.getByRole('button', { name: 'Log in' })).toBeDisabled();
  const announced = await alert.textContent();
  const first = await countdown.textContent();
  await expect(countdown).not.toHaveText(first ?? '', { timeout: 3_000 });
  await expect(alert).toHaveText(announced ?? '');
  // the countdown is no live region and not inside one
  await expect(page.locator('[role="alert"] [data-testid="login-countdown"], [aria-live] [data-testid="login-countdown"]')).toHaveCount(0);
  await expect(countdown).not.toHaveAttribute('role');
  await expect(countdown).not.toHaveAttribute('aria-live');

  // a short lockout ends by itself, and the next attempt goes through
  gw.loginLockedFor = 1;
  await page.reload();
  await page.getByLabel('Password').fill(gw.password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page.getByRole('alert')).toHaveText(/^Too many failed attempts\. You can try again at \d\d:\d\d:\d\d\.$/);
  gw.loginLockedFor = 0;
  await expect(page.getByRole('alert')).toBeHidden({ timeout: 5_000 });
  await expect(countdown).toBeHidden();
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page).toHaveURL(/\/$/);
  expect(gw.requestsTo('POST', '/auth/login')).toHaveLength(3);
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
