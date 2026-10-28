import { expect, test, type Page } from '@playwright/test';
import type { WireGuardNetwork } from '../src/api/generated/model';
import { BGP, CLIENT_RA, HUB, IOT, MockGateway, withNetworks, withProbes } from './mock';

// The Phase 3 audit: a revision applied while an edit dialog is open, on a clean draft. The form was
// built on the revision before; the edit is saved against that revision, so the next preview meets
// the newer one in the concurrent-change dialog, and "Reload and reapply mine" keeps the field the
// other revision changed. Without it the newer revision became the base of the old form and its
// change was sent back as removed, under If-Match of the newer revision (no conflict seen).

const IOT_SLOW = 'c0f00000-0000-4000-8000-000000000009';
const SLOW_LAB = 'c0000000-0000-4000-8000-0000000000c1';

const conflict = (page: Page) => page.getByRole('dialog', { name: 'The configuration was changed' });
const ci = { type: 'token' as const, id: 't1', name: 'ci-runner' };

/** Another revision is applied through the API while the dialog is open, and the UI has read it. */
async function appliedMeanwhile(page: Page, gw: MockGateway, patch: Record<string, unknown>) {
  const reads = gw.requestsTo('GET', '/revisions/active').length;
  const id = gw.applyFromApi(patch, ci, []);
  await gw.emit(page, { type: 'revision_applied', data: { revision: id }, message: `revision ${id} applied` });
  await expect.poll(() => gw.requestsTo('GET', '/revisions/active').length).toBeGreaterThan(reads);
  return id;
}

/** Preview → the conflict dialog → reload and reapply: the candidate's patch. */
async function reapplied(page: Page, gw: MockGateway) {
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(conflict(page)).toContainText('Revision 43 was applied by token ci-runner while you were editing (your change is based on revision 42)');
  const before = gw.requestsTo('POST', '/revisions').length;
  await conflict(page).getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(before + 1);
  return gw.requestsTo('POST', '/revisions').at(-1)!;
}

test('a configured fault edited while another revision changes it: their field stays', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.config = { ...gw.config, faults: { ...gw.config.faults, [IOT_SLOW]: { name: 'iot-slow', source: { network: IOT }, latency: '80ms' } } };
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-row').filter({ hasText: 'iot-slow' }).getByRole('button', { name: /^Edit / }).click();
  const d = page.getByRole('dialog', { name: 'Edit fault' });
  await expect(d.getByLabel('Name')).toHaveValue('iot-slow');
  await appliedMeanwhile(page, gw, { faults: { [IOT_SLOW]: { loss: '5%' } } });
  await d.getByLabel('Name').fill('iot-slower');
  await d.getByRole('button', { name: 'Add to the changes' }).click();
  const req = await reapplied(page, gw);
  expect(req.headers['if-match']).toBe('"43"');
  expect((req.body as { faults: unknown }).faults).toEqual({ [IOT_SLOW]: { name: 'iot-slower' } });
});

test('a custom profile edited while another revision changes it: their field stays', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.config = { ...gw.config, profiles: { [SLOW_LAB]: { name: 'slow-lab', parts: { impairment: { latency: '20ms' } } } } };
  await gw.install(page);
  await page.goto('/profiles');
  await page.getByRole('button', { name: 'Edit slow-lab' }).click();
  const d = page.getByRole('dialog', { name: 'Edit profile' });
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('20');
  await appliedMeanwhile(page, gw, { profiles: { [SLOW_LAB]: { description: 'set by CI' } } });
  await d.getByLabel('Latency (ms)').fill('30');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  const req = await reapplied(page, gw);
  expect((req.body as { profiles: unknown }).profiles).toEqual({ [SLOW_LAB]: { parts: { impairment: { latency: '30ms' } } } });
});

test('a network edited while another revision changes it: their field stays', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/networks/${IOT}`);
  await page.getByTestId('network-settings').getByRole('button', { name: 'Edit IoT' }).click();
  const d = page.getByRole('dialog', { name: 'Edit IoT' });
  await expect(d.getByTestId('network-ports')).toBeVisible();
  await appliedMeanwhile(page, gw, { networks: { [IOT]: { dhcp: { lease_time: '2h' } } } });
  await d.getByTestId('network-ports').getByRole('checkbox', { name: /^eth3 / }).check();
  await d.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(conflict(page)).toContainText('Revision 43 was applied by token ci-runner while you were editing (your change is based on revision 42)');
  await conflict(page).getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(2);
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { networks: unknown }).networks).toEqual({
    [IOT]: { interfaces: [{ name: 'eth1' }, { name: 'eth3', mac: '52:54:00:00:00:04' }] },
  });
});

test('a routing protocol and the routing settings edited while another revision changes them: their fields stay', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await page.goto('/networks/routing');
  const bgp = page.getByTestId('routing-config').getByTestId('protocol').filter({ hasText: 'site-b-bgp' });
  await bgp.getByRole('button', { name: 'Edit site-b-bgp' }).click();
  const d = page.getByRole('dialog', { name: 'Edit site-b-bgp' });
  await expect(d.getByLabel('Neighbor AS')).toHaveValue('65002');
  await appliedMeanwhile(page, gw, { routing: { protocols: { [BGP]: { bgp: { hold_time: '9s', keepalive_time: '3s' } } } } });
  await d.getByLabel('Neighbor AS').fill('65003');
  await d.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(conflict(page)).toBeVisible();
  await conflict(page).getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(2);
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { routing: unknown }).routing).toEqual({ protocols: { [BGP]: { bgp: { neighbor_asn: 65003 } } } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  await page.getByRole('button', { name: 'Discard' }).click();

  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const s = page.getByRole('dialog', { name: 'Routing settings' });
  await expect(s.getByLabel('Local AS')).toHaveValue('65001');
  await appliedMeanwhile(page, gw, { routing: { router_id: '10.10.0.253' } });
  await s.getByLabel('Local AS').fill('65010');
  await s.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(conflict(page)).toBeVisible();
  await conflict(page).getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(4);
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { routing: unknown }).routing).toEqual({ asn: 65010 });
});

test("a client's keys rotated while another revision changes it: their field stays", async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/networks/${HUB}`);
  const rA = page.getByTestId('client-row').filter({ hasText: 'rA' });
  await rA.getByRole('button', { name: 'Rotate the keys of rA' }).click();
  const d = page.getByRole('dialog', { name: 'Rotate the keys of rA' });
  await expect(d).toBeVisible();
  await appliedMeanwhile(page, gw, { networks: { [HUB]: { clients: { [CLIENT_RA]: { keepalive: '5s' } } } } });
  await d.getByRole('button', { name: 'Rotate keys' }).click();
  const req = await reapplied(page, gw);
  expect((req.body as { networks: unknown }).networks).toEqual({ [HUB]: { clients: { [CLIENT_RA]: { key: { generation: 2 } } } } });
  expect((gw.config.networks![HUB] as WireGuardNetwork).clients![CLIENT_RA]!.keepalive).toBe('5s');
});

test('a probe removed while another revision is applied: the first removal meets the concurrent-change dialog (P6-AUDIT-42)', async ({ page }) => {
  const gw = withProbes(withNetworks(MockGateway.sample()));
  await gw.install(page);
  await page.goto('/diagnostics');
  // the first Remove of the page mounts its dialog in the tick that opens it
  await page.getByRole('button', { name: 'Remove probe-a' }).click();
  const d = page.getByRole('dialog', { name: 'Remove probe-a' });
  await expect(d.getByTestId('remove-probe-preview')).toBeVisible();
  await appliedMeanwhile(page, gw, { networks: { [IOT]: { dhcp: { lease_time: '2h' } } } });
  await d.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(conflict(page)).toContainText('Revision 43 was applied by token ci-runner while you were editing (your change is based on revision 42)');
});
