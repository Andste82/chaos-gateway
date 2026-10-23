import { expect, test } from '@playwright/test';
import { MockGateway, manyDevices, toast, withRules } from './mock';

// M12: the device list with many devices (more than one page of the API), search, the filter
// chips, the empty state, and adopting a discovered device.
test('many devices: all pages are listed, search and filter chips narrow them', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.devices = manyDevices(620); // two pages of 500
  await gw.install(page);
  await page.goto('/devices');

  const filter = page.getByRole('group', { name: 'Filter' });
  await expect(filter.getByRole('button', { name: 'All 620' })).toHaveAttribute('aria-pressed', 'true');
  // online: i % 3 != 0 → 413; with faults: i % 7 == 0 → 89; not adopted: i % 10 == 9 → 62
  await expect(filter.getByRole('button', { name: 'Online 413' })).toBeVisible();
  await expect(filter.getByRole('button', { name: 'With faults 89' })).toBeVisible();
  await expect(filter.getByRole('button', { name: 'Not adopted 62' })).toBeVisible();
  await expect(filter.getByRole('button', { name: 'Adopted 558', exact: true })).toBeVisible();
  await expect(page.getByTestId('device-row')).toHaveCount(620);
  expect(gw.requestsTo('GET', '/devices').length).toBeGreaterThanOrEqual(2);

  await filter.getByRole('button', { name: 'Not adopted 62' }).click();
  await expect(filter.getByRole('button', { name: 'Not adopted 62' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('device-row')).toHaveCount(62);
  await expect(page.getByTestId('device-row').first().getByRole('button', { name: /^Adopt / })).toBeVisible();

  await filter.getByRole('button', { name: 'All 620' }).click();
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('sensor-123');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await expect(page.getByTestId('device-row')).toContainText('10.20.0.125');
  // by MAC and by IP too
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('02:00:00:00:02:6b');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await page.getByRole('searchbox', { name: 'Search devices' }).fill(' 10.20.2.121 ');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('nothing-like-this');
  await expect(page.getByTestId('no-match')).toHaveText('No devices match “nothing-like-this”.');
});

test('the rows show identity, traffic, faults and status, and open the device', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');

  const esp = page.getByTestId('device-row').filter({ hasText: 'ESP32-42' });
  await expect(esp).toContainText('10.10.0.42');
  await expect(esp).toContainText('02:00:00:00:00:2a');
  await expect(esp.getByRole('link', { name: 'IoT' })).toHaveAttribute('href', '/networks/0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21');
  await expect(esp).toContainText('3.1 KB/s / 2.4 KB/s');
  await expect(esp.getByTestId('device-faults').locator('[title]')).toHaveText(['↑↓ 200 ms ±50', '5 % loss']);
  await expect(page.getByTestId('device-row').filter({ hasText: 'Sensor-07' }).getByTestId('device-faults')).toHaveText('↓ 64 kbit/s');
  await expect(esp).toContainText('Online');
  await expect(page.getByTestId('device-row').filter({ hasText: 'Sensor-09' })).toContainText('Offline');
  await expect(page.getByTestId('device-row').filter({ hasText: 'Test-Phone' })).toContainText('Test');

  await esp.getByText('02:00:00:00:00:2a').click();
  await expect(page).toHaveURL(/\/devices\/a1b2c3d4-0000-4000-8000-000000000042$/);
  await expect(page.getByRole('heading', { name: 'ESP32-42' })).toBeVisible();
});

test('empty: no devices yet', async ({ page }) => {
  const gw = new MockGateway();
  await gw.install(page);
  await page.goto('/devices');
  // one sentence and the primary action (plan §2.17 States)
  await expect(page.getByTestId('empty-state')).toContainText('No devices yet: a device appears as soon as it gets a DHCP lease');
  const action = page.getByTestId('empty-state').getByRole('link', { name: 'Check the test networks' });
  await expect(action).toHaveAttribute('href', '/networks');
  await expect(action).toHaveClass(/btn-primary/);
});

test('the search field shows the focus ring when it has the focus', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await page.getByRole('searchbox', { name: 'Search devices' }).focus();
  const box = page.getByTestId('device-search');
  await expect(box).toHaveCSS('outline-style', 'solid');
  await expect(box).toHaveCSS('outline-width', '2px');
  await expect(box).toHaveCSS('outline-color', 'rgb(108, 178, 255)');
});

test('an adoption in the changes is marked in the list, Undo takes it back and keeps the focus there', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  await page.getByRole('dialog', { name: 'Adopt device' }).getByLabel('Name').fill('Thermostat-A');
  await page.getByRole('dialog', { name: 'Adopt device' }).getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 43' }).getByRole('button', { name: 'Close' }).click();
  const row = page.getByTestId('device-row').filter({ hasText: 'dev-3a-f1-9c-22-07-b4' });
  await expect(row.getByTestId('device-adopted-mark')).toHaveText('ADOPTED as Thermostat-A · not applied yet');
  await expect(row.getByRole('button', { name: /^Adopt / })).toHaveCount(0);
  await row.getByRole('button', { name: 'Undo the adoption of dev-3a-f1-9c-22-07-b4' }).focus();
  await page.keyboard.press('Enter');
  await expect(row.getByTestId('device-adopted-mark')).toHaveCount(0);
  await expect(row.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' })).toBeFocused();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
});

test('adopting a discovered device: the name, what changes, then the preview drawer and the apply', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');

  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('adopt-preview')).toContainText('with MAC 3a:f1:9c:22:07:b4');
  await expect(dialog.getByTestId('adopt-preview')).toContainText('in network IoT');
  await dialog.getByLabel('Name').fill('bad name');
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  await expect(dialog).toBeHidden();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer).toContainText('dry run · nothing changed yet');
  await expect(drawer.getByTestId('preview-changes')).toContainText('device Thermostat-A added');
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(0);
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('device-row').filter({ hasText: 'Thermostat-A' })).toBeVisible();

  const create = gw.requestsTo('POST', '/revisions').at(-1)!;
  expect(create.headers['if-match']).toBe('"42"');
  expect(create.headers['content-type']).toBe('application/merge-patch+json');
  expect(create.headers['x-csrf-token']).toBe('csrf-1');
  expect(create.body).toEqual({
    devices: { 'd15c0000-0000-4000-8000-3af19c2207b4': { name: 'Thermostat-A', identifiers: { macs: ['3a:f1:9c:22:07:b4'] }, network: '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21' } },
  });
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(1);
});

test('adopting while the draft is based on an older revision opens the conflict dialog: reload and reapply', async ({ page }) => {
  const gw = withRules(MockGateway.sample());
  await gw.install(page);
  // an edit of a rule, not applied yet: the draft is based on revision 42
  await page.goto('/rules');
  const ruleSwitch = page.getByRole('switch', { name: 'Enable rule no-dot' });
  await expect(ruleSwitch).toHaveAttribute('aria-checked', 'true');
  await ruleSwitch.click();
  // the edit has landed in the draft before the page changes (under load the click on the link
  // could otherwise come before the app is ready and leave the page on the rules)
  await expect(ruleSwitch).toHaveAttribute('aria-checked', 'false');
  await expect(page.getByTestId('unapplied-bar')).toBeVisible();
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Devices' }).click();
  await expect(page).toHaveURL(/\/devices$/);
  await expect(page.getByRole('heading', { name: 'Devices', exact: true })).toBeVisible();
  // a test applies a revision through the API meanwhile
  gw.applyFromApi({ groups: { 'c0a80001-0000-4000-8000-000000000002': { name: 'lab' } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'added', kind: 'group', path: '/groups/c0a80001-0000-4000-8000-000000000002', summary: 'group lab added' },
  ]);
  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await expect(dialog.getByTestId('adopt-preview')).toContainText('based on revision 42');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict).toContainText('Revision 43 was applied by token ci-runner while you were editing (your change is based on revision 42).');
  await expect(conflict.getByTestId('conflict-theirs')).toContainText('group lab added');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Rule no-dot enabled: yes → no');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Device dev-3a-f1-9c-22-07-b4 device: discovered → configured (3a:f1:9c:22:07:b4)');
  expect(gw.requestsTo('GET', '/revisions/43/diff')).toHaveLength(1);
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 44' }).getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  const posts = gw.requestsTo('POST', '/revisions');
  expect(posts.map((r) => r.headers['if-match'])).toEqual(['"42"', '"43"']);
  expect(Object.keys((posts[1]!.body as { devices: object }).devices)).toEqual(['d15c0000-0000-4000-8000-3af19c2207b4']);
});

test('adopting after someone else changed the configuration: discard mine drops the adoption', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/devices');
  await expect(page.getByTestId('context-line')).toContainText('Revision 42');
  gw.conflictOnce = true;
  await page.getByRole('button', { name: 'Adopt dev-3a-f1-9c-22-07-b4' }).click();
  await page.getByRole('dialog', { name: 'Adopt device' }).getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await conflict.getByRole('button', { name: 'Discard mine' }).click();
  await expect(conflict).toBeHidden();
  await expect(page.getByRole('dialog', { name: 'Adopt device' })).toBeHidden();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(1);
});
