import { expect, test, type Page } from '@playwright/test';
import type { Overlay, Scenario } from '../src/api/generated/model';
import { CreateOverlayBody } from '../src/api/generated/zod/overlays/overlays';
import { ESP32, IOT, MockGateway, SCN_MQTT, toast, withScenarios } from './mock';

// M23b (plan §2.17 "Add fault", "Device detail", "Networks", "Scenarios"): the forms of the application-layer kinds against the
// mocked API. A DNS fault and a TLS case are kinds of the Add fault dialog and steps of the step dialog, the DHCP actions are
// made in the DHCP card of the device detail and the network card and in the step dialog, and the test CA is downloaded from
// the TLS fields, the faults screen and the device detail. Every request the forms send is checked against the spec's schema.

const ESP = ESP32;
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const GROUP = 'c0a80001-0000-4000-8000-000000000001';

const faultDialog = (page: Page) => page.getByRole('dialog', { name: /^(Add|Edit) fault$/ });
const dhcpDialog = (page: Page) => page.getByRole('dialog', { name: /^(Edit )?DHCP action$/ });
const stepDialog = (page: Page, name: 'Add step' | 'Edit step') => page.getByRole('dialog', { name });
const kind = (page: Page, name: string) => faultDialog(page).getByRole('group', { name: 'Kind' }).getByRole('button', { name, exact: true });
const step = (page: Page, id: string) => page.locator(`[data-testid="timeline-step"][data-step="${id}"]`);

/** The overlay requests the page sent, each checked against the schema of the API. */
function sentOverlays(gw: MockGateway): unknown[] {
  const bodies = gw.requestsTo('POST', '/overlays').map((r) => r.body);
  for (const b of bodies) expect(CreateOverlayBody.safeParse(b).success, JSON.stringify(b)).toBe(true);
  return bodies;
}

function overlay(id: string, over: Partial<Overlay>): Overlay {
  return {
    id,
    kind: 'dns',
    owner: { type: 'user', id: 'admin', name: 'admin' },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    generation: 42,
    state: 'effective',
    ...over,
  };
}

// ---- DNS faults

test('a DNS fault: made in the Add fault dialog of a device, with its checks under the fields, then edited where it is listed', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await expect(d.getByRole('group', { name: 'Kind' }).getByRole('button')).toHaveText(['Impairment', 'MTU / PMTUD', 'DNS', 'TLS']);
  await kind(page, 'DNS').click();
  // a DNS fault has no traffic, no impairment fields and no direction; the target is the device
  await expect(d.getByLabel('Traffic', { exact: true })).toHaveCount(0);
  await expect(d.getByLabel('Latency (ms)')).toHaveCount(0);
  await expect(d.getByLabel('Target')).toBeDisabled();
  await expect(d.getByLabel('Action', { exact: true })).toHaveValue('nxdomain');
  await expect(d.getByTestId('dns-hint')).toContainText('without asking the upstream resolver');
  await expect(d.getByTestId('overlay-only-note')).toHaveText(/A DNS fault is an overlay/);
  // the field of an action appears with it
  await d.getByLabel('Action', { exact: true }).selectOption('delay');
  await expect(d.getByLabel('Delay', { exact: true })).toHaveValue('2s');
  await d.getByLabel('Delay', { exact: true }).fill('soon');
  await expect(d.getByLabel('Delay', { exact: true })).toHaveAccessibleDescription('A delay longer than zero, such as 500ms or 2s.');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Delay', { exact: true }).fill('2s');
  await d.getByLabel('Host names').fill('broker.example.com, not a name!');
  await expect(d.getByLabel('Host names')).toHaveAccessibleDescription('name! is no host name: a name like broker.example.com, or *.example.com.');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Host names').fill('broker.example.com, *.cdn.example.net');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await expect(d.getByTestId('fault-preview')).toHaveText(/ESP32-42 · DNS queries for broker.example.com, \*.cdn.example.net → answers delayed by 2s · expires after 5 min/);
  await d.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(d.getByTestId('api-call')).toContainText('"delay": "2s"');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, dns: { action: 'delay', names: ['broker.example.com', '*.cdn.example.net'], delay: '2s' }, ttl: '5m' });
  expect(gw.overlays.find((o) => o.kind === 'dns')?.state).toBe('effective');

  // listed with the device's faults, and edited there: the action changes, the names are the key, so the overlay stays one
  const row = page.getByTestId('active-fault').filter({ hasText: 'DNS delay 2s' });
  await expect(row).toContainText('broker.example.com, *.cdn.example.net');
  await row.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByRole('group', { name: 'Kind' })).toHaveCount(0);
  await expect(d.getByLabel('Action', { exact: true })).toHaveValue('delay');
  await expect(d.getByLabel('Host names')).toHaveValue('broker.example.com, *.cdn.example.net');
  await expect(d.getByLabel('Duration')).toHaveValue('keep');
  await d.getByLabel('Action', { exact: true }).selectOption('wrong_answer');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await expect(d.getByLabel('Answer with')).toHaveAccessibleDescription('Give the IPv4 address the name is answered with.');
  await d.getByLabel('Answer with').fill('10.9.0.1');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42').last()).toBeVisible();
  const second = sentOverlays(gw)[1] as { dns: unknown; ttl: string; target: unknown };
  expect(second.dns).toEqual({ action: 'wrong_answer', names: ['broker.example.com', '*.cdn.example.net'], answers: ['10.9.0.1'] });
  expect(second.ttl).toBe('5m');
  expect(gw.overlays.filter((o) => o.kind === 'dns')).toHaveLength(1);
  expect(gw.requests.filter((r) => r.method === 'DELETE')).toHaveLength(0);
  await expect(page.getByTestId('active-fault').filter({ hasText: 'DNS answer 10.9.0.1' })).toBeVisible();
});

test('a DNS fault for a network from the faults screen; it cannot be saved in the configuration', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  // saving in the configuration is for the kinds the configuration has
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /^Configuration/ }).click();
  await expect(d.getByRole('group', { name: 'Kind' }).getByRole('button')).toHaveText(['Impairment', 'MTU / PMTUD', 'Tunnel']);
  await expect(d.getByText('DNS faults and TLS cases are overlays: choose Overlay under Save as to make one.')).toBeVisible();
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /^Overlay/ }).click();
  await kind(page, 'DNS').click();
  await expect(d.getByRole('group', { name: 'Save as' })).toHaveCount(0);
  await d.getByLabel('Target').selectOption({ label: 'Network · IoT' });
  await d.getByLabel('Action', { exact: true }).selectOption('timeout');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Network IoT')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { network: IOT }, dns: { action: 'timeout' }, ttl: '15m' });
  const rows = page.getByTestId('fault-family-dns').getByTestId('fault-row');
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('Network IoT');
  await expect(rows).toContainText('DNS timeout');
  // edited from the faults screen with the same dialog
  await rows.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByLabel('Action', { exact: true })).toHaveValue('timeout');
  await expect(d.getByLabel('Target')).toBeDisabled();
});

test('the gateway\'s refusal of a DNS fault stands under the field it names', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.rejectOverlay = [{ path: '/dns/names/1', code: 'duplicate_name', message: '"broker.example.com" is listed twice' }];
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'DNS').click();
  await d.getByLabel('Host names').fill('broker.example.com');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(d.getByLabel('Host names')).toHaveAccessibleDescription('"broker.example.com" is listed twice');
  await expect(d.getByRole('alert')).toContainText('see the marked fields');
  // an edit takes the message away
  await d.getByLabel('Host names').fill('other.example.com');
  await expect(d.getByLabel('Host names')).not.toHaveAccessibleDescription('"broker.example.com" is listed twice');
});

// ---- the keyboard

test('keyboard: a DNS fault and a DHCP action are made without the mouse, and Escape gives the focus back to the control that opened the dialog', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);

  // the kind is chosen with the key, a text field's Enter applies the form
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'DNS').focus();
  await page.keyboard.press('Enter');
  await expect(d.getByLabel('Action', { exact: true })).toHaveValue('nxdomain');
  await d.getByLabel('Host names').fill('broker.example.com');
  await d.getByLabel('Host names').press('Enter');
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, dns: { action: 'nxdomain', names: ['broker.example.com'] }, ttl: '15m' });

  // a button of the DHCP card opens its dialog, which Enter applies and Escape leaves
  const card = page.getByTestId('dhcp-card');
  await card.getByTestId('dhcp-add-short_lease').focus();
  await page.keyboard.press('Enter');
  const h = dhcpDialog(page);
  await expect(h.getByLabel('Lease time')).toBeVisible();
  await h.getByLabel('Lease time').fill('9s');
  await h.getByLabel('Lease time').press('Enter');
  await expect(toast(page, 'DHCP action active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[1]).toEqual({ target: { device: ESP }, dhcp: { action: 'short_lease', lease_time: '9s' }, ttl: '15m' });
  const edit = card.getByTestId('dhcp-action').getByRole('button', { name: /^Edit / });
  await edit.focus();
  await page.keyboard.press('Enter');
  await expect(h).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(h).toBeHidden();
  await expect(edit).toBeFocused();
  expect(gw.requestsTo('POST', '/overlays')).toHaveLength(2);
});

// ---- TLS cases

test('a TLS case: its selection, the checks, the note about the test CA, the request, and the download of the CA', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await expect(d.getByLabel('Case', { exact: true })).toHaveValue('untrusted_ca');
  await expect(d.getByLabel('Case', { exact: true }).locator('option')).toHaveText(['Untrusted CA', 'Self-signed', 'Expired', 'Not yet valid', 'Hostname mismatch', 'Handshake reset', 'Handshake closed', 'Handshake stalled']);
  await expect(d.getByLabel('Case', { exact: true }).locator('optgroup')).toHaveCount(2);
  // a case that any firmware must reject needs nothing of the device
  await expect(d.getByTestId('tls-trust')).toHaveCount(0);
  await d.getByLabel('Case', { exact: true }).selectOption('expired');
  await expect(d.getByTestId('tls-hint')).toContainText('signed by the test CA');
  await expect(d.getByTestId('tls-trust')).toHaveText('ESP32-42 does not trust the test CA: it rejects this certificate as an unknown issuer, like every other case.');
  // the selection: the ports (443 and 8883 when there are none), a destination, server names, the cut
  await expect(d.getByLabel('Ports')).toHaveAttribute('placeholder', '443, 8883');
  await expect(d.getByLabel('Protocol')).toHaveCount(0);
  await d.getByLabel('Ports').fill('8883, 70000');
  await expect(d.getByLabel('Ports')).toHaveAccessibleDescription(/70000 is no port/);
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Ports').fill('8883');
  await d.getByLabel('Destination', { exact: true }).selectOption('cidr');
  await d.getByLabel('Address or prefix').fill('nope');
  await expect(d.getByLabel('Address or prefix')).toHaveAccessibleDescription('An IPv4 address or a prefix like 203.0.113.0/24.');
  await d.getByLabel('Address or prefix').fill('203.0.113.0/24');
  await d.getByLabel('Server names').fill('broker.example.com, bad!name');
  await expect(d.getByLabel('Server names')).toHaveAccessibleDescription(/bad!name is no server name/);
  await d.getByLabel('Server names').fill('broker.example.com');
  await d.getByLabel('Also cut existing connections').check();
  await d.getByLabel('Duration').selectOption({ label: '1 min' });
  await expect(d.getByTestId('fault-preview')).toHaveText(
    'Preview ESP32-42 · TLS connections on TCP 8883 to 203.0.113.0/24 for broker.example.com, existing ones cut → an expired certificate · expires after 1 min',
  );
  // the test CA: a plain link, the browser saves the gateway's file
  const download = page.waitForEvent('download');
  await d.getByRole('link', { name: 'Download the test CA' }).click();
  expect((await download).suggestedFilename()).toBe('chaos-gateway-test-ca.pem');
  expect(gw.requestsTo('GET', '/tls/ca')).toHaveLength(1);
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({
    target: { device: ESP },
    tls: { case: 'expired', ports: [8883], destination: { cidr: '203.0.113.0/24' }, sni: ['broker.example.com'], cut_existing: true },
    ttl: '1m',
  });
  await expect(page.getByTestId('active-fault').filter({ hasText: 'TLS expired' })).toContainText('8883 · 203.0.113.0/24 · SNI broker.example.com · cuts existing');
});

test('a TLS case: handshake cases, the default ports, a device that trusts the test CA, and the case edited on the faults screen', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.devices = gw.devices.map((x) => (x.id === ESP ? { ...x, config: { ...x.config!, trusts_test_ca: true } } : x));
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await d.getByLabel('Target').selectOption({ label: 'Device · ESP32-42' });
  await d.getByLabel('Case', { exact: true }).selectOption('hostname_mismatch');
  await expect(d.getByTestId('tls-trust')).toHaveText('ESP32-42 trusts the test CA: it must reject this certificate with the specific error.');
  // not one device: the note says what any device does
  await d.getByLabel('Target').selectOption({ label: 'Network · IoT' });
  await expect(d.getByTestId('tls-trust')).toContainText('Only a device that trusts the test CA');
  await d.getByLabel('Case', { exact: true }).selectOption('handshake_stall');
  await expect(d.getByTestId('tls-trust')).toHaveCount(0);
  await d.getByLabel('Duration').selectOption({ label: 'Until removed' });
  await expect(d.getByTestId('fault-preview')).toContainText('Network IoT · TLS connections on TCP 443, 8883 → a stalled handshake · until removed');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Network IoT')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { network: IOT }, tls: { case: 'handshake_stall' } });
  const row = page.getByTestId('fault-family-tls').getByTestId('fault-row');
  await expect(row).toContainText('TLS handshake stall');
  await row.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByLabel('Case', { exact: true })).toHaveValue('handshake_stall');
  await d.getByLabel('Case', { exact: true }).selectOption('self_signed');
  await d.getByLabel('Ports').fill('443');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Network IoT').last()).toBeVisible();
  // the key of a case is its selection: the new ports are a new overlay and the old one is removed
  expect(sentOverlays(gw)[1]).toEqual({ target: { network: IOT }, tls: { case: 'self_signed', ports: [443] } });
  expect(gw.overlays.filter((o) => o.kind === 'tls')).toHaveLength(1);
  expect(gw.requests.filter((r) => r.method === 'DELETE')).toHaveLength(1);
});

test('an interception case is opened with its settings kept as they are', async ({ page }) => {
  const gw = MockGateway.sample();
  const settings = { keylog: false, upstream: 'insecure' as const, http_rules: [{ action: 'status' as const, status: 503, match: { path_prefix: '/v1' } }] };
  gw.overlays.push(overlay('f0000000-0000-4000-8000-0000000000a1', { kind: 'tls', target: { device: ESP }, tls: { case: 'intercept', ports: [443], intercept: settings }, ttl: '1h', expires_at: new Date(Date.now() + 3_000_000).toISOString() }));
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-family-tls').getByRole('button', { name: /^Edit / }).click();
  const d = faultDialog(page);
  await expect(d.getByLabel('Case', { exact: true })).toHaveValue('intercept');
  await expect(d.getByTestId('tls-hint')).toContainText('Its settings and HTTP rules are not edited here');
  await d.getByLabel('Server names').fill('api.example.com');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  const body = sentOverlays(gw)[0] as { tls: { intercept: unknown; case: string; sni: string[] } };
  expect(body.tls.case).toBe('intercept');
  expect(body.tls.intercept).toEqual(settings);
  expect(body.tls.sni).toEqual(['api.example.com']);
});

test('the gateway\'s refusal of a TLS case stands under the field it names', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.rejectOverlay = [{ path: '/tls/destination/cidr', code: 'invalid_prefix', message: '203.0.113.7/24 has host bits set' }];
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await d.getByLabel('Destination', { exact: true }).selectOption('cidr');
  await d.getByLabel('Address or prefix').fill('203.0.113.7/24');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(d.getByLabel('Address or prefix')).toHaveAccessibleDescription('203.0.113.7/24 has host bits set');
});

test('the test CA is downloaded from the faults screen and from the device detail', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  let download = page.waitForEvent('download');
  await page.getByRole('link', { name: 'Test CA' }).click();
  expect((await download).suggestedFilename()).toBe('chaos-gateway-test-ca.pem');
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByText('not trusted')).toBeVisible();
  download = page.waitForEvent('download');
  await page.getByRole('link', { name: 'Download the test CA' }).click();
  expect((await download).suggestedFilename()).toBe('chaos-gateway-test-ca.pem');
  expect(gw.requestsTo('GET', '/tls/ca')).toHaveLength(2);
});

// ---- DHCP actions

test('the DHCP card of a device: a short lease, with its checks, the request, the list and the removal', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const card = page.getByTestId('dhcp-card');
  await expect(card.getByRole('group', { name: 'DHCP actions' }).getByRole('button')).toHaveText(['Short lease', 'Delete lease', 'Force a new IP', 'Set options', 'Silence']);
  await expect(card.getByTestId('dhcp-none')).toBeVisible();
  await card.getByTestId('dhcp-add-short_lease').click();
  const d = dhcpDialog(page);
  await expect(d.getByTestId('dhcp-target')).toHaveText(/For ESP32-42 · the device/);
  await expect(d.getByLabel('Action', { exact: true }).locator('option')).toHaveText(['Short lease', 'Delete lease', 'Force a new IP', 'Set options', 'Silence']);
  await d.getByLabel('Lease time').fill('0s');
  await expect(d.getByLabel('Lease time')).toHaveAccessibleDescription('A lease time of at least 1s, such as 30s or 7s.');
  await expect(d.getByRole('button', { name: 'Apply action' })).toBeDisabled();
  await d.getByLabel('Lease time').fill('7s');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await expect(d.getByTestId('dhcp-preview')).toContainText('ESP32-42 · DHCP → leases of 7s · expires after 5 min');
  await d.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(d.getByTestId('api-call')).toContainText('"lease_time": "7s"');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(toast(page, 'DHCP action active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, dhcp: { action: 'short_lease', lease_time: '7s' }, ttl: '5m' });
  const row = card.getByTestId('dhcp-action');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('DHCP short lease 7s');
  await expect(row.getByTestId('dhcp-action-state')).toHaveText('effective');
  // the device's own list shows it too
  await expect(page.getByTestId('active-fault').filter({ hasText: 'DHCP short lease 7s' })).toBeVisible();

  // edit: the action is the key and stays; the lease time changes
  await row.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByLabel('Action', { exact: true })).toBeDisabled();
  await expect(d.getByLabel('Lease time')).toHaveValue('7s');
  await expect(d.getByLabel('Duration')).toHaveValue('keep');
  await d.getByLabel('Lease time').fill('15s');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(card.getByTestId('dhcp-action')).toContainText('DHCP short lease 15s');
  expect(gw.overlays.filter((o) => o.kind === 'dhcp')).toHaveLength(1);

  await card.getByTestId('dhcp-action').getByRole('button', { name: /^Remove / }).click();
  await expect(toast(page, 'DHCP action removed')).toBeVisible();
  await expect(card.getByTestId('dhcp-none')).toBeVisible();
  expect(gw.requests.filter((r) => r.method === 'DELETE')).toHaveLength(1);
});

test('a forced new IP shows the address the gateway chose, and the address that is named', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const card = page.getByTestId('dhcp-card');
  await card.getByTestId('dhcp-add-force_new_ip').click();
  const d = dhcpDialog(page);
  await expect(d.getByLabel('Action', { exact: true })).toHaveValue('force_new_ip');
  await expect(d.getByLabel('New address')).toHaveAttribute('placeholder', 'chosen by the gateway');
  await expect(d.getByTestId('dhcp-hint')).toContainText('The renewing client is refused');
  await d.getByLabel('New address').fill('not an address');
  await expect(d.getByLabel('New address')).toHaveAccessibleDescription(/An IPv4 address in the network of the device/);
  await expect(d.getByRole('button', { name: 'Apply action' })).toBeDisabled();
  await d.getByLabel('New address').fill('');
  await d.getByRole('button', { name: 'Apply action' }).click();
  // the address is only in the overlay as it reads back
  await expect(toast(page, 'ESP32-42 is moved to 10.10.0.254')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, dhcp: { action: 'force_new_ip' }, ttl: '15m' });
  await expect(card.getByTestId('dhcp-new-ip')).toHaveText(/ESP32-42 is moved to 10.10.0.254 when its lease renews/);
  await expect(card.getByTestId('dhcp-action')).toContainText('DHCP new IP 10.10.0.254');

  // writing the action again chooses again, or takes the address that is named
  await card.getByTestId('dhcp-add-force_new_ip').click();
  await d.getByLabel('New address').fill('10.10.0.77');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(toast(page, 'ESP32-42 is moved to 10.10.0.77')).toBeVisible();
  expect(sentOverlays(gw)[1]).toEqual({ target: { device: ESP }, dhcp: { action: 'force_new_ip', ip: '10.10.0.77' }, ttl: '15m' });
  await expect(card.getByTestId('dhcp-new-ip')).toHaveText(/moved to 10.10.0.77/);
  expect(gw.overlays.filter((o) => o.kind === 'dhcp')).toHaveLength(1);
});

test('delete lease, set options and silence of a device; each is the request of the API', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const card = page.getByTestId('dhcp-card');

  await card.getByTestId('dhcp-add-delete_lease').click();
  const d = dhcpDialog(page);
  await expect(d.getByTestId('dhcp-hint')).toContainText('deleted once, when the action is applied');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(toast(page, 'DHCP action active on ESP32-42')).toBeVisible();
  await expect(card.getByTestId('dhcp-action').filter({ hasText: 'DHCP delete lease' })).toContainText('The leases were deleted when the action was applied.');

  await card.getByTestId('dhcp-add-set_options').click();
  // an action that sets nothing is none
  await expect(d.getByRole('button', { name: 'Apply action' })).toBeDisabled();
  await expect(d.getByText('Set at least one option')).toBeVisible();
  await d.getByLabel('Router (option 3)').fill('gateway');
  await expect(d.getByLabel('Router (option 3)')).toHaveAccessibleDescription('An IPv4 address; empty keeps the configured router.');
  await d.getByLabel('Router (option 3)').fill('10.10.0.9');
  await d.getByLabel('DNS servers (option 6)').fill('192.0.2.53, 192.0.2.54');
  await d.getByLabel('Custom options (code and value per line)').fill('15 lab.example');
  await expect(d.getByLabel('Custom options (code and value per line)')).toHaveAccessibleDescription(/Option 15 is set by the gateway/);
  await d.getByLabel('Custom options (code and value per line)').fill('66 tftp.lab');
  await d.getByLabel('Take the domain away').check();
  await expect(d.getByLabel('Domain (option 15)')).toBeDisabled();
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(toast(page, 'DHCP action active on ESP32-42').last()).toBeVisible();
  expect(sentOverlays(gw)[1]).toEqual({
    target: { device: ESP },
    dhcp: { action: 'set_options', options: { router: '10.10.0.9', dns_servers: ['192.0.2.53', '192.0.2.54'], domain: '', custom: [{ code: 66, value: 'tftp.lab' }] } },
    ttl: '15m',
  });
  await expect(card.getByTestId('dhcp-action').filter({ hasText: 'DHCP options' })).toContainText('options: router 10.10.0.9, DNS 192.0.2.53, 192.0.2.54, no domain, option 66');

  await card.getByTestId('dhcp-add-silence').click();
  await expect(d.getByTestId('dhcp-hint')).toContainText('does not answer');
  await d.getByLabel('Duration').selectOption({ label: 'Until removed' });
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(card.getByTestId('dhcp-action')).toHaveCount(3);
  expect(sentOverlays(gw)[2]).toEqual({ target: { device: ESP }, dhcp: { action: 'silence' } });
});

test('the gateway\'s refusal of a DHCP action stands under the field it names', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.rejectOverlay = [{ path: '/dhcp/ip', code: 'invalid_dhcp_action', message: '10.99.0.5 is not in the network of ESP32-42' }];
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByTestId('dhcp-add-force_new_ip').click();
  const d = dhcpDialog(page);
  await d.getByLabel('New address').fill('10.99.0.5');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(d.getByLabel('New address')).toHaveAccessibleDescription('10.99.0.5 is not in the network of ESP32-42');
  gw.rejectOverlay = [{ path: '/target', code: 'target_kind', message: 'a DHCP action targets a device or a network' }];
  await d.getByLabel('New address').fill('');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(d.getByRole('alert')).toContainText('a DHCP action targets a device or a network');
});

test('a device without a MAC cannot be moved, and a network without DHCP has nothing for an action to change', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.devices = gw.devices.map((x) => (x.id === SENSOR ? { ...x, observed: { ...x.observed, macs: [] } } : x));
  await gw.install(page);
  await page.goto(`/devices/${SENSOR}`);
  const card = page.getByTestId('dhcp-card');
  await expect(card.getByTestId('dhcp-add-force_new_ip')).toBeDisabled();
  await expect(card.getByTestId('dhcp-add-short_lease')).toBeEnabled();
  await expect(card.getByTestId('dhcp-unavailable')).toHaveText('The device has no MAC address: a reservation cannot be made for it.');

  const off = MockGateway.sample();
  off.networks = off.networks.map((n) => (n.id === IOT ? { ...n, config: { ...n.config, dhcp: { enabled: false } } } : n));
  await off.install(page);
  await page.goto(`/devices/${ESP}`);
  for (const a of ['short_lease', 'delete_lease', 'force_new_ip', 'set_options', 'silence']) await expect(page.getByTestId(`dhcp-add-${a}`)).toBeDisabled();
  await expect(page.getByTestId('dhcp-unavailable')).toHaveText('DHCP is off in this network: an action has nothing to change.');
  await page.goto('/networks');
  for (const a of ['short_lease', 'delete_lease', 'set_options', 'silence']) await expect(page.getByTestId(`dhcp-add-${a}`)).toBeDisabled();
});

test('a gateway that has no DHCP actions has no card for them', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.capabilities.overlay_kinds = gw.capabilities.overlay_kinds.filter((k) => k !== 'dhcp');
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByRole('heading', { name: 'Port forwards' })).toBeVisible();
  await expect(page.getByTestId('dhcp-card')).toHaveCount(0);
  await page.goto('/networks');
  await expect(page.getByRole('heading', { name: 'DHCP leases' })).toBeVisible();
  await expect(page.getByTestId('dhcp-card')).toHaveCount(0);
});

test('the network card: the actions of a network, without a forced new IP, listed and removed there', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/networks');
  const card = page.getByTestId('dhcp-card');
  await expect(card.getByRole('group', { name: 'DHCP actions' }).getByRole('button')).toHaveText(['Short lease', 'Delete lease', 'Set options', 'Silence']);
  await card.getByTestId('dhcp-add-silence').click();
  const d = dhcpDialog(page);
  await expect(d.getByTestId('dhcp-target')).toHaveText(/For Network IoT · every client of the network/);
  await expect(d.getByLabel('Action', { exact: true }).locator('option')).toHaveText(['Short lease', 'Delete lease', 'Set options', 'Silence']);
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(toast(page, 'DHCP action active on Network IoT')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { network: IOT }, dhcp: { action: 'silence' }, ttl: '15m' });
  const row = card.getByTestId('dhcp-action');
  await expect(row).toContainText('DHCP silence');
  // an action of a device is not in the card of its network
  await page.goto(`/devices/${ESP}`);
  await page.getByTestId('dhcp-add-short_lease').click();
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(page.getByTestId('dhcp-card').getByTestId('dhcp-action')).toHaveCount(1);
  await page.goto('/networks');
  await expect(page.getByTestId('dhcp-card').getByTestId('dhcp-action')).toHaveCount(1);
  await expect(page.getByTestId('dhcp-card').getByTestId('dhcp-action')).toContainText('DHCP silence');
  await page.getByTestId('dhcp-card').getByTestId('dhcp-action').getByRole('button', { name: /^Remove / }).click();
  await expect(page.getByTestId('dhcp-none')).toBeVisible();
});

test('an action that another overrides says so, and the faults screen edits a DHCP action in the DHCP dialog', async ({ page }) => {
  const gw = MockGateway.sample();
  const at = new Date().toISOString();
  gw.overlays.push(
    overlay('f0000000-0000-4000-8000-0000000000b1', { kind: 'dhcp', target: { device: ESP }, dhcp: { action: 'short_lease', lease_time: '7s' }, state: 'overridden', created_at: at }),
    overlay('f0000000-0000-4000-8000-0000000000b2', { kind: 'dhcp', target: { device: ESP }, dhcp: { action: 'silence' }, created_at: at }),
  );
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const lease = page.getByTestId('dhcp-action').filter({ hasText: 'short lease' });
  await expect(lease.getByTestId('dhcp-action-state')).toHaveText('overridden');
  await expect(lease).toContainText('Another action of this device wins.');
  await page.goto('/faults');
  await page.getByTestId('fault-family-dhcp').getByTestId('fault-row').filter({ hasText: 'DHCP short lease 7s' }).getByRole('button', { name: /^Edit / }).click();
  const d = dhcpDialog(page);
  await expect(d).toContainText('Edit DHCP action');
  await expect(d.getByLabel('Lease time')).toHaveValue('7s');
  await d.getByLabel('Lease time').fill('9s');
  await d.getByRole('button', { name: 'Apply action' }).click();
  await expect(page.getByTestId('fault-family-dhcp')).toContainText('DHCP short lease 9s');
});

// ---- the step dialog

test('the steps dns, tls and dhcp: made with the same fields, shown on the timeline and sent in the scenario', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);

  // a DNS step
  await page.getByTestId('add-step').click();
  const a = stepDialog(page, 'Add step');
  await a.getByLabel('Type', { exact: true }).selectOption('dns');
  await expect(a.getByLabel('Acts on')).toContainText("The scenario's target (ESP32-42)");
  await a.getByLabel('Id', { exact: true }).fill('no-dns');
  await a.getByLabel('At', { exact: true }).fill('5s');
  await a.getByLabel('Action', { exact: true }).selectOption('wrong_answer');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a.getByLabel('Answer with')).toHaveAccessibleDescription('Give the IPv4 address the name is answered with.');
  await a.getByLabel('Answer with').fill('10.9.0.1');
  await a.getByLabel('Host names').fill('broker.example.com');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a).toBeHidden();
  await expect(step(page, 'no-dns').getByTestId('step-type')).toHaveText('DNS fault');
  await expect(step(page, 'no-dns')).toContainText('DNS answer 10.9.0.1 · broker.example.com');

  // a TLS step with the case, the ports and the cut
  await page.getByTestId('add-step').click();
  const t = stepDialog(page, 'Add step');
  await t.getByLabel('Type', { exact: true }).selectOption('tls');
  await t.getByLabel('Id', { exact: true }).fill('bad-cert');
  await t.getByLabel('At', { exact: true }).fill('6s');
  await t.getByLabel('Case', { exact: true }).selectOption('not_yet_valid');
  await expect(t.getByTestId('tls-trust')).toContainText('ESP32-42 does not trust the test CA');
  await t.getByLabel('Ports').fill('8883, x');
  await t.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(t.getByLabel('Ports')).toHaveAccessibleDescription(/x is no port/);
  await t.getByLabel('Ports').fill('8883');
  await t.getByLabel('Also cut existing connections').check();
  await t.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'bad-cert').getByTestId('step-type')).toHaveText('TLS case');
  await expect(step(page, 'bad-cert')).toContainText('TLS not yet valid · 8883 · cuts existing');

  // a DHCP step: the scenario's target is the device, so a new IP is a choice
  await page.getByTestId('add-step').click();
  const h = stepDialog(page, 'Add step');
  await h.getByLabel('Type', { exact: true }).selectOption('dhcp');
  await h.getByLabel('Id', { exact: true }).fill('new-ip');
  await h.getByLabel('At', { exact: true }).fill('7s');
  await expect(h.getByLabel('Action', { exact: true }).locator('option')).toHaveText(['Short lease', 'Delete lease', 'Force a new IP', 'Set options', 'Silence']);
  await h.getByLabel('Action', { exact: true }).selectOption('short_lease');
  await h.getByLabel('Lease time').fill('0s');
  await h.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(h.getByLabel('Lease time')).toHaveAccessibleDescription('A lease time of at least 1s, such as 30s or 7s.');
  await h.getByLabel('Lease time').fill('7s');
  await h.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'new-ip').getByTestId('step-type')).toHaveText('DHCP action');
  await expect(step(page, 'new-ip')).toContainText('DHCP short lease 7s');

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  const byId = (id: string) => sent.steps!.find((s) => s.id === id);
  expect(byId('no-dns')).toEqual({ id: 'no-dns', at: '5s', dns: { action: 'wrong_answer', names: ['broker.example.com'], answers: ['10.9.0.1'] } });
  expect(byId('bad-cert')).toEqual({ id: 'bad-cert', at: '6s', tls: { case: 'not_yet_valid', ports: [8883], cut_existing: true } });
  expect(byId('new-ip')).toEqual({ id: 'new-ip', at: '7s', dhcp: { action: 'short_lease', lease_time: '7s' } });
});

test('a DHCP step of a scenario that targets a group or a network names a device or a network', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  gw.groups = [{ id: GROUP, config: { name: 'sensors', members: [ESP, SENSOR] } }];
  gw.config = {
    ...gw.config,
    groups: { [GROUP]: { name: 'sensors', members: [ESP, SENSOR] } },
    scenarios: { ...gw.config.scenarios, 'c5000000-0000-4000-8000-000000000001': { name: 'sensors-lease', target: { group: GROUP }, steps: [{ id: 'quiet', at: '0s', restore: true }] } },
  };
  await gw.install(page);
  await page.goto('/scenarios/c5000000-0000-4000-8000-000000000001');
  await page.getByTestId('add-step').click();
  const h = stepDialog(page, 'Add step');
  await h.getByLabel('Type', { exact: true }).selectOption('dhcp');
  await h.getByLabel('Id', { exact: true }).fill('silent');
  await h.getByLabel('At', { exact: true }).fill('1s');
  await h.getByLabel('Action', { exact: true }).selectOption('silence');
  // a group has no DHCP of its own: the step has to act on one device
  await expect(h.getByLabel('Acts on')).toHaveAccessibleDescription(/targets a device or a network, not a group/);
  await h.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(h.getByRole('button', { name: 'Add to the changes' })).toBeDisabled();
  await h.getByLabel('Acts on').selectOption({ label: 'Device · ESP32-42' });
  await expect(h.getByLabel('Action', { exact: true }).locator('option')).toHaveText(['Short lease', 'Delete lease', 'Force a new IP', 'Set options', 'Silence']);
  await expect(h.getByRole('button', { name: 'Add to the changes' })).toBeEnabled();
  await h.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'silent')).toContainText('DHCP silence');
});

test('a DHCP step on a network offers no forced new IP, and the step is edited with its fields', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  await gw.install(page);
  // boot-loop acts on the IoT network
  await page.goto('/scenarios');
  await page.getByTestId('scenario-item').filter({ hasText: 'boot-loop' }).click();
  await page.getByTestId('add-step').click();
  const h = stepDialog(page, 'Add step');
  await h.getByLabel('Type', { exact: true }).selectOption('dhcp');
  await expect(h.getByLabel('Action', { exact: true }).locator('option')).toHaveText(['Short lease', 'Delete lease', 'Set options', 'Silence']);
  await h.getByLabel('Id', { exact: true }).fill('opts');
  await h.getByLabel('At', { exact: true }).fill('3s');
  await h.getByLabel('Action', { exact: true }).selectOption('set_options');
  await h.getByLabel('NTP servers (option 42)').fill('192.0.2.123');
  await h.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'opts')).toContainText('DHCP options · NTP');
  // the step is edited with its fields
  await page.getByRole('button', { name: 'Edit step opts' }).click();
  const e = stepDialog(page, 'Edit step');
  await expect(e.getByLabel('NTP servers (option 42)')).toHaveValue('192.0.2.123');
  await expect(e.getByLabel('Type', { exact: true })).toHaveValue('dhcp');
});
