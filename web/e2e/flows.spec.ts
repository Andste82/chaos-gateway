import { expect, test } from '@playwright/test';
import type { DnsQueryLogEntry, Explanation, Flow } from '../src/api/generated/model';
import { IOT, MockGateway } from './mock';

// M26 (plan §2.17 "Flows", "Device detail"; `docs/open-items.md` P3-M12-02, P3-AUDIT-37, P31-M35a-08): the flow view, a device's
// flows and recent DNS queries, the path view with and without a fault on it, and the source behind the uplink or the management
// network in the explain card.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const SENSOR = 'a1b2c3d4-0000-4000-8000-000000000057';
const DNS_FAULT = 'f0000000-0000-4000-8000-0000000000d1';

const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();

const MQTT: Flow = {
  id: 'tcp/10.10.0.42:45566>203.0.113.10:8883',
  device: ESP,
  network: IOT,
  protocol: 'tcp',
  src: '10.10.0.42',
  sport: 45566,
  dst: '203.0.113.10',
  dport: 8883,
  dst_hostname: 'broker.example.com',
  state: 'ESTABLISHED',
  started_at: ago(300),
  upload: { packets: 60, bytes: 4120 },
  download: { packets: 40, bytes: 5_000_000 },
};
const HTTPS: Flow = {
  id: 'tcp/10.10.0.42:45577>192.0.2.5:443',
  device: ESP,
  network: IOT,
  protocol: 'tcp',
  src: '10.10.0.42',
  sport: 45577,
  dst: '192.0.2.5',
  dport: 443,
  state: 'TIME_WAIT',
  started_at: ago(120),
  upload: { packets: 4, bytes: 900 },
  download: { packets: 4, bytes: 1500 },
};
const NTP: Flow = {
  id: 'udp/10.10.0.42:123>192.0.2.123:123',
  device: ESP,
  network: IOT,
  protocol: 'udp',
  src: '10.10.0.42',
  sport: 123,
  dst: '192.0.2.123',
  dport: 123,
  started_at: ago(60),
  upload: { packets: 1, bytes: 76 },
  download: { packets: 1, bytes: 76 },
};
const SENSOR_DNS: Flow = {
  id: 'udp/10.10.0.57:5000>8.8.8.8:53',
  device: SENSOR,
  network: IOT,
  protocol: 'udp',
  src: '10.10.0.57',
  sport: 5000,
  dst: '8.8.8.8',
  dport: 53,
  service: 'dns_proxy',
  started_at: ago(30),
};
/** A peer behind the uplink that connected to the forward esp-web: a flow of the target device. */
const FORWARDED: Flow = {
  id: 'tcp/198.51.100.7:51000>192.168.1.57:8080',
  device: ESP,
  network: IOT,
  protocol: 'tcp',
  src: '198.51.100.7',
  sport: 51000,
  dst: '192.168.1.57',
  dport: 8080,
  forwarded: true,
  state: 'ESTABLISHED',
  started_at: ago(20),
  upload: { packets: 3, bytes: 300 },
  download: { packets: 3, bytes: 4200 },
};

function lab(): MockGateway {
  const gw = MockGateway.sample();
  gw.flows = [MQTT, HTTPS, NTP, SENSOR_DNS, FORWARDED];
  return gw;
}

const FORWARD_EXPLANATION: Explanation = {
  generation: 42,
  source: { ip: '198.51.100.7' },
  destination: { ip: '192.168.1.57' },
  access: { verdict: 'allow', layer: 'port_forward', reason: 'no rule decided; the forward esp-web accepts it' },
  forward: {
    id: 'f0000000-0000-4000-8000-00000000f001',
    name: 'esp-web',
    entry: 'uplink',
    target_ip: '10.10.0.42',
    target_port: 80,
    device: { id: ESP, name: 'ESP32-42' },
    status: { state: 'active', target_address: '10.10.0.42' },
    note: 'faults do not apply to forwarded connections (M35b)',
  },
  faults: [],
};

const plain: Explanation = { generation: 42, source: { ip: '10.10.0.42' }, access: { verdict: 'allow', layer: 'access_matrix' }, faults: [] };

test('the Flows screen lists the connections newest first with what they carry, where they go and the bytes they moved', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Flows' }).click();
  await expect(page.getByRole('heading', { name: 'Flows', exact: true })).toBeVisible();
  const rows = page.getByTestId('connection-row');
  await expect(rows).toHaveCount(5);
  await expect(page.getByTestId('flows-count')).toHaveText('5 connections, newest first');
  // newest first: the forwarded one (20 s), the DNS (30 s), NTP, HTTPS, MQTT (300 s)
  await expect(rows.nth(0).getByTestId('connection-service')).toHaveText('TCP');
  await expect(rows.nth(1).getByTestId('connection-service')).toHaveText('DNS');
  await expect(rows.nth(4).getByTestId('connection-service')).toHaveText('MQTT/TLS');

  const mqtt = rows.nth(4);
  await expect(mqtt.getByTestId('connection-device').getByRole('link', { name: 'ESP32-42' })).toHaveAttribute('href', `/devices/${ESP}`);
  await expect(mqtt.getByTestId('connection-device')).toContainText('IoT');
  await expect(mqtt.getByTestId('connection-from')).toHaveText('10.10.0.42:45566');
  await expect(mqtt.getByTestId('connection-to')).toHaveText('broker.example.com:8883');
  await expect(mqtt.getByTestId('connection-state')).toHaveText('established');
  await expect(mqtt.getByTestId('connection-up')).toHaveText('4.1 KB');
  await expect(mqtt.getByTestId('connection-down')).toHaveText('5.0 MB');
  await expect(mqtt.getByTestId('connection-age')).toHaveText('5 min ago');
  await expect(rows.nth(2).getByTestId('connection-state')).toHaveText('udp');
  await expect(rows.nth(3).getByTestId('connection-state')).toHaveText('time wait');
  // a connection that came in through a forward says so, with the peer it came from
  await expect(rows.nth(0).getByTestId('connection-forwarded')).toHaveText('FORWARDED');
  await expect(rows.nth(0).getByTestId('connection-from')).toHaveText('198.51.100.7:51000');
  await expect(rows.nth(0).getByTestId('connection-to')).toContainText('192.168.1.57:8080');
  await expect(rows.nth(4).getByTestId('connection-forwarded')).toHaveCount(0);
  expect(gw.requestsTo('GET', '/flows').length).toBeGreaterThan(0);
});

test('the filters of the flow view select by device, text, protocol and port forward, and stay in the address', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/flows');
  const rows = page.getByTestId('connection-row');
  await expect(rows).toHaveCount(5);

  // the device is the gateway's filter
  await page.getByLabel('Device').selectOption({ label: 'Sensor-07' });
  await expect(rows).toHaveCount(1);
  await expect(page).toHaveURL(new RegExp(`device=${SENSOR}`));
  expect(gw.requestsTo('GET', '/flows').some((r) => r.query.device === SENSOR)).toBe(true);
  await page.getByLabel('Device').selectOption({ label: 'All devices' });
  await expect(rows).toHaveCount(5);

  // the text is matched here, over names, addresses, ports and services
  await page.getByLabel('Search').fill('BROKER');
  await expect(rows).toHaveCount(1);
  await expect(page).toHaveURL(/q=BROKER/);
  await page.getByLabel('Search').fill('ntp');
  await expect(rows).toHaveCount(1);
  await expect(rows.first().getByTestId('connection-to')).toHaveText('192.0.2.123:123');
  await page.getByLabel('Search').fill('');
  await page.getByLabel('Protocol').selectOption('udp');
  await expect(rows).toHaveCount(2);
  await page.getByLabel('Protocol').selectOption('');
  await page.getByLabel('Only forwarded connections').check();
  await expect(rows).toHaveCount(1);
  await expect(rows.first().getByTestId('connection-forwarded')).toBeVisible();
  await expect(page).toHaveURL(/forwarded=1/);

  // nothing matches: the empty state offers to clear the filters
  await page.getByLabel('Search').fill('no such thing');
  await expect(page.getByTestId('flows-none-match')).toContainText('No connection matches these filters.');
  await page.getByTestId('flows-none-match').getByRole('button', { name: 'Clear the filters' }).click();
  await expect(rows).toHaveCount(5);
  await expect(page.getByLabel('Search')).toHaveValue('');
});

test('a link with the filters in its address lands on the filtered list', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/flows?device=${ESP}&protocol=tcp&q=443`);
  await expect(page.getByLabel('Device')).toHaveValue(ESP);
  await expect(page.getByLabel('Protocol')).toHaveValue('tcp');
  await expect(page.getByLabel('Search')).toHaveValue('443');
  await expect(page.getByTestId('connection-row')).toHaveCount(1);
  await expect(page.getByTestId('connection-to')).toHaveText('192.0.2.5:443');
});

test('Pause holds the table while the gateway goes on, and Resume reads it again', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/flows');
  await expect(page.getByTestId('connection-row')).toHaveCount(5);
  await expect(page.getByTestId('flows-live')).toContainText('Live');
  await page.getByTestId('flows-pause').click();
  await expect(page.getByTestId('flows-live')).toContainText('Paused');
  gw.flows = [...gw.flows, { ...NTP, id: 'udp/10.10.0.42:124>192.0.2.124:123', dst: '192.0.2.124', started_at: ago(1) }];
  const before = gw.requestsTo('GET', '/flows').length;
  await page.waitForTimeout(3_500);
  expect(gw.requestsTo('GET', '/flows')).toHaveLength(before); // nothing is read while it is paused
  await expect(page.getByTestId('connection-row')).toHaveCount(5);
  await page.getByTestId('flows-pause').click();
  await expect(page.getByTestId('connection-row')).toHaveCount(6, { timeout: 8_000 });
});

test('a read of the live table that fails says it is not updating, and keeps the rows it has (P6-AUDIT-47)', async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto('/flows');
  await expect(page.getByTestId('connection-row')).toHaveCount(5);
  await expect(page.getByTestId('flows-live')).toContainText('Live');
  gw.failing = { '/flows': 500 };
  await expect(page.getByTestId('flows-live')).toContainText('Not updating', { timeout: 15_000 });
  await expect(page.getByTestId('load-error')).toContainText('The connections are not updating: the gateway failed to read /flows.');
  await expect(page.getByTestId('connection-row')).toHaveCount(5);
  // the gateway answers again: Try again reads the table and the dot is Live
  gw.failing = {};
  await page.getByTestId('load-error').getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByTestId('flows-live')).toContainText('Live');
  await expect(page.getByTestId('load-error')).toHaveCount(0);
});

test('Show more on the last page leaves the focus on the count line, not on the page start (P6-AUDIT-48)', async ({ page }) => {
  const gw = lab();
  gw.flows = Array.from({ length: 230 }, (_, i) => ({ ...NTP, id: `udp/10.10.0.42:${2000 + i}>192.0.2.123:123`, sport: 2000 + i, started_at: ago(10 + i) }));
  await gw.install(page);
  await page.goto('/flows');
  await expect(page.getByTestId('connection-row')).toHaveCount(200);
  const more = page.getByTestId('flows-more');
  await more.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('connection-row')).toHaveCount(230);
  await expect(more).toHaveCount(0);
  await expect(page.getByTestId('flows-count')).toBeFocused();
});

test('a connection without a device says unknown host, with no link, and the network under it where it is known (plan §2.17)', async ({ page }) => {
  const gw = lab();
  // a source in the subnet of a test network that no device has the address of
  const STRANGER: Flow = { ...NTP, id: 'udp/10.10.0.99:4000>192.0.2.123:123', device: undefined, src: '10.10.0.99', sport: 4000, started_at: ago(5) };
  const BEYOND: Flow = { ...NTP, id: 'udp/10.77.0.9:4001>192.0.2.123:123', device: undefined, network: undefined, src: '10.77.0.9', sport: 4001, started_at: ago(4) };
  gw.flows = [MQTT, STRANGER, BEYOND];
  await gw.install(page);
  await page.goto('/flows');
  const rows = page.getByTestId('connection-row');
  await expect(rows).toHaveCount(3);
  // newest first: the one of no network, the stranger, the known device
  for (const [i, from] of [[0, '10.77.0.9:4001'], [1, '10.10.0.99:4000']] as const) {
    const cell = rows.nth(i).getByTestId('connection-device');
    await expect(cell).toContainText('unknown host');
    await expect(cell.getByRole('link')).toHaveCount(0);
    await expect(rows.nth(i).getByTestId('connection-from')).toHaveText(from);
    await expect(rows.nth(i).getByRole('link', { name: /^Explain the connection/ })).toBeVisible();
  }
  await expect(rows.nth(1).getByTestId('connection-device')).toContainText('IoT');
  await expect(rows.nth(0).getByTestId('connection-device')).not.toContainText('IoT');
  await expect(rows.nth(2).getByTestId('connection-device').getByRole('link', { name: 'ESP32-42' })).toBeVisible();
});

test('200 connections are drawn and Show more adds 200 each time, and the list is kept while the table is read again (plan §2.17)', async ({ page }) => {
  const gw = lab();
  gw.flows = Array.from({ length: 450 }, (_, i) => ({ ...NTP, id: `udp/10.10.0.42:${2000 + i}>192.0.2.123:123`, sport: 2000 + i, started_at: ago(10 + i) }));
  await gw.install(page);
  await page.goto('/flows');
  const rows = page.getByTestId('connection-row');
  const more = page.getByTestId('flows-more');
  await expect(rows).toHaveCount(200);
  await expect(page.getByTestId('flows-count')).toHaveText('450 connections, newest first');
  await more.click();
  await expect(rows).toHaveCount(400);
  await expect(more).toBeVisible();
  // the table is read every 3 s: the 400 rows stay, the button too
  const reads = gw.requestsTo('GET', '/flows').length;
  await expect.poll(() => gw.requestsTo('GET', '/flows').length, { timeout: 15_000 }).toBeGreaterThan(reads);
  await expect(rows).toHaveCount(400);
  await more.click();
  await expect(rows).toHaveCount(450);
  await expect(more).toHaveCount(0);
});

test('the flow view says when there is no connection and when it cannot read them', async ({ page }) => {
  const gw = lab();
  gw.flows = [];
  await gw.install(page);
  await page.goto('/flows');
  await expect(page.getByTestId('flows-empty')).toContainText('No connection is tracked');
  gw.failing['/flows'] = 500;
  await page.reload();
  await expect(page.getByRole('alert')).toContainText('The connections are not loaded');
});

test("the device detail lists the device's connections, a forwarded one included, and links to all of them", async ({ page }) => {
  const gw = lab();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Flows' });
  const rows = panel.getByTestId('connection-row');
  await expect(rows).toHaveCount(4); // not the sensor's
  expect(gw.requestsTo('GET', '/flows').some((r) => r.query.device === ESP)).toBe(true);
  await expect(rows.nth(3).getByTestId('connection-service')).toHaveText('MQTT/TLS');
  await expect(rows.nth(3).getByTestId('connection-to')).toHaveText('broker.example.com:8883');
  await expect(rows.nth(0).getByTestId('connection-forwarded')).toBeVisible();
  await expect(rows.nth(0)).toContainText('from 198.51.100.7:51000');
  await expect(panel.getByRole('columnheader', { name: 'Device' })).toHaveCount(0);
  await panel.getByTestId('device-flows-all').click();
  await expect(page).toHaveURL(new RegExp(`/flows\\?device=${ESP}`));
  await expect(page.getByLabel('Device')).toHaveValue(ESP);
});

test('the device detail shows only the newest connections and says how many more there are', async ({ page }) => {
  const gw = lab();
  gw.flows = Array.from({ length: 11 }, (_, i) => ({ ...NTP, id: `udp/10.10.0.42:${2000 + i}>192.0.2.123:123`, sport: 2000 + i, started_at: ago(10 + i) }));
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByRole('region', { name: 'Flows' }).getByTestId('connection-row')).toHaveCount(8);
  await expect(page.getByTestId('device-flows-more')).toContainText('3 more');
});

test('a device without connections says so, and so does one without queries', async ({ page }) => {
  const gw = lab();
  gw.flows = [];
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('device-flows-empty')).toContainText('The gateway tracks no connection of ESP32-42 right now.');
  await expect(page.getByTestId('device-dns-empty')).toContainText('The DNS proxy has logged no query of ESP32-42 yet.');
  await expect(page.getByTestId('path-empty')).toContainText('tracks none of them right now');
});

function queries(): DnsQueryLogEntry[] {
  // oldest first, as the mock keeps them
  return [
    { time: ago(50), client: '10.10.0.42', device: ESP, protocol: 'udp', name: 'typo.example.com', type: 'A', rcode: 'NXDOMAIN', duration_ms: 12.4 },
    { time: ago(40), client: '10.10.0.42', device: ESP, protocol: 'udp', name: 'broker.example.com', type: 'A', rcode: 'NOERROR', answers: ['203.0.113.10'], duration_ms: 1.4 },
    { time: ago(30), client: '10.10.0.42', device: ESP, protocol: 'udp', name: 'broker.example.com', type: 'A', rcode: 'NOERROR', answers: ['10.9.9.9'], duration_ms: 0.4, fault: DNS_FAULT, cached: false },
    { time: ago(20), client: '10.10.0.42', device: ESP, protocol: 'udp', name: 'slow.example.com', type: 'AAAA', rcode: 'TIMEOUT', duration_ms: 5000 },
    { time: ago(10), client: '10.10.0.57', device: SENSOR, protocol: 'udp', name: 'sensor.example.com', type: 'A', rcode: 'NOERROR', answers: ['192.0.2.77'], duration_ms: 2 },
  ];
}

test("the device detail lists the device's recent DNS queries, an answer a fault changed and one that failed marked in words", async ({ page }) => {
  const gw = lab();
  gw.dnsQueries = queries();
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const panel = page.getByRole('region', { name: 'Recent DNS queries' });
  const rows = panel.getByTestId('device-dns-query');
  await expect(rows).toHaveCount(4); // not the sensor's
  const req = gw.requestsTo('GET', '/dns/queries');
  expect(req[0]!.query).toMatchObject({ device: ESP, limit: '10' });
  // newest first
  await expect(rows.nth(0)).toContainText('slow.example.com');
  await expect(rows.nth(0).getByTestId('device-dns-answer')).toHaveText('no answer');
  await expect(rows.nth(0).getByTestId('device-dns-note')).toHaveText('TIMEOUT');
  await expect(rows.nth(0)).toHaveAttribute('data-tone', 'block');
  await expect(rows.nth(1).getByTestId('device-dns-answer')).toHaveText('10.9.9.9');
  await expect(rows.nth(1).getByTestId('device-dns-note')).toHaveText('changed by a DNS fault');
  await expect(rows.nth(1)).toHaveAttribute('data-tone', 'fault');
  await expect(rows.nth(2).getByTestId('device-dns-answer')).toHaveText('203.0.113.10');
  await expect(rows.nth(2).getByTestId('device-dns-note')).toHaveCount(0);
  await expect(rows.nth(2)).toHaveAttribute('data-tone', 'normal');
  await expect(rows.nth(2)).toContainText('1.4 ms');
  await expect(rows.nth(3).getByTestId('device-dns-answer')).toHaveText('NXDOMAIN');
});

test('the device detail says when the DNS queries cannot be read', async ({ page }) => {
  const gw = lab();
  gw.failing['/dns/queries'] = 500;
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByRole('region', { name: 'Recent DNS queries' }).getByRole('alert')).toContainText('The DNS queries are not loaded');
});

// ---- the path view

test('the path view draws the device, the gateway, the uplink and the destination that moved the most, blue when no fault is on it', async ({ page }) => {
  const gw = lab();
  gw.explainWith = () => plain;
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  const path = page.getByTestId('path');
  await expect(path).toBeVisible();
  await expect(path.getByTestId('path-device')).toHaveText('ESP32-42');
  await expect(path.getByTestId('path-gateway')).toHaveText('Gateway');
  await expect(path.getByTestId('path-uplink')).toHaveText('Uplink');
  await expect(path.getByTestId('path-destination')).toHaveText('MQTT/TLS broker.example.com:8883');
  await expect(path).toHaveAttribute('aria-label', 'The path from ESP32-42 to MQTT/TLS broker.example.com:8883');
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'normal');
  await expect(page.getByTestId('path-badge')).toHaveCount(0);
  await expect(page.getByTestId('path-sentence')).toHaveText("No fault acts on the way: ESP32-42's traffic to MQTT/TLS broker.example.com:8883 passes unimpaired.");
  await expect(page.getByTestId('path-fault')).toHaveCount(0);
  // the question asked of the gateway is the path's: the device, the destination, the protocol and the port
  const asked = gw.requestsTo('GET', '/explain');
  expect(asked[0]!.query).toEqual({ device: ESP, src: '10.10.0.42', dst: '203.0.113.10', protocol: 'tcp', port: '8883' });
});

test('the path view carries the fault badge of the faults that win for the path', async ({ page }) => {
  const gw = lab();
  gw.explainWith = () => ({
    ...plain,
    faults: [
      { family: 'impairment', winner: { family: 'impairment', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000001', level: 2, summary: 'upload 200 ms ± 50 ms, download 200 ms ± 50 ms' } },
      { family: 'mtu', winner: { family: 'mtu', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000002', summary: 'mtu 1280' } },
    ],
  });
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'fault');
  await expect(page.getByTestId('path-badge')).toHaveText('2 faults');
  await expect(page.getByTestId('path-sentence')).toHaveText('Faults act on the way from ESP32-42 to MQTT/TLS broker.example.com:8883.');
  const faults = page.getByTestId('path-fault');
  await expect(faults).toHaveCount(2);
  await expect(faults.nth(0)).toContainText('Impairment');
  await expect(faults.nth(0)).toContainText('upload 200 ms ± 50 ms, download 200 ms ± 50 ms');
  await expect(faults.nth(1)).toContainText('MTU');
  await expect(faults.nth(1)).toContainText('mtu 1280');
});

test('a DNS or DHCP fault is listed apart and does not turn the path orange (P6-AUDIT-50)', async ({ page }) => {
  const gw = lab();
  gw.explainWith = () => ({
    ...plain,
    faults: [
      { family: 'dns', winner: { family: 'dns', layer: 'overlay', id: DNS_FAULT, name: 'slow-dns' } },
      { family: 'dhcp', winner: { family: 'dhcp', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000003', summary: 'dhcp short_lease 30s' } },
    ],
  });
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'normal');
  await expect(page.getByTestId('path-badge')).toHaveCount(0);
  await expect(page.getByTestId('path-sentence')).toContainText('No fault acts on the way');
  await expect(page.getByTestId('path-fault')).toHaveCount(0);
  const other = page.getByTestId('path-other');
  await expect(other).toHaveCount(2);
  await expect(other.nth(0)).toContainText('slow-dns');
  await expect(other.nth(1)).toContainText('dhcp short_lease 30s');
});

test('a blocked path says that the faults on it take effect only if a rule allows the traffic, and a reset is not a drop (P6-AUDIT-53)', async ({ page }) => {
  const gw = lab();
  gw.explainWith = () => ({
    ...plain,
    access: { verdict: 'reset', layer: 'config_rule', rule: 'a0000000-0000-4000-8000-000000000001' },
    faults: [{ family: 'impairment', winner: { family: 'impairment', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000001', summary: 'latency 200 ms' } }],
  });
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'block');
  await expect(page.getByTestId('path-badge')).toHaveText('Reset by a configured rule');
  await expect(page.getByTestId('path-sentence')).toContainText('Reset by a configured rule: ESP32-42 cannot reach');
  await expect(page.getByTestId('path-faults-note')).toHaveText('These faults take effect only if a rule allows the traffic.');
  await expect(page.getByTestId('path-fault')).toHaveCount(1);
});

test('the badge of the path names a single fault, and an access rule that stops the path turns it red', async ({ page }) => {
  const gw = lab();
  gw.explainWith = (q) =>
    q.dst === '192.0.2.5'
      ? { ...plain, access: { verdict: 'drop', layer: 'config_rule', rule: 'a0000000-0000-4000-8000-000000000001' } }
      : { ...plain, faults: [{ family: 'impairment', winner: { family: 'impairment', layer: 'config', id: 'c0f00000-0000-4000-8000-000000000007', name: 'sensor7-slow', summary: 'download rate 64kbit' } }] };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await expect(page.getByTestId('path-badge')).toHaveText('download rate 64kbit');
  await expect(page.getByTestId('path-sentence')).toContainText('A fault acts on the way');
  // the choice shows the destination that is drawn before anything is chosen (P6-AUDIT-54)
  await expect(page.getByTestId('path-target').locator('option:checked')).toHaveText('MQTT/TLS broker.example.com:8883');
  // another destination of the device's connections is another question
  await page.getByTestId('path-target').selectOption({ label: 'HTTPS 192.0.2.5:443' });
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'block');
  await expect(page.getByTestId('path-badge')).toHaveText('Dropped by a configured rule');
  await expect(page.getByTestId('path-sentence')).toHaveText('Dropped by a configured rule: ESP32-42 cannot reach HTTPS 192.0.2.5:443.');
  await expect(page.getByTestId('path-destination')).toHaveText('HTTPS 192.0.2.5:443');
  const last = gw.requestsTo('GET', '/explain').at(-1)!;
  expect(last.query).toEqual({ device: ESP, src: '10.10.0.42', dst: '192.0.2.5', protocol: 'tcp', port: '443' });
  // a datagram flow is asked for with its protocol and port
  await page.getByTestId('path-target').selectOption({ label: 'NTP 192.0.2.123:123' });
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'fault');
  expect(gw.requestsTo('GET', '/explain').at(-1)!.query).toEqual({ device: ESP, src: '10.10.0.42', dst: '192.0.2.123', protocol: 'udp', port: '123' });
});

test('the path view follows a fault that is applied or expires, and does not offer a forwarded connection as a destination', async ({ page }) => {
  const gw = lab();
  let faulted = false;
  gw.explainWith = () =>
    faulted ? { ...plain, faults: [{ family: 'impairment', winner: { family: 'impairment', layer: 'overlay', id: 'f0000000-0000-4000-8000-000000000001', summary: 'loss 20%' } }] } : plain;
  gw.flows = [FORWARDED];
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  // only a connection that came in through a forward: no path out of the device to draw
  await expect(page.getByTestId('path-empty')).toBeVisible();
  gw.flows = [FORWARDED, MQTT];
  await expect(page.getByTestId('path')).toBeVisible({ timeout: 8_000 });
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'normal');
  faulted = true;
  await expect(page.getByTestId('path-line')).toHaveAttribute('data-tone', 'fault', { timeout: 8_000 });
  await expect(page.getByTestId('path-badge')).toHaveText('loss 20%');
});

// ---- the explain card

test('the explain card explains an address behind the uplink to the uplink address, the way a port forward carries a connection', async ({ page }) => {
  const gw = lab();
  gw.explanation = FORWARD_EXPLANATION;
  await gw.install(page);
  await page.goto('/faults');
  const explain = page.getByRole('region', { name: 'Explain' });
  await expect(explain.getByLabel('Source', { exact: true })).toHaveValue('device');
  await explain.getByLabel('Source', { exact: true }).selectOption({ label: 'An address behind the uplink' });
  await expect(explain.getByLabel('Device')).toHaveCount(0);
  // the destination defaults to the address of the uplink interface
  await expect(explain.getByLabel('Destination')).toHaveValue('192.168.1.57');
  await expect(explain.getByTestId('explain-entry-hint')).toContainText('behind the uplink');
  await expect(explain.getByTestId('explain-entry-hint')).toContainText('192.168.1.57');
  await expect(explain.getByRole('button', { name: 'Explain' })).toBeDisabled();
  await explain.getByLabel('Source address').fill('not an address');
  await expect(explain.getByText('An IPv4 address, for example 198.51.100.7.')).toBeVisible();
  await expect(explain.getByRole('button', { name: 'Explain' })).toBeDisabled();
  await explain.getByLabel('Source address').fill('198.51.100.7');
  await explain.getByLabel('Protocol').selectOption('tcp');
  await explain.getByLabel('Port').fill('8080');
  await explain.getByRole('button', { name: 'Explain' }).click();
  const result = page.getByTestId('explain-result');
  await expect(result.getByTestId('explain-forward')).toContainText('port forward esp-web from the uplink to ESP32-42:80');
  await expect(result).toContainText('by the port forward (no rule decided)');
  const asked = gw.requestsTo('GET', '/explain');
  expect(asked).toHaveLength(1);
  expect(asked[0]!.query).toEqual({ src: '198.51.100.7', dst: '192.168.1.57', protocol: 'tcp', port: '8080' });
});

test('the explain card offers the management network with its own address, keeps a destination that was typed and goes back to a device', async ({ page }) => {
  const gw = lab();
  gw.explanation = plain;
  await gw.install(page);
  await page.goto('/faults');
  const explain = page.getByRole('region', { name: 'Explain' });
  await explain.getByLabel('Source', { exact: true }).selectOption({ label: 'An address behind the management network' });
  await expect(explain.getByLabel('Destination')).toHaveValue('192.168.56.10');
  await expect(explain.getByTestId('explain-entry-hint')).toContainText('behind the management network');
  // the entry side's address is the default only: what was typed stays
  await explain.getByLabel('Destination').fill('203.0.113.99');
  await explain.getByLabel('Source', { exact: true }).selectOption({ label: 'An address behind the uplink' });
  await expect(explain.getByLabel('Destination')).toHaveValue('203.0.113.99');
  // and any other address has no default and no hint
  await explain.getByLabel('Source', { exact: true }).selectOption({ label: 'Another address' });
  await expect(explain.getByTestId('explain-entry-hint')).toHaveCount(0);
  await explain.getByLabel('Source address').fill('10.10.0.99');
  await explain.getByRole('button', { name: 'Explain' }).click();
  await expect(page.getByTestId('explain-result')).toBeVisible();
  expect(gw.requestsTo('GET', '/explain')[0]!.query).toEqual({ src: '10.10.0.99', dst: '203.0.113.99' });
  await explain.getByLabel('Source', { exact: true }).selectOption({ label: 'A device' });
  await expect(explain.getByLabel('Device')).toBeVisible();
});

test('a gateway whose management interface is the uplink interface has no management source in the explain card', async ({ page }) => {
  const gw = lab();
  gw.hostInterfaces = gw.hostInterfaces.filter((i) => i.assigned?.role !== 'management');
  await gw.install(page);
  await page.goto('/faults');
  await expect(page.getByRole('region', { name: 'Explain' }).getByLabel('Source', { exact: true }).locator('option')).toHaveText(['A device', 'An address behind the uplink', 'Another address']);
});

test('a forwarded connection picked in the flow view is explained in the explain card at once, from the peer behind the uplink', async ({ page }) => {
  const gw = lab();
  gw.explanation = FORWARD_EXPLANATION;
  await gw.install(page);
  await page.goto('/flows');
  const row = page.getByTestId('connection-row').filter({ has: page.getByTestId('connection-forwarded') });
  await row.getByRole('link', { name: /^Explain/ }).click();
  await expect(page).toHaveURL(/\/faults\?/);
  const explain = page.getByRole('region', { name: 'Explain' });
  await expect(explain.getByLabel('Source', { exact: true })).toHaveValue('uplink');
  await expect(explain.getByLabel('Source address')).toHaveValue('198.51.100.7');
  await expect(explain.getByLabel('Destination')).toHaveValue('192.168.1.57');
  await expect(explain.getByLabel('Protocol')).toHaveValue('tcp');
  await expect(explain.getByLabel('Port')).toHaveValue('8080');
  await expect(page.getByTestId('explain-forward')).toContainText('port forward esp-web from the uplink to ESP32-42:80');
  const asked = gw.requestsTo('GET', '/explain');
  expect(asked).toHaveLength(1);
  expect(asked[0]!.query).toEqual({ src: '198.51.100.7', dst: '192.168.1.57', protocol: 'tcp', port: '8080' });
});

test("a device's connection picked in the flow view is explained for the device", async ({ page }) => {
  const gw = lab();
  gw.explanation = plain;
  await gw.install(page);
  await page.goto('/flows');
  const row = page.getByTestId('connection-row').filter({ hasText: 'broker.example.com:8883' });
  await row.getByRole('link', { name: /^Explain/ }).click();
  const explain = page.getByRole('region', { name: 'Explain' });
  await expect(explain.getByLabel('Source', { exact: true })).toHaveValue('device');
  await expect(explain.getByLabel('Device')).toHaveValue(ESP);
  await expect(explain.getByLabel('Destination')).toHaveValue('203.0.113.10');
  await expect(page.getByTestId('explain-result')).toContainText('No fault applies');
  expect(gw.requestsTo('GET', '/explain')[0]!.query).toEqual({ device: ESP, dst: '203.0.113.10', protocol: 'tcp', port: '8883' });
});
