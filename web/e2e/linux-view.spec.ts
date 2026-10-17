import { expect, test } from '@playwright/test';
import { MockGateway, RULE_DOT, withRules } from './mock';

// P3-M14-03 (plan §2.17 "Technical view"): "Show Linux view" on an access rule, a fault and a device opens
// what the kernel holds for that one object, as the gateway finds it by the keys the compiler gave it
// (`GET /state/linux/{kind}/{id}`): the rule in both hooks with its counter and last match, the fault's
// classes and netem queues, the device's identity element and classification.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const ESP_FAULT = 'f0000000-0000-4000-8000-000000000001';

const ruleNft = `table inet chaosgw {
  counter rule_a41eb09061 { packets 18342 bytes 12400000 }
  chain access_forward {
    ct original ip saddr @asrc_00074ad1 meta l4proto tcp ct original proto-dst 853 counter name "rule_a41eb09061" last used 2s reject with icmpx port-unreachable
  }
  chain access_input {
    ct original ip saddr @asrc_00074ad1 meta l4proto tcp ct original proto-dst 853 counter name "rule_a41eb09061" last used never reject with icmpx port-unreachable
  }
}
`;

test('"Show Linux view" on a rule shows its compiled rule and counter; another rule closes it, one the kernel lacks says so (P3-M14-03)', async ({ page }) => {
  const gw = withRules(MockGateway.sample());
  gw.linuxObjects[`rule/${RULE_DOT}`] = { keys: ['counter rule_a41eb09061'], nftables: ruleNft, tc: '' };
  await gw.install(page);
  await page.goto('/rules');
  // the first page of a worker may start cold while the whole suite runs
  await expect(page.getByRole('heading', { name: 'no-dot' })).toBeVisible({ timeout: 15_000 });

  const show = page.getByRole('button', { name: 'Show Linux view (compiled rule and counter)' });
  await expect(show.getByTestId('disclosure-mark')).toHaveText('▸'); // issue #63
  await show.click();
  const hide = page.getByRole('button', { name: 'Hide Linux view' });
  await expect(hide.getByTestId('disclosure-mark')).toHaveText('▾');
  await expect(page.locator(`[id="${await hide.getAttribute('aria-controls')}"]`).getByTestId('linux-object')).toBeVisible();
  const nft = page.getByTestId('linux-object-nftables');
  await expect(nft).toContainText('counter rule_a41eb09061 { packets 18342 bytes 12400000 }');
  await expect(nft).toContainText('counter name "rule_a41eb09061" last used 2s reject with icmpx port-unreachable');
  await expect(page.getByTestId('linux-object-tc')).toHaveCount(0);
  await expect(page.getByTestId('linux-object').getByRole('link', { name: 'Open the whole Linux view →' })).toHaveAttribute('href', '/linux');
  expect(gw.requestsTo('GET', `/state/linux/rule/${RULE_DOT}`).length).toBeGreaterThan(0);
  await page.getByRole('button', { name: 'Hide Linux view' }).click();
  await expect(page.getByTestId('linux-object')).toHaveCount(0);

  // the system rule by its key: the kernel holds nothing for it here
  await page.getByRole('button', { name: 'Show Linux view (compiled rule and counter)' }).click();
  await expect(page.getByTestId('linux-object')).toBeVisible();
  await page.getByTestId('rule-row').nth(0).getByRole('button').first().click();
  await expect(page.getByRole('heading', { name: 'Control plane access' })).toBeVisible();
  await expect(page.getByTestId('linux-object')).toHaveCount(0);
  await page.getByRole('button', { name: 'Show Linux view (compiled rule and counter)' }).click();
  await expect(page.getByTestId('linux-object')).toContainText("The kernel holds nothing of Chaos Gateway's for this.");
  expect(gw.requestsTo('GET', '/state/linux/rule/system:anti_lockout').length).toBeGreaterThan(0);
});

test('"Show Linux view" on a fault shows its mark chain, classes and netem queues with their counters below its row; a DNS fault has none (P3-M14-03)', async ({ page }) => {
  const gw = MockGateway.sample();
  // a DNS fault is carried out by the DNS proxy: the kernel holds nothing of it, so its row offers no Linux view
  const at = new Date().toISOString();
  gw.overlays.push({
    id: 'f0000000-0000-4000-8000-0000000000d1',
    kind: 'dns',
    owner: { type: 'user', id: 'admin' },
    target: { device: ESP },
    dns: { action: 'nxdomain' },
    created_at: at,
    updated_at: at,
    generation: 43,
    state: 'effective',
  });
  gw.linuxObjects[`fault/${ESP_FAULT}`] = {
    keys: ['chain mark_7', 'class br-iot 1:1e', 'qdisc br-iot 1e:'],
    nftables: 'table inet chaosgw {\n  map cls_devdestport_1a {\n    elements = { 10.10.0.42 . 0.0.0.0/0 . tcp . 8883 : goto mark_7 }\n  }\n  chain mark_7 {\n    ct direction original counter name "fault_7_up"\n  }\n}\n',
    tc: 'br-iot: class 1:1e parent 1: htb rate=1250000000 ceil=1250000000 prio=0 leaf=1e:\nbr-iot: qdisc 1e: parent 1:1e netem limit=1000 delay=0.2 jitter=0.05\nbr-iot:   sent 1200 bytes 18 pkt (dropped 0, overlimits 0, requeues 0) backlog 0b 0p\n',
  };
  await gw.install(page);
  await page.goto('/faults');
  const row = page.getByTestId('fault-family-impairment').getByTestId('fault-row').nth(0);
  await expect(row).toContainText('ESP32-42');
  const dnsRow = page.getByTestId('fault-family-dns').getByTestId('fault-row');
  await expect(dnsRow).toContainText('DNS fault');
  await expect(dnsRow.getByRole('button', { name: /Linux view/ })).toHaveCount(0);
  const show = row.getByRole('button', { name: /^Show the Linux view of .* on ESP32-42/ });
  await expect(show).toHaveAttribute('aria-expanded', 'false');
  await show.click();
  await expect(page.getByTestId('linux-object-tc')).toContainText('br-iot: qdisc 1e: parent 1:1e netem limit=1000 delay=0.2 jitter=0.05');
  await expect(page.getByTestId('linux-object-tc')).toContainText('sent 1200 bytes 18 pkt');
  await expect(page.getByTestId('linux-object-nftables')).toContainText('10.10.0.42 . 0.0.0.0/0 . tcp . 8883 : goto mark_7');
  expect(gw.requestsTo('GET', `/state/linux/fault/${ESP_FAULT}`).length).toBeGreaterThan(0);
  // and hidden again
  await row.getByRole('button', { name: /^Hide the Linux view of/ }).click();
  await expect(page.getByTestId('linux-object')).toHaveCount(0);
});

test('"Show Linux view" on a device shows its identity element and the classification of its address (P3-M14-03)', async ({ page }) => {
  const gw = MockGateway.sample();
  gw.linuxObjects[`device/${ESP}`] = {
    keys: ['map ident4_b89cfe 10.10.0.42', 'map cls_dev_14ef7f 10.10.0.42'],
    nftables:
      'table inet chaosgw {\n  map cls_dev_14ef7f {\n    type ipv4_addr : verdict\n    elements = { 10.10.0.42 : goto mark_7 }\n  }\n  map ident4_b89cfe {\n    type ipv4_addr : mark\n    elements = { 10.10.0.42 : 3 }\n  }\n}\n',
    tc: '',
  };
  await gw.install(page);
  await page.goto(`/devices/${ESP}`);
  await page.getByRole('button', { name: 'Show Linux view (identity and classification)' }).click();
  await expect(page.getByTestId('linux-object-nftables')).toContainText('elements = { 10.10.0.42 : 3 }');
  await expect(page.getByTestId('linux-object-nftables')).toContainText('10.10.0.42 : goto mark_7');
  expect(gw.requestsTo('GET', `/state/linux/device/${ESP}`).length).toBeGreaterThan(0);
});
