import { expect, test, type Page } from '@playwright/test';
import { IOT, LTE_ID, MockGateway, RULE_DNS, sampleProbe, SCN_BOOT, SCN_MQTT, TEST, toast, withNetworks, withProbes, withRules, withScenarios } from './mock';

// M26a (plan §2.12, §2.17 "Probes"): a probe is added, edited and removed on the Diagnostics screen, through the configuration
// draft, the unapplied-changes bar and the preview-and-apply drawer, like a port forward — the dialog (name, network, trusts the
// test CA), the list with its address once the revision is applied, Remove together with the objects that name the probe, Undo,
// the gateway's messages under their fields, and the empty state that points to the dialog. Against the mocked API.

const PROBE = '3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8';

async function open(page: Page, gw = withProbes(withNetworks(MockGateway.sample()))) {
  await gw.install(page);
  await page.goto('/diagnostics');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 15_000 });
  return gw;
}

const drawer = (page: Page) => page.getByTestId('preview-drawer');
const probe = (page: Page, name: string) => page.locator(`[data-probe="${name}"]`);

/** The `probes` of the merge patch of the candidate the drawer previews (once it shows the dry run). */
async function lastPatch(page: Page, gw: MockGateway) {
  await expect(drawer(page).getByTestId('preview-changes')).toBeVisible();
  return (gw.requestsTo('POST', '/revisions').at(-1)!.body as { probes?: Record<string, unknown> }).probes!;
}

test('probes: add one through the dialog and the drawer — it is listed with its address after the apply', async ({ page }) => {
  const gw = await open(page);
  await expect(probe(page, 'probe-a')).toContainText('10.10.0.77');
  await page.getByTestId('probe-add').click();
  const dialog = page.getByRole('dialog', { name: 'Add probe' });
  await dialog.getByLabel('Name').fill('probe-b');
  await dialog.getByLabel('Network').selectOption({ label: 'Test (10.20.0.0/24)' });
  await dialog.getByLabel('Trusts the test CA').check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-b added');
  const [id, p] = Object.entries(await lastPatch(page, gw))[0]!;
  expect(p).toEqual({ name: 'probe-b', network: TEST, trusts_test_ca: true });
  // the draft shows it before the apply, marked, without an address
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(probe(page, 'probe-b').getByTestId('probe-mark')).toHaveText('NEW');
  await expect(probe(page, 'probe-b').getByTestId('probe-status')).toHaveText('not applied yet');
  await expect(probe(page, 'probe-b')).toContainText('trusts the test CA');
  await expect(page.getByTestId('unapplied-bar')).toContainText('1 change in 1 probe');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  // the candidate of the first preview was discarded by Back to editing: this is the next revision
  await page.getByRole('button', { name: 'Apply revision 44' }).click();
  // not lockout-relevant: applied and verified at once
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Confirm' })).toHaveCount(0);
  const row = probe(page, 'probe-b');
  await expect(row).toContainText('Test');
  await expect(row).toContainText('10.10.0.82');
  await expect(row.getByTestId('probe-status')).toHaveText('Online');
  await expect(row.getByTestId('probe-mark')).toHaveCount(0);
  expect(gw.config.probes?.[id]).toEqual({ name: 'probe-b', network: TEST, trusts_test_ca: true });
  // the new probe is a probe like the first one: a diagnostic can run from it
  await row.getByRole('button', { name: 'Diagnose from probe-b' }).click();
  await expect(page.getByTestId('diagnostic-from')).toHaveValue(id);
});

test('probes: remove one — the drawer lists the removal and the probe is gone after the apply', async ({ page }) => {
  const gw = await open(page);
  await probe(page, 'probe-a').getByRole('button', { name: 'Remove probe-a' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove probe-a' });
  await expect(dialog.getByTestId('remove-probe-preview')).toContainText('No rule, fault, group, port forward or scenario of the configuration names it.');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a removed');
  expect(await lastPatch(page, gw)).toEqual({ [PROBE]: null });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(probe(page, 'probe-a')).toHaveCount(0);
  // no probe is left: the empty state offers the dialog again
  await expect(page.getByTestId('probes-empty')).toBeVisible();
  expect(gw.config.probes?.[PROBE]).toBeUndefined();
});

test('probes: edit the network and the CA switch; Undo takes the change back', async ({ page }) => {
  const gw = await open(page);
  await probe(page, 'probe-a').getByRole('button', { name: 'Edit probe-a' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit probe-a' });
  await expect(dialog.getByLabel('Name')).toHaveValue('probe-a');
  await expect(dialog.getByLabel('Network')).toHaveValue(IOT);
  await dialog.getByLabel('Network').selectOption({ label: 'Test (10.20.0.0/24)' });
  await dialog.getByLabel('Trusts the test CA').check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a changed');
  expect(await lastPatch(page, gw)).toEqual({ [PROBE]: { network: TEST, trusts_test_ca: true } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  const row = probe(page, 'probe-a');
  await expect(row.getByTestId('probe-mark')).toHaveText('CHANGED');
  // the address is the applied probe's: the row says that the change is not applied yet
  await expect(row.getByTestId('probe-status')).toHaveText('Online · change not applied yet');
  await expect(page.getByTestId('unapplied-bar')).toContainText('2 changes in 1 probe');
  await row.getByRole('button', { name: 'Undo the changes of probe-a' }).click();
  await expect(row.getByTestId('probe-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toHaveCount(0);
});

test('probes: Remove, then Undo, leaves the draft clean', async ({ page }) => {
  await open(page);
  await probe(page, 'probe-a').getByRole('button', { name: 'Remove probe-a' }).click();
  await page.getByRole('dialog', { name: 'Remove probe-a' }).getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a removed');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  const row = probe(page, 'probe-a');
  await expect(row.getByTestId('probe-mark')).toHaveText('DELETED');
  await expect(row.getByRole('button', { name: 'Diagnose from probe-a' })).toHaveCount(0);
  await row.getByRole('button', { name: 'Undo the removal of probe-a' }).click();
  await expect(row.getByTestId('probe-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toHaveCount(0);
});

test('probes: a probe only the draft has is taken out of the draft with Undo', async ({ page }) => {
  await open(page);
  await page.getByTestId('probe-add').click();
  const dialog = page.getByRole('dialog', { name: 'Add probe' });
  await dialog.getByLabel('Name').fill('probe-b');
  await dialog.getByLabel('Network').selectOption({ label: 'IoT (10.10.0.0/24)' });
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await expect(probe(page, 'probe-b')).toBeVisible();
  // no Remove for what the gateway does not have; no diagnostic from a probe that is not built
  await expect(probe(page, 'probe-b').getByRole('button', { name: 'Remove probe-b' })).toHaveCount(0);
  await expect(probe(page, 'probe-b').getByRole('button', { name: 'Diagnose from probe-b' })).toHaveCount(0);
  await probe(page, 'probe-b').getByRole('button', { name: 'Take probe-b out of the draft' }).click();
  await expect(probe(page, 'probe-b')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toHaveCount(0);
});

test('probes: the form checks the name and the network under their fields before the gateway does', async ({ page }) => {
  await open(page);
  await page.getByTestId('probe-add').click();
  const dialog = page.getByRole('dialog', { name: 'Add probe' });
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(dialog.getByText('The name is required.')).toBeVisible();
  await expect(dialog.getByText('Choose the test network the probe is a port of.')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  // a name that a device has already: devices, WireGuard clients and probes share their names
  await dialog.getByLabel('Name').fill('esp32-42');
  await expect(dialog.getByText('esp32-42 is already the name of a device, a WireGuard client or a probe.')).toBeVisible();
  await dialog.getByLabel('Name').fill('PROBE-A');
  await expect(dialog.getByText('PROBE-A is already the name of a device, a WireGuard client or a probe.')).toBeVisible();
  await dialog.getByLabel('Name').fill('-bad');
  await expect(dialog.getByText('Letters, digits, dot, dash and underscore')).toBeVisible();
  await dialog.getByLabel('Name').fill('probe-b');
  await dialog.getByLabel('Network').selectOption({ label: 'IoT (10.10.0.0/24)' });
  await expect(dialog.getByRole('button', { name: 'Preview & apply' })).toBeEnabled();
  // the WireGuard networks are no choice: a probe is a port of a local test network's bridge
  await expect(dialog.getByRole('option', { name: /lab-hub/ })).toHaveCount(0);
});

test("probes: a probe the gateway refuses is shown at its field, and Fix opens it again with the gateway's message", async ({ page }) => {
  const gw = await open(page);
  gw.rejectRevision = (patch) => {
    const id = Object.keys((patch as { probes: Record<string, unknown> }).probes)[0]!;
    return [{ path: `/probes/${id}/name`, code: 'duplicate_name', message: 'the name "probe-b" is already used (names are unique per kind, ignoring case)' }];
  };
  await page.getByTestId('probe-add').click();
  const dialog = page.getByRole('dialog', { name: 'Add probe' });
  await dialog.getByLabel('Name').fill('probe-b');
  await dialog.getByLabel('Network').selectOption({ label: 'IoT (10.10.0.0/24)' });
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-invalid')).toContainText('Probe probe-b · name: the name "probe-b" is already used');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  const row = probe(page, 'probe-b');
  await expect(row.getByTestId('probe-refused')).toContainText('already used');
  await row.getByRole('button', { name: 'Fix probe-b' }).click();
  const fix = page.getByRole('dialog', { name: 'Edit probe-b' });
  await expect(fix.getByLabel('Name')).toHaveAttribute('aria-invalid', 'true');
  await expect(fix.getByText('the name "probe-b" is already used')).toBeVisible();
  await fix.getByLabel('Name').fill('probe-c');
  await fix.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(probe(page, 'probe-c').getByTestId('probe-mark')).toHaveCount(0);
});

test('probes: removing a probe that a rule names takes the rule along, and the preview lists both', async ({ page }) => {
  const gw = withRules(withProbes(withNetworks(MockGateway.sample())));
  gw.config.access_rules![RULE_DNS] = { name: 'probe-no-dns', source: { device: PROBE }, protocol: 'udp', ports: [53], action: 'drop' };
  await open(page, gw);
  await probe(page, 'probe-a').getByRole('button', { name: 'Remove probe-a' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove probe-a' });
  await expect(dialog.getByTestId('remove-probe-reference')).toContainText('rule probe-no-dns');
  await expect(dialog.getByTestId('remove-probe-along')).toContainText('only probe-a is deleted');
  await dialog.getByRole('button', { name: 'Remove the probe and these references' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a removed');
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('rule probe-no-dns removed');
  const patch = gw.requestsTo('POST', '/revisions').at(-1)!.body as { probes: Record<string, unknown>; access_rules: Record<string, unknown> };
  expect(patch.probes).toEqual({ [PROBE]: null });
  expect(patch.access_rules).toEqual({ [RULE_DNS]: null });
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(probe(page, 'probe-a')).toHaveCount(0);
  expect(gw.config.access_rules?.[RULE_DNS]).toBeUndefined();
});

test('probes: a scenario that targets the probe blocks its removal, with the reason, and nothing is sent', async ({ page }) => {
  const gw = withScenarios(withProbes(withNetworks(MockGateway.sample())));
  // one scenario targets the probe itself, the other has a step that does
  gw.config.scenarios![SCN_MQTT] = { ...gw.config.scenarios![SCN_MQTT]!, name: 'probe-outage', target: { device: PROBE } };
  gw.config.scenarios![SCN_BOOT] = { name: 'probe-steps', target: { network: IOT }, steps: [{ id: 'offline', at: '0s', target: { device: PROBE }, profile: LTE_ID }, { id: 'back', at: '20s', restore: true }] };
  await open(page, gw);
  await probe(page, 'probe-a').getByRole('button', { name: 'Remove probe-a' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove probe-a' });
  const refs = dialog.getByTestId('remove-probe-references');
  await expect(refs).toHaveAttribute('role', 'alert');
  await expect(dialog.getByTestId('remove-probe-reference')).toHaveText([
    'scenario probe-outage · blocks the removal: a scenario is a file of its own, change its target first',
    'scenario probe-steps · blocks the removal: a scenario is a file of its own, change its target first',
  ]);
  await expect(dialog.getByRole('link', { name: 'probe-outage' })).toHaveAttribute('href', `/scenarios/${SCN_MQTT}`);
  await expect(refs.getByTestId('remove-probe-blocked')).toContainText('The removal waits for these scenarios');
  await expect(dialog.getByTestId('remove-probe-preview')).toHaveCount(0);
  const confirm = dialog.getByRole('button', { name: 'Remove the probe and these references' });
  await expect(confirm).toBeDisabled();
  // a submit by the keyboard does nothing either: no revision is created, the dialog stays
  await dialog.getByTestId('remove-probe-form').evaluate((f) => (f as HTMLFormElement).requestSubmit());
  await expect(dialog).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')).toHaveLength(0);
  await expect(drawer(page)).toHaveCount(0);
});

test('probes: an active overlay that targets the probe is named in the removal, and the preview asks before it is removed', async ({ page }) => {
  const gw = withProbes(withNetworks(MockGateway.sample()));
  gw.overlays = [
    ...gw.overlays,
    {
      id: 'f0000000-0000-4000-8000-0000000000b1',
      kind: 'fault',
      owner: { type: 'token', id: 'ci' },
      target: { device: PROBE },
      fault: { latency: '100ms' },
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      generation: 40,
      counters: { packets: 0, bytes: 0, epoch: 1 },
      state: 'effective',
    },
  ];
  await open(page, gw);
  await probe(page, 'probe-a').getByRole('button', { name: 'Remove probe-a' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove probe-a' });
  await expect(dialog.getByTestId('remove-probe-overlays')).toContainText('An active overlay');
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a removed');
  await expect(drawer(page).getByTestId('preview-references')).toBeVisible();
});

test('probes: the empty state points to the dialog, and the first probe is added from there', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  gw.capabilities.features = [...gw.capabilities.features, 'probes'];
  await open(page, gw);
  const empty = page.getByTestId('probes-empty');
  await expect(empty).toContainText('virtual client');
  // the list has no Add probe of its own while it is empty: the empty state has it
  await expect(page.getByTestId('probe-add')).toHaveCount(1);
  await empty.getByRole('button', { name: 'Add probe' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add probe' });
  await dialog.getByLabel('Name').fill(sampleProbe().config.name);
  await dialog.getByLabel('Network').selectOption({ label: 'IoT (10.10.0.0/24)' });
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(drawer(page).getByTestId('preview-changes')).toContainText('probe probe-a added');
  await page.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(probe(page, 'probe-a')).toContainText('10.10.0.81');
  await expect(page.getByTestId('probes-empty')).toHaveCount(0);
});

test('probes: a gateway without the feature has no Add probe', async ({ page }) => {
  await open(page, withNetworks(MockGateway.sample()));
  await expect(page.getByTestId('probe-add')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Probes' })).toHaveCount(0);
});

test('probes: while a revision waits for confirmation the dialogs say so and wait (P6-AUDIT-43)', async ({ page }) => {
  const gw = withProbes(withNetworks(MockGateway.sample()));
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 60_000).toISOString() } };
  await open(page, gw);
  await expect(page.getByTestId('countdown-banner')).toBeVisible();
  // Add
  await page.getByTestId('probe-add').click();
  const add = page.getByRole('dialog', { name: 'Add probe' });
  await add.getByLabel('Name').fill('probe-b');
  await add.getByLabel('Network').selectOption({ label: 'Test (10.20.0.0/24)' });
  await expect(add.getByTestId('probe-pending-confirm')).toContainText('Another revision waits for confirmation; change the probe after it is resolved.');
  await expect(add.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await add.getByRole('button', { name: 'Cancel' }).click();
  // Edit
  await probe(page, 'probe-a').getByRole('button', { name: 'Edit probe-a' }).click();
  const edit = page.getByRole('dialog', { name: 'Edit probe-a' });
  await expect(edit.getByTestId('probe-pending-confirm')).toBeVisible();
  await expect(edit.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await edit.getByRole('button', { name: 'Cancel' }).click();
  // Measure a profile waits (its overlay would go with a rollback), the probe's faults now do not
  await probe(page, 'probe-a').getByRole('button', { name: 'Measure with probe-a' }).click();
  const measure = page.getByRole('dialog', { name: 'Measure with a probe' });
  await measure.getByLabel('Echo server').fill('203.0.113.10');
  await expect(measure.getByTestId('calibrate-run')).toBeEnabled(); // the faults now: no overlay
  await measure.getByRole('group', { name: 'Measure' }).getByRole('button', { name: 'A profile' }).click();
  await expect(measure.getByTestId('calibrate-pending-confirm')).toContainText('Another revision waits for confirmation');
  await expect(measure.getByTestId('calibrate-run')).toBeDisabled();
  await measure.getByRole('button', { name: 'Close', exact: true }).last().click();
  // resolved: the dialogs work again
  await page.getByTestId('countdown-banner').getByRole('button', { name: 'Confirm' }).click();
  await page.getByTestId('probe-add').click();
  await expect(page.getByRole('dialog', { name: 'Add probe' }).getByTestId('probe-pending-confirm')).toHaveCount(0);
  await expect(page.getByRole('dialog', { name: 'Add probe' }).getByRole('button', { name: 'Preview & apply' })).toBeEnabled();
});
