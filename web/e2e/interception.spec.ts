import { expect, test, type Page } from '@playwright/test';
import type { InterceptFlow, Overlay, Scenario } from '../src/api/generated/model';
import { CreateOverlayBody } from '../src/api/generated/zod/overlays/overlays';
import { ESP32, MockGateway, SCN_MQTT, toast, withScenarios } from './mock';

// M23c (plan §2.17 "Interception UI"): the interception in the web UI against the mocked API. The case `intercept` with its settings
// and its list of HTTP rules is made in the Add fault dialog and as a `tls` step; the Interception screen lists the exchanges of the
// proxy with the filters of the API and updates itself; the test CA is downloaded there and replaced after a question; the device
// detail has a panel of the device's exchanges. Every request the forms send is checked against the spec's schema.

const ESP = ESP32;
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const CASE = 'f0000000-0000-4000-8000-0000000000a1';

const faultDialog = (page: Page) => page.getByRole('dialog', { name: /^(Add|Edit) fault$/ });
const kind = (page: Page, name: string) => faultDialog(page).getByRole('group', { name: 'Kind' }).getByRole('button', { name, exact: true });
const rule = (page: Page, n: number) => faultDialog(page).getByRole('group', { name: `Rule ${n}`, exact: true });

function sentOverlays(gw: MockGateway): unknown[] {
  const bodies = gw.requestsTo('POST', '/overlays').map((r) => r.body);
  for (const b of bodies) expect(CreateOverlayBody.safeParse(b).success, JSON.stringify(b)).toBe(true);
  return bodies;
}

function overlay(id: string, over: Partial<Overlay>): Overlay {
  return {
    id,
    kind: 'tls',
    owner: { type: 'user', id: 'admin', name: 'admin' },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    generation: 42,
    state: 'effective',
    ...over,
  };
}

/** An exchange of the proxy `n` seconds before `now`. */
function flow(n: number, over: Partial<InterceptFlow>): InterceptFlow {
  return {
    time: new Date(Date.now() - n * 1000).toISOString(),
    client: '10.10.0.42',
    original_dst: '203.0.113.10:443',
    protocol: 'http',
    method: 'GET',
    host: 'api.example.com',
    path: '/v1/status',
    status: 200,
    request_bytes: 120,
    response_bytes: 2048,
    duration_ms: 12.4,
    device: ESP,
    ...over,
  };
}

/** The proxy has seen eight exchanges of two devices and an unknown client; two of them were acted on. */
function withFlows(gw: MockGateway): MockGateway {
  gw.interceptFlows = [
    flow(70, { host: 'cdn.example.net', path: '/img/logo.png', response_bytes: 40960, device: SENSOR, client: '10.10.0.57' }),
    flow(60, { path: '/v1/a', status: 503, actions: ['status'], overlay: CASE, response_bytes: 0 }),
    flow(50, { protocol: 'tcp', method: undefined, path: undefined, host: 'broker.example.com', status: undefined, original_dst: '203.0.113.10:8883', request_bytes: 900, response_bytes: 300, device: undefined, client: '10.10.0.99' }),
    flow(40, { protocol: 'websocket', host: 'ws.example.com', path: '/socket', status: 101, messages: 7 }),
    flow(30, { path: '/v1/b', actions: ['delay', 'modify'], overlay: CASE, duration_ms: 2010 }),
    flow(20, { method: 'POST', path: '/v1/upload', status: undefined, actions: ['abort'], error: 'the device went away', overlay: CASE }),
    flow(10, { host: 'cdn.example.net', path: '/img/b.png', device: SENSOR, client: '10.10.0.57' }),
    flow(5, { path: '/v1/c', status: 429, actions: ['status'], overlay: CASE, response_bytes: 0 }),
  ];
  return gw;
}

// ---- the form

test('an interception case with several rules: made in the Add fault dialog, the checks under the fields, the order, the request', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await expect(d.getByTestId('intercept-fields')).toHaveCount(0);
  await d.getByLabel('Case', { exact: true }).selectOption('intercept');
  await expect(d.getByTestId('tls-hint')).toContainText('The interception proxy answers instead of the server');
  // the device does not trust the test CA: the note says what that means for the interception
  await expect(d.getByTestId('tls-trust')).toContainText("ESP32-42 does not trust the test CA: it rejects the proxy's certificate in the handshake");
  await expect(d.getByTestId('no-rules')).toContainText('the proxy only watches');
  await expect(d.getByLabel('Give the TLS secrets to the captures that run')).toBeChecked();
  await expect(d.getByLabel('Block QUIC (UDP 443)')).toBeChecked();
  await expect(d.getByLabel("Servers' certificates")).toHaveValue('');
  await expect(d.getByTestId('upstream-hint')).toContainText('Verify the server');
  await d.getByLabel("Servers' certificates").selectOption('insecure');
  await expect(d.getByTestId('upstream-hint')).toContainText('The server\'s certificate is not checked');
  await d.getByLabel('Give the TLS secrets to the captures that run').uncheck();

  // a first rule: answer 503 for a host and a path
  await d.getByRole('button', { name: 'Add rule' }).click();
  const r1 = rule(page, 1);
  await expect(r1.getByLabel('Action')).toHaveValue('block');
  await expect(r1.getByTestId('action-hint')).toContainText('Answers 403');
  await expect(r1.getByTestId('rule-summary')).toHaveText('every request → block (403)');
  await r1.getByLabel('Action').selectOption('status');
  await r1.getByLabel('Host', { exact: true }).fill('not a name!');
  await expect(r1.getByLabel('Host', { exact: true })).toHaveAccessibleDescription(/is no host name/);
  await r1.getByLabel('Host', { exact: true }).fill('api.example.com');
  await r1.getByLabel('Path starts with').fill('v1');
  await expect(r1.getByLabel('Path starts with')).toHaveAccessibleDescription(/A path starts with \//);
  await r1.getByLabel('Path starts with').fill('/v1/');
  await r1.getByLabel('Status code').fill('700');
  await expect(r1.getByLabel('Status code')).toHaveAccessibleDescription('A status code from 100 to 599, such as 503.');
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await r1.getByLabel('Status code').fill('503');
  await expect(r1.getByTestId('rule-summary')).toHaveText('api.example.com /v1/ → answer 503');

  // a second: delay everything of one method
  await d.getByRole('button', { name: 'Add rule' }).click();
  const r2 = rule(page, 2);
  await r2.getByLabel('Action').selectOption('delay');
  await expect(r2.getByLabel('Delay')).toHaveValue('1s');
  await r2.getByLabel('Delay').fill('11m');
  await expect(r2.getByLabel('Delay')).toHaveAccessibleDescription(/at most 10m/);
  await r2.getByLabel('Delay').fill('250ms');
  await r2.getByLabel('Method').fill('POST');

  // a third: throttle a host
  await d.getByRole('button', { name: 'Add rule' }).click();
  const r3 = rule(page, 3);
  await r3.getByLabel('Action').selectOption('throttle');
  await r3.getByLabel('Host', { exact: true }).fill('cdn.example.net');
  await r3.getByLabel('Rate').fill('3 bit');
  await expect(r3.getByLabel('Rate')).toHaveAccessibleDescription('At least 8 bit/s.');
  await r3.getByLabel('Rate').fill('64 kbit/s');

  // a fourth: modify headers and a text of the response
  await d.getByRole('button', { name: 'Add rule' }).click();
  const r4 = rule(page, 4);
  await r4.getByLabel('Action').selectOption('modify');
  await expect(r4.getByText('This rule changes nothing')).toBeVisible();
  await expect(d.getByRole('button', { name: 'Apply fault' })).toBeDisabled();
  await r4.getByRole('button', { name: 'Add a request header to rule 4' }).click();
  await r4.getByLabel('Request headers of rule 4, header 1 name').fill('X-Chaos');
  await expect(r4.getByText('X-Chaos has no value')).toBeVisible();
  await r4.getByLabel('Request headers of rule 4, header 1 value').fill('on');
  await r4.getByRole('button', { name: 'Add a request header to rule 4' }).click();
  await r4.getByLabel('Request headers of rule 4, header 2 name').fill('Cookie');
  await r4.getByLabel('Request headers of rule 4, header 2: take the header away').check();
  await expect(r4.getByLabel('Request headers of rule 4, header 2 value')).toBeDisabled();
  await r4.getByRole('button', { name: 'Add a response header to rule 4' }).click();
  await r4.getByLabel('Response headers of rule 4, header 1 name').fill('Cache-Control');
  await r4.getByLabel('Response headers of rule 4, header 1 value').fill('no-store');
  await r4.getByRole('button', { name: 'Add a replacement to rule 4' }).click();
  await r4.getByLabel('Replacement 1 of rule 4: search for').fill('ok');
  await r4.getByLabel('Replacement 1 of rule 4: replace with').fill('fail');
  await expect(r4.getByTestId('rule-summary')).toHaveText('every request → change 3 headers and 1 text in the body');

  // a fifth, then removed again; the rules before it are what is sent
  await d.getByRole('button', { name: 'Add rule' }).click();
  await rule(page, 5).getByRole('button', { name: 'Remove rule 5' }).click();
  await expect(d.getByTestId('http-rule')).toHaveCount(4);

  // the order: rule 4 moves above rule 3
  await d.getByRole('button', { name: 'Move rule 4 up' }).click();
  await expect(rule(page, 3).getByTestId('rule-summary')).toHaveText(/change 3 headers/);
  await expect(rule(page, 4).getByTestId('rule-summary')).toHaveText('cdn.example.net → throttle 64kbit/s');
  await expect(d.getByRole('button', { name: 'Move rule 1 up' })).toBeDisabled();
  await expect(d.getByRole('button', { name: 'Move rule 4 down' })).toBeDisabled();

  await d.getByLabel('Ports').fill('443');
  await d.getByLabel('Server names').fill('api.example.com, cdn.example.net');
  await d.getByLabel('Duration').selectOption({ label: '5 min' });
  await expect(d.getByTestId('fault-preview')).toHaveText(
    'Preview ESP32-42 · TLS connections on TCP 443 for api.example.com, cdn.example.net → the interception proxy with 4 HTTP rules · expires after 5 min',
  );
  await d.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(d.getByTestId('api-call')).toContainText('"http_rules"');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({
    target: { device: ESP },
    tls: {
      case: 'intercept',
      ports: [443],
      sni: ['api.example.com', 'cdn.example.net'],
      intercept: {
        keylog: false,
        upstream: 'insecure',
        http_rules: [
          { action: 'status', status: 503, match: { host: 'api.example.com', path_prefix: '/v1/' } },
          { action: 'delay', delay: '250ms', match: { method: 'POST' } },
          {
            action: 'modify',
            modify: {
              request_headers: { 'X-Chaos': 'on', Cookie: '' },
              response_headers: { 'Cache-Control': 'no-store' },
              response_body_replace: [{ search: 'ok', replace: 'fail' }],
            },
          },
          { action: 'throttle', rate: '64kbit', match: { host: 'cdn.example.net' } },
        ],
      },
    },
    ttl: '5m',
  });
  await expect(page.getByTestId('active-fault').filter({ hasText: 'TLS intercept' })).toContainText('4 HTTP rules · no key log · servers not verified');
});

test('an interception case is edited with its rules, and a rule that can never act is marked', async ({ page }) => {
  const gw = MockGateway.sample();
  const settings = {
    keylog: false,
    block_quic: false,
    upstream: 'own_ca' as const,
    http_rules: [
      { action: 'block' as const },
      { action: 'delay' as const, delay: '1s' },
      { action: 'modify' as const, modify: { response_headers: { Server: '' } } },
    ],
  };
  gw.overlays.push(overlay(CASE, { target: { device: ESP }, tls: { case: 'intercept', ports: [443], intercept: settings }, ttl: '1h', expires_at: new Date(Date.now() + 3_000_000).toISOString() }));
  await gw.install(page);
  await page.goto('/faults');
  await page.getByTestId('fault-family-tls').getByRole('button', { name: /^Edit / }).click();
  const d = faultDialog(page);
  await expect(d.getByLabel('Case', { exact: true })).toHaveValue('intercept');
  await expect(d.getByLabel('Give the TLS secrets to the captures that run')).not.toBeChecked();
  await expect(d.getByLabel('Block QUIC (UDP 443)')).not.toBeChecked();
  await expect(d.getByLabel("Servers' certificates")).toHaveValue('own_ca');
  await expect(d.getByTestId('http-rule')).toHaveCount(3);
  // the first rule blocks every request: the two after it never act
  await expect(rule(page, 1).getByTestId('rule-unreachable')).toHaveCount(0);
  await expect(rule(page, 2).getByTestId('rule-unreachable')).toContainText('never acts');
  await expect(rule(page, 3).getByTestId('rule-unreachable')).toContainText('never acts');
  await expect(rule(page, 3).getByLabel('Response headers of rule 3, header 1 name')).toHaveValue('Server');
  await expect(rule(page, 3).getByLabel('Response headers of rule 3, header 1: take the header away')).toBeChecked();
  // the rule above is limited to one host and the others act again
  await rule(page, 1).getByLabel('Host', { exact: true }).fill('api.example.com');
  await expect(d.getByTestId('rule-unreachable')).toHaveCount(0);
  await rule(page, 2).getByRole('button', { name: 'Remove rule 2' }).click();
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  const body = sentOverlays(gw)[0] as { tls: { intercept: unknown } };
  expect(body.tls.intercept).toEqual({
    keylog: false,
    block_quic: false,
    upstream: 'own_ca',
    http_rules: [{ action: 'block', match: { host: 'api.example.com' } }, { action: 'modify', modify: { response_headers: { Server: '' } } }],
  });
  // the key of the case is its selection, so it is the same overlay
  expect(gw.overlays.filter((o) => o.kind === 'tls')).toHaveLength(1);
});

test('the gateway\'s refusal of a rule stands under the field of that rule', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.rejectOverlay = [
    { path: '/tls/intercept/http_rules/1/delay', code: 'invalid_duration', message: 'the delay must be longer than zero and at most 10m0s' },
    { path: '/tls/intercept/http_rules/0/modify/request_headers/X-Chaos', code: 'invalid_tls_case', message: '"X-Chaos" is not a header name' },
    { path: '/tls/intercept/upstream', code: 'enum', message: 'must be one of verify, own_ca, insecure' },
  ];
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await d.getByLabel('Case', { exact: true }).selectOption('intercept');
  await d.getByRole('button', { name: 'Add rule' }).click();
  await rule(page, 1).getByLabel('Action').selectOption('modify');
  await rule(page, 1).getByRole('button', { name: 'Add a request header to rule 1' }).click();
  await rule(page, 1).getByLabel('Request headers of rule 1, header 1 name').fill('X-Chaos');
  await rule(page, 1).getByLabel('Request headers of rule 1, header 1 value').fill('on');
  await d.getByRole('button', { name: 'Add rule' }).click();
  await rule(page, 2).getByLabel('Action').selectOption('delay');
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(rule(page, 2).getByLabel('Delay')).toHaveAccessibleDescription('the delay must be longer than zero and at most 10m0s');
  await expect(rule(page, 1).getByText('"X-Chaos" is not a header name')).toBeVisible();
  await expect(d.getByLabel("Servers' certificates")).toHaveAccessibleDescription('must be one of verify, own_ca, insecure');
});

test('a gateway without the interception does not offer the case for a new case', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.capabilities = { ...gw.capabilities, features: gw.capabilities.features.filter((f) => f !== 'tls.intercept') };
  await gw.install(page);
  await page.goto('/faults');
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  await kind(page, 'TLS').click();
  await expect(faultDialog(page).getByLabel('Case', { exact: true }).locator('option')).toHaveCount(8);
  await page.getByRole('button', { name: 'Cancel' }).click();
  await page.goto('/interception');
  await expect(page.getByTestId('intercept-unsupported')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add interception' })).toBeDisabled();
  expect(gw.requestsTo('GET', '/intercept/flows')).toHaveLength(0);
});

test('keyboard: a rule is added, filled and moved without the mouse', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Add fault' }).first().click();
  const d = faultDialog(page);
  await kind(page, 'TLS').click();
  await d.getByLabel('Case', { exact: true }).selectOption('intercept');
  const add = d.getByRole('button', { name: 'Add rule' });
  await add.focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  await expect(d.getByTestId('http-rule')).toHaveCount(2);
  const second = rule(page, 2);
  await second.getByLabel('Action').focus();
  await page.keyboard.press('ArrowDown'); // the select moves to the next action: status
  await expect(second.getByLabel('Status code')).toBeVisible();
  await second.getByLabel('Status code').fill('429');
  const up = d.getByRole('button', { name: 'Move rule 2 up' });
  await up.focus();
  await page.keyboard.press('Space');
  await expect(rule(page, 1).getByTestId('rule-summary')).toHaveText('every request → answer 429');
  await d.getByRole('button', { name: 'Apply fault' }).focus();
  await page.keyboard.press('Enter');
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  const body = sentOverlays(gw)[0] as { tls: { intercept: { http_rules: unknown[] } } };
  expect(body.tls.intercept.http_rules).toEqual([{ action: 'status', status: 429 }, { action: 'block' }]);
});

test('the step tls with the case intercept: its rules are made in the step dialog and sent in the scenario', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  await gw.install(page);
  await page.goto(`/scenarios/${SCN_MQTT}`);
  await page.getByTestId('add-step').click();
  const t = page.getByRole('dialog', { name: 'Add step' });
  await t.getByLabel('Type', { exact: true }).selectOption('tls');
  await t.getByLabel('Id', { exact: true }).fill('mitm');
  await t.getByLabel('At', { exact: true }).fill('4s');
  await t.getByLabel('Case', { exact: true }).selectOption('intercept');
  await t.getByRole('button', { name: 'Add rule' }).click();
  await t.getByRole('group', { name: 'Rule 1', exact: true }).getByLabel('Action').selectOption('delay');
  await t.getByRole('group', { name: 'Rule 1', exact: true }).getByLabel('Delay').fill('soon');
  await t.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(t.getByRole('group', { name: 'Rule 1', exact: true }).getByLabel('Delay')).toHaveAccessibleDescription(/longer than zero/);
  await t.getByRole('group', { name: 'Rule 1', exact: true }).getByLabel('Delay').fill('2s');
  await t.getByRole('group', { name: 'Rule 1', exact: true }).getByLabel('Host', { exact: true }).fill('api.example.com');
  await t.getByRole('button', { name: 'Add to the changes' }).click();
  await expect(t).toBeHidden();
  await expect(page.locator('[data-testid="timeline-step"][data-step="mitm"]')).toContainText('TLS intercept · 1 HTTP rule');
  await page.getByRole('button', { name: 'Preview & apply' }).click();
  const sent = (gw.requestsTo('POST', '/revisions')[0]!.body as { scenarios: Record<string, Scenario> }).scenarios[SCN_MQTT]!;
  expect(sent.steps!.find((s) => s.id === 'mitm')).toEqual({
    id: 'mitm',
    at: '4s',
    tls: { case: 'intercept', intercept: { http_rules: [{ action: 'delay', delay: '2s', match: { host: 'api.example.com' } }] } },
  });
});

// ---- the screen

test('the exchanges: the table, the details of a row, the filters in the address, and the empty states', async ({ page }) => {
  const gw = withFlows(MockGateway.sample());
  gw.overlays.push(overlay(CASE, { target: { device: ESP }, tls: { case: 'intercept', ports: [443], intercept: { http_rules: [{ action: 'status', status: 503 }] } }, ttl: '1h', expires_at: new Date(Date.now() + 3_000_000).toISOString() }));
  await gw.install(page);
  await page.goto('/');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Interception' }).click();
  await expect(page).toHaveURL(/\/interception$/);
  await expect(page.getByRole('heading', { name: 'Interception', exact: true })).toBeVisible();

  // the case in force, listed as faults are
  await expect(page.getByTestId('active-fault')).toContainText('TLS intercept · 443 · 1 HTTP rule');
  const rows = page.getByTestId('flow-row');
  await expect(rows).toHaveCount(8);
  await expect(page.getByTestId('flow-count')).toHaveText('8 exchanges, newest first');
  // newest first: the last exchange of the log is the first row
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/v1/c');
  await expect(rows.first().getByTestId('flow-status')).toHaveText('429');
  await expect(rows.first().getByTestId('flow-acted')).toHaveText('Answered');
  await expect(rows.first().getByTestId('flow-device')).toHaveText('ESP32-42');
  await expect(rows.first().getByTestId('flow-kind')).toHaveText('HTTP');
  // what a rule did is a word, and a stop is told from an impairment by its tone as well
  await expect(rows.nth(2).getByTestId('flow-acted').locator('[data-action="abort"]')).toHaveText('Aborted');
  await expect(rows.nth(2).getByTestId('flow-status')).toHaveText('no answer');
  await expect(rows.nth(3).getByTestId('flow-acted')).toHaveText('DelayedModified');
  await expect(rows.nth(3).getByTestId('flow-duration')).toHaveText('2.0 s');
  // a WebSocket and a stream that is no HTTP, whose client no device has
  await expect(rows.nth(4).getByTestId('flow-kind')).toHaveText('WebSocket');
  await expect(rows.nth(4).getByTestId('flow-size')).toContainText('7 messages');
  await expect(rows.nth(5).getByTestId('flow-kind')).toHaveText('Stream');
  await expect(rows.nth(5).getByTestId('flow-request')).toHaveText('broker.example.com');
  await expect(rows.nth(5).getByTestId('flow-status')).toHaveText('no HTTP');
  await expect(rows.nth(5).getByTestId('flow-device')).toHaveText('10.10.0.99');
  await expect(rows.nth(6).getByTestId('flow-acted')).toHaveText('Answered');
  // a row opens its details
  await expect(page.getByTestId('flow-details')).toHaveCount(0);
  await rows.nth(2).getByRole('button', { name: 'Details' }).click();
  const details = rows.nth(2).getByTestId('flow-details');
  await expect(details).toContainText('203.0.113.10:443');
  await expect(details).toContainText('/v1/upload');
  await expect(rows.nth(2).getByTestId('flow-error')).toHaveText('the device went away');
  // and stays open on its exchange when a new one arrives above it
  gw.interceptFlows.push(flow(0, { path: '/v1/new', status: 200 }));
  await gw.emit(page, { type: 'http_fault_triggered', data: { host: 'api.example.com' } });
  await expect(rows).toHaveCount(9);
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/v1/new');
  await expect(rows.nth(3).getByTestId('flow-details')).toBeVisible();

  // the filters: the device, the host (a name, a suffix), only what a rule acted on; each is a request and a part of the address
  await page.getByLabel('Device', { exact: true }).selectOption({ label: 'Sensor-07' });
  await expect(rows).toHaveCount(2);
  await expect(page).toHaveURL(new RegExp(`device=${SENSOR}`));
  await expect(page.getByTestId('flow-device').first()).toHaveText('Sensor-07');
  await page.getByLabel('Device', { exact: true }).selectOption({ label: 'All devices' });
  await page.getByLabel('Host', { exact: true }).fill('not a name!');
  await expect(page.getByLabel('Host', { exact: true })).toHaveAccessibleDescription(/is no host name/);
  await page.getByLabel('Host', { exact: true }).fill('*.example.net');
  await expect(rows).toHaveCount(2);
  await expect(page).toHaveURL(/host=\*\.example\.net/);
  await page.getByLabel('Host', { exact: true }).fill('');
  await page.getByLabel('Only exchanges a rule acted on').check();
  await expect(rows).toHaveCount(4);
  await expect(page).toHaveURL(/faulted=1/);
  await expect.poll(() => gw.requestsTo('GET', '/intercept/flows').at(-1)?.query['faulted']).toBe('true');
  // nothing matches: the table says so and offers to clear the filters
  await page.getByLabel('Host', { exact: true }).fill('nobody.example.org');
  await expect(page.getByTestId('flows-none-match')).toContainText('No exchange matches these filters');
  await page.getByRole('button', { name: 'Clear the filters' }).click();
  await expect(rows).toHaveCount(9);
  await expect(page).toHaveURL(/\/interception$/);

  // a link with filters opens on them
  await page.goto(`/interception?device=${SENSOR}&faulted=1`);
  await expect(page.getByLabel('Device', { exact: true })).toHaveValue(SENSOR);
  await expect(page.getByLabel('Only exchanges a rule acted on')).toBeChecked();
  await expect(page.getByTestId('flows-none-match')).toBeVisible();

  // the address is the filters' own: the navigation entry clears them, and Back brings them again
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Interception' }).click();
  await expect(page).toHaveURL(/\/interception$/);
  await expect(page.getByLabel('Device', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Only exchanges a rule acted on')).not.toBeChecked();
  await expect(rows).toHaveCount(9);
  await page.goBack();
  await expect(page.getByLabel('Device', { exact: true })).toHaveValue(SENSOR);
  await expect(page.getByLabel('Only exchanges a rule acted on')).toBeChecked();
});

test('an empty log says why, and a proxy that does not run says so', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.health = { status: 'healthy', components: [{ name: 'tls_proxy', status: 'disabled', detail: 'the interception proxy is not deployed: no case of the kind intercept can work' }] };
  await gw.install(page);
  await page.goto('/interception');
  await expect(page.getByTestId('flows-empty')).toContainText('No exchange yet');
  await expect(page.getByTestId('no-cases')).toContainText('No interception case is in force');
  await expect(page.getByTestId('proxy-problem')).toHaveText('the interception proxy is not deployed: no case of the kind intercept can work');
  // the dialog that adds a case starts as one
  await page.getByTestId('add-interception').click();
  const d = faultDialog(page);
  await expect(d.getByLabel('Case', { exact: true })).toHaveValue('intercept');
  await expect(d.getByRole('group', { name: 'Kind' }).getByRole('button', { name: 'TLS', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await d.getByLabel('Target').selectOption({ label: 'Device · ESP32-42' });
  await d.getByRole('button', { name: 'Apply fault' }).click();
  await expect(toast(page, 'Fault active on ESP32-42')).toBeVisible();
  expect(sentOverlays(gw)[0]).toEqual({ target: { device: ESP }, tls: { case: 'intercept' }, ttl: '15m' });
  await expect(page.getByTestId('active-fault')).toContainText('TLS intercept');
  await expect(page.getByTestId('no-cases')).toHaveCount(0);
});

test('the table is live: it follows the gateway, can be paused, and shows older exchanges on request', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.interceptFlows = Array.from({ length: 130 }, (_, i) => flow(1000 - i, { path: `/p/${i}` }));
  await gw.install(page);
  await page.goto('/interception');
  const rows = page.getByTestId('flow-row');
  await expect(rows).toHaveCount(100);
  await expect(page.getByTestId('flow-live')).toHaveText('Live');
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/p/129');
  // the first request asks for a page of 100
  expect(gw.requestsTo('GET', '/intercept/flows')[0]!.query['limit']).toBe('100');
  await page.getByTestId('flow-more').click();
  await expect(rows).toHaveCount(130);
  await expect(page.getByTestId('flow-more')).toHaveCount(0);
  expect(gw.requestsTo('GET', '/intercept/flows').some((r) => r.query['limit'] === '200')).toBe(true);

  // a rule acted: the event reads the log at once
  gw.interceptFlows.push(flow(0, { path: '/p/new', actions: ['block'], status: 403 }));
  await gw.emit(page, { type: 'http_fault_triggered', data: {} });
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/p/new');
  // paused: the gateway goes on, the table holds still, and it catches up on resume
  await page.getByTestId('flow-pause').click();
  await expect(page.getByTestId('flow-live')).toHaveText('Paused');
  gw.interceptFlows.push(flow(0, { path: '/p/while-paused' }));
  await gw.emit(page, { type: 'http_fault_triggered', data: {} });
  await expect(page.getByTestId('flow-count')).toHaveText('131 exchanges, newest first');
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/p/new');
  await page.getByTestId('flow-pause').click();
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/p/while-paused');
  await expect(page.getByTestId('flow-live')).toHaveText('Live');
});

test('the exchanges that cannot be loaded say so', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.failing['/intercept/flows'] = 500;
  await gw.install(page);
  await page.goto('/interception');
  await expect(page.getByTestId('load-error').filter({ hasText: 'The exchanges are not loaded' })).toBeVisible();
  await expect(page.getByTestId('flows-empty')).toHaveCount(0);
});

// ---- the test CA

test('the test CA: downloaded from the screen, and replaced only after the question says what it costs', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await page.goto('/interception');
  const card = page.getByRole('region', { name: 'Test CA' });
  const download = page.waitForEvent('download');
  await card.getByRole('link', { name: 'Download the test CA' }).click();
  expect((await download).suggestedFilename()).toBe('chaos-gateway-test-ca.pem');

  // a question first; Cancel changes nothing
  await card.getByRole('button', { name: 'Replace the test CA…' }).click();
  const q = page.getByRole('dialog', { name: 'Replace the test CA?' });
  await expect(q).toContainText('trusts nothing the gateway shows it until it installs the new one');
  await expect(q).toContainText('recorded in the audit log');
  await q.getByRole('button', { name: 'Cancel' }).click();
  await expect(q).toBeHidden();
  expect(gw.requestsTo('POST', '/tls/ca/regenerate')).toHaveLength(0);
  await expect(page.getByTestId('ca-replaced')).toHaveCount(0);

  // a refusal stays in the dialog
  gw.rejectRegenerate = { status: 403, code: 'forbidden', detail: 'this token has not the scope full' };
  await card.getByRole('button', { name: 'Replace the test CA…' }).click();
  await q.getByTestId('ca-regenerate-confirm').click();
  await expect(q.getByRole('alert')).toHaveText('this token has not the scope full');
  expect(gw.regenerated).toBe(0);

  // and the replacement itself: the toast, the reminder and the new certificate to download
  await q.getByTestId('ca-regenerate-confirm').click();
  await expect(toast(page, 'The test CA is replaced')).toBeVisible();
  await expect(q).toBeHidden();
  expect(gw.regenerated).toBe(1);
  await expect(page.getByTestId('ca-replaced')).toContainText('Every device that installed the old certificate trusts nothing');
  await page.getByRole('button', { name: /^<\/> API/ }).click();
  await expect(page.getByText('POST /api/v1/tls/ca/regenerate').first()).toBeVisible();
});

test('a session without the scope full cannot replace the test CA, and says why', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.sessionScope = 'overlays';
  await gw.install(page);
  await page.goto('/interception');
  await expect(page.getByTestId('ca-regenerate')).toBeDisabled();
  await expect(page.getByTestId('ca-needs-full')).toBeVisible();
  // the download is for every reader
  await expect(page.getByRole('link', { name: 'Download the test CA' })).toBeVisible();
});

// ---- the device detail

test('the device detail has a panel of the exchanges of the device while a case acts on it, and links to the filtered screen', async ({ page }) => {
  const gw = withFlows(MockGateway.sample());
  gw.overlays.push(overlay(CASE, { target: { device: ESP }, tls: { case: 'intercept', ports: [443] }, ttl: '1h', expires_at: new Date(Date.now() + 3_000_000).toISOString() }));
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'HTTP exchanges' });
  await expect(panel).toBeVisible();
  const rows = panel.getByTestId('flow-row');
  await expect(rows).toHaveCount(5);
  await expect(rows.first().getByTestId('flow-request')).toHaveText('GET api.example.com/v1/c');
  await expect(panel.getByTestId('flow-device')).toHaveCount(0);
  expect(gw.requestsTo('GET', '/intercept/flows').at(-1)!.query).toMatchObject({ device: ESP, limit: '8' });
  await panel.getByTestId('device-exchanges-all').click();
  await expect(page).toHaveURL(new RegExp(`/interception\\?device=${ESP}`));
  await expect(page.getByLabel('Device', { exact: true })).toHaveValue(ESP);
  await expect(page.getByTestId('flow-row')).toHaveCount(5);
});

test('a device that no case acts on and the proxy never saw has no panel of exchanges', async ({ page }) => {
  const gw = withFlows(MockGateway.sample());
  await gw.install(page);
  await page.goto('/devices/a1b2c3d4-0000-4000-8000-000000000059');
  await expect(page.getByRole('heading', { name: 'Sensor-09' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'HTTP exchanges' })).toHaveCount(0);
  // a case that acts on a device without an exchange yet says what the panel waits for
  gw.overlays.push(overlay(CASE, { target: { device: 'a1b2c3d4-0000-4000-8000-000000000059' }, tls: { case: 'intercept' } }));
  await page.goto('/devices/a1b2c3d4-0000-4000-8000-000000000059');
  await expect(page.getByTestId('device-exchanges-empty')).toContainText('The proxy has seen no request of Sensor-09 yet');
});
