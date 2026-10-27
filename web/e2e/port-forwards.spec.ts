import { expect, test, type Page } from '@playwright/test';
import { FWD_ADMIN, FWD_RANGE, FWD_SENSOR, FWD_WEB, MockGateway, toast, withForwards, withNetworks } from './mock';

// M35a, the UI of port forwarding (plan §2.17 "Networks" and "Device detail"): the uplink card and the
// management card list their forwards with counter and status; add, edit, disable and delete go through
// the form, the configuration draft, the unapplied-changes bar, the preview-and-apply drawer and the
// concurrent-change dialog, never through commit-confirm; the gateway's messages stand at their fields; a
// range; the device detail lists the forwards to the device with a link to edit them; the preview says
// that faults do not apply to forwarded connections.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR9 = 'a1b2c3d4-0000-4000-8000-000000000059';
const DISCOVERED = 'd15c0000-0000-4000-8000-3af19c2207b4';

async function open(page: Page, path = '/networks', gw = withForwards(withNetworks(MockGateway.sample()))) {
  await gw.install(page);
  await page.goto(path);
  // the first render after a cold start can be slow under load
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 15_000 });
  return gw;
}

const uplinkCard = (page: Page) => page.getByTestId('uplink-card');
const managementCard = (page: Page) => page.getByTestId('management-card');
const forward = (page: Page, text: string) => page.getByTestId('forward').filter({ hasText: text });
const drawer = (page: Page) => page.getByTestId('preview-drawer');
const bar = (page: Page) => page.getByTestId('unapplied-bar');

/** The merge patch of the candidate the drawer previews (once it shows the dry run). */
async function lastPatch(page: Page, gw: MockGateway) {
  await expect(drawer(page).getByTestId('preview-changes')).toBeVisible();
  return (gw.requestsTo('POST', '/revisions').at(-1)!.body as { port_forwards?: Record<string, unknown> }).port_forwards;
}

test('port forwards: the uplink and the management card list their forwards with target, ports, counter and status', async ({ page }) => {
  await open(page);
  const up = uplinkCard(page).getByTestId('forward');
  await expect(up).toHaveCount(3);
  const web = forward(page, 'TCP 8080');
  await expect(web.getByTestId('forward-summary')).toHaveText('TCP 8080 → ESP32-42:80');
  await expect(web).toContainText('esp-web');
  await expect(web.getByTestId('forward-status')).toHaveText('active · to 10.10.0.42');
  await expect(web.getByTestId('forward-counter')).toHaveText('12 connections');
  await expect(web.getByTestId('forward-counter')).toHaveAttribute('title', /Connections that arrived through the forward/);
  await expect(forward(page, 'UDP 8000-8010').getByTestId('forward-summary')).toHaveText('UDP 8000-8010 → 10.20.0.50:9000-9010');
  // a device without an address leaves its forward inactive, and the status says why
  await expect(forward(page, 'TCP 2222').getByTestId('forward-status')).toHaveText('inactive: the device has no address');
  await expect(forward(page, 'TCP 2222').getByTestId('forward-counter')).toHaveCount(0);
  const admin = managementCard(page).getByTestId('forward');
  await expect(admin).toHaveCount(1);
  await expect(admin.getByTestId('forward-summary')).toHaveText('TCP 8443 → ESP32-42:443');
  await expect(admin.getByTestId('forward-status')).toHaveText('disabled');
  await expect(admin.getByRole('button', { name: 'Enable esp-admin' })).toBeVisible();
});

test('port forwards: add a forward to a device through the drawer — no commit-confirm, and it is listed after the apply', async ({ page }) => {
  const gw = await open(page);
  await uplinkCard(page).getByRole('button', { name: 'Add a port forward from the uplink' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  await dialog.getByLabel('External port').fill('9090');
  await dialog.getByLabel('Device', { exact: true }).selectOption({ label: 'ESP32-42 (10.10.0.42)' });
  await dialog.getByLabel('Target port').fill('90');
  await dialog.getByLabel('Name (optional)').fill('esp-ota');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('port forward esp-ota added');
  const [id, p] = Object.entries((await lastPatch(page, gw))!)[0]!;
  expect(p).toEqual({ name: 'esp-ota', protocol: 'tcp', entry: 'uplink', external_port: 9090, target: { device: ESP }, target_port: 90 });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  // not lockout-relevant: applied and verified at once, nothing waits for confirmation
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Confirm' })).toHaveCount(0);
  const ota = forward(page, 'TCP 9090');
  await expect(ota.getByTestId('forward-summary')).toHaveText('TCP 9090 → ESP32-42:90');
  await expect(ota.getByTestId('forward-status')).toHaveText('active · to 10.10.0.42');
  await expect(ota.getByTestId('forward-mark')).toHaveCount(0);
  expect(gw.config.port_forwards?.[id]).toBeDefined();
});

test('port forwards: a range to an address is shifted one to one; ranges of different length are refused under the field', async ({ page }) => {
  const gw = await open(page);
  await uplinkCard(page).getByRole('button', { name: 'Add a port forward from the uplink' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  await dialog.getByRole('button', { name: 'UDP' }).click();
  await dialog.getByLabel('External port').fill('7000-7010');
  await dialog.getByRole('button', { name: 'Address', exact: true }).click();
  await dialog.getByLabel('Address', { exact: true }).fill('10.20.0.60');
  await dialog.getByLabel('Target port').fill('9100-9105');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByText('The target range must be as long as the external one (11 ports).')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  // an overlap with another forward of the protocol stands under the external port
  await dialog.getByLabel('External port').fill('8005-8015');
  await expect(dialog.getByText('UDP 8000-8010 is forwarded already (UDP 8000-8010).')).toBeVisible();
  await dialog.getByLabel('External port').fill('7000-7010');
  await dialog.getByLabel('Target port').fill('9100');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect(Object.values((await lastPatch(page, gw))!)[0]).toEqual({
    protocol: 'udp',
    entry: 'uplink',
    external_port: 7000,
    external_port_end: 7010,
    target: { address: '10.20.0.60' },
    target_port: 9100,
  });
  await expect(drawer(page).getByTestId('preview-forwards')).toContainText('UDP 7000-7010 from the uplink → 10.20.0.60:9100-9110');
  await expect(drawer(page).getByTestId('preview-forward-mark')).toHaveText('NEW');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(forward(page, 'UDP 7000-7010').getByTestId('forward-summary')).toHaveText('UDP 7000-7010 → 10.20.0.60:9100-9110');
});

test('port forwards: edit, disable and delete through the draft and the drawer; Undo takes each back', async ({ page }) => {
  const gw = await open(page);
  const web = forward(page, 'esp-web');
  await web.getByRole('button', { name: 'Edit esp-web' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit esp-web' });
  await expect(dialog.getByLabel('External port')).toHaveValue('8080');
  await expect(dialog.getByLabel('Target port')).toHaveValue('80');
  await dialog.getByLabel('External port').fill('8081');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect(await lastPatch(page, gw)).toEqual({ [FWD_WEB]: { external_port: 8081 } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(web.getByTestId('forward-mark')).toHaveText('CHANGED');
  await expect(bar(page)).toContainText('1 change in 1 port forward');
  await web.getByRole('button', { name: 'Undo the changes of esp-web' }).click();
  await expect(bar(page)).toBeHidden();

  // disable one, delete another: both in one revision
  await web.getByRole('button', { name: 'Disable esp-web' }).click();
  await expect(web.getByTestId('forward-status')).toHaveText('disabled · not applied yet');
  await forward(page, 'sensor-ssh').getByRole('button', { name: 'Delete sensor-ssh' }).click();
  await expect(forward(page, 'sensor-ssh').getByTestId('forward-mark')).toHaveText('DELETED');
  await expect(bar(page)).toContainText('2 changes in 2 port forwards');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  expect(await lastPatch(page, gw)).toEqual({ [FWD_WEB]: { enabled: false }, [FWD_SENSOR]: null });
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('port forward esp-web changed');
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('port forward sensor-ssh removed');
  // the candidate of the first preview was discarded: this one is revision 44
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  await expect(forward(page, 'esp-web').getByTestId('forward-status')).toHaveText('disabled');
  await expect(forward(page, 'sensor-ssh')).toHaveCount(0);
  expect(gw.config.port_forwards?.[FWD_SENSOR]).toBeUndefined();
  expect(gw.config.port_forwards?.[FWD_WEB]?.enabled).toBe(false);

  // a deletion undone before the apply brings the forward back unchanged
  await forward(page, 'UDP 8000-8010').getByRole('button', { name: 'Delete UDP 8000-8010' }).click();
  await forward(page, 'UDP 8000-8010').getByRole('button', { name: 'Undo the deletion of UDP 8000-8010' }).click();
  await expect(bar(page)).toBeHidden();
  expect(gw.config.port_forwards?.[FWD_RANGE]).toBeDefined();
});

test("port forwards: a port the gateway refuses is shown at its field, and Fix opens the forward again with the gateway's message", async ({ page }) => {
  const gw = await open(page);
  gw.rejectRevision = (patch) => {
    const id = Object.keys((patch as { port_forwards: Record<string, unknown> }).port_forwards)[0]!;
    return [{ path: `/port_forwards/${id}/external_port`, code: 'reserved_port', message: '443/tcp is the UI/API port of the gateway' }];
  };
  await uplinkCard(page).getByRole('button', { name: 'Add a port forward from the uplink' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  await dialog.getByLabel('External port').fill('443');
  await dialog.getByLabel('Device', { exact: true }).selectOption({ label: 'ESP32-42 (10.10.0.42)' });
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-invalid')).toContainText('Port forward TCP 443 · external_port: 443/tcp is the UI/API port of the gateway');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  const refused = forward(page, 'TCP 443');
  await expect(refused.getByTestId('forward-mark')).toHaveText('NEW');
  await expect(refused.getByTestId('forward-refused')).toContainText('443/tcp is the UI/API port of the gateway');
  await refused.getByRole('button', { name: 'Fix TCP 443' }).click();
  const fix = page.getByRole('dialog', { name: 'Edit TCP 443' });
  await expect(fix.getByLabel('External port')).toHaveAttribute('aria-invalid', 'true');
  await expect(fix.getByText('443/tcp is the UI/API port of the gateway')).toBeVisible();
  // 8443/tcp is forwarded already (esp-admin, from the management network): the form says so before the gateway does
  await fix.getByLabel('External port').fill('8443');
  await expect(fix.getByText('TCP 8443 is forwarded already (esp-admin).')).toBeVisible();
  await fix.getByLabel('External port').fill('8444');
  await fix.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(forward(page, 'TCP 8444 → ESP32-42:8444').getByTestId('forward-mark')).toHaveCount(0);
});

test('port forwards: the preview says that faults do not apply to forwarded connections, and names the fault of the target', async ({ page }) => {
  await open(page);
  // ESP32-42 has a fault overlay (sample lab): it does not impair the connections through esp-web
  await forward(page, 'esp-web').getByRole('button', { name: 'Edit esp-web' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit esp-web' });
  await dialog.getByLabel('Target port').fill('8080');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  const forwards = drawer(page).getByTestId('preview-forwards');
  await expect(forwards).toContainText('esp-web');
  await expect(forwards).toContainText('TCP 8080 from the uplink → device ESP32-42:8080');
  // a changed forward is CHANGED in the preview too, as on the cards
  await expect(forwards.getByTestId('preview-forward-mark')).toHaveText('CHANGED');
  await expect(drawer(page).getByTestId('preview-forwards-note')).toContainText('Faults do not apply to forwarded connections');
  await expect(drawer(page).getByTestId('preview-warnings')).toContainText(
    'faults do not apply to forwarded connections (M35b): the impairment fault f0000000-0000-4000-8000-000000000001 of device ESP32-42 does not impair the connections through the forward esp-web',
  );
  // the disabled forward is not in effect after the change: it is not listed
  await expect(forwards).not.toContainText('esp-admin');
});

test('port forwards: the device detail lists the forwards to the device, and Edit opens the forward on the networks page', async ({ page }) => {
  await open(page, `/devices/${ESP}`);
  const list = page.getByTestId('device-forward');
  await expect(list).toHaveCount(2);
  await expect(list.filter({ hasText: 'TCP 8080' })).toContainText('TCP 8080 → ESP32-42:80');
  await expect(list.filter({ hasText: 'TCP 8080' })).toContainText('from the uplink');
  await expect(list.filter({ hasText: 'TCP 8080' })).toContainText('12 connections');
  await expect(list.filter({ hasText: 'TCP 8443' })).toContainText('from the management network');
  await expect(list.filter({ hasText: 'TCP 8443' })).toContainText('disabled');
  await page.getByRole('link', { name: 'Edit the port forward esp-web' }).click();
  await expect(page).toHaveURL(/\/networks$/);
  const dialog = page.getByRole('dialog', { name: 'Edit esp-web' });
  await expect(dialog.getByLabel('External port')).toHaveValue('8080');
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  // a device without an address: its forward is inactive and says why; Add starts a forward to the device
  await page.goto(`/devices/${SENSOR9}`);
  await expect(page.getByTestId('device-forward')).toContainText('inactive: the device has no address');
  await page.goto(`/devices/a1b2c3d4-0000-4000-8000-000000000015`);
  await expect(page.getByTestId('device-forwards-empty')).toHaveText('No port forward leads to this device.');
  await page.getByRole('link', { name: 'Add a port forward to Test-Phone' }).click();
  const add = page.getByRole('dialog', { name: 'Add port forward' });
  await expect(add.getByLabel('Device', { exact: true })).toHaveValue('a1b2c3d4-0000-4000-8000-000000000015');
});

test('port forwards: a forward changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change', async ({ page }) => {
  const gw = await open(page);
  await forward(page, 'esp-web').getByRole('button', { name: 'Edit esp-web' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit esp-web' });
  await dialog.getByLabel('External port').fill('8081');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Back to editing' }).click();
  // someone else renames it meanwhile
  gw.applyFromApi({ port_forwards: { [FWD_WEB]: { description: 'OTA updates' } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'port_forward', id: FWD_WEB, name: 'esp-web', path: `/port_forwards/${FWD_WEB}`, summary: 'port forward esp-web changed' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-theirs')).toContainText('port forward esp-web changed');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Port forward esp-web external ports: 8080 → 8081');
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  expect(await lastPatch(page, gw)).toEqual({ [FWD_WEB]: { external_port: 8081 } });
  await page.getByRole('button', { name: 'Apply revision 45' }).click();
  await expect(toast(page, 'Revision 45 applied and verified in 87 ms')).toBeVisible();
  expect(gw.config.port_forwards?.[FWD_WEB]).toMatchObject({ external_port: 8081, description: 'OTA updates' });
});

test('port forwards: in the two-port topology the management card offers no forward and the form only the uplink', async ({ page }) => {
  const gw = withForwards(withNetworks(MockGateway.sample()));
  gw.config = { ...gw.config, management: { interface: { name: 'eth0' } }, port_forwards: { [FWD_WEB]: gw.config.port_forwards![FWD_WEB]! } };
  await open(page, '/networks', gw);
  await expect(managementCard(page).getByRole('button', { name: 'Add a port forward from the management network' })).toHaveCount(0);
  await uplinkCard(page).getByRole('button', { name: 'Add a port forward from the uplink' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  await expect(dialog.getByRole('group', { name: 'Entry' }).getByRole('button')).toHaveText(['Uplink']);
  await expect(dialog.getByText('Two-port topology: the management network is the uplink.')).toBeVisible();
  await expect(dialog.getByRole('group', { name: 'Entry' })).toHaveAttribute('aria-describedby', 'forward-entry-hint');
});

test('port forwards: from the management network in the three-port topology', async ({ page }) => {
  const gw = await open(page);
  await managementCard(page).getByRole('button', { name: 'Add a port forward from the management network' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  await expect(dialog.getByRole('button', { name: 'Management network' })).toHaveAttribute('aria-pressed', 'true');
  // where the replies go (P31-M35a-05)
  await expect(dialog.getByText('Replies reach the management subnet and peers behind its default gateway only')).toBeVisible();
  await dialog.getByLabel('External port').fill('2201');
  await dialog.getByLabel('Device', { exact: true }).selectOption({ label: 'Sensor-07 (10.10.0.57)' });
  await dialog.getByLabel('Target port').fill('22');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  expect(Object.values((await lastPatch(page, gw))!)[0]).toMatchObject({ entry: 'management', external_port: 2201, target_port: 22 });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(managementCard(page).getByTestId('forward')).toHaveCount(2);
  await expect(forward(page, 'TCP 2201').getByTestId('forward-mark')).toHaveText('NEW');
});

test("port forwards: explain shows a forwarded connection's forward, the access layer of the forward and its note on faults", async ({ page }) => {
  // The card renders the forward of an answer (P31-M35a-08; the API test
  // TestExplainNamesTheForwardAndTheRuleThatDecidesAForwardedConnection covers the question, and flows.spec.ts the
  // source "an address behind the uplink" that asks it). The mock answers with the shape the gateway gives for one:
  // a peer behind the uplink, which is no device, to the uplink's address on a forwarded port; the test checks that
  // the card renders it.
  const gw = withForwards(withNetworks(MockGateway.sample()));
  gw.explanation = {
    generation: 42,
    source: { ip: '198.51.100.7' },
    destination: { ip: '192.168.1.57' },
    access: { verdict: 'allow', layer: 'port_forward', reason: 'no rule decided; the forward esp-web accepts it' },
    forward: {
      id: FWD_WEB,
      name: 'esp-web',
      entry: 'uplink',
      target_ip: '10.10.0.42',
      target_port: 80,
      device: { id: ESP, name: 'ESP32-42' },
      note: 'faults do not apply to forwarded connections (M35b)',
    },
    faults: [],
  };
  await open(page, '/faults', gw);
  const explain = page.getByRole('region', { name: 'Explain' });
  await explain.getByLabel('Device').selectOption({ label: 'ESP32-42' });
  await explain.getByLabel('Destination').fill('192.168.1.57');
  await explain.getByRole('button', { name: 'Explain' }).click();
  const result = page.getByTestId('explain-result');
  await expect(result).toContainText('by the port forward (no rule decided) — no rule decided; the forward esp-web accepts it');
  await expect(result.getByTestId('explain-forward')).toContainText('port forward esp-web from the uplink to ESP32-42:80');
  await expect(result.getByTestId('explain-forward')).toContainText('faults do not apply to forwarded connections (M35b)');
  await expect(result).toContainText('No fault applies');
});

test('port forwards: enabling a disabled forward and changing a target say that the change is not applied yet', async ({ page }) => {
  const gw = await open(page);
  const admin = forward(page, 'esp-admin');
  await admin.getByRole('button', { name: 'Enable esp-admin' }).click();
  await expect(admin.getByTestId('forward-mark')).toHaveText('CHANGED');
  await expect(admin.getByTestId('forward-status')).toHaveText('enabled · not applied yet');
  await expect(admin.getByRole('button', { name: 'Disable esp-admin' })).toBeVisible();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  expect(await lastPatch(page, gw)).toEqual({ [FWD_ADMIN]: { enabled: null } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await admin.getByRole('button', { name: 'Undo the changes of esp-admin' }).click();

  // a new target: the gateway's status is the old target's, so its address is not shown
  const web = forward(page, 'esp-web');
  await web.getByRole('button', { name: 'Edit esp-web' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit esp-web' });
  await dialog.getByRole('button', { name: 'Address', exact: true }).click();
  await dialog.getByLabel('Address', { exact: true }).fill('10.20.0.60');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(web.getByTestId('forward-summary')).toHaveText('TCP 8080 → 10.20.0.60:80');
  await expect(web.getByTestId('forward-status')).toHaveText('active · change not applied yet');
});

test('port forwards: the status not loaded says so with Try again on the cards and the device detail; the forwards stay listed', async ({ page }) => {
  const gw = withForwards(withNetworks(MockGateway.sample()));
  gw.failing['/port-forwards'] = 500;
  await open(page, '/networks', gw);
  const error = uplinkCard(page).getByTestId('load-error');
  await expect(error).toContainText('The status of the port forwards is not loaded: the gateway failed to read /port-forwards');
  // the configuration lists the forwards; their status is unknown
  await expect(forward(page, 'esp-web').getByTestId('forward-status')).toHaveText('status unknown');
  await expect(forward(page, 'esp-web').getByTestId('forward-counter')).toHaveCount(0);
  delete gw.failing['/port-forwards'];
  await error.getByRole('button', { name: 'Try again' }).click();
  await expect(uplinkCard(page).getByTestId('load-error')).toHaveCount(0);
  await expect(forward(page, 'esp-web').getByTestId('forward-status')).toHaveText('active · to 10.10.0.42');

  gw.failing['/port-forwards'] = 500;
  await page.goto(`/devices/${ESP}`);
  const deviceError = page.getByTestId('load-error').filter({ hasText: 'The status of the port forwards is not loaded' });
  await expect(deviceError).toBeVisible();
  await expect(page.getByTestId('device-forward').filter({ hasText: 'TCP 8080' })).toContainText('status unknown');
  delete gw.failing['/port-forwards'];
  await deviceError.getByRole('button', { name: 'Try again' }).click();
  await expect(deviceError).toHaveCount(0);
  await expect(page.getByTestId('device-forward').filter({ hasText: 'TCP 8080' })).toContainText('active · to 10.10.0.42');
});

test('port forwards: the configuration not loaded says so with Try again on the cards and the device detail, never "none"', async ({ page }) => {
  // the forwards come from the active revision: while it loads or after it failed, no card claims an answer
  const gw = withForwards(withNetworks(MockGateway.sample()));
  let release = () => {};
  gw.holding['/revisions/active'] = new Promise<void>((r) => (release = r));
  await open(page, `/devices/${ESP}`, gw);
  await expect(page.getByTestId('device-forwards-loading')).toHaveText('Loading the port forwards…');
  await expect(page.getByTestId('device-forwards-empty')).toHaveCount(0);
  release();
  await expect(page.getByTestId('device-forward')).toHaveCount(2);

  delete gw.holding['/revisions/active'];
  gw.failing['/revisions/active'] = 500;
  await page.goto(`/devices/${ESP}`);
  const deviceError = page.getByTestId('load-error').filter({ hasText: 'The port forwards are not loaded' });
  await expect(deviceError).toContainText('the gateway failed to read /revisions/active');
  await expect(page.getByTestId('device-forwards-empty')).toHaveCount(0);
  delete gw.failing['/revisions/active'];
  await deviceError.getByRole('button', { name: 'Try again' }).click();
  await expect(deviceError).toHaveCount(0);
  await expect(page.getByTestId('device-forward')).toHaveCount(2);

  gw.failing['/revisions/active'] = 500;
  await page.goto('/networks');
  for (const card of [uplinkCard(page), managementCard(page)]) {
    await expect(card.getByTestId('load-error')).toContainText('The port forwards are not loaded: the gateway failed to read /revisions/active');
  }
  await expect(page.getByTestId('forward')).toHaveCount(0);
  delete gw.failing['/revisions/active'];
  await uplinkCard(page).getByTestId('load-error').getByRole('button', { name: 'Try again' }).click();
  await expect(uplinkCard(page).getByTestId('forward')).toHaveCount(3);
  await expect(managementCard(page).getByTestId('forward')).toHaveCount(1);
  await expect(page.getByTestId('load-error')).toHaveCount(0);

  gw.holding['/revisions/active'] = new Promise<void>(() => {});
  await page.goto('/networks');
  await expect(page.getByTestId('uplink-forwards-loading')).toHaveText('Loading the port forwards…');
  await expect(page.getByTestId('management-forwards-loading')).toHaveText('Loading the port forwards…');
});

test('port forwards: a deleted forward is marked in the blocked tone on the cards and the device detail alike', async ({ page }) => {
  await open(page);
  await forward(page, 'esp-web').getByRole('button', { name: 'Delete esp-web' }).click();
  const cardMark = forward(page, 'esp-web').getByTestId('forward-mark');
  await expect(cardMark).toHaveText('DELETED');
  await expect(cardMark).toHaveClass(/text-block-text/);
  // within the app, so the draft stays
  await page.getByRole('link', { name: 'Devices' }).first().click();
  await page.getByRole('link', { name: 'ESP32-42' }).first().click();
  const mark = page.getByTestId('device-forward').filter({ hasText: 'TCP 8080' }).getByTestId('device-forward-mark');
  await expect(mark).toHaveText('DELETED');
  await expect(mark).toHaveClass(/border-block-border/);
  await expect(mark).toHaveClass(/text-block-text/);
  await expect(mark).not.toHaveClass(/text-accent-text/);
});

test('port forwards: without forwards the cards list none and offer Add', async ({ page }) => {
  const gw = withForwards(withNetworks(MockGateway.sample()));
  gw.config = { ...gw.config, port_forwards: {} };
  await open(page, '/networks', gw);
  await expect(uplinkCard(page).getByRole('button', { name: 'Add a port forward from the uplink' })).toBeEnabled();
  await expect(managementCard(page).getByRole('button', { name: 'Add a port forward from the management network' })).toBeEnabled();
  await expect(page.getByTestId('forwards')).toHaveCount(0);
  await expect(page.getByTestId('forward')).toHaveCount(0);
});

test('port forwards: a configured device without a network (identified by MAC) is offered a forward, and the gateway takes it', async ({ page }) => {
  const SENSOR7 = 'a1b2c3d4-0000-4000-8000-000000000057';
  const gw = withForwards(withNetworks(MockGateway.sample()));
  gw.devices = gw.devices.map((d) => {
    if (d.id !== SENSOR7) return d;
    const { network: _, ...rest } = d;
    return rest;
  });
  await open(page, `/devices/${SENSOR7}`, gw);
  await expect(page.getByTestId('device-forwards-blocked')).toHaveCount(0);
  await page.getByRole('link', { name: 'Add a port forward to Sensor-07' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  const select = dialog.getByLabel('Device', { exact: true });
  await expect(select).toHaveValue(SENSOR7);
  await expect(select.locator('option', { hasText: 'Sensor-07 (10.10.0.57)' })).toHaveCount(1);
  await expect(dialog.getByText("On the entry side's addresses.")).toBeVisible();
  await dialog.getByLabel('External port').fill('9292');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.locator('#forward-device-error')).toHaveCount(0);
  const [, p] = Object.entries((await lastPatch(page, gw))!)[0]!;
  expect(p).toEqual({ protocol: 'tcp', entry: 'uplink', external_port: 9292, target: { device: SENSOR7 } });
});

test('port forwards: a discovered device is offered no forward; it is adopted first, and the form refuses it as the gateway does', async ({ page }) => {
  const gw = await open(page, `/devices/${DISCOVERED}`);
  await expect(page.getByTestId('device-forwards-empty')).toBeVisible();
  await expect(page.getByRole('link', { name: /Add a port forward to/ })).toHaveCount(0);
  await expect(page.getByTestId('device-forwards-blocked')).toHaveText('Adopt the device to forward to it, or forward to its address on the networks page.');

  // a link made by hand: the form does not offer the device and says why under the field
  await page.goto(`/networks?forward=new&device=${DISCOVERED}`);
  const dialog = page.getByRole('dialog', { name: 'Add port forward' });
  const select = dialog.getByLabel('Device', { exact: true });
  await expect(select.locator('option', { hasText: 'dev-3a-f1-9c-22-07-b4' })).toHaveCount(0);
  await expect(select).toHaveAttribute('aria-describedby', 'forward-device-hint');
  await dialog.getByLabel('External port').fill('9191');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(select).toHaveAttribute('aria-invalid', 'true');
  await expect(select).toHaveAttribute('aria-describedby', 'forward-device-error');
  await expect(dialog.locator('#forward-device-error')).toHaveText(
    'A forward goes to a configured device in a local test network: adopt a discovered device first, or forward to its address.',
  );
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
});
