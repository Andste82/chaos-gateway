import { expect, test, type Page } from '@playwright/test';
import type { LanNetwork, WireGuardNetwork } from '../src/api/generated/model';
import { ADMIN_HUB, BGP, HUB, IOT, LINK, MockGateway, TEST, toast, withNetworks, withRules } from './mock';

// P3-M14-01, parts 2 and 3: the network forms (add, edit and delete local test networks — with several
// ports, P3-M12-04 — and WireGuard hubs and links) and the routing forms (the BIRD settings and the
// protocols on the links), each through the configuration draft, the preview-and-apply drawer, the
// concurrent-change dialog and commit-confirm where the gateway says the change is lockout-relevant; the
// checks under the fields, the gateway's messages under the fields, and what still refers to a deleted
// network.

const CAM = 'd0000000-0000-4000-8000-0000000000c1';
const IOT_FAULT = 'c0f00000-0000-4000-8000-0000000000c2';

async function open(page: Page, path = '/networks', gw = withNetworks(MockGateway.sample()), heading = 'Networks') {
  await gw.install(page);
  await page.goto(path);
  // the first render after a cold start can be slow under load
  await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible({ timeout: 15_000 });
  return gw;
}

const cards = (page: Page) => page.getByTestId('network-card');
const card = (page: Page, name: string) => cards(page).filter({ has: page.getByTestId('network-name').getByText(name, { exact: true }) });
const bar = (page: Page) => page.getByTestId('unapplied-bar');
const drawer = (page: Page) => page.getByTestId('preview-drawer');
const settings = (page: Page) => page.getByTestId('network-settings');

/** The merge patch of the candidate the drawer previews (once it shows the dry run). */
async function lastPatch(page: Page, gw: MockGateway) {
  await expect(drawer(page).getByTestId('preview-changes')).toBeVisible();
  return gw.requestsTo('POST', '/revisions').at(-1)!.body as { networks?: Record<string, unknown>; routing?: Record<string, unknown> };
}

test('a tall form keeps its action row in view at 1440 × 900', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  // with Advanced open the form is taller than the dialog: Cancel and Preview & apply stay at its bottom
  // edge, unscrolled
  await dialog.getByRole('button', { name: /^Advanced/ }).click();
  await expect(dialog.getByRole('button', { name: /^Hide advanced/ })).toBeVisible();
  const content = await dialog.evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight }));
  expect(content.scroll).toBeGreaterThan(content.client);
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeInViewport({ ratio: 1 });
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeInViewport({ ratio: 1 });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toBeHidden();
});

test('a tall form scrolled to its end: the action row covers nothing of the last field and closes the dialog (#80)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByRole('button', { name: /^Advanced/ }).click();
  await expect(dialog.getByRole('button', { name: /^Hide advanced/ })).toBeVisible();
  const at = await dialog.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
    const row = el.querySelector('.dialog-actions')!;
    const above = row.previousElementSibling!.getBoundingClientRect();
    const box = el.getBoundingClientRect();
    return { scrolled: el.scrollTop > 0, aboveBottom: above.bottom, rowTop: row.getBoundingClientRect().top, rowBottom: row.getBoundingClientRect().bottom, innerBottom: box.bottom - el.clientTop };
  });
  expect(at.scrolled).toBe(true);
  expect(at.aboveBottom).toBeLessThanOrEqual(at.rowTop);
  expect(Math.abs(at.rowBottom - at.innerBottom)).toBeLessThanOrEqual(1);
});

test('network forms: add a test network with several ports through the drawer; after the apply it is listed with both ports (P3-M12-04)', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await expect(dialog.getByRole('button', { name: 'Test network' })).toHaveAttribute('aria-pressed', 'true');
  // a free subnet is proposed
  await expect(dialog.getByLabel('Gateway address and subnet')).toHaveValue('10.30.0.1/24');
  const ports = dialog.getByTestId('network-ports');
  // the uplink, the management interface and the ports of other networks cannot be chosen
  await expect(ports.getByRole('checkbox', { name: /^eth0 / })).toBeDisabled();
  await expect(ports.getByRole('checkbox', { name: /^eth5 / })).toBeDisabled();
  await expect(ports.getByRole('checkbox', { name: /^eth1 / })).toBeDisabled();
  await expect(ports.getByText('port of IoT')).toBeVisible();
  await dialog.getByLabel('Name').fill('cams');
  await ports.getByRole('checkbox', { name: /^eth3 / }).check();
  await ports.getByRole('checkbox', { name: /^eth4 / }).check();
  await dialog.getByLabel('Pools').fill('10.30.0.100-10.30.0.199');
  await expect(dialog.getByTestId('network-form-preview')).toContainText('Test network cams is added with the subnet 10.30.0.0/24 on ports eth3, eth4');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network cams added');
  const patch = (await lastPatch(page, gw));
  const [id, net] = Object.entries(patch.networks!)[0]!;
  expect(net).toEqual({
    type: 'lan',
    name: 'cams',
    address: '10.30.0.1/24',
    interfaces: [
      { name: 'eth3', mac: '52:54:00:00:00:04' },
      { name: 'eth4', mac: '52:54:00:00:00:05' },
    ],
    dhcp: { pools: [{ start: '10.30.0.100', end: '10.30.0.199' }] },
  });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(card(page, 'cams')).toContainText('10.30.0.0/24');
  await expect(card(page, 'cams')).toContainText('test network');
  await expect(card(page, 'cams').getByTestId('network-mark')).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`/networks/${id}$`));
  await expect(settings(page)).toContainText('physical interfaces eth3, eth4');
  await expect(page.getByTestId('dhcp-pool')).toContainText('10.30.0.100 – 10.30.0.199');
});

test('network forms: edit a test network — a second port, a pool and DNS; the patch has only what changed, Undo takes it back (P3-M12-04)', async ({ page }) => {
  const gw = await open(page, `/networks/${IOT}`);
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  const ports = dialog.getByTestId('network-ports');
  await expect(ports.getByRole('checkbox', { name: /^eth1 / })).toBeChecked();
  await ports.getByRole('checkbox', { name: /^eth3 / }).check();
  await dialog.getByLabel('Pools').fill('10.10.0.100-10.10.0.150');
  await dialog.getByLabel("Redirect DNS to other resolvers to the gateway's DNS proxy").check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network IoT changed');
  expect((await lastPatch(page, gw)).networks).toEqual({
    [IOT]: {
      interfaces: [{ name: 'eth1' }, { name: 'eth3', mac: '52:54:00:00:00:04' }],
      dhcp: { pools: [{ start: '10.10.0.100', end: '10.10.0.150' }] },
      dns: { redirect_hardcoded: true },
    },
  });

  // back to editing: the draft keeps it, the card and the settings say so, Undo takes it back
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(bar(page)).toContainText('3 changes in 1 network · not applied yet');
  await expect(card(page, 'IoT').getByTestId('network-mark')).toHaveText('CHANGED');
  await expect(settings(page)).toContainText('physical interfaces eth1 (link up), eth3');
  await settings(page).getByRole('button', { name: 'Undo the changes of IoT' }).click();
  await expect(bar(page)).toHaveCount(0);
  await expect(settings(page)).toContainText('physical interface eth1 (link up)');

  // again, and applied: the network has both ports
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  await ports.getByRole('checkbox', { name: /^eth3 / }).check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  await expect(settings(page)).toContainText('physical interfaces eth1 (link up), eth3 (link up)');
  expect((gw.config.networks![IOT] as LanNetwork).interfaces).toEqual([{ name: 'eth1' }, { name: 'eth3', mac: '52:54:00:00:00:04' }]);
});

test('network forms: a port the configuration names by MAC only is shown ticked on its host interface, once, and can be removed', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  // eth1 by its MAC only
  gw.config.networks![IOT] = { ...(gw.config.networks![IOT] as LanNetwork), interfaces: [{ mac: '52:54:00:00:00:02' }] };
  await open(page, `/networks/${IOT}`, gw);
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  const ports = dialog.getByTestId('network-ports');
  await expect(ports.getByRole('checkbox', { name: /^eth1 / })).toBeChecked();
  await expect(ports.getByText('not present on the host')).toHaveCount(0);
  await expect(ports.getByRole('checkbox')).toHaveCount(6);
  // unticked and ticked again it stays one reference by MAC; eth3 joins it
  await ports.getByRole('checkbox', { name: /^eth1 / }).uncheck();
  await ports.getByRole('checkbox', { name: /^eth1 / }).check();
  await ports.getByRole('checkbox', { name: /^eth3 / }).check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).networks).toEqual({ [IOT]: { interfaces: [{ mac: '52:54:00:00:00:02' }, { name: 'eth3', mac: '52:54:00:00:00:04' }] } });
  await page.getByRole('button', { name: 'Back to editing' }).click();

  // the MAC-only port can be removed
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  await ports.getByRole('checkbox', { name: /^eth1 / }).uncheck();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).networks).toEqual({ [IOT]: { interfaces: [{ name: 'eth3', mac: '52:54:00:00:00:04' }] } });
});

test('network forms: a check of a field under Advanced opens Advanced; hidden again, the form says where the problem is', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  gw.config.networks![IOT] = { ...(gw.config.networks![IOT] as LanNetwork), routes: [{ destination: '10.40.0.0/24', via: '10.10.0.254' }] };
  await open(page, `/networks/${IOT}`, gw);
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  const toggle = dialog.getByRole('button', { name: /^(Advanced|Hide advanced)/ });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  // issue #63: the triangle says that the toggle shows more, ▸ while collapsed, ▾ while expanded
  await expect(toggle.getByTestId('disclosure-mark')).toHaveText('▸');
  const region = await toggle.getAttribute('aria-controls');
  expect(region).toBeTruthy();
  // another subnet leaves the downstream route's router outside it
  await dialog.getByLabel('Gateway address and subnet').fill('10.11.0.1/24');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(toggle.getByTestId('disclosure-mark')).toHaveText('▾');
  await expect(page.locator(`[id="${region}"]`)).toContainText('Downstream routes');
  await expect(dialog.getByText('The router 10.10.0.254 must be a host of 10.11.0.0/24 other than the gateway.')).toBeVisible();
  await expect(dialog.getByLabel(/^Downstream routes/)).toHaveAttribute('aria-invalid', 'true');
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await toggle.click();
  await expect(dialog.getByTestId('network-form-advanced-errors')).toHaveText('Fix the fields under Advanced.');
  await toggle.click();
  await dialog.getByLabel(/^Downstream routes/).fill('10.40.0.0/24 via 10.11.0.254');
  await expect(dialog.getByTestId('network-form-advanced-errors')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).networks).toEqual({ [IOT]: { address: '10.11.0.1/24', routes: [{ destination: '10.40.0.0/24', via: '10.11.0.254' }] } });
});

test('network forms: delete a test network through the drawer; Undo before brings it back', async ({ page }) => {
  const gw = await open(page, `/networks/${TEST}`);
  await settings(page).getByRole('button', { name: 'Delete Test' }).click();
  await expect(card(page, 'Test').getByTestId('network-mark')).toHaveText('DELETED');
  await expect(page.getByTestId('network-pending')).toContainText('Deleted when the changes are applied');
  await expect(bar(page)).toContainText('1 change in 1 network');
  await settings(page).getByRole('button', { name: 'Undo the deletion of Test' }).click();
  await expect(bar(page)).toHaveCount(0);

  await settings(page).getByRole('button', { name: 'Delete Test' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network Test removed');
  expect((await lastPatch(page, gw)).networks).toEqual({ [TEST]: null });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(card(page, 'Test')).toHaveCount(0);
  await expect(cards(page)).toHaveCount(4);
  // the page of the deleted network goes back to the list
  await expect(page).toHaveURL(/\/networks$/);
  await expect(page.getByTestId('load-error')).toHaveCount(0);
});

test('network forms: a new network of the draft that is undone or deleted again goes back to the list, not to "not found"', async ({ page }) => {
  await open(page);
  for (const action of ['Undo the changes of cams', 'Delete cams']) {
    await page.getByRole('button', { name: 'Add network' }).first().click();
    const dialog = page.getByRole('dialog', { name: 'Add network' });
    await dialog.getByLabel('Name').fill('cams');
    await dialog.getByTestId('network-ports').getByRole('checkbox', { name: /^eth3 / }).check();
    await dialog.getByRole('button', { name: 'Preview & apply' }).click();
    await expect(drawer(page).getByTestId('preview-changes')).toContainText('network cams added');
    await page.getByRole('button', { name: 'Back to editing' }).click();
    await expect(card(page, 'cams').getByTestId('network-mark')).toHaveText('NEW');
    await expect(page).toHaveURL(/\/networks\/[0-9a-f-]{36}$/);
    await settings(page).getByRole('button', { name: action }).click();
    await expect(card(page, 'cams')).toHaveCount(0);
    await expect(bar(page)).toHaveCount(0);
    await expect(page).toHaveURL(/\/networks$/);
    await expect(page.getByTestId('load-error')).toHaveCount(0);
  }
});

test('network forms: DHCP switched off and the subnet changed — the hidden pools that no longer fit leave the network, so the gateway does not refuse them', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  gw.config.networks![IOT] = { ...(gw.config.networks![IOT] as LanNetwork), dhcp: { enabled: true, pools: [{ start: '10.10.0.100', end: '10.10.0.199' }], options: { domain: 'lab' } } };
  await open(page, `/networks/${IOT}`, gw);
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  await dialog.getByLabel('The gateway hands out addresses (DHCP)').uncheck();
  await expect(dialog.getByLabel('Pools')).toHaveCount(0);
  await dialog.getByLabel('Gateway address and subnet').fill('10.11.0.1/24');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network IoT changed');
  expect((await lastPatch(page, gw)).networks).toEqual({ [IOT]: { address: '10.11.0.1/24', dhcp: { enabled: false, pools: null } } });
});

test('network forms: deleting a network that a device, a fault, rules, the access matrix and the routing still refer to shows what the gateway reports', async ({ page }) => {
  const gw = withRules(withNetworks(MockGateway.sample()));
  gw.config = {
    ...gw.config,
    devices: { ...gw.config.devices, [CAM]: { name: 'Cam-1', identifiers: { macs: ['02:00:00:00:00:77'] }, network: IOT } },
    faults: { ...gw.config.faults, [IOT_FAULT]: { name: 'iot-slow', source: { network: IOT }, download: { rate: '64kbit' }, enabled: true } },
  };
  await open(page, `/networks/${IOT}`, gw);
  await settings(page).getByRole('button', { name: 'Delete IoT' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const refused = page.getByTestId('preview-invalid');
  await expect(refused).toContainText('The gateway refused the change.');
  await expect(refused).toContainText('Rule no-dns · source network: refers to IoT, which this change deletes');
  await expect(refused).toContainText('Rule no-dot · source network: refers to IoT');
  await expect(refused).toContainText('Access between networks: refers to IoT');
  await expect(refused).toContainText('Routing protocol site-b-bgp · announce 0 network: refers to IoT');
  await expect(refused).toContainText('Device Cam-1 · network: refers to IoT');
  await expect(refused).toContainText('Fault iot-slow · source network: refers to IoT');
  await expect(page.getByRole('button', { name: /^Apply revision/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Back to editing' }).click();
  // the network stays until the references are gone or the deletion is undone
  await settings(page).getByRole('button', { name: 'Undo the deletion of IoT' }).click();
  await expect(bar(page)).toHaveCount(0);
});

test('network forms: the checks stand under the fields, and Preview & apply waits until the form is valid', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByLabel('Name').fill('IoT');
  await dialog.getByLabel('Gateway address and subnet').fill('10.10.0.9/24');
  // the address is checked from the start
  await expect(dialog.getByText('10.10.0.0/24 overlaps IoT (10.10.0.0/24).')).toBeVisible();
  await dialog.getByLabel('Pools').fill('10.10.0.200-10.10.0.100');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByText('A network is already called IoT.')).toBeVisible();
  await expect(dialog.getByText('Choose at least one port')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveAttribute('aria-invalid', 'true');
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await dialog.getByLabel('Name').fill('cams');
  await dialog.getByLabel('Gateway address and subnet').fill('10.30.0.1/24');
  await dialog.getByTestId('network-ports').getByRole('checkbox', { name: /^eth3 / }).check();
  await expect(dialog.getByText('The end 10.10.0.100 is before the start 10.10.0.200.')).toBeVisible();
  await dialog.getByLabel('Pools').fill('10.10.0.100-10.10.0.200');
  await expect(dialog.getByText('10.10.0.100-10.10.0.200 must lie inside 10.30.0.0/24, without its network and broadcast address.')).toBeVisible();
  await dialog.getByLabel('Pools').fill('');
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeEnabled();

  // a WireGuard link: a /31 and the peer in it, a free port
  await dialog.getByRole('button', { name: 'WireGuard link' }).click();
  await expect(dialog.getByLabel('Gateway address (/31 transfer network)')).toHaveValue('10.255.0.2/31');
  await expect(dialog.getByLabel("Remote side's address")).toHaveValue('10.255.0.3');
  await expect(dialog.getByLabel('Listen port (UDP)')).toHaveValue('51823');
  await dialog.getByLabel('Listen port (UDP)').fill('51820');
  await dialog.getByLabel("Remote side's address").fill('10.255.0.9');
  // Preview & apply was pressed: the messages follow every change
  await expect(dialog.getByText('UDP port 51820 is already used by lab-hub.')).toBeVisible();
  await expect(dialog.getByText('It must lie in the transfer network 10.255.0.2/31.')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
});

test("network forms: a network the gateway refuses says why; Fix opens it again with the gateway's message under its field", async ({ page }) => {
  const gw = await open(page);
  gw.rejectRevision = (patch) => {
    const id = Object.keys((patch as { networks: Record<string, unknown> }).networks)[0]!;
    return [{ path: `/networks/${id}/dhcp/pools/0`, code: 'pool_overlap', message: 'the pool overlaps the reservation of broker' }];
  };
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByLabel('Name').fill('cams');
  await dialog.getByTestId('network-ports').getByRole('checkbox', { name: /^eth3 / }).check();
  await dialog.getByLabel('Pools').fill('10.30.0.100-10.30.0.199');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-invalid')).toContainText('Network cams · dhcp pools 0: the pool overlaps the reservation of broker');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(card(page, 'cams').getByTestId('network-mark')).toHaveText('NEW');
  await expect(page.getByTestId('network-refused')).toContainText('The gateway refused it: the pool overlaps the reservation of broker');
  await settings(page).getByRole('button', { name: 'Fix cams' }).click();
  const fix = page.getByRole('dialog', { name: 'Edit cams' });
  await expect(fix.getByText('the pool overlaps the reservation of broker')).toBeVisible();
  await expect(fix.getByLabel('Pools')).toHaveAttribute('aria-invalid', 'true');
  // unchanged, it is not sent again; changed, it is
  await expect(fix.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await fix.getByLabel('Pools').fill('10.30.0.150-10.30.0.199');
  await fix.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(card(page, 'cams')).toBeVisible();
});

test('network forms: a WireGuard hub is added, and edited — its clients stay out of the patch', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByRole('button', { name: 'WireGuard hub' }).click();
  await expect(dialog.getByLabel('Gateway address and subnet')).toHaveValue('10.90.0.1/24');
  await dialog.getByLabel('Name').fill('lab-hub-2');
  await expect(dialog.getByTestId('network-form-lockout')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page)).not.toContainText('This change can cut you off');
  expect(Object.values((await lastPatch(page, gw)).networks!)[0]).toEqual({ type: 'wireguard', kind: 'hub', name: 'lab-hub-2', address: '10.90.0.1/24', listen_port: 51823 });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(card(page, 'lab-hub-2')).toContainText('WireGuard hub');
  await expect(page.getByText('No client yet')).toBeVisible();

  // the hub with a client: an edit sends its own settings only
  await card(page, 'lab-hub').click();
  await settings(page).getByRole('button', { name: 'Edit lab-hub' }).click();
  const edit = page.getByRole('dialog', { name: 'Edit lab-hub' });
  await expect(edit.getByRole('group', { name: 'Kind of network' })).toHaveCount(0);
  await edit.getByRole('button', { name: /^Advanced/ }).click();
  await edit.getByLabel('Public endpoint').fill('gw.example.org:51820');
  await edit.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network lab-hub changed');
  expect((await lastPatch(page, gw)).networks).toEqual({ [HUB]: { endpoint: 'gw.example.org:51820' } });
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(settings(page)).toContainText('gw.example.org:51820');
  await expect(page.getByTestId('client-row')).toHaveCount(1);
});

test('network forms: a WireGuard link is added with its peer and routes, and edited', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByRole('button', { name: 'WireGuard link' }).click();
  await dialog.getByLabel('Name').fill('site-c');
  await dialog.getByLabel('Remote endpoint').fill('203.0.113.50:51821');
  await dialog.getByLabel('Static routes via the link').fill('10.61.0.0/24');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const [id, net] = Object.entries((await lastPatch(page, gw)).networks!)[0]!;
  expect(net).toEqual({
    type: 'wireguard',
    kind: 'link',
    name: 'site-c',
    address: '10.255.0.2/31',
    listen_port: 51823,
    peer: { address: '10.255.0.3', endpoint: '203.0.113.50:51821', key: { mode: 'generated' } },
    routes: ['10.61.0.0/24'],
  });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(card(page, 'site-c')).toContainText('WireGuard link');
  await expect(page.getByTestId('link-peer')).toContainText('10.255.0.3 at 203.0.113.50:51821 (the gateway connects)');

  await settings(page).getByRole('button', { name: 'Edit site-c' }).click();
  const edit = page.getByRole('dialog', { name: 'Edit site-c' });
  await edit.getByLabel('Remote endpoint').fill('');
  await edit.getByRole('button', { name: /^Advanced/ }).click();
  await edit.getByLabel('Keepalive').fill('10s');
  await edit.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).networks).toEqual({ [id]: { peer: { endpoint: null, keepalive: '10s' } } });
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(page.getByTestId('link-peer')).not.toContainText('the gateway connects');
  expect((gw.config.networks![id] as WireGuardNetwork).peer).toEqual({ address: '10.255.0.3', keepalive: '10s', key: { mode: 'generated' } });
});

test('network forms: a management WireGuard network is lockout-relevant — the apply waits for confirmation, Confirm makes it active', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add network' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add network' });
  await dialog.getByRole('button', { name: 'WireGuard hub' }).click();
  await dialog.getByLabel('Name').fill('ops');
  await dialog.getByLabel('Role').selectOption('management');
  await expect(dialog.getByTestId('network-form-lockout')).toContainText('the apply waits for your confirmation');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page)).toContainText('This change can cut you off from the gateway');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied, waiting for confirmation')).toBeVisible();
  const banner = page.getByTestId('countdown-banner');
  await expect(banner).toContainText('Revision 43 is applied and waits for confirmation');
  // the views show the active revision until it is confirmed
  await expect(card(page, 'ops')).toHaveCount(0);
  await banner.getByRole('button', { name: 'Confirm' }).click();
  await expect(toast(page, 'Revision 43 confirmed')).toBeVisible();
  await expect(card(page, 'ops')).toContainText('WireGuard hub · management');

  // deleting the management hub admin waits for confirmation too
  await card(page, 'admin').click();
  await settings(page).getByRole('button', { name: 'Delete admin' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page)).toContainText('This change can cut you off from the gateway');
  expect((await lastPatch(page, gw)).networks).toEqual({ [ADMIN_HUB]: null });
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied, waiting for confirmation')).toBeVisible();
  // until it is confirmed, the hub is still there
  await expect(card(page, 'admin')).toHaveCount(1);
  await banner.getByRole('button', { name: 'Confirm' }).click();
  await expect(toast(page, 'Revision 44 confirmed')).toBeVisible();
  await expect(card(page, 'admin')).toHaveCount(0);
  expect(gw.config.networks![ADMIN_HUB]).toBeUndefined();
});

test('network forms: deleting a WireGuard link its routing protocol runs on is refused with the protocol named; deleted with the protocol, the link is gone', async ({ page }) => {
  const gw = await open(page, `/networks/${LINK}`);
  await settings(page).getByRole('button', { name: 'Delete site-b' }).click();
  await expect(card(page, 'site-b').getByTestId('network-mark')).toHaveText('DELETED');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const refused = page.getByTestId('preview-invalid');
  await expect(refused).toContainText('Routing protocol site-b-bgp · link: refers to site-b, which this change deletes');
  await expect(page.getByRole('button', { name: /^Apply revision/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Back to editing' }).click();

  // the protocol goes with the link, in the same draft
  await page.getByRole('link', { name: 'Routing', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Routing', exact: true })).toBeVisible();
  const bgp = page.getByTestId('routing-config').getByTestId('protocol').filter({ hasText: 'site-b-bgp' });
  await bgp.getByRole('button', { name: 'Delete site-b-bgp' }).click();
  await expect(bar(page)).toContainText('2 changes');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('network site-b removed');
  const patch = await lastPatch(page, gw);
  expect(patch.networks).toEqual({ [LINK]: null });
  expect(patch.routing).toEqual({ protocols: { [BGP]: null } });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.config.networks![LINK]).toBeUndefined();
  expect(gw.config.routing?.protocols?.[BGP]).toBeUndefined();
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Networks', exact: true })).toBeVisible();
  await expect(card(page, 'site-b')).toHaveCount(0);
  await expect(cards(page)).toHaveCount(4);
});

test('network forms: a network changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change', async ({ page }) => {
  const gw = await open(page, `/networks/${IOT}`);
  await settings(page).getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  await dialog.getByTestId('network-ports').getByRole('checkbox', { name: /^eth3 / }).check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Back to editing' }).click();
  // someone else sets a lease time meanwhile
  gw.applyFromApi({ networks: { [IOT]: { dhcp: { lease_time: '2h' } } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'network', id: IOT, name: 'IoT', path: `/networks/${IOT}`, summary: 'network IoT changed' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Network IoT ports: eth1 → eth1, eth3');
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect(drawer(page)).toBeVisible();
  expect((await lastPatch(page, gw)).networks).toEqual({ [IOT]: { interfaces: [{ name: 'eth1' }, { name: 'eth3', mac: '52:54:00:00:00:04' }] } });
  await page.getByRole('button', { name: 'Apply revision 45' }).click();
  await expect(page.getByTestId('dhcp-settings')).toContainText('lease time 2 h');
  expect(gw.config.networks![IOT]).toMatchObject({ dhcp: { lease_time: '2h' }, interfaces: [{ name: 'eth1' }, { name: 'eth3' }] });
});

test('routing forms: add an OSPF protocol on a link through the drawer; it is listed with what it announces and imports', async ({ page }) => {
  const gw = await open(page, '/networks/routing', withNetworks(MockGateway.sample()), 'Routing');
  const config = page.getByTestId('routing-config');
  await expect(config.getByTestId('protocol')).toHaveCount(1);
  await expect(config.getByTestId('routing-settings')).toContainText(/Local AS\s*65001/);
  await page.getByRole('button', { name: 'Add protocol' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add routing protocol' });
  await dialog.getByRole('button', { name: 'OSPF' }).click();
  await expect(dialog.getByLabel('Name')).toHaveValue('site-b-ospf');
  await expect(dialog.getByLabel('Link')).toHaveValue(LINK);
  await dialog.getByLabel('Area').fill('0.0.0.1');
  await dialog.getByRole('checkbox', { name: /^Test \(10.20.0.0\/24\)/ }).check();
  await dialog.getByLabel(/^Allowed prefixes/).fill('10.70.0.0/16 le 24');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('routing protocol site-b-ospf added');
  const [pid, p] = Object.entries(((await lastPatch(page, gw)).routing as { protocols: Record<string, unknown> }).protocols)[0]!;
  expect(p).toEqual({
    name: 'site-b-ospf',
    type: 'ospf',
    link: LINK,
    announce: [{ network: TEST }],
    import: { allowed_prefixes: [{ prefix: '10.70.0.0/16', max_length: 24 }] },
    ospf: { area: '0.0.0.1' },
  });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  const ospf = config.getByTestId('protocol').filter({ hasText: 'site-b-ospf' });
  await expect(ospf).toContainText('ospf');
  await expect(ospf).toContainText('on link site-b');
  await expect(ospf.getByTestId('protocol-settings')).toHaveText('area 0.0.0.1, cost 10');
  await expect(ospf.getByTestId('protocol-announce')).toHaveText('Test');
  await expect(ospf.getByTestId('protocol-import')).toHaveText('10.70.0.0/16 le 24');
  await expect(ospf.getByTestId('protocol-mark')).toHaveCount(0);
  expect(gw.config.routing?.protocols?.[pid]).toBeDefined();
});

test('routing forms: edit and delete a protocol and change the BIRD settings through the draft; Undo takes each back', async ({ page }) => {
  const gw = await open(page, '/networks/routing', withNetworks(MockGateway.sample()), 'Routing');
  const config = page.getByTestId('routing-config');
  const bgp = config.getByTestId('protocol').filter({ hasText: 'site-b-bgp' });
  await bgp.getByRole('button', { name: 'Edit site-b-bgp' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit site-b-bgp' });
  await expect(dialog.getByLabel('Neighbor AS')).toHaveValue('65002');
  await dialog.getByLabel('Neighbor AS').fill('65003');
  await dialog.getByLabel('Hold time').fill('9s');
  await dialog.getByLabel('Keepalive time').fill('3s');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).routing).toEqual({ protocols: { [BGP]: { bgp: { neighbor_asn: 65003, hold_time: '9s', keepalive_time: '3s' } } } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(bgp.getByTestId('protocol-mark')).toHaveText('CHANGED');
  await expect(bgp.getByTestId('protocol-settings')).toHaveText('neighbor AS 65003');
  await expect(bar(page)).toContainText('3 changes in the routing');
  await bgp.getByRole('button', { name: 'Undo the changes of site-b-bgp' }).click();
  await expect(bar(page)).toHaveCount(0);

  // the settings: router id and AS
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const s = page.getByRole('dialog', { name: 'Routing settings' });
  await s.getByLabel('Local AS').fill('');
  await s.getByRole('button', { name: 'Preview & apply' }).click();
  // BGP runs: the local AS stays required
  await expect(s.getByText('BGP needs the local AS number.')).toBeVisible();
  await s.getByLabel('Local AS').fill('65010');
  await s.getByLabel('Router id').fill('10.10.0.254');
  await s.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).routing).toEqual({ asn: 65010, router_id: '10.10.0.254' });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(page.getByTestId('routing-settings-changed')).toContainText('CHANGED');
  await page.getByRole('button', { name: 'Undo the changes of the routing settings' }).click();
  await expect(bar(page)).toHaveCount(0);

  // delete the protocol, Undo, delete again and apply
  await bgp.getByRole('button', { name: 'Delete site-b-bgp' }).click();
  await expect(bgp.getByTestId('protocol-mark')).toHaveText('DELETED');
  await bgp.getByRole('button', { name: 'Undo the deletion of site-b-bgp' }).click();
  await expect(bar(page)).toHaveCount(0);
  await bgp.getByRole('button', { name: 'Delete site-b-bgp' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('routing protocol site-b-bgp removed');
  expect((await lastPatch(page, gw)).routing).toEqual({ protocols: { [BGP]: null } });
  // the two candidates before were discarded with Back to editing
  await page.getByRole('button', { name: 'Apply revision 45' }).click();
  await expect(toast(page, 'Revision 45 applied and verified in 87 ms')).toBeVisible();
  await expect(config.getByTestId('protocol')).toHaveCount(0);
  await expect(config).toContainText('No routing protocol: static routes only.');
});

test('routing forms: the external mode switched off again takes its hidden fields out of the checks and keeps them out of the patch', async ({ page }) => {
  const gw = await open(page, '/networks/routing', withNetworks(MockGateway.sample()), 'Routing');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const s = page.getByRole('dialog', { name: 'Routing settings' });
  await s.getByLabel('Import the routes another daemon writes into a kernel table').check();
  await s.getByRole('textbox', { name: 'Kernel table' }).fill('200');
  await s.getByLabel(/^Allowed prefixes/).fill('10.80.0.0/33');
  await s.getByLabel('Most prefixes taken').fill('many');
  await s.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(s.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await expect(s.getByLabel(/^Allowed prefixes/)).toHaveAttribute('aria-invalid', 'true');
  // switched off, the fields are hidden: they block nothing and are not sent
  await s.getByLabel('Import the routes another daemon writes into a kernel table').uncheck();
  await expect(s.getByRole('button', { name: 'Preview & apply' })).toBeEnabled();
  await s.getByLabel('Router id').fill('10.10.0.254');
  await s.getByRole('button', { name: 'Preview & apply' }).click();
  expect((await lastPatch(page, gw)).routing).toEqual({ router_id: '10.10.0.254' });
});

test("routing forms: the checks stand under the fields; a protocol the gateway refuses opens again with the gateway's message under its field", async ({ page }) => {
  const gw = await open(page, '/networks/routing', withNetworks(MockGateway.sample()), 'Routing');
  await page.getByRole('button', { name: 'Add protocol' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add routing protocol' });
  // site-b runs BGP already
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByText('site-b already runs a BGP protocol (site-b-bgp).')).toBeVisible();
  await expect(dialog.getByText("The neighbor's AS number is required.")).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await dialog.getByRole('button', { name: 'Babel' }).click();
  await dialog.getByLabel('Receive cost').fill('0');
  await expect(dialog.getByText('From 1 to 65535; empty for the default of 96.')).toBeVisible();
  await dialog.getByLabel('Receive cost').fill('128');
  await dialog.getByLabel('Name').fill('site-b-babel');

  gw.rejectRevision = (patch) => {
    const id = Object.keys((patch as { routing: { protocols: Record<string, unknown> } }).routing.protocols)[0]!;
    return [{ path: `/routing/protocols/${id}/custom_snippet`, code: 'bird_check_failed', message: 'bird -p: syntax error, line 3' }];
  };
  await dialog.getByLabel('Custom BIRD snippet (unmanaged)').fill('import all;;');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-invalid')).toContainText('Routing protocol site-b-babel · custom_snippet: bird -p: syntax error, line 3');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  const babel = page.getByTestId('protocol').filter({ hasText: 'site-b-babel' });
  await expect(babel.getByTestId('protocol-mark')).toHaveText('NEW');
  await expect(babel).toContainText('unmanaged snippet');
  await expect(babel.getByTestId('protocol-refused')).toContainText('bird -p: syntax error, line 3');
  await babel.getByRole('button', { name: 'Fix site-b-babel' }).click();
  const fix = page.getByRole('dialog', { name: 'Edit site-b-babel' });
  await expect(fix.getByLabel('Custom BIRD snippet (unmanaged)')).toHaveAttribute('aria-invalid', 'true');
  await expect(fix.getByText('bird -p: syntax error, line 3')).toBeVisible();
  await fix.getByLabel('Custom BIRD snippet (unmanaged)').fill('import all;');
  await fix.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(babel.getByTestId('protocol-mark')).toHaveCount(0);
});
