import { expect, test } from '@playwright/test';
import { IOT, MockGateway } from './mock';

// M23 (plan §2.7): the DHCP test actions are made over the API and in scenario files; the faults screen lists them under
// DHCP with what each does, the address a device is moved to, and the state the gateway reports (P5-M23-01 is the form).

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';

test('the faults screen lists the DHCP actions with what they do and the state of each', async ({ page }) => {
  const gw = MockGateway.sample();
  const at = new Date().toISOString();
  const owner = { type: 'user', id: 'admin' } as const;
  gw.overlays.push(
    { id: 'f0000000-0000-4000-8000-0000000000e1', kind: 'dhcp', owner, target: { network: IOT }, dhcp: { action: 'short_lease', lease_time: '7s' }, created_at: at, updated_at: at, generation: 43, state: 'effective' },
    { id: 'f0000000-0000-4000-8000-0000000000e2', kind: 'dhcp', owner, target: { device: ESP }, dhcp: { action: 'force_new_ip', ip: '10.10.0.254' }, created_at: at, updated_at: at, generation: 44, state: 'effective' },
    { id: 'f0000000-0000-4000-8000-0000000000e3', kind: 'dhcp', owner, target: { device: ESP }, dhcp: { action: 'set_options', options: { router: '10.10.0.9', dns_servers: ['192.0.2.53'] } }, created_at: at, updated_at: at, generation: 45, state: 'disabled' },
    { id: 'f0000000-0000-4000-8000-0000000000e4', kind: 'dhcp', owner, target: { network: IOT }, dhcp: { action: 'delete_lease' }, created_at: at, updated_at: at, generation: 46, state: 'effective' },
  );
  await gw.install(page);
  await page.goto('/faults');
  const rows = page.getByTestId('fault-family-dhcp').getByTestId('fault-row');
  await expect(rows).toHaveCount(4);
  await expect(rows.filter({ hasText: 'DHCP short lease 7s' })).toContainText('IoT');
  await expect(rows.filter({ hasText: 'DHCP new IP 10.10.0.254' })).toContainText('ESP32-42');
  await expect(rows.filter({ hasText: 'DHCP options · router, DNS' })).toContainText('ESP32-42');
  await expect(rows.filter({ hasText: 'DHCP delete lease' })).toContainText('IoT');
});
