import { expect, test, type Page } from '@playwright/test';
import { IOT, MockGateway, toast, withNetworks } from './mock';

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR7 = 'a1b2c3d4-0000-4000-8000-000000000057';
const SENSOR9 = 'a1b2c3d4-0000-4000-8000-000000000059';
const DEV = 'd15c0000-0000-4000-8000-3af19c2207b4';
const MAC = '02:00:00:00:00:2a';
const DEV_MAC = '3a:f1:9c:22:07:b4';

// #75: Reserve in the DHCP leases card makes a lease static in one dialog (the adopt dialog for a
// discovered device, the device dialog for a configured one), and the network lists its reservations
// with their status and the draft's edits of them.
function lab(devices: Record<string, { name: string; macs: string[]; fixed_ip?: string }> = { [ESP]: { name: 'ESP32-42', macs: [MAC] } }): MockGateway {
  const gw = withNetworks(MockGateway.sample());
  gw.config = {
    ...gw.config,
    devices: Object.fromEntries(
      Object.entries(devices).map(([id, d]) => [id, { name: d.name, identifiers: { macs: d.macs }, network: IOT, ...(d.fixed_ip ? { fixed_ip: d.fixed_ip } : {}) }]),
    ),
  };
  gw.devices = gw.devices.map((d) => (gw.config.devices?.[d.id] ? { ...d, config: gw.config.devices[d.id] } : d));
  return gw;
}

async function openIoT(page: Page) {
  await page.goto(`/networks/${IOT}`);
  await expect(page.getByTestId('leases')).toBeVisible();
}

const leaseRow = (page: Page, ip: string) => page.getByTestId('lease').filter({ hasText: ip });
const reservationRow = (page: Page, ip: string) => page.getByTestId('reservation').filter({ hasText: ip });

test('Reserve on the lease of a configured device: the device dialog with the lease as its fixed IP, via preview and apply', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await openIoT(page);
  await expect(page.getByTestId('reservations')).toHaveCount(0);
  await expect(page.getByText('No device has a fixed IP in this network.')).toBeVisible();
  await expect(leaseRow(page, '10.10.0.42').getByTestId('lease-reserved')).toHaveCount(0);

  await page.getByRole('button', { name: `Reserve 10.10.0.42 for ${MAC}` }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.42');
  await expect(dialog.getByLabel('Network')).toHaveValue(IOT);
  await expect(dialog.locator('[data-hint="kept"]')).toHaveText('The device has 10.10.0.42 from its current lease and keeps it; from now on the address is reserved for it.');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device ESP32-42 changed');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({ [ESP]: { fixed_ip: '10.10.0.42' } });
  // backing out: the reservation is listed as NEW, not applied yet
  await drawer.getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.42').getByTestId('reservation-mark')).toHaveText('NEW');
  await expect(reservationRow(page, '10.10.0.42').getByTestId('reservation-status')).toHaveText('pending: not applied yet');
  await expect(page.getByTestId('dhcp-settings')).toContainText('· 1 reserved');

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: /^Apply revision/ }).click();
  // applied: the lease is marked reserved and has no Reserve any more; the reservation is active
  await expect(leaseRow(page, '10.10.0.42').getByTestId('lease-reserved')).toHaveText('reserved');
  expect(gw.config.devices?.[ESP]).toMatchObject({ fixed_ip: '10.10.0.42' });
  await expect(page.getByRole('button', { name: `Reserve 10.10.0.42 for ${MAC}` })).toHaveCount(0);
  await expect(reservationRow(page, '10.10.0.42').getByTestId('reservation-mark')).toHaveCount(0);
  await expect(reservationRow(page, '10.10.0.42').getByTestId('reservation-status')).toHaveText('active');
  await expect(reservationRow(page, '10.10.0.42')).toContainText(MAC);
  await expect(reservationRow(page, '10.10.0.42').getByRole('link', { name: 'ESP32-42' })).toHaveAttribute('href', `/devices/${ESP}`);
});

test('Reserve on the lease of a discovered device: the adopt dialog with "Keep this address" checked, via preview and apply', async ({ page }) => {
  const gw = lab();
  // the lease has a host name, the device's view has no lease: the name comes from the lease reserved
  gw.leases[IOT] = gw.leases[IOT]!.map((l) => (l.mac === DEV_MAC ? { ...l, hostname: 'thermostat-a' } : l));
  await gw.install(page);
  await openIoT(page);
  // the gateway names no device for this lease: the discovered device with its MAC is the one
  await page.getByRole('button', { name: `Reserve 10.10.0.133 for ${DEV_MAC}` }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await expect(dialog.getByRole('checkbox', { name: 'Keep this address (DHCP reservation)' })).toBeChecked();
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.133');
  // in the pool: allowed, a note (decision on #76)
  await expect(dialog.locator('[data-hint="pool"]')).toContainText('10.10.0.133 lies in the default DHCP pool 10.10.0.128–10.10.0.254 of IoT. The gateway allows it');
  await expect(dialog.locator('[data-hint="kept"]')).toBeVisible();
  await expect(dialog.getByTestId('adopt-preview')).toContainText(`with MAC ${DEV_MAC} in network IoT and always gets 10.10.0.133 from DHCP`);
  await expect(dialog.getByLabel('Name')).toHaveValue('thermostat-a');
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device Thermostat-A added');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({
    [DEV]: { name: 'Thermostat-A', identifiers: { macs: [DEV_MAC] }, network: IOT, fixed_ip: '10.10.0.133' },
  });
  await drawer.getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.133').getByTestId('reservation-mark')).toHaveText('NEW');
  await expect(reservationRow(page, '10.10.0.133')).toContainText('Thermostat-A');
  // adopted in the draft: Reserve does not adopt it again
  await expect(page.getByRole('button', { name: `Reserve 10.10.0.133 for ${DEV_MAC}` })).toHaveCount(0);

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: /^Apply revision/ }).click();
  await expect(leaseRow(page, '10.10.0.133').getByTestId('lease-reserved')).toHaveText('reserved');
  expect(gw.config.devices?.[DEV]).toMatchObject({ name: 'Thermostat-A', fixed_ip: '10.10.0.133' });
  await expect(reservationRow(page, '10.10.0.133').getByTestId('reservation-status')).toHaveText('active');
});

test('the reservations of a network: status active, pending and conflict, and the draft edits marked CHANGED and DELETED', async ({ page }) => {
  const gw = lab({
    [ESP]: { name: 'ESP32-42', macs: [MAC], fixed_ip: '10.10.0.42' },
    [SENSOR7]: { name: 'Sensor-07', macs: ['02:00:00:00:00:39'], fixed_ip: '10.10.0.57' },
    [SENSOR9]: { name: 'Sensor-09', macs: ['02:00:00:00:00:3b'], fixed_ip: '10.10.0.133' },
  });
  // Sensor-07 still holds an address from the pool
  gw.leases[IOT]!.push({ ip: '10.10.0.140', mac: '02:00:00:00:00:39', network: IOT, device: SENSOR7, expires_at: new Date(Date.now() + 30 * 60_000).toISOString(), state: 'active' });
  await gw.install(page);
  await openIoT(page);

  const rows = page.getByTestId('reservation');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('10.10.0.42');
  await expect(rows.nth(0).getByTestId('reservation-status')).toHaveText('active');
  await expect(rows.nth(1)).toContainText('10.10.0.57');
  await expect(rows.nth(1).getByTestId('reservation-status')).toHaveText('pending: has 10.10.0.140 until it renews its lease');
  await expect(rows.nth(2)).toContainText('10.10.0.133');
  await expect(rows.nth(2).getByTestId('reservation-status')).toHaveText(`conflict: leased to ${DEV_MAC}`);
  await expect(page.getByTestId('dhcp-settings')).toContainText('· 3 reserved');
  await expect(leaseRow(page, '10.10.0.42').getByTestId('lease-reserved')).toHaveText('reserved');
  await expect(leaseRow(page, '10.10.0.140').getByTestId('lease-reserved')).toHaveCount(0);

  // change one in the device dialog: CHANGED, pending until applied; Undo takes it back
  await page.getByRole('button', { name: 'Edit the reservation of Sensor-07' }).click();
  let dialog = page.getByRole('dialog', { name: 'Edit Sensor-07' });
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.57');
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.10.0.58');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.58').getByTestId('reservation-mark')).toHaveText('CHANGED');
  await expect(reservationRow(page, '10.10.0.58').getByTestId('reservation-status')).toHaveText('pending: not applied yet');
  await page.getByRole('button', { name: 'Undo the reservation change of Sensor-07' }).click();
  await expect(reservationRow(page, '10.10.0.57').getByTestId('reservation-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toHaveCount(0);

  // remove one: DELETED, still listed until applied
  await page.getByRole('button', { name: 'Edit the reservation of Sensor-09' }).click();
  dialog = page.getByRole('dialog', { name: 'Edit Sensor-09' });
  await dialog.getByRole('button', { name: 'Remove the fixed IP 10.10.0.133' }).click();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.133').getByTestId('reservation-mark')).toHaveText('DELETED');
  await expect(page.getByTestId('dhcp-settings')).toContainText('· 2 reserved');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: /^Apply revision/ }).click();
  await expect(toast(page, 'Revision 45 applied and verified in 87 ms')).toBeVisible();
  await expect(rows).toHaveCount(2);
  expect(gw.config.devices?.[SENSOR9]?.fixed_ip).toBeUndefined();
});

// #97: Undo on a reservation row takes back only the reservation and keeps the device's other draft edits.
test('Undo on the reservation of a device adopted in the draft keeps the adoption and drops the reservation', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await openIoT(page);
  await page.getByRole('button', { name: `Reserve 10.10.0.133 for ${DEV_MAC}` }).click();
  const dialog = page.getByRole('dialog', { name: 'Adopt device' });
  await dialog.getByLabel('Name').fill('Thermostat-A');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.133').getByTestId('reservation-mark')).toHaveText('NEW');

  await page.getByRole('button', { name: 'Undo the reservation change of Thermostat-A' }).click();
  await expect(page.getByTestId('reservations')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeVisible();
  // the device is still adopted, so Reserve opens its device dialog now
  await expect(page.getByRole('button', { name: `Reserve 10.10.0.133 for ${DEV_MAC}` })).toBeVisible();

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: /^Preview revision/ });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device Thermostat-A added');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({
    [DEV]: { name: 'Thermostat-A', identifiers: { macs: [DEV_MAC] }, network: IOT },
  });
});

test('Undo on the reservation of a device renamed in the draft keeps its new name', async ({ page }) => {
  const gw = lab({ [ESP]: { name: 'ESP32-42', macs: [MAC], fixed_ip: '10.10.0.42' } });
  await gw.install(page);
  await openIoT(page);
  await page.getByRole('button', { name: 'Edit the reservation of ESP32-42' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  await dialog.getByLabel('Name').fill('ESP32-kitchen');
  await dialog.getByLabel('Fixed IP (DHCP reservation)').fill('10.10.0.43');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: /^Preview revision/ }).getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.43').getByTestId('reservation-mark')).toHaveText('CHANGED');

  await page.getByRole('button', { name: 'Undo the reservation change of ESP32-kitchen' }).click();
  await expect(reservationRow(page, '10.10.0.42').getByTestId('reservation-mark')).toHaveCount(0);
  await expect(reservationRow(page, '10.10.0.42').getByRole('link', { name: 'ESP32-kitchen' })).toBeVisible();
  await expect(reservationRow(page, '10.10.0.43')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeVisible();

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: /^Preview revision/ }).getByTestId('preview-changes')).toContainText('device ESP32-kitchen changed');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({ [ESP]: { name: 'ESP32-kitchen' } });
});

// #98: a lease the gateway assigns to a configured device whose MACs do not include the lease's (here by
// the device's address) is reserved for the device's first MAC; the device dialog says so and offers to
// add the lease's MAC to the device first, checked (decision on #98).
const FOREIGN = '02:00:00:00:00:99';
function foreignLease(gw: MockGateway) {
  gw.leases[IOT]!.push({ ip: '10.10.0.60', mac: FOREIGN, network: IOT, device: ESP, expires_at: new Date(Date.now() + 40 * 60_000).toISOString(), state: 'active' });
}

test('Reserve on a lease whose MAC the device does not have: the dialog adds the MAC first, and the reservation holds for it', async ({ page }) => {
  const gw = lab();
  foreignLease(gw);
  await gw.install(page);
  await openIoT(page);
  await page.getByRole('button', { name: `Reserve 10.10.0.60 for ${FOREIGN}` }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  await expect(dialog.getByLabel('Fixed IP (DHCP reservation)')).toHaveValue('10.10.0.60');
  const offer = dialog.getByTestId('reserve-mac');
  await expect(offer).toContainText(`The lease of 10.10.0.60 is held by MAC ${FOREIGN}, which is not among the device's MACs (${MAC}). A DHCP reservation is for the device's first MAC.`);
  await expect(offer.getByRole('checkbox', { name: `Add ${FOREIGN} to the device as its first MAC` })).toBeChecked();
  await expect(offer.getByTestId('reserve-mac-effect')).toHaveText(`The reservation is for ${FOREIGN}, the MAC that holds the lease; the device keeps ${MAC} as its other MAC.`);
  await expect(dialog.locator('[data-hint="kept"]')).toHaveText('The device has 10.10.0.60 from its current lease and keeps it; from now on the address is reserved for it.');
  await expect(dialog.locator('[data-hint="leased"]')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('device ESP32-42 changed');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({
    [ESP]: { identifiers: { macs: [FOREIGN, MAC] }, fixed_ip: '10.10.0.60' },
  });
  await drawer.getByRole('button', { name: /^Apply revision/ }).click();
  await expect(leaseRow(page, '10.10.0.60').getByTestId('lease-reserved')).toHaveText('reserved');
  expect(gw.config.devices?.[ESP]).toMatchObject({ identifiers: { macs: [FOREIGN, MAC] }, fixed_ip: '10.10.0.60' });
  await expect(reservationRow(page, '10.10.0.60')).toContainText(FOREIGN);
  await expect(reservationRow(page, '10.10.0.60').getByTestId('reservation-status')).toHaveText('active');
});

test('Reserve on a lease whose MAC the device does not have, the offer declined: the reservation stays on the first MAC, and the dialog says it may not apply', async ({ page }) => {
  const gw = lab();
  foreignLease(gw);
  await gw.install(page);
  await openIoT(page);
  await page.getByRole('button', { name: `Reserve 10.10.0.60 for ${FOREIGN}` }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit ESP32-42' });
  const offer = dialog.getByTestId('reserve-mac');
  await offer.getByRole('checkbox', { name: `Add ${FOREIGN} to the device as its first MAC` }).uncheck();
  await expect(offer.getByTestId('reserve-mac-effect')).toHaveText(
    `The reservation is for ${MAC}, which does not hold this lease: it takes effect only when the device asks for an address with that MAC.`,
  );
  await expect(dialog.locator('[data-hint="first_mac"]')).toHaveText(`The reservation is for the device's MAC ${MAC}.`);
  await expect(dialog.locator('[data-hint="leased"]')).toContainText(`10.10.0.60 is leased to ${FOREIGN} now`);
  await expect(dialog.locator('[data-hint="kept"]')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog).toBeHidden();
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { devices: unknown }).devices).toEqual({ [ESP]: { fixed_ip: '10.10.0.60' } });
  await page.getByRole('dialog', { name: 'Preview revision 43' }).getByRole('button', { name: 'Close' }).click();
  await expect(reservationRow(page, '10.10.0.60')).toContainText(MAC);
  await expect(reservationRow(page, '10.10.0.60').getByTestId('reservation-mark')).toHaveText('NEW');
});

test('Reserve on a lease whose MAC identifies another device: no offer, the dialog says why', async ({ page }) => {
  const gw = lab({ [ESP]: { name: 'ESP32-42', macs: [MAC] }, [SENSOR7]: { name: 'Sensor-07', macs: [FOREIGN] } });
  foreignLease(gw);
  await gw.install(page);
  await openIoT(page);
  await page.getByRole('button', { name: `Reserve 10.10.0.60 for ${FOREIGN}` }).click();
  const offer = page.getByRole('dialog', { name: 'Edit ESP32-42' }).getByTestId('reserve-mac');
  await expect(offer.getByRole('checkbox')).toHaveCount(0);
  await expect(offer.getByTestId('reserve-mac-effect')).toHaveText(
    `${FOREIGN} already identifies device Sensor-07, so it cannot be added to this one: the reservation is for ${MAC}, which does not hold this lease.`,
  );
});
