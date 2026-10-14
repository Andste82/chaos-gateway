import { expect, test, type Page } from '@playwright/test';
import { CLIENT_RA, IOT, MockGateway, RULE_DNS, RULE_DOT, RULE_WEB, toast, withNetworks, withRules } from './mock';

// M13: the access-rules screen (ordered list, rule editor IF/THEN, counters), the unapplied-changes
// bar, the preview-and-apply drawer, validation, a failed apply and the concurrent-change dialog.

async function open(page: Page, gw = withRules(MockGateway.sample())) {
  await gw.install(page);
  await page.goto('/rules');
  await expect(page.getByRole('heading', { name: 'Access rules' })).toBeVisible();
  return gw;
}

const rows = (page: Page) => page.getByTestId('rule-row');
const bar = (page: Page) => page.getByTestId('unapplied-bar');

test('the ordered list: the system rule locked at the top, overlay rules before the configured rules, hits and switches', async ({ page }) => {
  await open(page);
  await expect(rows(page)).toHaveCount(5);
  await expect(rows(page).nth(0)).toContainText('Control plane access');
  await expect(rows(page).nth(0)).toContainText('SYSTEM');
  await expect(rows(page).nth(0)).toContainText('4 812');
  await expect(rows(page).nth(0).getByRole('switch')).toBeDisabled();
  await expect(rows(page).nth(1)).toContainText('OVERLAY');
  await expect(rows(page).nth(1)).toContainText('Network IoT → any · TCP 8883');
  await expect(rows(page).nth(1)).toContainText('TCP reset');
  await expect(rows(page).nth(1)).toContainText(/\d:\d\d left/);
  // the configured rules in their order, with position, selector, access chip and hits
  await expect(rows(page).nth(2)).toContainText('1no-dot');
  await expect(rows(page).nth(2)).toContainText('Network IoT → any · TCP 853');
  await expect(rows(page).nth(2)).toContainText('Reject');
  await expect(rows(page).nth(2)).toContainText('cut existing');
  await expect(rows(page).nth(2)).toContainText('18 342');
  await expect(rows(page).nth(3)).toContainText('2no-dns');
  await expect(rows(page).nth(4)).toContainText('Network Test → uplink · TCP 80, 443');
  await expect(rows(page).nth(4)).toContainText('Allow');
  await expect(rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' })).toHaveAttribute('aria-checked', 'true');

  // the first configured rule is open in the editor, with its matches
  const matches = page.getByTestId('rule-matches');
  await expect(matches).toContainText('18 342 hits · 12.4 MB · last');
  await expect(matches).toContainText('Rejected for new connections with ICMP port unreachable');
  await expect(matches).toContainText('existing connections are cut once');
  await expect(matches).toContainText('1 overlay rule is checked first.');

  // the system rule explains itself; the overlay rule says where it comes from
  await rows(page).nth(0).getByRole('button').first().click();
  await expect(page.getByRole('heading', { name: 'Control plane access' })).toBeVisible();
  await expect(page.getByText('so no other rule can lock you out')).toBeVisible();
  await expect(page.getByTestId('rule-matches')).toContainText('4 812 hits · 38.0 MB · last just now');
  await rows(page).nth(1).getByRole('button').first().click();
  await expect(page.getByText('Set by the token ci-runner for Network IoT')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Remove overlay rule' })).toBeVisible();
});

test('when the selected overlay rule is gone, the first rule is selected', async ({ page }) => {
  const gw = await open(page);
  // the overlay rules arrive with their own request: wait for the row before selecting it
  await expect(rows(page).nth(1)).toContainText('OVERLAY');
  await rows(page).nth(1).getByRole('button').first().click();
  await expect(page.getByText('Set by the token ci-runner for Network IoT')).toBeVisible();
  // it expires
  const gone = gw.overlays.find((o) => o.kind === 'rule')!;
  gw.overlays = gw.overlays.filter((o) => o.id !== gone.id);
  await gw.emit(page, { type: 'overlay_expired', subject: { kind: 'overlay', id: gone.id, name: 'rule' } });
  await expect(rows(page)).toHaveCount(4);
  await expect(page.getByTestId('rule-matches')).toContainText('18 342 hits');
  await expect(page.getByText('Select a rule to see it here.')).toHaveCount(0);
});

test('editing a rule marks it CHANGED and the bar counts the changes; Discard takes them back', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  const editor = page.getByTestId('rule-editor');
  await expect(page.getByTestId('rule-matches')).toContainText('311 hits · 24.0 KB · last 3 min ago');
  await editor.getByRole('group', { name: 'Access' }).getByRole('button', { name: 'Reject' }).click();
  await editor.getByLabel('Ports').fill('53, 5353');
  await editor.getByLabel('Ports').blur();
  await expect(rows(page).nth(3)).toContainText('CHANGED');
  await expect(rows(page).nth(3)).toContainText('UDP 53, 5353');
  await expect(bar(page)).toHaveText(/2 changes in 1 rule · not applied yet/);
  // the switch of another rule: one more change in one more rule
  await rows(page).nth(4).getByRole('switch', { name: 'Enable rule test-web' }).click();
  await expect(bar(page)).toHaveText(/3 changes in 2 rules · not applied yet/);
  await expect(rows(page).nth(4)).toContainText('disabled');
  // nothing went to the gateway
  expect(gw.requests.filter((r) => r.method !== 'GET')).toHaveLength(0);

  await page.getByRole('region', { name: 'Unapplied changes' }).getByRole('button', { name: 'Discard' }).click();
  await expect(bar(page)).toBeHidden();
  await expect(page.getByTestId('rule-changed')).toHaveCount(0);
  await expect(rows(page).nth(3)).toContainText('UDP 53');
});

test('preview and apply: the drawer says what changes, Linux changes on request, Apply → verified toast', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByTestId('rule-editor').getByRole('group', { name: 'Access' }).getByRole('button', { name: 'Reject' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();

  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer).toContainText('dry run · nothing changed yet');
  await expect(drawer.getByTestId('preview-changes')).toContainText('access rule no-dns changed');
  await expect(drawer.getByTestId('preview-changes')).toContainText('action: drop → reject');
  await expect(drawer.getByTestId('preview-linux')).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Show Linux changes' }).click();
  await expect(drawer.getByTestId('preview-linux')).toContainText('udp dport 53 counter drop');
  await expect(drawer.getByTestId('preview-linux')).toContainText('read the nftables and tc state back');
  await expect(drawer).toContainText('If verification fails, revision 42 is restored automatically.');

  // the candidate is a merge patch against the base, If-Match the base
  const post = gw.requestsTo('POST', '/revisions')[0]!;
  expect(post.headers['if-match']).toBe('"42"');
  expect(post.headers['content-type']).toBe('application/merge-patch+json');
  expect(post.body).toEqual({ access_rules: { [RULE_DNS]: { action: 'reject' } } });

  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(drawer).toBeHidden();
  await expect(bar(page)).toBeHidden();
  expect(gw.requestsTo('POST', '/revisions/43/apply')).toHaveLength(1);
  await expect(rows(page).nth(3)).toContainText('Reject');
  await expect(page.getByTestId('rule-changed')).toHaveCount(0);
  await expect(page.getByTestId('context-line')).toContainText('revision 43');

  // the API panel shows the same action from a test
  await page.getByRole('button', { name: '</> API' }).click();
  const panel = page.getByTestId('api-panel');
  await expect(panel).toContainText('Last action: Preview & apply revision 43');
  await expect(panel.getByTestId('api-call').nth(0)).toContainText('POST /api/v1/revisions?message=');
  await expect(panel.getByTestId('api-call').nth(0)).toContainText('If-Match: "42"');
  await expect(panel.getByTestId('api-call').nth(2)).toContainText('POST /api/v1/revisions/43/apply');
  await panel.getByRole('button', { name: 'curl' }).click();
  await expect(panel.getByTestId('api-call').nth(0)).toContainText('-H "Authorization: Bearer $CHAOSGW_TOKEN"');
});

test('Back to editing discards the candidate and keeps the draft', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await drawer.getByRole('button', { name: 'Back to editing' }).click();
  await expect(drawer).toBeHidden();
  await expect.poll(() => gw.requestsTo('DELETE', '/revisions/43').length).toBe(1);
  await expect(bar(page)).toHaveText(/1 change in 1 rule/);
});

test('add, move and delete rules: new rows, the order in the patch, a deleted rule as null', async ({ page }) => {
  const gw = await open(page);
  await page.getByRole('button', { name: 'Add rule' }).first().click();
  await expect(rows(page)).toHaveCount(6);
  await expect(rows(page).nth(5)).toContainText('NEW');
  await expect(rows(page).nth(5)).toContainText('rule-4');
  await expect(rows(page).nth(5)).toContainText('Network IoT → uplink · any protocol');
  await page.getByRole('button', { name: 'Move up' }).click();
  await expect(rows(page).nth(4)).toContainText('rule-4');
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByRole('button', { name: 'Delete' }).click();
  // the deleted rule stays at its place, struck through, until the draft is applied
  await expect(rows(page)).toHaveCount(6);
  await expect(rows(page).nth(3).getByTestId('rule-deleted')).toBeVisible();
  await expect(rows(page).nth(3)).toContainText('no-dns');
  await expect(bar(page)).toHaveText(/2 changes in 2 rules/);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 43' })).toBeVisible();
  const body = gw.requestsTo('POST', '/revisions')[0]!.body as { access_rules: Record<string, unknown>; access_rule_order: string[] };
  expect(body.access_rules[RULE_DNS]).toBeNull();
  const added = Object.keys(body.access_rules).find((k) => k !== RULE_DNS)!;
  expect(body.access_rules[added]).toEqual({ name: 'rule-4', source: { network: IOT }, destination: { uplink: true }, protocol: 'any', action: 'drop', enabled: true });
  expect(body.access_rule_order).toEqual([RULE_DOT, added, RULE_WEB]);
});

test("a rule from a prefix inside a WireGuard client's networks: From offers the remote networks and takes the prefix, the patch carries it (P3-M13-08)", async ({ page }) => {
  const gw = await open(page, withNetworks(withRules(MockGateway.sample())));
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  const editor = page.getByTestId('rule-editor');
  await editor.getByLabel('From').selectOption({ label: 'Client networks · rA (10.50.0.0/24)' });
  await expect(rows(page).nth(3)).toContainText('CHANGED');
  // a prefix inside them is checked under From
  await editor.getByLabel('From').selectOption({ label: 'Remote network · a prefix inside them…' });
  await expect(editor.getByText('Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  // the choice stays, and the prefix is typed beside it
  await expect(editor.getByLabel('From')).toHaveValue('remote_network:cidr:');
  await editor.getByLabel('Prefix inside a remote network').fill('10.50.0.0/25');
  await expect(editor.getByText('Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.')).toBeHidden();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 43' })).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')[0]!.body).toEqual({ access_rules: { [RULE_DNS]: { source: { network: null, remote_network: { cidr: '10.50.0.0/25' } } } } });
});

test('a deleted rule is shown struck through at its place, and Undo brings it back there', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('button', { name: 'Edit rule no-dot' }).click();
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(rows(page)).toHaveCount(5);
  await expect(rows(page).nth(2).getByTestId('rule-deleted')).toBeVisible();
  await expect(rows(page).nth(2)).toContainText('no-dot');
  await expect(rows(page).nth(2)).toContainText('removed when applied');
  await expect(rows(page).nth(2).getByRole('switch')).toHaveCount(0);
  // the rules behind it move up one position
  await expect(rows(page).nth(3)).toContainText('1no-dns');
  await expect(bar(page)).toHaveText(/1 change in 1 rule/);
  await rows(page).nth(2).getByRole('button', { name: 'Undo the deletion of rule no-dot' }).click();
  await expect(page.getByTestId('rule-deleted')).toHaveCount(0);
  await expect(rows(page).nth(2)).toContainText('1no-dot');
  await expect(rows(page).nth(3)).toContainText('2no-dns');
  await expect(bar(page)).toBeHidden();

  // applied: the deleted rule is gone from the list
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByRole('button', { name: 'Delete' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await page.getByRole('dialog', { name: 'Preview revision 43' }).getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('rule-deleted')).toHaveCount(0);
  await expect(rows(page)).toHaveCount(4);
  expect((gw.requestsTo('POST', '/revisions')[0]!.body as { access_rules: Record<string, unknown> }).access_rules[RULE_DNS]).toBeNull();
});

test('validation: the message stands under the field and Preview & apply waits until the rule is valid', async ({ page }) => {
  await open(page);
  await rows(page).nth(4).getByRole('button', { name: 'Edit rule test-web' }).click();
  const editor = page.getByTestId('rule-editor');
  await editor.getByLabel('Ports').fill('80, 99999');
  await expect(editor.getByText('99999 is no port: a number from 1 to 65535, or a range like 1000-2000.')).toBeVisible();
  await expect(editor.getByLabel('Ports')).toHaveAttribute('aria-invalid', 'true');
  await editor.getByLabel('Name').fill('-bad name');
  await expect(editor.getByText('Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.')).toBeVisible();
  await expect(bar(page)).toContainText('a rule needs fixing first');
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
  await expect(rows(page).nth(4)).toContainText('NEEDS FIXING');
  // ports without TCP or UDP
  await editor.getByLabel('Name').fill('test-web');
  await editor.getByLabel('Ports').fill('80');
  await editor.getByLabel('Ports').blur();
  await editor.getByLabel('Protocol').selectOption('icmp');
  await expect(editor.getByText('Ports need the protocol TCP or UDP.')).toBeVisible();
  await editor.getByLabel('Protocol').selectOption('tcp');
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeEnabled();
  // a destination that is no address
  await editor.getByLabel('To', { exact: true }).selectOption('cidr');
  await editor.getByLabel('Address or prefix').fill('10.0.0');
  await expect(editor.getByText('An IPv4 address or a prefix like 203.0.113.0/24.')).toBeVisible();
  await editor.getByLabel('Address or prefix').fill('203.0.113.0/24');
  await expect(editor.getByText('An IPv4 address or a prefix like 203.0.113.0/24.')).toBeHidden();
  // a host name gets the hint of DNS-derived matching
  await editor.getByLabel('To', { exact: true }).selectOption('hostname');
  await editor.getByLabel('Host name').fill('broker.example.com');
  await expect(editor.getByTestId('hostname-hint')).toContainText('matched via DNS answers (best effort)');
});

test("the gateway's refusal of a candidate is shown in the drawer and under the field", async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('button', { name: 'Edit rule no-dot' }).click();
  await page.getByTestId('rule-editor').getByLabel('Ports').fill('853, 8853');
  await page.getByTestId('rule-editor').getByLabel('Ports').blur();
  gw.rejectRevision = [{ path: `/access_rules/${RULE_DOT}/ports`, code: 'capacity_exceeded', message: 'too many source addresses for one rule' }];
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview' });
  await expect(drawer.getByTestId('preview-invalid')).toContainText('The gateway refused the change.');
  await expect(drawer.getByTestId('preview-invalid')).toContainText('Rule no-dot · ports: too many source addresses for one rule');
  await expect(drawer.getByRole('button', { name: /^Apply/ })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Back to editing' }).click();
  await expect(page.getByTestId('rule-editor').getByText('too many source addresses for one rule')).toBeVisible();
  await expect(rows(page).nth(2)).toContainText('NEEDS FIXING');
});

test('a failed apply says which step failed and which revision the gateway restored', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  gw.failApply = 'verify';
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  const failed = drawer.getByTestId('apply-failed');
  await expect(failed).toContainText('Apply failed at the verify step.');
  await expect(failed).toContainText('the nftables state differs from the target');
  await expect(failed).toContainText('Revision 42 restored; nothing of revision 43 is active.');
  await expect(failed).toHaveAttribute('role', 'alert');
  // the draft is kept for another try
  await drawer.getByRole('button', { name: 'Back to editing' }).click();
  await expect(bar(page)).toHaveText(/1 change in 1 rule/);
});

test('a conflicting change made through the API opens the conflict dialog; reload and reapply mine', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByTestId('rule-editor').getByRole('group', { name: 'Access' }).getByRole('button', { name: 'Reject' }).click();
  await expect(bar(page)).toHaveText(/1 change in 1 rule/);

  // meanwhile a test changes the configuration through the API: rule test-web is disabled
  gw.applyFromApi({ access_rules: { [RULE_WEB]: { enabled: false } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'access_rule', id: RULE_WEB, name: 'test-web', path: `/access_rules/${RULE_WEB}`, summary: 'access rule test-web: enabled true → false' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();

  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict).toContainText('Revision 43 was applied by token ci-runner while you were editing (your change is based on revision 42).');
  await expect(conflict.getByTestId('conflict-theirs')).toContainText('access rule test-web: enabled true → false');
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Rule no-dns access: Drop → Reject');
  // only reload-and-reapply and discard: their change is never overwritten blindly
  await expect(conflict.getByRole('button')).toHaveText(['', 'Discard mine', 'Reload and reapply mine']);

  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 44' });
  await expect(drawer).toContainText('Based on revision 43.');
  const posts = gw.requestsTo('POST', '/revisions');
  expect(posts.map((r) => r.headers['if-match'])).toEqual(['"42"', '"43"']);
  expect(posts[1]!.body).toEqual({ access_rules: { [RULE_DNS]: { action: 'reject' } } });
  await drawer.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  // both changes are in the gateway: theirs and mine
  expect(gw.config.access_rules?.[RULE_WEB]?.enabled).toBe(false);
  expect(gw.config.access_rules?.[RULE_DNS]?.action).toBe('reject');
  await expect(rows(page).nth(4)).toContainText('disabled');
});

test('reload and reapply where both changed the same rule: their field stays, mine is added', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByTestId('rule-editor').getByRole('group', { name: 'Access' }).getByRole('button', { name: 'Reject' }).click();
  await expect(bar(page)).toHaveText(/1 change in 1 rule/);

  // meanwhile two revisions through the API: a token disables the same rule, then admin changes nothing
  gw.applyFromApi({ access_rules: { [RULE_DNS]: { enabled: false } } }, { type: 'token', id: 't1', name: 'ci-runner' }, []);
  gw.applyFromApi({}, { type: 'user', id: 'admin', name: 'admin' }, [
    { op: 'changed', kind: 'access_rule', id: RULE_DNS, name: 'no-dns', path: `/access_rules/${RULE_DNS}`, summary: 'access rule no-dns: enabled true → false' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  // the diff covers both revisions, so both are named with their authors
  await expect(conflict).toContainText(
    'Revisions 43 and 44 were applied by token ci-runner and admin while you were editing (your change is based on revision 42); their change below covers them all.',
  );
  await expect(conflict).toContainText('what only they changed stays as they made it, and where both changed the same field of an object, yours wins');
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 45' });
  await expect(drawer).toContainText('Based on revision 44.');
  // only my field: their enabled:false is not set back
  const posts = gw.requestsTo('POST', '/revisions');
  expect(posts[1]!.body).toEqual({ access_rules: { [RULE_DNS]: { action: 'reject' } } });
  await drawer.getByRole('button', { name: 'Apply revision 45' }).click();
  await expect(toast(page, 'Revision 45 applied and verified in 87 ms')).toBeVisible();
  expect(gw.config.access_rules?.[RULE_DNS]).toMatchObject({ action: 'reject', enabled: false });
});

test('the conflict dialog names a field both changed, their value → mine, and reapplying takes mine (P3-M13-07)', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByTestId('rule-editor').getByRole('group', { name: 'Access' }).getByRole('button', { name: 'Reject' }).click();
  await page.getByTestId('rule-editor').getByLabel('Name').fill('no-dns-2');
  // a token changes the same rule's access to a TCP reset and its ports, but not its name
  gw.applyFromApi({ access_rules: { [RULE_DNS]: { action: 'reset', protocol: 'tcp', ports: [53, 853] } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'access_rule', id: RULE_DNS, name: 'no-dns', path: `/access_rules/${RULE_DNS}`, summary: 'access rule no-dns: action drop → reset' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  const both = conflict.getByTestId('conflict-both');
  await expect(both).toContainText('CHANGED ON BOTH SIDES · THEIRS → YOURS');
  await expect(both.getByRole('listitem')).toHaveText(['Rule no-dns-2 action: TCP reset → Reject']);
  await expect(conflict).toContainText('where both changed the same field of an object, yours wins (the fields above)');
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 44' });
  await expect(drawer).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')[1]!.body).toEqual({ access_rules: { [RULE_DNS]: { name: 'no-dns-2', action: 'reject' } } });
  await drawer.getByRole('button', { name: 'Apply revision 44' }).click();
  await expect(toast(page, 'Revision 44 applied and verified in 87 ms')).toBeVisible();
  // their ports stay, my access wins
  expect(gw.config.access_rules?.[RULE_DNS]).toMatchObject({ name: 'no-dns-2', action: 'reject', protocol: 'tcp', ports: [53, 853] });
});

test("the conflict dialog names a rule's From both changed, their scope → mine, and reapplying sends exactly mine (P3-M13-07)", async ({ page }) => {
  const gw = await open(page, withNetworks(withRules(MockGateway.sample())));
  await rows(page).nth(3).getByRole('button', { name: 'Edit rule no-dns' }).click();
  await page.getByTestId('rule-editor').getByLabel('From').selectOption({ label: 'Client networks · rA (10.50.0.0/24)' });
  // a token changes the same rule's From to everything
  gw.applyFromApi({ access_rules: { [RULE_DNS]: { source: { network: null, global: true } } } }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'access_rule', id: RULE_DNS, name: 'no-dns', path: `/access_rules/${RULE_DNS}`, summary: 'access rule no-dns: source changed' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-both').getByRole('listitem')).toHaveText(['Rule no-dns source: Everything → Client networks of rA']);
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  // mine as a whole: their global goes, no scope with two keys
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' })).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')[1]!.body).toEqual({ access_rules: { [RULE_DNS]: { source: { global: null, remote_network: { client: CLIENT_RA } } } } });
});

test('the conflict dialog names the rule order when both moved rules, and reapplying takes mine (P3-M13-07)', async ({ page }) => {
  const gw = await open(page);
  // mine: test-web to the top
  await rows(page).nth(4).getByRole('button', { name: 'Edit rule test-web' }).click();
  await page.getByRole('button', { name: 'Move up' }).click();
  await page.getByRole('button', { name: 'Move up' }).click();
  await expect(rows(page).nth(2)).toContainText('test-web');
  // theirs: no-dns to the top
  gw.applyFromApi({ access_rule_order: [RULE_DNS, RULE_DOT, RULE_WEB] }, { type: 'token', id: 't1', name: 'ci-runner' }, [
    { op: 'changed', kind: 'access_rule', path: '/access_rule_order', summary: 'order of the access rules changed' },
  ]);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-both').getByRole('listitem')).toHaveText(['Rule order: no-dns, no-dot, test-web → test-web, no-dot, no-dns']);
  await conflict.getByRole('button', { name: 'Reload and reapply mine' }).click();
  await expect(page.getByRole('dialog', { name: 'Preview revision 44' })).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions')[1]!.body).toEqual({ access_rule_order: [RULE_WEB, RULE_DOT, RULE_DNS] });
});

test('the conflict dialog lists no field when the two changes do not meet', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' }).click();
  gw.applyFromApi({ access_rules: { [RULE_DNS]: { action: 'reset' } } }, { type: 'token', id: 't1', name: 'ci-runner' }, []);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-yours')).toContainText('Rule no-dot enabled: yes → no');
  await expect(conflict.getByTestId('conflict-both')).toHaveCount(0);
  await expect(conflict).not.toContainText('(the fields above)');
});

test('the concurrent-change dialog can discard the draft instead', async ({ page }) => {
  const gw = await open(page);
  await rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' }).click();
  gw.applyFromApi({}, { type: 'user', id: 'admin', name: 'admin' }, []);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const conflict = page.getByRole('dialog', { name: 'The configuration was changed' });
  await expect(conflict.getByTestId('conflict-theirs')).toContainText("No change in the configuration's objects.");
  await conflict.getByRole('button', { name: 'Discard mine' }).click();
  await expect(conflict).toBeHidden();
  await expect(bar(page)).toBeHidden();
});

test('empty: no rules yet, the dashed box offers Add rule', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(page, gw);
  await expect(page.getByTestId('empty-state')).toContainText('No access rules yet: traffic between the networks follows the access matrix.');
  await page.getByTestId('empty-state').getByRole('button', { name: 'Add rule' }).click();
  await expect(rows(page)).toHaveCount(2);
});

test('Preview & apply waits while a revision waits for confirmation', async ({ page }) => {
  const gw = withRules(MockGateway.sample());
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 60_000).toISOString() } };
  await open(page, gw);
  await rows(page).nth(2).getByRole('switch', { name: 'Enable rule no-dot' }).click();
  await expect(page.getByRole('button', { name: 'Preview & apply' })).toBeDisabled();
});
