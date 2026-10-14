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
  await bad.getByRole('button', { name: 'Activate on Network Test' }).click();
  await expect(toast(page, 'Bad LTE active on Network Test')).toBeVisible();
  const post = gw.requestsTo('POST', '/overlays')[0]!;
  expect(post.body).toEqual({ target: { network: '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7' }, profile: BAD_LTE, ttl: '15m' });
  await expect(bad.getByTestId('profile-active')).toContainText(/Network Test · 1[45]:\d\d left/);
  await expect(bad.getByRole('button', { name: 'Deactivate on Network Test' })).toBeVisible();

  // on IoT, LTE is active: the card offers to deactivate it there, the others to switch
  await page.getByLabel('Activate on').selectOption({ label: 'Network · IoT' });
  await expect(card(page, 'LTE').getByRole('button', { name: 'Deactivate on Network IoT' })).toBeVisible();
  await card(page, 'LTE').getByRole('button', { name: 'Deactivate on Network IoT' }).click();
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
  await card(page, 'Bad LTE').getByRole('button', { name: 'Activate on Remote networks of link site-b' }).click();
  await expect(toast(page, 'Bad LTE active on Remote networks of link site-b')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toEqual({ target: { remote_network: { link: LINK } }, profile: BAD_LTE });
  await expect(card(page, 'Bad LTE').getByTestId('profile-active')).toContainText('Remote networks of link site-b');

  // a prefix: Activate waits for a valid one
  await scope.selectOption({ label: 'Remote network · a prefix inside them…' });
  await expect(page.getByText('Enter a prefix inside a client network or a route of a link, like 10.50.0.0/25.')).toBeVisible();
  await expect(card(page, 'LTE').getByRole('button', { name: /^Activate on/ })).toBeDisabled();
  await page.getByLabel('Prefix inside a remote network').fill('10.50.0.0/25');
  await card(page, 'LTE').getByRole('button', { name: 'Activate on Remote network 10.50.0.0/25' }).click();
  await expect(toast(page, 'LTE active on Remote network 10.50.0.0/25')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[1]!.body).toEqual({ target: { remote_network: { cidr: '10.50.0.0/25' } }, profile: LTE });
});

test("the device page's Apply profile opens the profiles with the device as the scope", async ({ page }) => {
  await lab().install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('link', { name: 'Apply profile' }).click();
  await expect(page).toHaveURL(/\/profiles\?scope=device/);
  await expect(page.getByLabel('Activate on')).toHaveValue(`device:${ESP}`);
  await expect(card(page, 'LTE').getByRole('button', { name: 'Activate on ESP32-42' })).toBeVisible();
});

test('activating waits while a revision waits for confirmation', async ({ page }) => {
  const gw = lab();
  gw.state = { ...gw.state, pending_confirm: { revision: 42, deadline: new Date(Date.now() + 60_000).toISOString() } };
  await gw.install(page);
  await page.goto('/profiles');
  await expect(card(page, 'Bad LTE').getByRole('button', { name: /^Activate on/ })).toBeDisabled();

});
