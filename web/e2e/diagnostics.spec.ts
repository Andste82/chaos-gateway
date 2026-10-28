import { expect, test, type Page } from '@playwright/test';
import type { DiagnosticRequest, DiagnosticResult } from '../src/api/generated/model';
import { defaultDiagnostic, MockGateway, tlsOf } from './mock';

// M24 (plan §2.12, §2.17 "Diagnostics"): the Diagnostics screen against the mocked API: every type of diagnostic with the
// request it sends and the result it shows, the failures (a path that stops at the TLS step, a refused connection, a
// certificate that does not verify, a gateway that refuses the request), the checks under the fields, the link of a device's
// page, the runs of the session, and a gateway without diagnostics.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';

async function open(gw: MockGateway, page: Page, path = '/diagnostics') {
  await gw.install(page);
  await page.goto(path);
}

const type = (page: Page, name: string) => page.getByRole('group', { name: 'Diagnostic' }).getByRole('button', { name }).click();
const run = (page: Page) => page.getByTestId('run-diagnostic');
const sent = (gw: MockGateway) => gw.requestsTo('POST', '/diagnostics').map((r) => r.body as DiagnosticRequest);
const failing = (r: DiagnosticRequest, over: Partial<DiagnosticResult>): DiagnosticResult => ({ ...defaultDiagnostic(r, new Date().toISOString()), ok: false, ...over });

test('the navigation entry Diagnostics is a link to the screen, which asks to run something', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/');
  const nav = page.getByRole('navigation', { name: 'Main' });
  await nav.getByRole('link', { name: 'Diagnostics' }).click();
  await expect(page).toHaveURL(/\/diagnostics$/);
  await expect(page.getByRole('heading', { name: 'Diagnostics', level: 1 })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Diagnostics' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByTestId('empty-state')).toContainText('Nothing run yet');
  await expect(run(page)).toBeDisabled();
  await expect(page.getByTestId('diagnostic-hint')).toContainText('DNS, route, TCP and TLS');
});

test('a path runs the four steps and shows each of them', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await page.getByLabel('Host').fill('broker.example.com');
  await expect(run(page)).toBeDisabled(); // the port is needed
  await page.getByLabel('Port').fill('8883');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Succeeded');
  const steps = page.getByTestId('diagnostic-steps').getByRole('listitem');
  await expect(steps).toHaveCount(4);
  for (const [i, name] of ['DNS', 'Route', 'TCP', 'TLS'].entries()) await expect(steps.nth(i)).toContainText(`${name}`);
  await expect(steps.nth(0)).toHaveAttribute('data-state', 'ok');
  await expect(steps.nth(3)).toContainText('TLS 1.3');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('DNS ✓  Route ✓  TCP ✓  TLS ✓');
  await expect(page.getByTestId('diagnostic-tls')).toContainText('verifies');
  expect(sent(gw)).toEqual([{ type: 'path', host: 'broker.example.com', port: 8883 }]);
});

test('a path that stops at the TLS step says which step and why', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnose = (r) =>
    failing(r, {
      error: 'tls: certificate has expired',
      path: [
        { step: 'dns', ok: true, detail: 'broker.example.com is 203.0.113.10' },
        { step: 'route', ok: true, detail: 'a route exists' },
        { step: 'tcp', ok: true, detail: 'connected to port 8883 in 1.0 ms' },
        { step: 'tls', ok: false, detail: 'certificate has expired' },
      ],
      tls: tlsOf(false),
    });
  await open(gw, page, '/diagnostics?type=path&host=broker.example.com&port=8883');
  await expect(page.getByLabel('Host')).toHaveValue('broker.example.com');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Failed');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('tls: certificate has expired');
  await expect(page.locator('[data-step="tls"]')).toHaveAttribute('data-state', 'failed');
  await expect(page.locator('[data-step="tls"]')).toContainText('certificate has expired');
  await expect(page.locator('[data-step="tcp"]')).toHaveAttribute('data-state', 'ok');
  await expect(page.getByTestId('diagnostic-verified')).toHaveText('does not verify: certificate has expired');
  await expect(page.getByTestId('diagnostic-certificate')).toContainText('CN=broker.example.com');
});

test('a path stops at the first step that fails and lists the others as not run', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnose = (r) => failing(r, { error: 'dns: cannot resolve gone.test: NXDOMAIN', path: [{ step: 'dns', ok: false, detail: 'cannot resolve gone.test: NXDOMAIN' }] });
  await open(gw, page);
  await page.getByLabel('Host').fill('gone.test');
  await page.getByLabel('Port').fill('8883');
  await run(page).click();
  await expect(page.locator('[data-step="dns"]')).toHaveAttribute('data-state', 'failed');
  for (const s of ['route', 'tcp', 'tls']) await expect(page.locator(`[data-step="${s}"]`)).toHaveAttribute('data-state', 'not run');
});

test('a path without the TLS step lists three steps', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await page.getByLabel('Host').fill('10.10.0.20');
  await page.getByLabel('Port').fill('1883');
  await page.getByTestId('diagnostic-advanced').click();
  await page.getByLabel('Check TLS as the last step').uncheck();
  await run(page).click();
  await expect(page.getByTestId('diagnostic-steps').getByRole('listitem')).toHaveCount(3);
  expect(sent(gw)).toEqual([{ type: 'path', host: '10.10.0.20', port: 1883, tls: false }]);
});

test('ping shows the loss and the round trip times, and sends the echoes and the timeout of the advanced fields', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnose = (r) => ({ ...defaultDiagnostic(r, new Date().toISOString()), ping: { sent: 10, received: 8, loss_percent: 20, rtt_min_ms: 11.2, rtt_avg_ms: 52.4, rtt_max_ms: 180, rtt_mdev_ms: 40.123 } });
  await open(gw, page);
  await type(page, 'Ping');
  await expect(page.getByLabel('Port')).toHaveCount(0);
  await page.getByLabel('Host').fill('203.0.113.10');
  await page.getByTestId('diagnostic-advanced').click();
  await page.getByLabel('Echoes').fill('10');
  await page.getByLabel('Timeout').fill('2s');
  await run(page).click();
  const ping = page.getByTestId('diagnostic-ping');
  await expect(ping).toContainText('Loss20 %');
  await expect(ping).toContainText('Received8');
  await expect(ping).toContainText('Average52.4 ms');
  await expect(ping).toContainText('Maximum180 ms');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('8 of 10 replies, 20 % loss, average 52.4 ms');
  expect(sent(gw)).toEqual([{ type: 'ping', host: '203.0.113.10', count: 10, timeout: '2s' }]);
});

test('a ping nobody answers fails with the gateway’s sentence and shows no round trip times', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnose = (r) => failing(r, { error: 'no reply to 5 echoes within 5s', ping: { sent: 5, received: 0, loss_percent: 100 } });
  await open(gw, page, '/diagnostics?type=ping&host=203.0.113.99');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Failed');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('no reply to 5 echoes within 5s');
  await expect(page.getByTestId('diagnostic-ping')).not.toContainText('Average');
});

test('TCP and UDP check a port; a refused connection is a failed result', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await type(page, 'TCP');
  await page.getByLabel('Host').fill('203.0.113.10');
  await page.getByLabel('Port').fill('9000');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-connect')).toContainText('Reachableyes');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('Connected in 1.20 ms');

  gw.diagnose = (r) => failing(r, { error: 'connection refused', connect: { reachable: false, detail: 'connection refused' } });
  await page.getByLabel('Port').fill('9999');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state').first()).toHaveText('Failed');
  await expect(page.getByTestId('diagnostic-connect')).toContainText('Reachableno');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('connection refused');

  gw.diagnose = undefined;
  await type(page, 'UDP');
  await page.getByTestId('diagnostic-advanced').click();
  await page.getByLabel('Datagram').fill('ping');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-connect')).toContainText('reply of 4 bytes');
  expect(sent(gw).map((r) => r.type)).toEqual(['tcp', 'tcp', 'udp']);
  expect(sent(gw)[2]).toEqual({ type: 'udp', host: '203.0.113.10', port: 9999, payload: 'ping' });
});

test('DNS asks the gateway’s proxy for a name, or the server it is given', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await type(page, 'DNS');
  await expect(page.getByLabel('Port')).toHaveCount(0);
  await page.getByLabel('Name to resolve').fill('broker.example.com');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-dns')).toContainText('203.0.113.10');
  await expect(page.getByTestId('diagnostic-dns')).toContainText('NOERROR');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('203.0.113.10 · NOERROR in 3.40 ms');

  gw.diagnose = (r) => failing(r, { error: 'NXDOMAIN', dns: { name: r.dns_name, server: '10.10.0.1:5353', rcode: 'NXDOMAIN', answers: [], rtt_ms: 2 } });
  await page.getByLabel('Server').fill('10.10.0.1');
  await page.getByLabel('Port').fill('5353');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('NXDOMAIN');
  await expect(page.getByTestId('diagnostic-dns')).toContainText('10.10.0.1:5353');
  expect(sent(gw)).toEqual([
    { type: 'dns', host: 'broker.example.com' },
    { type: 'dns', host: '10.10.0.1', dns_name: 'broker.example.com', port: 5353 },
  ]);
});

test('HTTP shows the status and the headers; a status of 400 and above is a failure', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await type(page, 'HTTP(S)');
  await expect(page.getByLabel('Host', { exact: true })).toHaveCount(0);
  await page.getByLabel('URL').fill('https://api.example.com:8443/health');
  await run(page).click();
  const http = page.getByTestId('diagnostic-http');
  await expect(http).toContainText('Status200');
  await expect(http.getByRole('table', { name: 'Response headers' })).toContainText('Content-Type');
  await expect(http.getByRole('table', { name: 'Response headers' })).toContainText('testbed');

  gw.diagnose = (r) => failing(r, { error: 'HTTP 503 Service Unavailable', http: { status: 503, rtt_ms: 9, headers: { Server: 'testbed' } } });
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state').first()).toHaveText('Failed');
  await expect(page.getByTestId('diagnostic-http')).toContainText('Status503');
  await expect(page.getByTestId('diagnostic-sentence')).toHaveText('HTTP 503 Service Unavailable');
  expect(sent(gw)[0]).toEqual({ type: 'http', host: 'api.example.com', url: 'https://api.example.com:8443/health' });
});

test('TLS shows the handshake and the chain; a certificate that does not verify is the failure', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await type(page, 'TLS');
  await page.getByLabel('Host').fill('203.0.113.10');
  await page.getByLabel('Port').fill('8883');
  await page.getByTestId('diagnostic-advanced').click();
  await page.getByLabel('SNI').fill('broker.example.com');
  await run(page).click();
  const tls = page.getByTestId('diagnostic-tls');
  await expect(tls).toContainText('TLS 1.3');
  await expect(tls).toContainText('TLS_AES_128_GCM_SHA256');
  await expect(tls).toContainText('h2');
  await expect(page.getByTestId('diagnostic-verified')).toHaveText('verifies');
  await expect(page.getByTestId('diagnostic-certificate')).toContainText('issued by CN=Chaos Gateway test CA,O=Chaos Gateway');
  await expect(page.getByTestId('diagnostic-certificate')).toContainText('valid 2026-10-01 to 2027-10-01 · for broker.example.com');

  gw.diagnose = (r) => failing(r, { error: 'certificate has expired', tls: tlsOf(false) });
  await run(page).click();
  await expect(page.getByTestId('diagnostic-sentence').first()).toHaveText('certificate has expired');
  await expect(page.getByTestId('diagnostic-verified')).toHaveText('does not verify: certificate has expired');
  expect(sent(gw)[0]).toEqual({ type: 'tls', host: '203.0.113.10', port: 8883, sni: 'broker.example.com' });
});

test('every message stands under its field and Run waits for a valid form', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page);
  await page.getByLabel('Host').fill('http://broker');
  await expect(page.getByText('An IPv4 address or a host name, without a port or a path.')).toBeVisible();
  await expect(page.getByLabel('Host')).toHaveAttribute('aria-invalid', 'true');
  await page.getByLabel('Host').fill('broker.example.com');
  await page.getByLabel('Port').fill('70000');
  await expect(page.getByText('A port is a number from 1 to 65535.')).toBeVisible();
  await page.getByLabel('Port').fill('8883');
  await expect(run(page)).toBeEnabled();
  await page.getByTestId('diagnostic-advanced').click();
  await page.getByLabel('Timeout').fill('90s');
  await expect(page.getByText('At most 60s.')).toBeVisible();
  await expect(run(page)).toBeDisabled();
  await page.getByLabel('Timeout').fill('soon');
  await expect(page.getByText('A duration like 5s or 500ms.')).toBeVisible();
  await page.getByLabel('Timeout').fill('');

  await type(page, 'Ping');
  await page.getByLabel('Echoes').fill('101');
  await expect(page.getByText('A number from 1 to 100.')).toBeVisible();
  await page.getByLabel('Echoes').fill('100');
  await page.getByLabel('Timeout').fill('41s');
  await expect(page.getByText('The echoes and the timeout take longer than 60s.')).toBeVisible();

  await type(page, 'HTTP(S)');
  await page.getByLabel('URL').fill('ftp://x.test/');
  await expect(page.getByText('An http:// or https:// URL')).toBeVisible();
  await page.getByLabel('URL').fill('https://user:pw@x.test/');
  await expect(page.getByText('An http:// or https:// URL')).toBeVisible();
  await page.getByLabel('URL').fill('https://x.test/');
  await page.getByLabel('Timeout').fill('');
  await expect(run(page)).toBeEnabled();
  expect(sent(gw)).toHaveLength(0);
});

test('the gateway’s refusal is put under the field it names; another one stands as the result', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnose = () => ({ refuse: { status: 422, code: 'validation_failed', detail: 'the request body is not valid', errors: [{ path: '/port', code: 'missing_field', message: 'the type path needs a port' }] } });
  await open(gw, page);
  await page.getByLabel('Host').fill('broker.example.com');
  await page.getByLabel('Port').fill('8883');
  await run(page).click();
  await expect(page.getByLabel('Port')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText('the type path needs a port')).toBeVisible();
  await page.getByLabel('Port').fill('8884');
  await expect(page.getByText('the type path needs a port')).toBeHidden();

  gw.diagnose = () => ({ refuse: { status: 422, code: 'capacity_exceeded', detail: '8 diagnostics run already: wait for one to end' } });
  await run(page).click();
  await expect(page.getByTestId('diagnostic-refused')).toHaveText('8 diagnostics run already: wait for one to end');
});

test('a diagnostic under way shows it, and the screen keeps the runs of the session', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.diagnosticDelay = 400;
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=9000');
  await run(page).click();
  await expect(run(page)).toHaveText('Running…');
  await expect(run(page)).toBeDisabled();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Succeeded');
  gw.diagnosticDelay = 0;
  await page.getByLabel('Port').fill('9001');
  await run(page).click();
  const history = page.getByTestId('diagnostic-history');
  await expect(history.getByRole('button')).toHaveCount(2);
  await expect(history.getByRole('button').first()).toContainText('203.0.113.10:9001');
  // an earlier run is shown again
  await history.getByRole('button').nth(1).click();
  await expect(page.getByTestId('diagnostic-result')).toContainText('203.0.113.10:9000');
  // and stays when the page is left and opened again
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Devices' }).click();
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Diagnostics' }).click();
  await expect(page.getByTestId('diagnostic-history').getByRole('button')).toHaveCount(2);
  await page.getByRole('button', { name: 'Clear' }).click();
  await expect(page.getByTestId('empty-state')).toContainText('Nothing run yet');
});

test('the session list says why a run has no result: refused, gateway error, no answer (P6-AUDIT-45)', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=9000');
  gw.diagnose = () => ({ refuse: { status: 422, code: 'capacity_exceeded', detail: '8 diagnostics run already: wait for one to end' } });
  await run(page).click();
  await expect(page.getByTestId('diagnostic-refused')).toBeVisible();
  gw.diagnose = () => ({ refuse: { status: 500, code: 'internal', detail: 'the diagnostic failed inside the gateway' } });
  await run(page).click();
  await expect(page.getByTestId('diagnostic-refused')).toHaveText('the diagnostic failed inside the gateway');
  // the connection drops while the diagnostic runs
  await page.route('**/api/v1/diagnostics', (route) => route.abort());
  await run(page).click();
  const rows = page.getByTestId('diagnostic-history').getByRole('button');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('No answer');
  await expect(rows.nth(1)).toContainText('Gateway error');
  await expect(rows.nth(2)).toContainText('Refused');
});

test('a message under a collapsed Advanced section stands below its button, and Run points to it (P6-AUDIT-46)', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=9000');
  const advanced = page.getByTestId('diagnostic-advanced');
  await advanced.click();
  await page.getByLabel('Timeout').fill('abc');
  await expect(page.getByText('A duration like 5s or 500ms.')).toBeVisible();
  await expect(page.getByTestId('diagnostic-advanced-errors')).toHaveCount(0); // the message stands under its field
  await advanced.click();
  await expect(page.getByLabel('Timeout')).toHaveCount(0);
  const errors = page.getByTestId('diagnostic-advanced-errors');
  await expect(errors).toHaveText('Timeout: A duration like 5s or 500ms.');
  await expect(run(page)).toBeDisabled();
  await expect(run(page)).toHaveAttribute('aria-describedby', (await errors.getAttribute('id'))!);
  // opened again, the message is under the field and the list is gone; a valid timeout lets Run through
  await advanced.click();
  await expect(errors).toHaveCount(0);
  await page.getByLabel('Timeout').fill('3s');
  await advanced.click();
  await expect(errors).toHaveCount(0);
  await expect(run(page)).toBeEnabled();
  await expect(run(page)).not.toHaveAttribute('aria-describedby');
});

test('the end of a run is announced by a status that was there before (P6-AUDIT-49)', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=9000');
  const status = page.getByTestId('diagnostic-announcement');
  await expect(status).toHaveAttribute('role', 'status');
  await expect(status).toHaveText('');
  await run(page).click();
  await expect(status).toHaveText('Succeeded: Connected in 1.20 ms');
  gw.diagnose = (r) => failing(r, { error: 'connection refused' });
  await run(page).click();
  await expect(status).toHaveText('Failed: connection refused');
  gw.diagnose = () => ({ refuse: { status: 422, code: 'capacity_exceeded', detail: '8 diagnostics run already: wait for one to end' } });
  await run(page).click();
  await expect(status).toHaveText('Refused: 8 diagnostics run already: wait for one to end');
});

test('the device page links to a ping of the device’s address', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, `/devices/${ESP}`);
  await page.getByTestId('diagnose-device').click();
  await expect(page).toHaveURL(/\/diagnostics\?type=ping&host=10\.10\.0\.42$/);
  await expect(page.getByRole('group', { name: 'Diagnostic' }).getByRole('button', { name: 'Ping' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByLabel('Host')).toHaveValue('10.10.0.42');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-ping')).toContainText('Loss0 %');
  expect(sent(gw)).toEqual([{ type: 'ping', host: '10.10.0.42' }]);
});

test('a device without an address has no link to diagnose', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/devices/a1b2c3d4-0000-4000-8000-000000000059');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByTestId('diagnose-device')).toHaveCount(0);
});

test('the API panel shows the request the form sends and the request that ran', async ({ page }) => {
  const gw = MockGateway.sample();
  await open(gw, page, '/diagnostics?type=tcp&host=203.0.113.10&port=9000');
  await page.getByRole('button', { name: '</> API' }).click();
  const panel = page.getByTestId('api-panel');
  await expect(panel).toContainText('This screen: the diagnostics');
  await expect(panel.getByTestId('api-call')).toContainText('POST /api/v1/diagnostics');
  await expect(panel.getByTestId('api-call')).toContainText('"port": 9000');
  await run(page).click();
  await expect(page.getByTestId('diagnostic-state')).toHaveText('Succeeded');
  await expect(panel).toContainText('Last action: Run a diagnostic');
});

test('a gateway without diagnostics says so', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.capabilities = { ...gw.capabilities, features: gw.capabilities.features.filter((f) => f !== 'diagnostics') };
  await open(gw, page);
  await expect(page.getByTestId('diagnostics-unsupported')).toContainText('no diagnostics');
  await expect(run(page)).toHaveCount(0);
});
