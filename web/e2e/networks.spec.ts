import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import jsQR from 'jsqr';
import { PNG } from 'pngjs';
import type { WireGuardNetwork } from '../src/api/generated/model';
import { ADMIN_HUB, CLIENT_RA, HUB, IOT, LINK, MockGateway, TEST, toast, withNetworks } from './mock';

// M14: the network cards and the detail, DHCP pool and leases, WireGuard networks with their clients and
// status, the add-client dialog with the proposed address, the QR dialog and the download, the routing
// view, the access matrix through the draft and the drawer, commit-confirm with its timeout and "Roll back
// now", and the technical view. The client actions (disable/enable, rotate keys, delete; P3-M14-01) go
// through the draft and the drawer too.

async function open(page: Page, path = '/networks', gw = withNetworks(MockGateway.sample()), heading = 'Networks') {
  await gw.install(page);
  await page.goto(path);
  await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
  return gw;
}

const bar = (page: Page) => page.getByTestId('unapplied-bar');
const clientRows = (page: Page) => page.getByTestId('client-row');

/** The text of the QR code on the page, decoded from a screenshot of it. */
async function scanQr(page: Page): Promise<string | undefined> {
  const png = PNG.sync.read(await page.getByTestId('client-qr').screenshot());
  return jsQR(new Uint8ClampedArray(png.data), png.width, png.height)?.data;
}

test('network cards and the detail of a test network: subnet, gateway, attachment, DHCP pool and leases, DNS, IPv6', async ({ page }) => {
  await open(page);
  const cards = page.getByTestId('network-card');
  await expect(cards).toHaveCount(5);
  await expect(cards.nth(0)).toContainText('IoT');
  await expect(cards.nth(0)).toContainText('test network');
  await expect(cards.nth(0)).toContainText('10.10.0.0/24');
  await expect(cards.nth(0)).toContainText('4 devices · DHCP · DNS');
  await expect(cards.nth(2)).toContainText('lab-hub');
  await expect(cards.nth(2)).toContainText('WireGuard hub');
  await expect(cards.nth(2)).toContainText('1 of 1 clients online');
  await expect(cards.nth(3)).toContainText('WireGuard hub · management');
  await expect(cards.nth(4)).toContainText('site-b');
  await expect(cards.nth(4)).toContainText('peer online');
  await expect(page.getByTestId('management-card')).toContainText('eth1');
  await expect(page.getByTestId('uplink-card')).toContainText('192.168.1.57/24 · eth0');
  await expect(page.getByTestId('uplink-card')).toContainText('via 192.168.1.1');

  // the first network is selected
  await expect(cards.nth(0)).toHaveAttribute('aria-current', 'page');
  const settings = page.getByTestId('network-settings');
  await expect(settings.getByRole('heading', { name: 'IoT network' })).toBeVisible();
  await expect(settings).toContainText(/Subnet\s*10.10.0.0\/24/);
  await expect(settings).toContainText(/Gateway\s*10.10.0.1/);
  await expect(settings).toContainText('physical interface eth1 (link up)');
  await expect(page.getByTestId('dhcp-settings')).toContainText('on · lease time 1 h · 2 active');
  await expect(page.getByTestId('dhcp-pool')).toContainText('10.10.0.128 – 10.10.0.254');
  await expect(page.getByTestId('dhcp-pool')).toContainText('the default: the second half of the subnet');
  await expect(page.getByTestId('dhcp-pool')).toContainText('125 addresses free');
  await expect(settings).toContainText(/DNS\s*Chaos Gateway proxy/);
  await expect(settings).toContainText(/IPv6\s*blocked \(no RA, no forwarding\)/);

  // the leases, with the device they belong to
  const leases = page.getByTestId('leases');
  await expect(leases.getByRole('row')).toHaveCount(3);
  await expect(leases.getByRole('row').nth(1)).toContainText('10.10.0.42');
  await expect(leases.getByRole('row').nth(1)).toContainText('esp32-42');
  await expect(leases.getByRole('row').nth(1)).toContainText(/57:\d\d|58:00/);
  await expect(leases.getByRole('link', { name: 'ESP32-42' })).toHaveAttribute('href', '/devices/a1b2c3d4-0000-4000-8000-000000000042');

  // the Linux view of the network: only what names its bridge or its subnet
  await settings.getByRole('button', { name: 'Show Linux view (compiled)' }).click();
  const linux = page.getByTestId('linux-excerpt');
  await expect(linux).toContainText('iifname "br-iot" oifname "eth0" counter packets 12 bytes 840 accept');
  await expect(linux).toContainText('bridge br-iot up');
  await expect(linux).toContainText('br-iot:   sent 840 bytes 12 pkt');
  await expect(linux).not.toContainText('br-test');

  // another card selects another network, the URL names it
  await cards.nth(1).click();
  await expect(page).toHaveURL(new RegExp(`/networks/${TEST}$`));
  await expect(settings.getByRole('heading', { name: 'Test network' })).toBeVisible();
  await expect(page.getByTestId('leases')).toHaveCount(0);
  await expect(page.getByText('No device has a lease yet.')).toBeVisible();
});

test('a WireGuard hub lists its clients with their status; a link shows its peer and its routes', async ({ page }) => {
  await open(page, `/networks/${HUB}`);
  const settings = page.getByTestId('network-settings');
  await expect(settings.getByRole('heading', { name: 'lab-hub WireGuard hub' })).toBeVisible();
  await expect(settings).toContainText(/Tunnel subnet\s*10.99.0.0\/24/);
  await expect(settings).toContainText(/Endpoint\s*uplink address:51820/);
  await expect(settings).toContainText('qU1BFDFDgsH5I7uGhzuhQjkMSw6bP8nXN6CN4AKxEF8=');
  await expect(settings).toContainText('1 of 1 online');
  await expect(clientRows(page)).toHaveCount(1);
  const ra = clientRows(page).nth(0);
  await expect(ra).toContainText('rA');
  await expect(ra).toContainText('10.99.0.2');
  await expect(ra).toContainText('10.50.0.0/24');
  await expect(ra).toContainText('Online');
  await expect(ra).toContainText(/handshake \d+ s ago/);
  await expect(ra).toContainText('198.51.100.7:40312');
  await expect(ra).toContainText('↓ 1.8 MB ↑ 240 KB');

  await page.goto(`/networks/${LINK}`);
  await expect(settings.getByRole('heading', { name: 'site-b WireGuard link' })).toBeVisible();
  const peer = page.getByTestId('link-peer');
  await expect(peer).toContainText('10.255.0.1 at 203.0.113.40:51821 (the gateway connects)');
  await expect(peer).toContainText('Online');
  await expect(peer).toContainText(/handshake \d+ s ago/);
  await expect(settings).toContainText('10.60.0.0/24 (static)');
  await expect(settings).toContainText('10.70.0.0/24 (learned)');
  await expect(clientRows(page)).toHaveCount(0);
});

test('a client that goes offline is shown offline after the next poll', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  await expect(clientRows(page).nth(0)).toContainText('Online');
  gw.clientStatus[CLIENT_RA] = { online: false, last_handshake: new Date(Date.now() - 200_000).toISOString() };
  await gw.emit(page, { type: 'wireguard_peer_offline', subject: { kind: 'client', id: CLIENT_RA, name: 'rA' } });
  await expect(clientRows(page).nth(0)).toContainText('Offline');
  await expect(clientRows(page).nth(0)).toContainText(/handshake 3 min ago/);
});

test('add a client: the next free address is proposed, the checks stand under the fields, the drawer previews it and the apply adds it', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  await page.getByRole('button', { name: '</> API' }).click();
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to lab-hub' });
  await expect(dialog.getByLabel('Tunnel address')).toHaveValue('10.99.0.3');
  await expect(dialog.getByText('The next free address of the hub.')).toBeVisible();

  // validation: the messages under their fields, Preview & apply waits until the form is valid
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByText('A name is required.')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await dialog.getByLabel('Name').fill('rA');
  await expect(dialog.getByText('A device or client is already called rA.')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveAttribute('aria-invalid', 'true');
  await dialog.getByLabel('Name').fill('rC');
  await dialog.getByLabel('Tunnel address').fill('10.99.0.2');
  await expect(dialog.getByText('Another client has this address.')).toBeVisible();
  await dialog.getByLabel('Tunnel address').fill('10.98.0.3');
  await expect(dialog.getByText("It must lie in the hub's subnet 10.99.0.0/24.")).toBeVisible();
  await dialog.getByLabel('Tunnel address').fill('10.99.0.3');
  await dialog.getByRole('button', { name: /Advanced/ }).click();
  await dialog.getByLabel('Client networks (subnets behind the client)').fill('10.51.0.0');
  await expect(dialog.getByText('10.51.0.0 is no IPv4 network such as 10.50.0.0/24.')).toBeVisible();
  await dialog.getByLabel('Client networks (subnets behind the client)').fill('10.51.0.0/24');
  await dialog.getByRole('checkbox', { name: /IoT/ }).check();
  await expect(dialog.getByTestId('add-client-preview')).toContainText('Client rC is added to lab-hub with the tunnel address 10.99.0.3');

  // the API panel shows the candidate the dialog would send
  await expect(page.getByTestId('api-panel')).toContainText('This dialog: Add WireGuard client');
  await expect(page.getByTestId('api-panel')).toContainText('"address": "10.99.0.3"');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();

  const drawer = page.getByTestId('preview-drawer');
  await expect(drawer).toContainText('WireGuard client rC added (10.99.0.3)');
  await expect(drawer).not.toContainText('can cut you off');
  const created = gw.requestsTo('POST', '/revisions').at(-1)!;
  const patch = created.body as { networks: Record<string, { clients: Record<string, unknown> }> };
  expect(Object.values(patch.networks[HUB]!.clients)).toEqual([
    { name: 'rC', address: '10.99.0.3', client_networks: ['10.51.0.0/24'], reachable: [{ network: IOT }], key: { mode: 'generated' } },
  ]);
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(clientRows(page)).toHaveCount(2);
  await expect(clientRows(page).nth(1)).toContainText('rC');
  await expect(clientRows(page).nth(1)).toContainText('10.99.0.3');
  await expect(clientRows(page).nth(1)).toContainText('Offline');
  await expect(clientRows(page).nth(1)).toContainText('no handshake yet');
  await expect(clientRows(page).nth(1)).not.toContainText('NEW');
  // the next client gets the next address
  await page.getByRole('button', { name: 'Add client' }).first().click();
  await expect(dialog.getByLabel('Tunnel address')).toHaveValue('10.99.0.4');
});

test('backing out of the drawer keeps the new client in the draft, marked NEW, until it is applied or discarded', async ({ page }) => {
  await open(page, `/networks/${HUB}`);
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to lab-hub' });
  await dialog.getByLabel('Name').fill('rC');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(bar(page)).toContainText('1 change in 1 WireGuard client · not applied yet');
  await expect(clientRows(page).nth(1)).toContainText('rC');
  await expect(clientRows(page).nth(1)).toContainText('NEW');
  await expect(clientRows(page).nth(1)).toContainText('not applied yet');
  await expect(clientRows(page).nth(1).getByRole('button', { name: /Configuration/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Discard' }).click();
  await expect(clientRows(page)).toHaveCount(1);
});

test("a new client the gateway refuses says why in its row; Fix opens it again with the message under its field", async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  let refusedId = '';
  gw.rejectRevision = (patch) => {
    refusedId = Object.keys((patch as { networks: Record<string, { clients: Record<string, unknown> }> }).networks[HUB]!.clients)[0]!;
    return [
      { path: `/networks/${HUB}/clients/${refusedId}/address`, code: 'conflict', message: 'the address 10.99.0.3 is used by the probe p1.' },
      { path: `/networks/${HUB}/clients/${refusedId}`, code: 'invalid', message: 'the hub has no room for another peer.' },
    ];
  };
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to lab-hub' });
  await dialog.getByLabel('Name').fill('rC');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByTestId('preview-drawer');
  await expect(drawer.getByTestId('preview-invalid')).toContainText('the address 10.99.0.3 is used by the probe p1.');
  await page.getByRole('button', { name: 'Back to editing' }).click();

  // the row says it as text, not only in a tooltip
  const row = clientRows(page).nth(1);
  await expect(row).toContainText('NEEDS FIXING');
  await expect(row.getByTestId('client-refused')).toHaveText('The gateway refused it: the address 10.99.0.3 is used by the probe p1. the hub has no room for another peer.');
  await row.getByRole('button', { name: 'Fix rC' }).click();

  // the dialog has the client as it was, the message under the address, the one about the client as a whole below
  const fix = page.getByRole('dialog', { name: 'Fix client of lab-hub' });
  await expect(fix.getByLabel('Name')).toHaveValue('rC');
  await expect(fix.getByLabel('Tunnel address')).toHaveValue('10.99.0.3');
  await expect(fix.getByLabel('Tunnel address')).toHaveAttribute('aria-invalid', 'true');
  await expect(fix.getByText('the address 10.99.0.3 is used by the probe p1.')).toBeVisible();
  await expect(fix.getByTestId('add-client-refused')).toHaveText('The gateway refused the client: the hub has no room for another peer.');
  await expect(fix.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await fix.getByLabel('Tunnel address').fill('10.99.0.4');
  await expect(fix.getByText('the address 10.99.0.3 is used by the probe p1.')).toHaveCount(0);
  await expect(fix.getByTestId('add-client-refused')).toHaveCount(0);
  await fix.getByRole('button', { name: 'Preview & apply' }).click();

  // the same client, with the new address, goes into the next candidate
  await expect(drawer).toContainText('WireGuard client rC added (10.99.0.4)');
  const created = gw.requestsTo('POST', '/revisions').at(-1)!;
  const patch = created.body as { networks: Record<string, { clients: Record<string, { address: string }> }> };
  expect(Object.keys(patch.networks[HUB]!.clients)).toEqual([refusedId]);
  expect(patch.networks[HUB]!.clients[refusedId]!.address).toBe('10.99.0.4');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified')).toBeVisible();
  await expect(clientRows(page).nth(1)).toContainText('10.99.0.4');
  await expect(clientRows(page).nth(1)).not.toContainText('NEEDS FIXING');
});

const hubClients = (gw: MockGateway, id: string) => (gw.config.networks![id] as WireGuardNetwork).clients ?? {};

test('client actions: disable and rotate the keys of a client through the draft, the drawer and the apply; Undo takes them back (P3-M14-01)', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  const rA = clientRows(page).filter({ hasText: 'rA' });
  await rA.getByRole('button', { name: 'Disable rA' }).click();
  await expect(rA).toContainText('CHANGED');
  await expect(rA.getByTestId('client-disabled')).toHaveText('disabled');
  await expect(bar(page)).toHaveText(/1 change in 1 WireGuard client/);
  // Undo takes it back: nothing is left to apply
  await rA.getByRole('button', { name: 'Undo the changes of rA' }).click();
  await expect(bar(page)).toBeHidden();
  await expect(rA.getByTestId('client-disabled')).toHaveCount(0);

  await rA.getByRole('button', { name: 'Disable rA' }).click();
  await rA.getByRole('button', { name: 'Rotate the keys of rA' }).click();
  const rotate = page.getByRole('dialog', { name: 'Rotate the keys of rA' });
  await expect(rotate).toContainText('The gateway makes a new key pair and a new preshared key when the change is applied.');
  await expect(rotate).toContainText("the client's current configuration no longer connects: give it its new configuration (QR code or download)");
  await rotate.getByRole('button', { name: 'Rotate keys' }).click();
  await expect(rotate).toBeHidden();
  await expect(rA.getByTestId('client-rotated')).toHaveText('New keys once applied: give the client its new configuration then.');
  await expect(bar(page)).toHaveText(/2 changes in 1 WireGuard client/);

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByTestId('preview-drawer');
  await expect(drawer).toContainText('WireGuard client rA changed');
  await expect(drawer).not.toContainText('can cut you off');
  expect(gw.requestsTo('POST', '/revisions')[0]!.body).toEqual({ networks: { [HUB]: { clients: { [CLIENT_RA]: { enabled: false, key: { generation: 2 } } } } } });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(hubClients(gw, HUB)[CLIENT_RA]).toMatchObject({ enabled: false, key: { mode: 'generated', preshared_key: true, generation: 2 } });
  await expect(rA).not.toContainText('CHANGED');
  await expect(rA.getByTestId('client-disabled')).toHaveText('disabled');
  await expect(rA.getByTestId('client-rotated')).toHaveCount(0);
  // a disabled client offers Enable
  await rA.getByRole('button', { name: 'Enable rA' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  expect(gw.requestsTo('POST', '/revisions')[1]!.body).toEqual({ networks: { [HUB]: { clients: { [CLIENT_RA]: { enabled: null } } } } });
});

test('client actions: a client changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change (P3-M14-01)', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  const rA = clientRows(page).filter({ hasText: 'rA' });
  await rA.getByRole('button', { name: 'Disable rA' }).click();
  // meanwhile a token gives rA another keepalive and enables it explicitly
  gw.applyFromApi({ networks: { [HUB]: { clients: { [CLIENT_RA]: { keepalive: '5s', enabled: true } } } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'client', id: CLIENT_RA, name: 'rA', path: `/networks/${HUB}/clients/${CLIENT_RA}`, summary: 'WireGuard client rA: keepalive 1s → 5s' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-theirs')).toContainText('WireGuard client rA: keepalive 1s → 5s');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('WireGuard client rA enabled: yes → no');
  await expect(conflict.getByTestId('conflict-both').getByRole('listitem')).toHaveText(['WireGuard client rA enabled: yes → no']);
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' })).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')[1]!.body).toEqual({ networks: { [HUB]: { clients: { [CLIENT_RA]: { enabled: false } } } } });
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  expect(hubClients(gw, HUB)[CLIENT_RA]).toMatchObject({ keepalive: '5s', enabled: false });
});

test('client actions: delete a client, Undo brings it back, deleted through the drawer it is gone (P3-M14-01)', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  const rA = clientRows(page).filter({ hasText: 'rA' });
  await rA.getByRole('button', { name: 'Delete rA' }).click();
  await expect(rA).toContainText('DELETED');
  await expect(rA.getByRole('button', { name: 'Disable rA' })).toHaveCount(0);
  await rA.getByRole('button', { name: 'Undo the deletion of rA' }).click();
  await expect(rA).not.toContainText('DELETED');
  await expect(bar(page)).toBeHidden();

  await rA.getByRole('button', { name: 'Delete rA' }).click();
  await expect(bar(page)).toHaveText(/1 change in 1 WireGuard client/);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-drawer')).toContainText('WireGuard client rA removed');
  expect(gw.requestsTo('POST', '/revisions')[0]!.body).toEqual({ networks: { [HUB]: { clients: { [CLIENT_RA]: null } } } });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(hubClients(gw, HUB)).toEqual({});
  await expect(page.getByText('No client yet')).toBeVisible();
});

test("client actions: a client with its own key rotates by its new public key, checked under the field (P3-M14-01)", async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const RP = 'c1000000-0000-4000-8000-0000000000f1';
  const OLD = 'qU1BFDFDgsH5I7uGhzuhQjkMSw6bP8nXN6CN4AKxEF8=';
  const NEW = 'FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=';
  (gw.config.networks![HUB] as WireGuardNetwork).clients![RP] = { name: 'rP', address: '10.99.0.5', key: { mode: 'provided', public_key: OLD } };
  await open(page, `/networks/${HUB}`, gw);
  const rP = clientRows(page).filter({ hasText: 'rP' });
  await rP.getByRole('button', { name: 'Rotate the keys of rP' }).click();
  const rotate = page.getByRole('dialog', { name: 'Rotate the keys of rP' });
  await expect(rotate).toContainText('The client has its own key: enter the public key of its new key pair.');
  await rotate.getByLabel('New public key').fill(OLD);
  await rotate.getByRole('button', { name: 'Rotate keys' }).click();
  await expect(rotate.getByText('This is the key the client has now.')).toBeVisible();
  await expect(rotate.getByLabel('New public key')).toHaveAttribute('aria-invalid', 'true');
  await rotate.getByLabel('New public key').fill('not a key');
  await expect(rotate.getByText("The client's new WireGuard public key (44 characters of base64).")).toBeVisible();
  await rotate.getByLabel('New public key').fill(NEW);
  await rotate.getByRole('button', { name: 'Rotate keys' }).click();
  await expect(rotate).toBeHidden();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-drawer')).toContainText('WireGuard client rP changed');
  expect(gw.requestsTo('POST', '/revisions')[0]!.body).toEqual({ networks: { [HUB]: { clients: { [RP]: { key: { public_key: NEW } } } } } });
});

test('client actions: disabling a client of the management hub waits for confirmation (P3-M14-01)', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const LAPTOP = 'c1000000-0000-4000-8000-0000000000a2';
  gw.config.networks![ADMIN_HUB] = { ...(gw.config.networks![ADMIN_HUB] as WireGuardNetwork), clients: { [LAPTOP]: { name: 'admin-laptop', address: '10.98.0.2' } } };
  await open(page, `/networks/${ADMIN_HUB}`, gw);
  const laptop = clientRows(page).filter({ hasText: 'admin-laptop' });
  await laptop.getByRole('button', { name: 'Disable admin-laptop' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-drawer')).toContainText('This change can cut you off from the gateway');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied, waiting for confirmation')).toBeVisible();
  // the views show the active revision until it is confirmed
  await expect(laptop.getByTestId('client-disabled')).toHaveCount(0);
  await page.getByTestId('countdown-banner').getByRole('button', { name: 'Confirm' }).click();
  await expect(toast(page, 'Revision 43 confirmed')).toBeVisible();
  await expect(laptop.getByTestId('client-disabled')).toHaveText('disabled');
  expect(hubClients(gw, ADMIN_HUB)[LAPTOP]).toEqual({ name: 'admin-laptop', address: '10.98.0.2', enabled: false });
});

test('the QR code of a client decodes to the configuration the download saves, from one export', async ({ page }) => {
  const gw = await open(page, `/networks/${HUB}`);
  await clientRows(page).nth(0).getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  const dialog = page.getByRole('dialog', { name: 'Configuration of rA' });
  await expect(dialog).toContainText("This configuration contains the client's private key");
  await expect(dialog.getByRole('img', { name: 'QR code of the configuration of rA' })).toBeVisible();

  const [download] = await Promise.all([page.waitForEvent('download'), dialog.getByRole('button', { name: 'Download rA.conf' }).click()]);
  expect(download.suggestedFilename()).toBe('rA.conf');
  const saved = readFileSync((await download.path())!, 'utf8');
  expect(saved).toContain('[Interface]');
  expect(saved).toContain('Address = 10.99.0.2/32');
  expect(await scanQr(page)).toBe(saved);
  // the gateway was asked once: the code and the file are that one export
  expect(gw.requestsTo('GET', `/networks/${HUB}/clients/${CLIENT_RA}/export`).map((r) => r.query)).toEqual([{ format: 'conf' }]);

  await dialog.getByRole('button', { name: 'Show the configuration text' }).click();
  await expect(dialog.getByTestId('client-conf-text')).toContainText('PersistentKeepalive = 25');
  await dialog.getByRole('button', { name: 'Close' }).first().click();
  await page.getByRole('button', { name: '</> API' }).click();
  await expect(page.getByTestId('api-panel')).toContainText('Last action: Configuration of rA');
  await expect(page.getByTestId('api-panel')).toContainText(`/networks/${HUB}/clients/${CLIENT_RA}/export?format=conf`);
});

test('a client exported once: the dialog says the key goes, the next export has the placeholder', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const hub = gw.config.networks![HUB] as { clients: Record<string, { key?: object }> };
  hub.clients[CLIENT_RA]!.key = { mode: 'generated', export_once: true };
  await open(page, `/networks/${HUB}`, gw);
  await clientRows(page).nth(0).getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  const dialog = page.getByRole('dialog', { name: 'Configuration of rA' });
  await expect(dialog).toContainText('The gateway deletes the key after this export: save the file now.');
  await expect.poll(() => scanQr(page)).toContain('PrivateKey = cHJpdmF0ZS');
  await dialog.getByRole('button', { name: 'Close' }).first().click();
  await clientRows(page).nth(0).getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  await expect(dialog).toContainText('the configuration has the placeholder <PRIVATE_KEY>');
  await expect.poll(() => scanQr(page)).toContain('PrivateKey = <PRIVATE_KEY>');
});

test("a slow export of one client never shows up in the dialog of another", async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const hub = gw.config.networks![HUB] as { clients: Record<string, object> };
  const RB = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4e';
  hub.clients[RB] = { name: 'rB', address: '10.99.0.3', key: { mode: 'generated' } };
  let release = () => {};
  gw.exportGate = new Promise((r) => (release = r));
  await open(page, `/networks/${HUB}`, gw);

  // rA's export hangs; the dialog is closed and opened for rB, whose export answers at once
  await page.getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  await expect(page.getByRole('dialog', { name: 'Configuration of rA' })).toContainText('Exporting the configuration…');
  await page.getByRole('dialog', { name: 'Configuration of rA' }).getByRole('button', { name: 'Close' }).first().click();
  await page.getByRole('button', { name: 'Configuration and QR code of rB' }).click();
  const dialog = page.getByRole('dialog', { name: 'Configuration of rB' });
  await expect.poll(() => scanQr(page)).toContain('Address = 10.99.0.3/32');

  // rA's answer arrives late: it is dropped
  const late = page.waitForResponse((r) => r.url().includes(`/clients/${CLIENT_RA}/export`));
  release();
  await late;
  await page.evaluate(() => new Promise((r) => setTimeout(r, 200)));
  expect(await scanQr(page)).toContain('Address = 10.99.0.3/32');
  await dialog.getByRole('button', { name: 'Show the configuration text' }).click();
  await expect(dialog.getByTestId('client-conf-text')).toContainText('Address = 10.99.0.3/32');
  await expect(dialog.getByTestId('client-conf-text')).not.toContainText('10.99.0.2');
  const [download] = await Promise.all([page.waitForEvent('download'), dialog.getByRole('button', { name: 'Download rB.conf' }).click()]);
  expect(readFileSync((await download.path())!, 'utf8')).toContain('Address = 10.99.0.3/32');
});

test('an export-once key is never lost: a dialog reopened while the export hangs waits for it', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const hub = gw.config.networks![HUB] as { clients: Record<string, { key?: object }> };
  hub.clients[CLIENT_RA]!.key = { mode: 'generated', export_once: true };
  let release = () => {};
  gw.exportGate = new Promise((r) => (release = r));
  await open(page, `/networks/${HUB}`, gw);
  const openRA = () => clientRows(page).nth(0).getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  const dialog = page.getByRole('dialog', { name: 'Configuration of rA' });

  // closed and reopened while the export hangs: the new opening waits for it instead of exporting again
  await openRA();
  await expect(dialog).toContainText('Exporting the configuration…');
  await dialog.getByRole('button', { name: 'Close' }).first().click();
  await openRA();
  await expect(dialog).toContainText('Exporting the configuration…');
  release();
  await expect.poll(() => scanQr(page)).toContain('PrivateKey = cHJpdmF0ZS');
  expect(gw.requestsTo('GET', `/networks/${HUB}/clients/${CLIENT_RA}/export`)).toHaveLength(1);
});

test('an export-once answer that arrives while no dialog is open is shown by the next opening', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  const hub = gw.config.networks![HUB] as { clients: Record<string, { key?: object }> };
  hub.clients[CLIENT_RA]!.key = { mode: 'generated', export_once: true };
  let release = () => {};
  gw.exportGate = new Promise((r) => (release = r));
  await open(page, `/networks/${HUB}`, gw);
  const openRA = () => clientRows(page).nth(0).getByRole('button', { name: 'Configuration and QR code of rA' }).click();
  const dialog = page.getByRole('dialog', { name: 'Configuration of rA' });
  await openRA();
  await expect(dialog).toContainText('Exporting the configuration…');
  await dialog.getByRole('button', { name: 'Close' }).first().click();
  const late = page.waitForResponse((r) => r.url().includes(`/clients/${CLIENT_RA}/export`));
  release();
  await late;
  await page.evaluate(() => new Promise((r) => setTimeout(r, 200)));
  await openRA();
  await expect(dialog).toContainText('The gateway deletes the key after this export: save the file now.');
  await expect.poll(() => scanQr(page)).toContain('PrivateKey = cHJpdmF0ZS');
  expect(gw.requestsTo('GET', `/networks/${HUB}/clients/${CLIENT_RA}/export`)).toHaveLength(1);
});

test('the access matrix: a click changes a cell in the draft, the bar counts it, the preview and the apply', async ({ page }) => {
  const gw = await open(page);
  const matrix = page.getByTestId('access-matrix');
  const cell = (from: string, to: string) => matrix.locator(`[data-cell="${from}>${to}"]`);
  await expect(cell('IoT', 'Internet')).toHaveText(/Allowed\s*default/);
  await expect(cell('IoT', 'Internet')).toHaveAttribute('aria-pressed', 'true');
  await expect(cell('IoT', 'lab-hub')).toHaveText('Allowed');
  await expect(cell('Test', 'IoT')).toHaveText(/Blocked\s*default/);
  await expect(cell('lab-hub', 'Internet')).toHaveText(/Blocked\s*default/);
  await expect(cell('IoT', 'Management')).toHaveAttribute('aria-label', 'IoT to Management: blocked (default). Click to change');

  // a click and a click back: no change
  await cell('IoT', 'Internet').click();
  await expect(cell('IoT', 'Internet')).toHaveText(/Blocked\s*CHANGED/);
  await expect(bar(page)).toContainText('1 change in the access matrix · not applied yet');
  await cell('IoT', 'Internet').click();
  await expect(bar(page)).toHaveCount(0);

  await cell('IoT', 'Internet').click();
  await cell('Test', 'lab-hub').click();
  await expect(bar(page)).toContainText('2 changes in the access matrix');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByTestId('preview-drawer');
  await expect(drawer).toContainText('access between networks changed');
  const created = gw.requestsTo('POST', '/revisions').at(-1)!;
  expect(created.body).toEqual({
    access_matrix: {
      entries: [
        { from: { network: 'IoT' }, to: { network: 'lab-hub' }, policy: 'allow' },
        { from: { network: IOT }, to: { uplink: true }, policy: 'deny' },
        { from: { network: TEST }, to: { network: HUB }, policy: 'allow' },
      ],
    },
  });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified')).toBeVisible();
  await expect(cell('IoT', 'Internet')).toHaveText('Blocked');
  await expect(cell('Test', 'lab-hub')).toHaveText('Allowed');

  // the Linux view of the matrix is the compiled forward chain
  await page.getByRole('button', { name: 'Show Linux view (compiled forward chain)' }).click();
  await expect(page.getByTestId('linux-excerpt').first()).toContainText('chain forward {');
  await expect(page.getByTestId('linux-excerpt').first()).toContainText('ct state { established, related } accept');
});

test('commit-confirm: a client of the management hub waits for confirmation; unconfirmed, it is rolled back after the timeout and the UI shows it', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  gw.confirmWindowMs = 4_000;
  await open(page, `/networks/${ADMIN_HUB}`, gw);
  await expect(page.getByText('No client yet')).toBeVisible();
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to admin' });
  await expect(dialog.getByLabel('Tunnel address')).toHaveValue('10.98.0.2');
  await expect(dialog.getByTestId('add-client-preview')).toContainText('admin is a management network: the apply waits for your confirmation, and the client is listed once it is confirmed.');
  await dialog.getByLabel('Name').fill('admin-laptop');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByTestId('preview-drawer');
  await expect(drawer).toContainText('This change can cut you off from the gateway');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied, waiting for confirmation')).toBeVisible();

  // the countdown runs; the views show the active revision, the client is listed once it is confirmed
  const banner = page.getByTestId('countdown-banner');
  await expect(banner).toContainText('Revision 43 is applied and waits for confirmation');
  await expect(banner.getByRole('button', { name: 'Roll back now' })).toBeVisible();
  await expect(banner.getByRole('button', { name: 'Confirm' })).toBeVisible();
  await expect(page.getByTestId('context-line')).toContainText('revision 43 waits for confirmation');
  await expect(page.getByText('No client yet')).toBeVisible();
  // nothing else can be applied meanwhile
  await page.getByTestId('access-matrix').locator('[data-cell="IoT>Internet"]').click();
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await page.getByRole('button', { name: 'Discard' }).click();

  // nobody confirms: the countdown ends, the gateway rolls back and says so
  await expect(banner).toContainText('0:00 left', { timeout: 10_000 });
  await gw.emit(page, gw.rollBackPending('confirmation timeout'));
  await expect(toast(page, 'Revision 43 rolled back')).toContainText('It was not confirmed in time. Rolled back to revision 42.');
  await expect(banner).toHaveCount(0);
  await expect(clientRows(page)).toHaveCount(0);
  await expect(page.getByText('No client yet')).toBeVisible();
  await expect(page.getByTestId('context-line')).toContainText('Revision 42');
});

test('commit-confirm: Confirm makes the client of the management hub active', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await open(page, `/networks/${ADMIN_HUB}`, gw);
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to admin' });
  await dialog.getByLabel('Name').fill('admin-laptop');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  const banner = page.getByTestId('countdown-banner');
  await banner.getByRole('button', { name: 'Confirm' }).click();
  await expect(toast(page, 'Revision 43 confirmed')).toBeVisible();
  await expect(banner).toHaveCount(0);
  await expect(clientRows(page)).toHaveCount(1);
  await expect(clientRows(page).nth(0)).toContainText('admin-laptop');
  await expect(clientRows(page).nth(0)).toContainText('10.98.0.2');
});

test('commit-confirm: Roll back now ends the waiting at once', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await open(page, `/networks/${ADMIN_HUB}`, gw);
  await page.getByRole('button', { name: 'Add client' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add client to admin' });
  await dialog.getByLabel('Name').fill('admin-laptop');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  const banner = page.getByTestId('countdown-banner');
  await expect(banner).toContainText('Revision 43 is applied and waits for confirmation');
  await banner.getByRole('button', { name: 'Roll back now' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions/43/rollback').length).toBe(1);
  await gw.emit(page, { type: 'revision_rolled_back', message: 'revision 43 rolled back', data: { revision: 43, reason: 'requested', restored: 42 } });
  await expect(toast(page, 'Revision 43 rolled back')).toContainText('Rolled back on request. Rolled back to revision 42.');
  await expect(banner).toHaveCount(0);
  await expect(clientRows(page)).toHaveCount(0);
});

test('the routing view: BIRD, the sessions with their announced and received prefixes, the routes by origin', async ({ page }) => {
  await open(page);
  await page.getByRole('link', { name: 'Routing' }).click();
  await expect(page.getByRole('heading', { name: 'Routing', exact: true })).toBeVisible();
  const bird = page.getByTestId('bird');
  await expect(bird).toContainText('Running');
  await expect(bird).toContainText('2.15.1');
  await expect(bird).toContainText(/Router id\s*10.10.0.1/);
  await expect(bird).toContainText(/ASN\s*65001/);
  const session = page.getByTestId('session');
  await expect(session).toHaveCount(1);
  await expect(session).toContainText('site-b-bgp');
  await expect(session).toContainText('bgp');
  await expect(session).toContainText('on link site-b');
  await expect(session).toContainText('up · Established');
  await expect(session).toContainText('since 4 min ago');
  await expect(session).toContainText('1 imported · 1 filtered · 1 exported');
  await expect(session.getByTestId('announced')).toHaveText('10.10.0.0/24');
  await expect(session.getByTestId('received')).toHaveText('10.70.0.0/24, 0.0.0.0/0');

  const routes = page.getByTestId('routes');
  await expect(routes.getByRole('row')).toHaveCount(5);
  await page.getByRole('group', { name: 'Routes by origin' }).getByRole('button', { name: /Learned/ }).click();
  await expect(routes.getByRole('row')).toHaveCount(2);
  await expect(routes.getByRole('row').nth(1)).toContainText('10.70.0.0/24');
  await expect(routes.getByRole('row').nth(1)).toContainText('10.255.0.1');
  await expect(routes.getByRole('row').nth(1)).toContainText('site-b-bgp');
});

test('the routing view without dynamic routing says so', async ({ page }) => {
  await open(page, '/networks/routing', MockGateway.sample(), 'Routing');
  await expect(page.getByTestId('bird')).toContainText('Not running: no dynamic routing configured');
  await expect(page.getByText('No routing session.')).toBeVisible();
});

test('the Linux view: the kernel state with its counters, by part and filtered', async ({ page }) => {
  await open(page);
  await page.getByRole('link', { name: 'Linux view' }).click();
  await expect(page.getByRole('heading', { name: 'Linux view' })).toBeVisible();
  await expect(page.getByText('revision 42 · read at')).toBeVisible();
  await expect(page.getByTestId('linux-nftables')).toContainText('counter packets 12 bytes 840 accept');
  await expect(page.getByTestId('linux-tc')).toContainText('br-iot: qdisc 1: parent root htb');
  await expect(page.getByTestId('linux-routes')).toContainText('route table 100 10.20.0.0/24 dev br-test');
  await expect(page.getByTestId('linux-wireguard')).toContainText('wireguard wg-lab-hub up public key');
  await page.getByRole('group', { name: 'Part of the kernel state' }).getByRole('button', { name: 'Routes' }).click();
  await expect(page.getByTestId('linux-nftables')).toHaveCount(0);
  await page.getByLabel('Show only lines that name').fill('br-test');
  await expect(page.getByTestId('linux-routes')).toContainText('bridge br-test up');
  await expect(page.getByTestId('linux-routes')).not.toContainText('br-iot');
});

test('empty and failed: no network yet, networks or leases the gateway did not deliver', async ({ page }) => {
  const empty = new MockGateway();
  await empty.install(page);
  await page.goto('/networks');
  await expect(page.getByText('No test network yet.')).toBeVisible();

  const failing = withNetworks(MockGateway.sample());
  failing.failing['/networks'] = 500;
  await page.unroute('**/api/v1/**');
  await failing.install(page);
  await page.goto('/networks');
  await expect(page.getByTestId('load-error')).toContainText('The networks are not loaded');
  await expect(page.getByText('No test network yet.')).toHaveCount(0);

  const leases = withNetworks(MockGateway.sample());
  leases.failing[`/networks/${IOT}/leases`] = 500;
  await page.unroute('**/api/v1/**');
  await leases.install(page);
  await page.goto(`/networks/${IOT}`);
  await expect(page.getByTestId('load-error')).toContainText('The leases are not loaded');
  await expect(page.getByText('No device has a lease yet.')).toHaveCount(0);
});

test('failed: an access matrix, an uplink or a routing configuration the gateway did not deliver says so instead of showing defaults', async ({ page }) => {
  // the active revision holds the matrix: without it no cell is shown, least of all a default
  const gw = withNetworks(MockGateway.sample());
  gw.failing['/revisions/active'] = 500;
  gw.failing['/uplink'] = 500;
  await open(page, '/networks', gw);
  const matrix = page.getByRole('region', { name: 'Access between networks' });
  await expect(matrix.getByTestId('load-error')).toContainText('The access matrix is not loaded');
  await expect(page.getByTestId('access-matrix')).toHaveCount(0);
  await expect(page.getByText(/Blocked\s*default/)).toHaveCount(0);
  await expect(page.getByTestId('management-card')).toContainText('Not loaded');
  await expect(page.getByTestId('uplink-error')).toContainText('Not loaded');
  await expect(page.getByTestId('uplink-card')).not.toContainText('no address');

  // Try again, once the gateway answers: the matrix with the configured entry, the uplink
  delete gw.failing['/revisions/active'];
  delete gw.failing['/uplink'];
  await matrix.getByTestId('load-error').getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByTestId('access-matrix').locator('[data-cell="IoT>lab-hub"]')).toHaveText('Allowed');
  await page.getByTestId('uplink-card').getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByTestId('uplink-card')).toContainText('192.168.1.57/24 · eth0');

  // the routing view: the status is there, the configuration (ASN, links) is not, and the BIRD card says so
  const routing = withNetworks(MockGateway.sample());
  routing.failing['/routing'] = 500;
  await page.unroute('**/api/v1/**');
  await open(page, '/networks/routing', routing, 'Routing');
  await expect(page.getByTestId('bird')).toContainText('Running');
  await expect(page.getByTestId('load-error')).toContainText('The routing configuration is not loaded');
  await expect(page.getByTestId('bird')).not.toContainText('ASN');
});
