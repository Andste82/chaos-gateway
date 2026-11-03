import { expect, test, type Page } from '@playwright/test';
import { ESP32, MockGateway, SCN_MQTT, runOf, withNetworks, withScenarios } from '../e2e/mock';

// The pictures of the README: docs/images/<name>.png. They show the app with the sample lab of the e2e tests, so they are made again
// with `npm run screenshots` whenever a screen looks different.
const out = (name: string) => `../docs/images/${name}.png`;

async function shot(page: Page, path: string, name: string, ready: () => Promise<void>) {
  await page.goto(path);
  await ready();
  await page.waitForTimeout(1500); // the charts and the first poll
  await page.screenshot({ path: out(name) });
}

test('overview', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await shot(page, '/', 'overview', () => expect(page.getByTestId('topology-network').first()).toBeVisible());
});

test('device', async ({ page }) => {
  const gw = MockGateway.sample();
  await gw.install(page);
  await shot(page, `/devices/${ESP32}`, 'device', () => expect(page.getByText('ESP32-42').first()).toBeVisible());
});

test('scenario', async ({ page }) => {
  const gw = withScenarios(MockGateway.sample());
  gw.runs = [runOf(SCN_MQTT, { id: 'r-live', status: 'running' })];
  await gw.install(page);
  await shot(page, `/scenarios/${SCN_MQTT}`, 'scenario', () => expect(page.getByRole('heading', { name: 'mqtt-outage' })).toBeVisible());
});

test('networks', async ({ page }) => {
  const gw = withNetworks(MockGateway.sample());
  await gw.install(page);
  await shot(page, '/networks', 'networks', () => expect(page.getByRole('heading', { name: 'Networks' })).toBeVisible());
});
