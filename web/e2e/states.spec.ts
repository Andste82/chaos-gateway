import { expect, test } from '@playwright/test';
import { MockGateway, device, toast } from './mock';

// M12: the states every screen must handle (plan §2.17) that the shell owns: offline, safe mode,
// commit-confirm, live updates over the event stream, expiring overlays.
test('offline: a banner, live data greyed out with the time it was last updated, and back', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await expect(page.getByTestId('device-row')).toHaveCount(5);
  await expect(page.getByTestId('banner-offline')).toHaveCount(0);

  gw.unreachable = true;
  await page.evaluate(() => (window as unknown as { __sse: { fail: () => void } }).__sse.fail());
  const banner = page.getByTestId('banner-offline');
  await expect(banner).toBeVisible();
  await expect(banner).toHaveAttribute('role', 'alert');
  await expect(banner).toContainText('The gateway does not answer.');
  await expect(page.getByTestId('last-updated')).toHaveText(/^Last updated \d\d:\d\d:\d\d\.$/);
  // the last data stays, greyed out
  await expect(page.getByTestId('page')).toHaveClass(/stale/);
  await expect(page.getByTestId('device-row')).toHaveCount(5);
  await expect(page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' })).toBeDisabled();

  gw.unreachable = false;
  await page.evaluate(() => (window as unknown as { __sse: { reopen: () => void } }).__sse.reopen());
  await expect(banner).toBeHidden();
  await expect(page.getByTestId('page')).not.toHaveClass(/stale/);
});

test('offline from the start: the page says so and tries again', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.unreachable = true;
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('unreachable')).toContainText('The gateway does not answer.');
  gw.unreachable = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
});

test('safe mode: a full-width red banner with the reason and the way to recovery', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.state = { ...gw.state, safe_mode: true, safe_mode_reason: 'revision 42 and the last known good revision 41 failed to apply' };
  gw.health = { status: 'unhealthy' };
  await gw.install(page);
  await page.goto('/');
  const banner = page.getByTestId('banner-safe-mode');
  await expect(banner).toBeVisible();
  await expect(banner).toHaveAttribute('role', 'alert');
  await expect(banner).toContainText('Safe mode: forwarding is disabled.');
  await expect(banner).toContainText('revision 42 and the last known good revision 41 failed to apply');
  await expect(banner.getByRole('link', { name: 'How to recover' })).toBeVisible();
  await expect(page.getByTestId('health')).toHaveText('Unhealthy');
  // on every page
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Devices' }).click();
  await expect(page.getByTestId('banner-safe-mode')).toBeVisible();
});

test('commit-confirm: an orange countdown, Confirm resolves it, changes wait until then', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 45_000).toISOString() } };
  await gw.install(page);
  await page.goto('/devices');
  const banner = page.getByTestId('countdown-banner');
  await expect(banner).toContainText('Revision 42 is applied and waits for confirmation');
  const first = await banner.getByRole('timer').textContent();
  expect(first).toMatch(/^0:4\d left$/);
  await expect(banner.getByRole('timer')).not.toHaveText(first!, { timeout: 3_000 });
  await expect(banner.getByRole('timer')).toHaveAttribute('aria-live', 'polite');
  await expect(page.getByTestId('context-line')).toContainText('revision 42 waits for confirmation');
  // a control that would change more is disabled until the confirmation is resolved
  await expect(page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' })).toBeDisabled();

  await banner.getByRole('button', { name: 'Confirm' }).click();
  await expect(toast(page, 'Revision 42 confirmed')).toBeVisible();
  await expect(banner).toBeHidden();
  await expect(page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' })).toBeEnabled();
});

test('commit-confirm timeout: the rollback is announced', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await gw.emit(page, { type: 'revision_rolled_back', data: { revision: 43, reason: 'confirmation timeout' }, message: 'revision 43 rolled back' });
  await expect(toast(page, 'Revision 43 rolled back')).toContainText('It was not confirmed in time; the previous revision is active again.');
});

test('live updates: a discovered device appears without a reload', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await expect(page.getByTestId('device-row')).toHaveCount(5);

  gw.devices = [
    ...gw.devices,
    device({ id: 'd15c0000-0000-4000-8000-8e025daa10c3', name: 'dev-8e-02-5d-aa-10-c3', origin: 'discovered', observed: { online: true, macs: ['8e:02:5d:aa:10:c3'], addresses: ['10.10.0.141'] } }),
  ];
  await gw.emit(page, { type: 'device_discovered', subject: { kind: 'device', id: 'd15c0000-0000-4000-8000-8e025daa10c3', name: 'dev-8e-02-5d-aa-10-c3' }, message: 'device dev-8e-02-5d-aa-10-c3 discovered' });
  // the event invalidates the list at once; the 5 s poll would be later
  await expect(page.getByTestId('device-row')).toHaveCount(6, { timeout: 2_000 });
  await expect(page.getByTestId('device-row').filter({ hasText: '10.10.0.141' })).toContainText('dev-8e-02-5d-aa-10-c3');

  // the overview's recent events show what the stream delivered
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Overview' }).click();
  await expect(page.getByTestId('recent-events')).toContainText('device dev-8e-02-5d-aa-10-c3 discovered');
});

test('expiring overlays: the fault leaves the list and a toast says so', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');
  await expect(page.getByTestId('active-fault')).toHaveCount(3);

  gw.overlays = gw.overlays.filter((o) => o.kind !== 'fault');
  await gw.emit(page, {
    type: 'overlay_expired',
    subject: { kind: 'overlay', id: 'f0000000-0000-4000-8000-000000000001', name: 'ESP32-42 latency' },
    message: 'overlay f0000000 expired (ttl)',
  });
  await expect(toast(page, 'ESP32-42 latency expired')).toBeVisible();
  await expect(page.getByTestId('active-fault')).toHaveCount(2, { timeout: 2_000 });
  await expect(page.getByTestId('faults-counter')).toHaveText('2 faults active');
});
