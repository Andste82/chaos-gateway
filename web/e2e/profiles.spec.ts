import { expect, test, type Page } from '@playwright/test';
import type { ProfileView } from '../src/api/generated/model';
import { LINK, MockGateway, toast, withNetworks } from './mock';

// M13: profile cards with their parameters and where they are active (TTL), the scope selector and
// the duration, activate and deactivate on the chosen scope (one profile per scope).

const LTE = 'b0000000-0000-5000-8000-0000000000e1';
const BAD_LTE = 'b0000000-0000-5000-8000-0000000000e2';
const DNS_BROKEN = 'b0000000-0000-5000-8000-0000000000e3';
const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';

function lab(): MockGateway {
  const gw = MockGateway.sample();
  const profiles: ProfileView[] = [
    { id: LTE, builtin: true, available: true, config: { name: 'lte', description: '50 ms ± 10 ms, 0.1 % loss', parts: { impairment: { latency: '50ms', jitter: '10ms', loss: '0.1%' } } } },
    { id: BAD_LTE, builtin: true, available: true, config: { name: 'bad-lte', parts: { impairment: { latency: '150ms', jitter: '50ms', loss: '3%', rate: '2Mbit' } } } },
    {
      id: DNS_BROKEN,
      builtin: true,
      available: false,
      unavailable_reason: 'the dns part arrives with milestone M20',
      config: { name: 'dns-broken', parts: { dns: { action: 'servfail' } } },
    },
    { id: 'c0000000-0000-4000-8000-0000000000c1', builtin: false, available: true, config: { name: 'slow-lab', parts: { impairment: { latency: '20ms' }, mtu: { size: 1280 } } } },
  ];
  gw.profiles = profiles;
  // the activation of the sample lab: LTE on network IoT, with a TTL
  gw.overlays = gw.overlays.map((o) => (o.kind === 'profile' ? { ...o, expires_at: new Date(Date.now() + 9 * 60_000).toISOString() } : o));
  return gw;
}

const card = (page: Page, name: string) => page.getByTestId('profile-card').filter({ has: page.getByRole('heading', { name, exact: true }) });

test('cards with their parameters and where each profile is active, with the remaining time', async ({ page }) => {
  await lab().install(page);
  await page.goto('/profiles');
  await expect(page.getByTestId('profile-card')).toHaveCount(4);
  const lte = card(page, 'LTE');
  await expect(lte).toContainText('active');
  await expect(lte).toContainText('↑↓ 50 ms ±10');
  await expect(lte).toContainText('0.1 % loss');
  await expect(lte.getByTestId('profile-active')).toContainText(/Network IoT · \d:\d\d left · partially overridden · by token ci/);
  await expect(card(page, 'Bad LTE')).toContainText('↑↓ 2 Mbit/s');
  await expect(card(page, 'Bad LTE').getByTestId('profile-active')).toHaveText('Not active');
  await expect(card(page, 'slow-lab')).toContainText('custom');
  await expect(card(page, 'slow-lab')).toContainText('MTU 1280');
  // a profile whose family comes later says so and cannot be activated
  await expect(card(page, 'DNS broken')).toContainText('the dns part arrives with milestone M20');
  await expect(card(page, 'DNS broken').getByRole('button', { name: /^Activate/ })).toBeDisabled();
});

test('activate on the chosen scope for a duration, then deactivate', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/profiles');
  await page.getByLabel('Activate on').selectOption({ label: 'Network · Test' });
  await page.getByLabel('For').selectOption({ label: '15 min' });
  const bad = card(page, 'Bad LTE');
  await bad.getByRole('button', { name: 'Activate Bad LTE on Network Test' }).click();
  await expect(toast(page, 'Bad LTE active on Network Test')).toBeVisible();
  const post = gw.requestsTo('POST', '/overlays')[0]!;
  expect(post.body).toEqual({ target: { network: '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7' }, profile: BAD_LTE, ttl: '15m' });
  await expect(bad.getByTestId('profile-active')).toContainText(/Network Test · 1[45]:\d\d left/);
  await expect(bad.getByRole('button', { name: 'Deactivate Bad LTE on Network Test' })).toBeVisible();

  // on IoT, LTE is active: the card offers to deactivate it there, the others to switch
  await page.getByLabel('Activate on').selectOption({ label: 'Network · IoT' });
  await expect(card(page, 'LTE').getByRole('button', { name: 'Deactivate LTE on Network IoT' })).toBeVisible();
  await card(page, 'LTE').getByRole('button', { name: 'Deactivate LTE on Network IoT' }).click();
  await expect(toast(page, 'LTE deactivated on Network IoT')).toBeVisible();
  expect(gw.requestsTo('DELETE', '/overlays/f0000000-0000-4000-8000-000000000002')).toHaveLength(1);
  await expect(card(page, 'LTE').getByTestId('profile-active')).toHaveText('Not active');
});

test("activate on a link's remote networks or a prefix inside them (P3-M13-08)", async ({ page }) => {
  const gw = withNetworks(lab());
  await gw.install(page);
  await page.goto('/profiles');
  const scope = page.getByLabel('Activate on');
  await scope.selectOption({ label: 'Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)' });
  await card(page, 'Bad LTE').getByRole('button', { name: 'Activate Bad LTE on Remote networks of link site-b' }).click();
  await expect(toast(page, 'Bad LTE active on Remote networks of link site-b')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({ target: { remote_network: { link: LINK } }, profile: BAD_LTE });
  await expect(card(page, 'Bad LTE').getByTestId('profile-active')).toContainText('Remote networks of link site-b');

  // a prefix: Activate waits for a valid one
  await scope.selectOption({ label: 'Remote network · a prefix inside them…' });
  await expect(page.getByText('Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.')).toBeVisible();
  await expect(card(page, 'LTE').getByRole('button', { name: /^Activate LTE on/ })).toBeDisabled();
  await page.getByLabel('Prefix inside a remote network').fill('10.50.0.0/25');
  await card(page, 'LTE').getByRole('button', { name: 'Activate LTE on Remote network 10.50.0.0/25' }).click();
  await expect(toast(page, 'LTE active on Remote network 10.50.0.0/25')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[1]!.body).toEqual({ target: { remote_network: { cidr: '10.50.0.0/25' } }, profile: LTE });
});

test("the device page's Apply profile opens the profiles with the device as the scope", async ({ page }) => {
  await lab().install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('link', { name: 'Apply profile' }).click();
  await expect(page).toHaveURL(/\/profiles\?scope=device/);
  await expect(page.getByLabel('Activate on')).toHaveValue(`device:${ESP}`);
  await expect(card(page, 'LTE').getByRole('button', { name: 'Activate LTE on ESP32-42' })).toBeVisible();
});

test('activating waits while a revision waits for confirmation', async ({ page }) => {
  const gw = lab();
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 60_000).toISOString() } };
  await gw.install(page);
  await page.goto('/profiles');
  await expect(card(page, 'Bad LTE').getByRole('button', { name: /^Activate Bad LTE on/ })).toBeDisabled();

});

// P3-M13-03: the profile editor — custom profiles are created, edited and deleted through the
// draft, the unapplied-changes bar and the preview-and-apply drawer.

const SLOW_LAB = 'c0000000-0000-4000-8000-0000000000c1';
const editor = (page: Page) => page.getByRole('dialog', { name: /^(New|Edit) profile$/ });

test('create a profile through the editor and the drawer, then activate it (P3-M13-03)', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/profiles');
  await page.getByRole('button', { name: 'New profile' }).click();
  const d = editor(page);
  const add = d.getByRole('button', { name: 'Add to the changes' });
  // a new editor opens without errors, and "no impairment" is no orange fault chip
  await expect(d.locator('.field-error')).toHaveCount(0);
  await expect(d.getByTestId('profile-no-impairment')).toHaveText('no impairment');
  await expect(d.getByTestId('profile-preview').locator('.mono')).toHaveCount(0);
  await expect(add).toBeEnabled();
  // the name is unique among all profiles, and a built-in profile's name is reserved
  await d.getByLabel('Name').fill('LTE');
  await expect(d.getByLabel('Name')).toHaveAccessibleDescription('This is the name of a built-in profile.');
  await d.getByLabel('Name').fill('Slow-Lab');
  await expect(d.getByLabel('Name')).toHaveAccessibleDescription('Another profile has this name.');
  // pressed with errors: it stays open, says what is missing, and waits until they are fixed
  await add.click();
  await expect(d).toBeVisible();
  await expect(d.getByText('Set at least one parameter', { exact: false })).toBeVisible();
  await expect(add).toBeDisabled();
  await d.getByLabel('Name').fill('flaky-lte');
  await d.getByLabel('Description').fill('LTE with loss in bursts');
  // the impairment part has the fault dialog's fields
  await d.getByLabel('Latency (ms)').fill('80');
  await d.getByLabel('Jitter (ms)').fill('20');
  await d.getByRole('button', { name: /^Advanced/ }).click();
  await d.getByRole('group', { name: 'Loss model' }).getByRole('button', { name: 'Burst (Gilbert-Elliott)' }).click();
  await expect(d.getByLabel('Good → bad (%)')).toHaveAccessibleDescription('Needed for burst loss.');
  await d.getByLabel('Good → bad (%)').fill('2');
  await d.getByLabel('Bad → good (%)').fill('40');
  await d.getByLabel('MTU part').check();
  await d.getByLabel('Packet size (bytes)').fill('1400');
  await d.getByLabel('Mode', { exact: true }).selectOption('mss_clamp');
  await expect(d.getByTestId('profile-preview')).toContainText('↑↓ 80 ms ±20');
  await expect(d.getByTestId('profile-preview')).toContainText('burst loss');
  await expect(d.getByTestId('profile-preview')).toContainText('MTU 1400 mss_clamp');
  await add.click();
  await expect(d).toBeHidden();

  // in the draft: marked NEW, not activated before it is applied
  const flaky = card(page, 'flaky-lte');
  await expect(flaky.getByTestId('profile-mark')).toHaveText('NEW');
  await expect(flaky.getByTestId('profile-pending')).toHaveText('Not applied yet: it can be activated after the changes are applied.');
  await expect(flaky.getByRole('button', { name: 'Activate flaky-lte on Everything' })).toBeDisabled();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 profile · not applied yet/);
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(drawer.getByTestId('preview-changes')).toContainText('profile flaky-lte added');
  const body = gw.requestsTo('POST', '/revisions')[0]!.body as { profiles: Record<string, unknown> };
  const [id] = Object.keys(body.profiles);
  expect(Object.values(body.profiles)).toEqual([
    {
      name: 'flaky-lte',
      description: 'LTE with loss in bursts',
      parts: { impairment: { latency: '80ms', jitter: '20ms', burst_loss: { p: '2%', r: '40%' } }, mtu: { size: 1400, mode: 'mss_clamp' } },
    },
  ]);
  await drawer.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  await expect(flaky.getByTestId('profile-mark')).toHaveCount(0);

  // applied: it is activated like any other profile
  await flaky.getByRole('button', { name: 'Activate flaky-lte on Everything' }).click();
  await expect(toast(page, 'flaky-lte active on Everything')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({ target: { global: true }, profile: id });
  await expect(flaky.getByTestId('profile-active')).toContainText('Everything');
});

test('edit and delete a custom profile through the draft; a refused profile shows why (P3-M13-03)', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/profiles');
  const slow = card(page, 'slow-lab');
  // a built-in profile is not edited
  await expect(card(page, 'LTE').getByRole('button', { name: /^(Edit|Delete) / })).toHaveCount(0);
  // saved without a change, it is no change: the defaults the editor fills in (the MTU mode) are not one
  await slow.getByRole('button', { name: 'Edit slow-lab' }).click();
  const d = editor(page);
  await expect(d.getByLabel('Mode', { exact: true })).toHaveValue('icmp');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await expect(d).toBeHidden();
  await expect(slow.getByTestId('profile-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();

  await slow.getByRole('button', { name: 'Edit slow-lab' }).click();
  await expect(d.getByLabel('Name')).toHaveValue('slow-lab');
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('20');
  await expect(d.getByLabel('MTU part')).toBeChecked();
  await expect(d.getByLabel('Packet size (bytes)')).toHaveValue('1280');
  // each direction its own: the download gets a loss of its own
  await d.getByRole('group', { name: 'Direction' }).getByRole('button', { name: 'Each its own' }).click();
  await d.getByRole('group', { name: 'Parameters of' }).getByRole('button', { name: 'Download' }).click();
  await d.getByLabel('Loss (%)').fill('1');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await expect(slow.getByTestId('profile-mark')).toHaveText('CHANGED');
  await expect(slow.getByText('↑ 20 ms', { exact: true })).toBeVisible();
  await expect(slow.getByText('1 % loss', { exact: true })).toBeVisible();
  await expect(page.getByTestId('unapplied-bar')).toHaveText(/1 change in 1 profile/);

  // the gateway refuses it: the drawer names the field, the card and the editor show it
  gw.rejectRevision = [{ path: `/profiles/${SLOW_LAB}/parts/impairment/download/loss`, code: 'invalid_value', message: 'the loss is not allowed here' }];
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const drawer = page.getByRole('dialog', { name: 'Preview' });
  // the merge patch takes the flat latency away and writes each direction (the latency went to the
  // upload, the download has its own loss); the MTU part is unchanged
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(1);
  expect((gw.requestsTo('POST', '/revisions')[0]!.body as { profiles: unknown }).profiles).toEqual({
    [SLOW_LAB]: { parts: { impairment: { latency: null, upload: { latency: '20ms' }, download: { loss: '1%' } } } },
  });
  await expect(drawer.getByTestId('preview-invalid')).toContainText('Profile slow-lab · parts impairment download loss: the loss is not allowed here');
  await drawer.getByRole('button', { name: 'Back to editing' }).click();
  await expect(slow).toContainText('NEEDS FIXING');
  await expect(slow.getByTestId('profile-problem')).toHaveText('the loss is not allowed here');
  await slow.getByRole('button', { name: 'Edit slow-lab' }).click();
  await d.getByRole('group', { name: 'Parameters of' }).getByRole('button', { name: 'Download' }).click();
  await expect(d.getByLabel('Loss (%)')).toHaveAccessibleDescription('the loss is not allowed here');
  await d.getByRole('button', { name: 'Cancel' }).click();

  // delete: struck through with Undo until applied; then it is gone
  await slow.getByRole('button', { name: 'Delete slow-lab' }).click();
  await expect(slow.getByTestId('profile-mark')).toHaveText('DELETED');
  await slow.getByRole('button', { name: 'Undo the deletion of slow-lab' }).click();
  await expect(slow.getByTestId('profile-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
  await slow.getByRole('button', { name: 'Delete slow-lab' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const preview = page.getByRole('dialog', { name: 'Preview revision 43' });
  await expect(preview.getByTestId('preview-changes')).toContainText('profile slow-lab removed');
  expect((gw.requestsTo('POST', '/revisions').at(-1)!.body as { profiles: unknown }).profiles).toEqual({ [SLOW_LAB]: null });
  await preview.getByRole('button', { name: 'Apply revision 43' }).click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  await expect(page.getByTestId('profile-card')).toHaveCount(3);
});

test('delete an active custom profile: the card says its activations end, the drawer lists them and Apply waits for "Remove them" (P3-M13-03)', async ({ page }) => {
  const ACT = 'f0000000-0000-4000-8000-0000000000a7';
  const gw = lab();
  const now = new Date().toISOString();
  gw.overlays = [
    ...gw.overlays,
    { id: ACT, kind: 'profile', owner: { type: 'user', id: 'admin', name: 'admin' }, target: { device: ESP }, profile: SLOW_LAB, created_at: now, updated_at: now, generation: 42, state: 'effective' },
  ];
  await gw.install(page);
  await page.goto('/profiles');
  const slow = card(page, 'slow-lab');
  await expect(slow.getByTestId('profile-active')).toContainText('Active on');
  await slow.getByRole('button', { name: 'Delete slow-lab' }).click();
  await expect(slow.getByTestId('profile-mark')).toHaveText('DELETED');
  await expect(slow.getByTestId('profile-pending')).toHaveText('Removed when the changes are applied, and with it its activation: the preview lists it and asks first.');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const preview = page.getByRole('dialog', { name: 'Preview revision 43' });
  const refs = preview.getByTestId('preview-references');
  await expect(refs).toContainText(`overlay ${ACT} (Profile slow-lab)`);
  const apply = preview.getByRole('button', { name: 'Apply revision 43' });
  await expect(apply).toBeDisabled();
  await refs.getByLabel('Remove them when applying').check();
  await expect(apply).toBeEnabled();
  await apply.click();
  await expect(toast(page, 'Revision 43 applied and verified in 87 ms')).toBeVisible();
  expect(gw.requestsTo('POST', '/revisions/43/apply')[0]!.query).toEqual({ force: 'true' });
  expect(gw.overlays.some((o) => o.id === ACT)).toBe(false);
  await expect(page.getByTestId('profile-card')).toHaveCount(3);
});

test('a custom profile saved without a change is no change, also where the editor reads it in another form (P3-M13-03)', async ({ page }) => {
  const gw = lab();
  // written per direction with the same parameters and a latency in seconds: the editor shows it flat in ms
  gw.profiles = [
    ...gw.profiles,
    { id: 'c0000000-0000-4000-8000-0000000000c2', builtin: false, available: true, config: { name: 'same-both', parts: { impairment: { upload: { latency: '1s' }, download: { latency: '1s' } } } } },
  ];
  await gw.install(page);
  await page.goto('/profiles');
  const same = card(page, 'same-both');
  await same.getByRole('button', { name: 'Edit same-both' }).click();
  const d = editor(page);
  await expect(d.getByLabel('Latency (ms)')).toHaveValue('1000');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await expect(d).toBeHidden();
  await expect(same.getByTestId('profile-mark')).toHaveCount(0);
  await expect(page.getByTestId('unapplied-bar')).toBeHidden();
});
