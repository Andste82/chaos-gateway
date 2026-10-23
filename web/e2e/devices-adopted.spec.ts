import { expect, test } from '@playwright/test';
import { discoveredDeviceId } from '../src/lib/ids';
import { FWD_SENSOR, FWD_WEB, IOT, MockGateway, device, toast, withForwards } from './mock';

// #78: the device list shows a discovered device under the host name of its lease and marks the
// configured ones (ADOPTED, a differing host name), filters them, edits them in the device dialog
// (#73) and removes them from the configuration (references listed first, undo while not applied).
const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR7 = 'a1b2c3d4-0000-4000-8000-000000000057';
const SENSOR9 = 'a1b2c3d4-0000-4000-8000-000000000059';
const PHONE = 'a1b2c3d4-0000-4000-8000-000000000015';
const THERMO = 'd15c0000-0000-4000-8000-0a0b0c0d0e0f';
const GROUP = 'c0a80001-0000-4000-8000-000000000001';
// adopted from discovery: its id is the one the gateway derived from its MAC
const PLUG_MAC = '02:00:00:00:00:77';
const PLUG = discoveredDeviceId(PLUG_MAC);
const PLUG_FAULT = 'f0000000-0000-4000-8000-000000000077';

function lab(): MockGateway {
  const gw = withForwards(MockGateway.sample());
  const lease = (ip: string, mac: string, hostname: string) => ({ ip, mac, hostname, network: IOT, expires_at: new Date(Date.now() + 3_600_000).toISOString(), state: 'active' as const });
  gw.devices = gw.devices.map((d) =>
    d.id === SENSOR7 ? { ...d, observed: { ...d.observed, lease: lease('10.10.0.57', '02:00:00:00:00:39', 'sensor-a7') } } : d,
  );
  // a discovered device that announces a host name
  gw.devices.push(
    device({
      id: THERMO,
      name: 'dev-0a-0b-0c-0d-0e-0f',
      origin: 'discovered',
      observed: { online: true, macs: ['0a:0b:0c:0d:0e:0f'], addresses: ['10.10.0.140'], sources: ['dhcp'], lease: lease('10.10.0.140', '0a:0b:0c:0d:0e:0f', 'thermostat-hall') },
    }),
    device({ id: PLUG, name: 'Plug-77', observed: { online: true, macs: [PLUG_MAC], addresses: ['10.10.0.77'], sources: ['dhcp'] } }),
  );
  // an active fault on Plug-77 (an overlay; nothing in the configuration names it)
  const now = new Date().toISOString();
  gw.overlays = [
    ...gw.overlays,
    { id: PLUG_FAULT, kind: 'fault', owner: { type: 'user', id: 'admin' }, target: { device: PLUG }, fault: { latency: '100ms' }, created_at: now, updated_at: now, generation: 42, state: 'effective' },
  ];
  // no port forward to Sensor-09: nothing names it
  const { [FWD_SENSOR]: _sensor, ...forwards } = gw.config.port_forwards ?? {};
  gw.config = {
    ...gw.config,
    port_forwards: forwards,
    devices: {
      [ESP]: { name: 'ESP32-42', identifiers: { macs: ['02:00:00:00:00:2a'] }, network: IOT },
      [SENSOR7]: { name: 'Sensor-07', identifiers: { macs: ['02:00:00:00:00:39'] }, network: IOT },
      [SENSOR9]: { name: 'Sensor-09', identifiers: { macs: ['02:00:00:00:00:3b'] }, network: IOT, fixed_ip: '10.10.0.59' },
      [PHONE]: { name: 'Test-Phone', identifiers: { macs: ['02:00:00:00:01:0f'] } },
      [PLUG]: { name: 'Plug-77', identifiers: { macs: [PLUG_MAC] }, network: IOT },
    },
    groups: { [GROUP]: { name: 'lab', members: [SENSOR7] } },
  };
  gw.devices = gw.devices.map((d) => (gw.config.devices?.[d.id] ? { ...d, config: gw.config.devices[d.id] } : d));
  return gw;
}

const row = (page: import('@playwright/test').Page, text: string) => page.getByTestId('device-row').filter({ hasText: text });

test('a discovered device is listed under its host name, a configured one with the ADOPTED badge; list and detail agree', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');

  const thermo = row(page, 'thermostat-hall');
  await expect(thermo.getByTestId('device-name')).toHaveText('thermostat-hall');
  await expect(thermo.getByTestId('device-name-detail')).toHaveText('dev-0a-0b-0c-0d-0e-0f');
  await expect(thermo.getByTestId('device-adopted-badge')).toHaveCount(0);
  await expect(thermo.getByRole('button', { name: 'Adopt thermostat-hall' })).toBeVisible();
  // without a host name: the generated name alone
  const plain = row(page, 'dev-3a-f1-9c-22-07-b4');
  await expect(plain.getByTestId('device-name')).toHaveText('dev-3a-f1-9c-22-07-b4');
  await expect(plain.getByTestId('device-name-detail')).toHaveCount(0);
  // the search finds it by both names
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('dev-0a-0b');
  await expect(page.getByTestId('device-row')).toHaveCount(1);
  await page.getByRole('searchbox', { name: 'Search devices' }).fill('');

  // configured: the badge, and the host name when it differs from the name
  const esp = row(page, 'ESP32-42');
  await expect(esp.getByTestId('device-adopted-badge')).toHaveText('ADOPTED');
  await expect(esp.getByTestId('device-name-detail')).toHaveCount(0);
  const sensor = row(page, 'Sensor-07');
  await expect(sensor.getByTestId('device-adopted-badge')).toHaveText('ADOPTED');
  await expect(sensor.getByTestId('device-name-detail')).toHaveText('host name sensor-a7');

  await thermo.getByTestId('device-name').click();
  await expect(page).toHaveURL(new RegExp(`/devices/${THERMO}$`));
  await expect(page.getByRole('heading', { name: 'thermostat-hall', level: 1 })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toHaveText('Devices / thermostat-hall');
  await expect(page.getByTestId('device-name-detail')).toHaveText('dev-0a-0b-0c-0d-0e-0f');
  await expect(page.getByTestId('device-adopted-badge')).toHaveCount(0);

  await page.goto(`/devices/${SENSOR7}`);
  await expect(page.getByRole('heading', { name: 'Sensor-07', level: 1 })).toBeVisible();
  await expect(page.getByTestId('device-adopted-badge')).toHaveText('ADOPTED');
  await expect(page.getByTestId('device-name-detail')).toHaveText('host name sensor-a7');
});

test('the filter chip Adopted lists the configured devices, with its count', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');
  const filter = page.getByRole('group', { name: 'Filter' });
  await expect(filter.getByRole('button', { name: 'Not adopted 2' })).toBeVisible();
  await filter.getByRole('button', { name: 'Adopted 5', exact: true }).click();
  await expect(filter.getByRole('button', { name: 'Adopted 5', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('device-row')).toHaveCount(5);
  await expect(page.getByTestId('device-row').getByTestId('device-adopted-badge')).toHaveCount(5);
  await expect(page.getByTestId('device-row').getByRole('button', { name: /^Adopt / })).toHaveCount(0);
});

test('Edit in the list opens the device dialog of the device', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');
  await row(page, 'Sensor-09').getByRole('button', { name: 'Edit Sensor-09' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit Sensor-09' });
  await expect(dialog.getByLabel('Name')).toHaveValue('Sensor-09');
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.59');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  // another device: the dialog is built from that one
  await row(page, 'ESP32-42').getByRole('button', { name: 'Edit ESP32-42' }).click();
  await expect(page.getByRole('dialog', { name: 'Edit ESP32-42' }).getByLabel('Name')).toHaveValue('ESP32-42');
});

test('removing a device configured with an id of its own: preview and apply, it comes back as discovered under a new id', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');

  await row(page, 'Sensor-09').getByRole('button', { name: 'Remove Sensor-09 from the configuration' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove Sensor-09' });
  const preview = dialog.getByTestId('remove-device-preview');
  await expect(preview).toContainText('Device Sensor-09 is removed from the configuration in a new revision based on revision 42.');
  await expect(preview).toContainText('Its fixed IP 10.10.0.59 (the DHCP reservation) goes with it.');
  await expect(preview.getByTestId('remove-device-comes-back')).toContainText('it is listed as a discovered device under a new id');
  await expect(preview.getByTestId('remove-device-overlays')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device Sensor-09 removed');
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions').at(-1)!.body).toEqual({ devices: { [SENSOR9]: null } });

  const back = row(page, 'dev-02-00-00-00-00-3b');
  await expect(back.getByTestId('device-adopted-badge')).toHaveCount(0);
  await expect(back.getByRole('button', { name: 'Adopt dev-02-00-00-00-00-3b' })).toBeVisible();
  await expect(back.getByRole('link', { name: 'dev-02-00-00-00-00-3b' })).toHaveAttribute('href', `/devices/${discoveredDeviceId('02:00:00:00:00:3b')}`);
  await expect(page.getByRole('group', { name: 'Filter' }).getByRole('button', { name: 'Adopted 4', exact: true })).toBeVisible();
});

test('removing a device adopted from discovery: its active faults go with it, it comes back as discovered under the same id', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');

  await row(page, 'Plug-77').getByRole('button', { name: 'Remove Plug-77 from the configuration' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove Plug-77' });
  const preview = dialog.getByTestId('remove-device-preview');
  await expect(preview.getByTestId('remove-device-overlays')).toContainText('An active overlay (a fault, profile or rule applied at runtime) targets it: the gateway removes it with the device.');
  await expect(preview.getByTestId('remove-device-comes-back')).toContainText(`When the gateway sees it again with the MAC ${PLUG_MAC} its id was derived from, it is listed as a discovered device under the same id.`);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  // the gateway refuses to orphan the overlay unless asked to remove it
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  const refs = drawer.getByTestId('preview-references');
  await expect(refs).toContainText(`overlay ${PLUG_FAULT} (Device Plug-77)`);
  const apply = drawer.getByRole('button', { name: 'Apply revision 43' });
  await expect(apply).toBeDisabled();
  await refs.getByLabel('Remove them when applying').check();
  await apply.click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions/43/apply')[0]!.query).toEqual({ force: 'true' });
  expect(gw.overlays.some((o) => o.id === PLUG_FAULT)).toBe(false);

  const back = row(page, 'dev-02-00-00-00-00-77');
  await expect(back.getByTestId('device-adopted-badge')).toHaveCount(0);
  await expect(back.getByRole('link', { name: 'dev-02-00-00-00-00-77' })).toHaveAttribute('href', `/devices/${PLUG}`);
});

test('removing a device that rules, faults, groups or forwards name lists them first and waits until they are gone', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');

  // Sensor-07: the configured fault sensor7-slow and the group lab
  await row(page, 'Sensor-07').getByRole('button', { name: 'Remove Sensor-07 from the configuration' }).click();
  let dialog = page.getByRole('dialog', { name: 'Remove Sensor-07' });
  await expect(dialog.getByTestId('remove-device-references')).toContainText('Sensor-07 is used by these 2 objects of the configuration');
  await expect(dialog.getByTestId('remove-device-reference')).toHaveText(['fault sensor7-slow', 'group lab']);
  await expect(dialog.getByTestId('remove-device-reference').first().getByRole('link', { name: 'sensor7-slow' })).toHaveAttribute('href', '/faults');
  await expect(dialog.getByTestId('remove-device-references')).toContainText("A group's members are changed in the configuration");
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  // ESP32-42 from its detail: the port forwards esp-web and esp-admin
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Remove ESP32-42 from the configuration' }).click();
  dialog = page.getByRole('dialog', { name: 'Remove ESP32-42' });
  await expect(dialog.getByTestId('remove-device-reference')).toHaveText(['port forward esp-admin', 'port forward esp-web']);
  await expect(dialog.getByRole('link', { name: 'esp-web' })).toHaveAttribute('href', `/networks?forward=${FWD_WEB}`);
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
});

test('a reference the gateway finds is shown as its refusal', async ({ page }) => {
  const gw = lab();
  // a reference the UI does not know (another client changed the configuration in a way the draft does not see)
  gw.rejectRevision = [{ path: `/access_rules/a1000000-0000-4000-8000-0000000000ff/source/device`, code: 'unknown_reference', message: `there is no device "${SENSOR9}"` }];
  await gw.install(page);
  await page.goto('/devices');
  await row(page, 'Sensor-09').getByRole('button', { name: 'Remove Sensor-09 from the configuration' }).click();
  await page.getByRole('dialog', { name: 'Remove Sensor-09' }).getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: /Preview/ })).toContainText(`there is no device "${SENSOR9}"`);
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(0);
});

test('a removal in the changes is marked in the list and on the detail, Undo takes it back', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/devices');
  await row(page, 'Sensor-09').getByRole('button', { name: 'Remove Sensor-09 from the configuration' }).click();
  await page.getByRole('dialog', { name: 'Remove Sensor-09' }).getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 43' }).getByRole('button', { name: 'Close' }).click();

  const sensor = row(page, 'Sensor-09');
  await expect(sensor.getByTestId('device-removed-mark')).toHaveText('REMOVED · not applied yet');
  await expect(sensor.getByRole('button', { name: 'Edit Sensor-09' })).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeVisible();

  // the detail agrees, and takes it back too
  await sensor.getByTestId('device-name').click();
  // the detail is a chunk of its own: under load the list stays a moment longer, and its row has an
  // Undo with the same name
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toBeVisible();
  await expect(page.getByTestId('device-removed-mark')).toHaveText('REMOVED · not applied yet');
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Undo the removal of Sensor-09' }).click();
  await expect(page.getByTestId('device-removed-mark')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Remove Sensor-09 from the configuration' })).toBeFocused();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();

  // in the list: Undo keeps the focus in the row
  await page.goto('/devices');
  await row(page, 'Sensor-09').getByRole('button', { name: 'Remove Sensor-09 from the configuration' }).click();
  await page.getByRole('dialog', { name: 'Remove Sensor-09' }).getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 44' }).getByRole('button', { name: 'Close' }).click();
  // the dialog hands the focus back to its trigger only once its close animation ends: wait for that,
  // or Enter goes to whatever has the focus then
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' })).toBeHidden();
  await expect(row(page, 'Sensor-09').getByTestId('device-removed-mark')).toHaveText('REMOVED · not applied yet');
  const undo = row(page, 'Sensor-09').getByRole('button', { name: 'Undo the removal of Sensor-09' });
  await undo.focus();
  await expect(undo).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(row(page, 'Sensor-09').getByTestId('device-removed-mark')).toHaveCount(0);
  await expect(row(page, 'Sensor-09').getByRole('button', { name: 'Remove Sensor-09 from the configuration' })).toBeFocused();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(0);
});
