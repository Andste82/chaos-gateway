import { expect, test, type Page } from '@playwright/test';
import type { LanNetwork, Overlay, ProfileView, Scenario } from '../src/api/generated/model';
import { CreateOverlayBody } from '../src/api/generated/zod/overlays/overlays';
import { ESP32, IOT, MockGateway, SCN_MQTT, toast, withNetworks, withScenarios } from './mock';

// M34 (plan §2.17 "Add fault", "Device detail", "Profiles", "Networks", "Scenarios", §2.18): the time faults of the gateway's NTP
// server against the mocked API — the kind "Time (NTP)" of the Add fault dialog with its presets and checks, the Time panel of the
// device detail, the time profiles, the time service of a network, and the step, the wait and the check of a scenario. Every request
// the forms send is checked against the spec's schema.

const ESP = ESP32;
const faultDialog = (page: Page) => page.getByRole('dialog', { name: /^(Add|Edit) fault$/ });
const stepDialog = (page: Page, name: 'Add step' | 'Edit step' | 'Add check') => page.getByRole('dialog', { name });
const kind = (page: Page, name: string) => faultDialog(page).getByRole('group', { name: 'Kind' }).getByRole('button', { name, exact: true });
const step = (page: Page, id: string) => page.locator(`[data-testid="timeline-step"][data-step="${id}"]`);

function sentOverlays(gw: MockGateway): unknown[] {
  const bodies = gw.requestsTo('POST', '/overlays').map((r) => r.body);
  for (const b of bodies) expect(CreateOverlayBody.safeParse(b).success, JSON.stringify(b)).toBe(true);
  return bodies;
}

function overlay(id: string, over: Partial<Overlay>): Overlay {
  return {
    id,
    kind: 'time',
    owner: { type: 'user', id: 'admin', name: 'admin' },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    generation: 42,
    state: 'effective',
    ...over,
  };
}

// ---- the Add fault dialog

test('a time fault: made in the Add fault dialog of a device with a preset, its checks under the fields, then edited where it is listed', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await expect(d.getByRole('group', { name: 'Kind' }).getByRole('button')).toHaveText(['Impairment', 'MTU / PMTUD', 'DNS', 'TLS', 'Time (NTP)']);
  await kind(page, 'Time (NTP)').click();
  // a time fault has no traffic preset, no impairment fields and no direction; the target is the device
  await expect(d.getByLabel('Traffic', { exact: true })).toHaveCount(0);
  await expect(d.getByLabel('Latency (ms)')).toHaveCount(0);
  await expect(d.getByLabel('Target')).toBeDisabled();
  await expect(d.getByTestId('overlay-only-note')).toHaveText(/A time fault is an overlay/);
  await expect(d.getByTestId('time-jump-note')).toContainText('jumps when the fault starts and jumps back when it ends');
  // the eight presets
  await expect(d.getByRole('group', { name: 'Presets' }).getByRole('button')).toHaveText(['+1 year', '-1 day', '1970', '2038', 'NTP era 1', 'Leap second', 'Unsynchronized', 'Kiss-of-death']);
  await expect(d.getByLabel('Offset')).toHaveValue('+1y');

  await d.getByLabel('Offset').fill('tomorrow');
  await expect(d.getByLabel('Offset')).toHaveAccessibleDescription(/A signed span such as \+400d, -90s or -1y2d/);
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Offset').fill('+400d');
  await d.getByLabel('Drift (ppm)').fill('fast');
  await expect(d.getByLabel('Drift (ppm)')).toHaveAccessibleDescription(/Parts per million/);
  await d.getByLabel('Drift (ppm)').fill('');

  // the preset 1970 serves a fixed instant; the leap second preset adds the indicator
  await d.getByTestId('time-preset-1970').click();
  await expect(d.getByLabel('Served time')).toHaveValue('absolute');
  await expect(d.getByLabel('Instant')).toHaveValue('1970-01-01T00:00:00Z');
  await d.getByLabel('Instant').fill('1899-12-31T23:59:59Z');
  await expect(d.getByLabel('Instant')).toHaveAccessibleDescription(/starts at 1900-01-01/);
  await d.getByTestId('time-preset-leap-second').click();
  await expect(d.getByLabel('Instant')).toHaveValue('2016-12-31T23:55:00Z');
  await expect(d.getByLabel('Leap indicator')).toHaveValue('insert');
  await d.getByTestId('time-preset-2038').click();
  await expect(d.getByLabel('Leap indicator')).toHaveValue('');

  // the server asked: a host name; the ports of the gateway's own DNS and DHCP are refused
  await d.getByLabel('Destination', { exact: true }).selectOption('hostname');
  await expect(d.getByLabel('Destination', { exact: true }).locator('option')).toHaveText(['Anywhere', 'An address or prefix', 'A host name (DNS-derived)']);
  await d.getByLabel('Host name', { exact: true }).fill('time.google.com');
  await d.getByLabel('Ports').fill('53');
  await expect(d.getByLabel('Ports')).toHaveAccessibleDescription(/DNS and DHCP services/);
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Ports').fill('');
  await d.getByLabel('Drift (ppm)').fill('500');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await expect(d.getByTestId('fault-preview')).toHaveText(
    /ESP32-42 · NTP queries to time.google.com → the time 2038-01-19 03:09:07 UTC, drifting 500 ppm · expires after 5 min/,
  );
  await d.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(d.getByTestId('api-call')).toContainText('"skew_ppm": 500');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({
    target: { device: ESP },
    time: { destination: { hostname: 'time.google.com' }, absolute: '2038-01-19T03:09:07Z', skew_ppm: 500 },
    ttl: '5m',
  });

  // listed with the device's faults, and edited there: the answer changes, the server is the key, so the overlay stays one
  const row = page.getByTestId('active-fault').filter({ hasText: 'Time 2038-01-19 03:09:07 UTC' });
  await expect(row).toContainText('skew 500 ppm');
  await expect(row).toContainText('time.google.com');
  await row.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByRole('group', { name: 'Kind' })).toHaveCount(0);
  await expect(d.getByLabel('Instant')).toHaveValue('2038-01-19T03:09:07Z');
  await expect(d.getByLabel('Host name', { exact: true })).toHaveValue('time.google.com');
  await expect(d.getByLabel('Duration')).toHaveValue('keep');
  await d.getByTestId('time-preset-kiss-of-death').click();
  await expect(d.getByLabel('Kiss code')).toHaveValue('DENY');
  await expect(d.getByLabel('Served time')).toHaveCount(0);
  await d.getByLabel('Kiss code').selectOption('RATE');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42').last()).toBeVisible();
  const second = sentOverlays(gw)[1] as { time: unknown; ttl: string };
  // only what the answer uses: the instant and the drift of the earlier choice stay out
  expect(second.time).toEqual({ destination: { hostname: 'time.google.com' }, answer: 'kiss_of_death', kiss_code: 'RATE' });
  expect(second.ttl).toBe('5m');
  expect(gw.overlays.filter((o) => o.kind === 'time')).toHaveLength(1);
  expect(gw.requests.filter((r) => r.method === 'DELETE')).toHaveLength(0);
  await expect(page.getByTestId('active-fault').filter({ hasText: 'Time kiss-of-death RATE' })).toBeVisible();
});

test('a time fault for a network from the faults screen: no Everything, no remote network, an overlay only, listed in its family and removed', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  // the target the dialog started with, Everything, is not one a time fault can have: it moves to one that can
  await expect(d.getByLabel('Target')).toContainText('Everything');
  await kind(page, 'Time (NTP)').click();
  await expect(d.getByLabel('Target')).not.toContainText('Everything');
  await expect(d.getByLabel('Target')).not.toContainText('Remote');
  await expect(d.getByLabel('Target')).not.toHaveValue('global');
  await expect(d.getByRole('group', { name: 'Save as' })).toHaveCount(0);
  // leaving the kind brings Everything back: the move to a network was the dialog's, not the user's
  await kind(page, 'Impairment').click();
  await expect(d.getByLabel('Target')).toHaveValue('global');
  await kind(page, 'Time (NTP)').click();
  await expect(d.getByLabel('Target')).not.toHaveValue('global');
  await d.getByLabel('Target').selectOption({ label: 'Network · IoT' });
  // a target the user chose stays when the kind is left
  await kind(page, 'Impairment').click();
  await expect(d.getByLabel('Target')).toContainText('Network · IoT');
  await kind(page, 'Time (NTP)').click();
  await d.getByTestId('time-preset-plus-1y').click();
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on Network IoT')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { network: IOT }, time: { offset: '+1y' }, ttl: '15m' });
  const rows = page.getByTestId('fault-family-time').getByTestId('fault-row');
  await expect(page.getByTestId('fault-family-time').getByRole('heading', { name: 'Time (NTP)' })).toBeVisible();
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('Network IoT');
  await expect(rows).toContainText('Time +1y');
  // edited from the faults screen with the same dialog: the target stays
  await rows.getByRole('button', { name: /^Edit / }).click();
  await expect(d.getByLabel('Offset')).toHaveValue('+1y');
  await expect(d.getByLabel('Target')).toBeDisabled();
  await d.getByRole('button', { name: 'Cancel' }).click();
  await rows.getByRole('button', { name: /^Remove / }).click();
  await expect(toast(page, 'Fault removed')).toBeVisible();
  expect(gw.overlays.filter((o) => o.kind === 'time')).toHaveLength(0);
});

test('a configured fault cannot be a time fault: the kind is gone under Save as: Configuration', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await d.getByRole('group', { name: 'Save as' }).getByRole('button', { name: /^Configuration/ }).click();
  await expect(d.getByRole('group', { name: 'Kind' }).getByRole('button')).toHaveText(['Impairment', 'MTU / PMTUD', 'Tunnel']);
  await expect(d.getByText('DNS faults, TLS cases and time faults are overlays: choose Overlay under Save as to make one.')).toBeVisible();
});

test('a gateway without the time kind does not offer it', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.capabilities.overlay_kinds = gw.capabilities.overlay_kinds.filter((k) => k !== 'time');
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  await expect(faultDialog(page).getByRole('group', { name: 'Kind' }).getByRole('button')).toHaveText(['Impairment', 'MTU / PMTUD', 'Tunnel', 'DNS', 'TLS']);
});

test("the gateway's refusal of a time fault stands under the field it names", async ({ page }) => {
  const gw = MockGateway.sample();
  gw.rejectOverlay = [{ path: '/time/offset', code: 'invalid_time_fault', message: '"+400d" is further than about 292 years away: use an absolute time' }];
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'Time (NTP)').click();
  await d.getByLabel('Offset').fill('+400d');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(d.getByLabel('Offset')).toHaveAccessibleDescription(/further than about 292 years away/);
  await expect(d.getByRole('alert')).toContainText('see the marked fields');
  // an edit takes the message away
  await d.getByLabel('Offset').fill('+399d');
  await expect(d.getByLabel('Offset')).not.toHaveAccessibleDescription(/further than about 292 years away/);
});

test('a fault that changes nothing is refused before it is sent; a zero offset is a fault', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'Time (NTP)').click();
  await d.getByLabel('Served time').selectOption('none');
  await expect(d.getByTestId('time-change-error')).toContainText('Give the fault an offset');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await d.getByLabel('Leap indicator').selectOption('delete');
  await expect(d.getByTestId('time-change-error')).toHaveCount(0);
  await d.getByLabel('Leap indicator').selectOption('');
  await d.getByLabel('Served time').selectOption('offset');
  await d.getByLabel('Offset').fill('+0s');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeEnabled();
  // an answer other than a time hides the fields of the time
  await d.getByLabel('Answer', { exact: true }).selectOption('silence');
  await expect(d.getByLabel('Offset')).toHaveCount(0);
  await expect(d.getByTestId('time-hint')).toContainText('The query is not answered but counted');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, time: { answer: 'silence' }, ttl: '15m' });
});

// ---- the Time panel of the device detail

test('the Time panel: the last query, the server asked, the time served and the fault that shaped it', async ({ page }) => {
  const gw = labWithTimeProfile();
  const fault = overlay('f0000000-0000-4000-8000-0000000000a1', { target: { device: ESP }, time: { offset: '+400d' } });
  // the faults that act on the device are its own, its network's and the activations of the built-in time profiles; a fault that
  // another one overrides changes nothing and is not listed
  const network = overlay('f0000000-0000-4000-8000-0000000000a2', { target: { network: IOT }, time: { offset: '-1d' } });
  const profile = overlay('f0000000-0000-4000-8000-0000000000a3', { kind: 'profile', target: { device: ESP }, profile: TIME_1970 });
  const overridden = overlay('f0000000-0000-4000-8000-0000000000a4', { target: { group: 'g' }, time: { offset: '+9y' }, state: 'overridden' });
  gw.overlays = [...gw.overlays, fault, network, profile, overridden];
  const asked = new Date(Date.now() - 20_000).toISOString();
  gw.ntp[ESP] = {
    queries: 12,
    silenced: 2,
    dropped: 1,
    answers: [{ fault: fault.id, count: 9 }],
    last_query: {
      time: asked,
      answer: 'normal',
      served_time: '2027-12-09T10:00:00Z',
      offset_ms: 400 * 86_400_000,
      fault: fault.id,
      server: '192.0.2.123:123',
    },
    recent: [
      { time: asked, client: '10.10.0.42', port: 123, answer: 'normal', served_time: '2027-12-09T10:00:00Z', offset_ms: 400 * 86_400_000, fault: fault.id, server: '192.0.2.123:123' },
      { time: new Date(Date.now() - 80_000).toISOString(), client: '10.10.0.42', port: 123, answer: 'silence', fault: fault.id },
    ],
  };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Time', exact: true });
  await expect(panel.getByTestId('device-ntp-server')).toHaveText('192.0.2.123:123');
  await expect(panel.getByTestId('device-ntp-answer')).toHaveText('answered');
  await expect(panel.getByTestId('device-ntp-served')).toHaveText('2027-12-09 10:00:00 UTC');
  await expect(panel.getByTestId('device-ntp-offset')).toHaveText('+1 y 35 d');
  await expect(panel.getByTestId('device-ntp-fault')).toContainText('Time +400d');
  const active = panel.getByTestId('device-ntp-active');
  await expect(active).toContainText('Time +400d');
  await expect(active).toContainText('Time -1d');
  await expect(active).toContainText('profile Time 1970');
  await expect(active).not.toContainText('+9y');
  await expect(active.getByTestId('device-ntp-active-none')).toHaveCount(0);
  await expect(panel.getByTestId('device-ntp-counters')).toContainText('12 queries · 2 left unanswered by a fault · 1 over the rate limit');
  await expect(panel.getByTestId('device-ntp-time')).toHaveText(/^\d\d:\d\d:\d\d$/);
  await expect(panel.getByTestId('device-ntp-recent').getByRole('listitem')).toHaveCount(2);
  await expect(panel.getByTestId('device-ntp-recent')).toContainText('no answer (silence)');
  await expect(panel.getByTestId('device-ntp-recent').getByRole('listitem').nth(1)).toHaveClass(/text-block-text/);
  await expect(panel.getByTestId('device-ntp-nts')).toHaveCount(0);
});

test('the Time panel: a fault that is gone, a silence in red, one query without a list, and the counters of a device with only dropped queries', async ({ page }) => {
  const gw = MockGateway.sample();
  const when = new Date(Date.now() - 5_000).toISOString();
  gw.ntp[ESP] = {
    queries: 1,
    last_query: { time: when, answer: 'silence', fault: 'f0000000-0000-4000-8000-0000000000ff', server: '192.0.2.123:123' },
    recent: [{ time: when, client: '10.10.0.42', port: 123, answer: 'silence', fault: 'f0000000-0000-4000-8000-0000000000ff' }],
  };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Time', exact: true });
  await expect(panel.getByTestId('device-ntp-answer')).toHaveText('no answer (silence)');
  await expect(panel.getByTestId('device-ntp-answer')).toHaveClass(/text-block-text/);
  await expect(panel.getByTestId('device-ntp-fault')).toHaveText('a time fault that is gone');
  await expect(panel.getByTestId('device-ntp-counters')).toHaveText('1 query');
  // one query is the last query: a list of one adds nothing
  await expect(panel.getByTestId('device-ntp-recent')).toHaveCount(0);

  // queries over the rate limit are not in `queries`: a device that only has those still shows them
  gw.ntp[ESP] = { queries: 0, dropped: 3 };
  await expect(panel.getByTestId('device-ntp-counters')).toHaveText('0 queries · 3 over the rate limit', { timeout: 15_000 });
});

test('the Time panel: no query yet, no time fault; a device that uses NTS gets a note; the server asked is "not known" where the API does not say', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Time', exact: true });
  await expect(panel.getByTestId('device-ntp-empty')).toContainText('The NTP server has seen no query of ESP32-42 yet');
  await expect(panel.getByTestId('device-ntp-active-none')).toHaveText('none: the server answers with the correct time');

  gw.ntp[ESP] = { queries: 3, nts_ke: 2, last_query: { time: new Date().toISOString(), answer: 'normal', served_time: new Date().toISOString(), offset_ms: 0, server: '' } };
  // the panel reads every 5 s
  await expect(panel.getByTestId('device-ntp-nts')).toContainText('2 NTS key-establishment connections or queries', { timeout: 15_000 });
  await expect(panel.getByTestId('device-ntp-server')).toHaveText('not known');
  await expect(panel.getByTestId('device-ntp-offset')).toHaveText('0 ms');
});

test('the Time panel: a read that fails says so and can be tried again', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.failing[`/devices/${ESP}/ntp`] = 500;
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Time', exact: true });
  await expect(panel).toContainText('The NTP queries are not loaded');
  delete gw.failing[`/devices/${ESP}/ntp`];
  await panel.getByRole('button', { name: /Try again/ }).click();
  await expect(panel.getByTestId('device-ntp-empty')).toBeVisible();
});

test('an answer shaped by a time fault is an event of the overview that leads to the device', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/');
  // the stream is open once the page has a listener for the event, which does not depend on how fast the card shows the past events
  await page.waitForFunction(() =>
    (window as unknown as { __sse: { instances: { closed: boolean; listeners: Record<string, unknown[]> }[] } }).__sse.instances.some((es) => !es.closed && (es.listeners.ntp_answered?.length ?? 0) > 0),
  );
  await expect(page.getByTestId('recent-events')).toBeVisible();
  await gw.emit(page, { type: 'ntp_answered', message: 'ESP32-42 got a time fault answer: offset +400d', data: { device: ESP, answer: 'normal' } });
  const link = page.getByTestId('recent-events').getByRole('link', { name: 'ESP32-42 got a time fault answer: offset +400d' });
  await expect(link).toHaveAttribute('href', `/devices/${ESP}`);
  // a fault event is orange
  await expect(link).toHaveClass(/text-fault-text/);
});

test('Explain names the family of a time fault "Time (NTP)" and the fault that wins', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.explanation = {
    generation: 42,
    source: { ip: '10.10.0.42', device: { id: ESP, name: 'ESP32-42' } },
    destination: { ip: '192.0.2.123' },
    access: { verdict: 'allow', layer: 'access_matrix' },
    faults: [{ family: 'time', winner: { family: 'time', layer: 'overlay', id: 'f0000000-0000-4000-8000-0000000000a1', level: 2, summary: 'Time +400d' } }],
  };
  await gw.install(page);
  await page.goto('/faults');
  const explain = page.getByRole('region', { name: 'Explain' });
  await explain.getByLabel('Device').selectOption({ label: 'ESP32-42' });
  await explain.getByLabel('Destination').fill('192.0.2.123');
  await explain.getByLabel('Protocol').selectOption('udp');
  await explain.getByLabel('Port').fill('123');
  await explain.getByRole('button', { name: 'Explain' }).click();
  const family = page.getByTestId('explain-result').getByTestId('explain-family');
  await expect(family).toHaveCount(1);
  await expect(family).toContainText('Time (NTP): an overlay (Time +400d) wins (overlay, level 2)');
});

// ---- profiles

const TIME_1970 = 'b0000000-0000-5000-8000-0000000000f1';

function labWithTimeProfile(): MockGateway {
  const gw = MockGateway.sample();
  const profiles: ProfileView[] = [
    ...gw.profiles,
    { id: TIME_1970, builtin: true, available: true, config: { name: 'time-1970', description: 'The served time is 1970-01-01 00:00:00 UTC', parts: { time: { absolute: '1970-01-01T00:00:00Z' } } } },
  ];
  gw.profiles = profiles;
  return gw;
}

test('a time profile: shown with its chip, activated on a device; not on Everything or a remote network', async ({ page }) => {
  const gw = withNetworks(labWithTimeProfile());
  await gw.install(page);
  await page.goto('/profiles');
  const card = page.getByTestId('profile-card').filter({ has: page.getByRole('heading', { name: 'Time 1970', exact: true }) });
  await expect(card).toContainText('Time 1970-01-01 00:00:00 UTC');
  // the page starts on Everything: a profile with a time part says it needs a device, a group or a network
  await expect(card.getByTestId('profile-time-scope')).toContainText('A time fault targets a device, a group or a network');
  await expect(card.getByRole('button', { name: /^Activate/ })).toBeDisabled();
  await page.getByLabel('Activate on').selectOption({ label: 'Device · ESP32-42' });
  await expect(card.getByTestId('profile-time-scope')).toHaveCount(0);
  await card.getByRole('button', { name: 'Activate Time 1970 on ESP32-42' }).click();
  await expect(toast(page, 'Time 1970 active on ESP32-42')).toBeVisible();
  expect(gw.requestsTo('POST', '/overlays')[0]!.body).toMatchObject({ target: { device: ESP }, profile: TIME_1970 });
  // a remote network has no clients of the NTP server either
  await page.getByLabel('Activate on').selectOption({ label: 'Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)' });
  await expect(card.getByTestId('profile-time-scope')).toContainText('not everything or a remote network');
  await expect(card.getByRole('button', { name: /^Activate/ })).toBeDisabled();
  // the profile without the part is not held back by the scope
  const lte = page.getByTestId('profile-card').filter({ has: page.getByRole('heading', { name: 'LTE', exact: true }) });
  await expect(lte.getByTestId('profile-time-scope')).toHaveCount(0);
  await page.getByLabel('Activate on').selectOption({ label: 'Everything' });
  await expect(lte.getByTestId('profile-time-scope')).toHaveCount(0);
});

test('editing a custom profile with a time part keeps the part: the editor says so, and the revision does not touch it', async ({ page }) => {
  const gw = labWithTimeProfile();
  const CUSTOM = 'c0000000-0000-4000-8000-0000000000c7';
  gw.profiles = [
    ...gw.profiles,
    { id: CUSTOM, builtin: false, available: true, config: { name: 'late-lab', parts: { impairment: { latency: '20ms' }, time: { offset: '+400d' } } } },
  ];
  await gw.install(page);
  await page.goto('/profiles');
  const lab = page.getByTestId('profile-card').filter({ has: page.getByRole('heading', { name: 'late-lab', exact: true }) });
  await lab.getByRole('button', { name: 'Edit late-lab' }).click();
  const d = page.getByRole('dialog', { name: 'Edit profile' });
  await expect(d.getByTestId('profile-kept')).toHaveText('Its time part is kept as it is; it is defined over the API.');
  await d.getByLabel('Latency (ms)').fill('30');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  await expect.poll(() => gw.requestsTo('POST', '/revisions').length).toBe(1);
  const profiles = (gw.requestsTo('POST', '/revisions')[0]!.body as { profiles: Record<string, { parts?: Record<string, unknown> }> }).profiles;
  expect(profiles[CUSTOM]).toEqual({ parts: { impairment: { latency: '30ms' } } });
  // a merge patch names what changed: the time part is not in it, so it stays, and it is not taken away (null)
  expect(profiles[CUSTOM]!.parts).not.toHaveProperty('time');
});

// ---- networks

test('a network: the time service is on, the redirect is off; both are written only when they differ from the defaults', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/networks/${IOT}`);
  await page.getByTestId('network-settings').getByRole('button', { name: 'Edit IoT' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit IoT' });
  const ntp = dialog.getByTestId('network-ntp');
  await expect(ntp.getByRole('checkbox', { name: /^The gateway's NTP server serves the devices/ })).toBeChecked();
  await expect(ntp.getByRole('checkbox', { name: /^Redirect NTP to other servers/ })).not.toBeChecked();
  await expect(ntp).toContainText('A time fault redirects the traffic of its target whether or not this is set');
  await ntp.getByRole('checkbox', { name: /^Redirect NTP to other servers/ }).check();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-drawer').getByTestId('preview-changes')).toBeVisible();
  const patch = gw.requestsTo('POST', '/revisions')[0]!.body as { networks: Record<string, unknown> };
  expect(patch.networks).toEqual({ [IOT]: { ntp: { redirect_hardcoded: true } } });
  await expect(page.getByTestId('preview-changes')).toContainText('network IoT changed');
  await expect(page.getByTestId('preview-changes')).toContainText('redirect_hardcoded');
  await page.getByRole('button', { name: 'Back to editing' }).click();
  // switched off: the service is off, the redirect stays as chosen
  await page.getByTestId('network-settings').getByRole('button', { name: 'Edit IoT' }).click();
  await ntp.getByRole('checkbox', { name: /^The gateway's NTP server serves the devices/ }).uncheck();
  await dialog.getByRole('button', { name: 'Preview & apply' }).click();
  await expect(page.getByTestId('preview-drawer').getByTestId('preview-changes')).toBeVisible();
  const second = gw.requestsTo('POST', '/revisions').at(-1)!.body as { networks: Record<string, LanNetwork> };
  expect(second.networks[IOT]).toMatchObject({ ntp: { enabled: false, redirect_hardcoded: true } });
});

// ---- scenarios

test('the step time, the wait for the next NTP query and the check NTP queried: made with the same fields, shown on the timeline and sent in the scenario', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);

  // a time step with a preset
  await page.getByTestId('add-step').click();
  const a = stepDialog(page, 'Add step');
  await expect(a.getByLabel('Type', { exact: true }).locator('option')).toHaveText(['Profile', 'Fault', 'DNS fault', 'TLS case', 'Time fault', 'DHCP action', 'Access rule', 'WireGuard action', 'Capture', 'Wait', 'Remove', 'Restore']);
  await a.getByLabel('Type', { exact: true }).selectOption('time');
  await expect(a.getByLabel('Acts on')).toContainText("The scenario's target (ESP32-42)");
  await a.getByLabel('Id', { exact: true }).fill('back-to-1970');
  await a.getByLabel('At', { exact: true }).fill('5s');
  await a.getByTestId('time-preset-1970').click();
  await a.getByLabel('Drift (ppm)').fill('fast');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a.getByLabel('Drift (ppm)')).toHaveAccessibleDescription(/Parts per million/);
  await a.getByLabel('Drift (ppm)').fill('500');
  await a.getByLabel('Destination', { exact: true }).selectOption('hostname');
  await a.getByLabel('Host name', { exact: true }).fill('time.google.com');
  await a.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(a).toBeHidden();
  await expect(step(page, 'back-to-1970').getByTestId('step-type')).toHaveText('Time fault');
  await expect(step(page, 'back-to-1970')).toContainText('Time 1970-01-01 00:00:00 UTC · skew 500 ppm · time.google.com');

  // a wait for the next NTP query: a time fault shows only when the device polls next
  await page.getByTestId('add-step').click();
  const w = stepDialog(page, 'Add step');
  await w.getByLabel('Type', { exact: true }).selectOption('wait');
  await w.getByLabel('Id', { exact: true }).fill('next-poll');
  await w.getByLabel('At', { exact: true }).fill('6s');
  await w.getByLabel('Wait until').selectOption('ntp_query_seen');
  await w.getByLabel('Timeout').fill('10m');
  await expect(w.getByLabel('Host name')).toHaveCount(0);
  await w.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(step(page, 'next-poll')).toContainText('Wait for the next NTP query of the target · timeout 10 min');

  // the check
  await page.getByTestId('add-check').click();
  const c = stepDialog(page, 'Add check');
  await expect(c.getByLabel('Check', { exact: true }).locator('option')).toHaveText(['Reconnected', 'No connection', 'Traffic seen', 'Traffic not seen', 'DNS query seen', 'TLS rejected', 'TLS accepted', 'NTP queried']);
  await c.getByLabel('Check', { exact: true }).selectOption('ntp_queried');
  await expect(c).toContainText('asks the gateway\'s NTP server for the time inside the window');
  await expect(c.getByLabel('Ports')).toHaveCount(0);
  await c.getByLabel('Opens when', { exact: true }).selectOption('back-to-1970');
  await c.getByLabel('Within').fill('10m');
  await c.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(c).toBeHidden();
  await expect(page.getByTestId('check').last()).toContainText('NTP queried: a query of the NTP server');

  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  const byId = (id: string) => sent.steps!.find((s) => s.id === id);
  expect(byId('back-to-1970')).toEqual({ id: 'back-to-1970', at: '5s', time: { destination: { hostname: 'time.google.com' }, absolute: '1970-01-01T00:00:00Z', skew_ppm: 500 } });
  expect(byId('next-poll')).toEqual({ id: 'next-poll', at: '6s', wait: { for: 'ntp_query_seen', timeout: '10m' } });
  expect(sent.checks!.at(-1)).toEqual({ ntp_queried: {}, window: { from: 'back-to-1970', within: '10m' } });
});

test('a time step is edited with the time fields; a gateway without the step type offers neither the step nor the wait nor the check', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  gw.config = {
    ...gw.config,
    scenarios: {
      ...gw.config.scenarios,
      [SCN_MQTT]: {
        ...gw.config.scenarios![SCN_MQTT]!,
        steps: [{ id: 'late', at: '0s', time: { destination: { cidr: '192.0.2.0/24' }, ports: [12300], protocol: 'udp', offset: '-1d' } }, { id: 'restore', at: '9s', restore: true }],
        checks: [{ ntp_queried: {}, window: { from: 'late', within: '5m' } }],
      },
    },
  };
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await expect(step(page, 'late')).toContainText('Time -1d · 192.0.2.0/24 · UDP 12300');
  await page.getByRole('button', { name: 'Edit step late' }).click();
  const d = stepDialog(page, 'Edit step');
  await expect(d.getByLabel('Offset')).toHaveValue('-1d');
  await expect(d.getByLabel('Destination', { exact: true })).toHaveValue('cidr');
  await expect(d.getByLabel('Address or prefix')).toHaveValue('192.0.2.0/24');
  await expect(d.getByLabel('Ports')).toHaveValue('12300');
  await d.getByLabel('Offset').fill('-2d');
  await d.getByRole('button', { name: 'Save to the changes' }).click();
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  expect(sent.steps![0]).toEqual({ id: 'late', at: '0s', time: { destination: { cidr: '192.0.2.0/24' }, ports: [12300], protocol: 'udp', offset: '-2d' } });
  await page.getByRole('button', { name: 'Back to editing' }).click();
  // an older gateway knows no time faults
  gw.capabilities.step_types = gw.capabilities.step_types.filter((t) => t !== 'time');
  gw.capabilities.check_types = gw.capabilities.check_types.filter((t) => t !== 'ntp_queried');
  await page.reload();
  await page.getByTestId('add-step').click();
  const n = stepDialog(page, 'Add step');
  await expect(n.getByLabel('Type', { exact: true }).locator('option')).not.toContainText(['Time fault']);
  await n.getByLabel('Type', { exact: true }).selectOption('wait');
  await expect(n.getByLabel('Wait until').locator('option')).toHaveText(['The target is online', 'A new connection is established', 'A DNS query for a name is seen']);
  await n.getByRole('button', { name: 'Cancel' }).click();
  await page.getByTestId('add-check').click();
  await expect(stepDialog(page, 'Add check').getByLabel('Check', { exact: true }).locator('option')).not.toContainText(['NTP queried']);
});
