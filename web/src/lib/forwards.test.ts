import { describe, expect, it } from 'vitest';
import type { PortForward, PortForwardView } from '../api/generated/model';
import { CompleteSetupBody } from '../api/generated/zod/system/system';
import {
  counterText,
  emptyForwardForm,
  formFromForward,
  forwardChanges,
  forwardFieldOf,
  forwardFormErrors,
  forwardFromForm,
  forwardRows,
  forwardSummary,
  forwardTitle,
  parsePortRange,
  rowStatus,
  targetRange,
  type ForwardContext,
  type ForwardForm,
} from './forwards';

// The port forward form (M35a): its checks under the fields, the forward it builds (which the spec
// accepts), the gateway's messages by field, the forward in words and the rows of the lists.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const WEB = 'e1000000-0000-4000-8000-000000000001';
const RANGE = 'e1000000-0000-4000-8000-000000000002';
const names = (id: string) => ({ [ESP]: 'ESP32-42' })[id];

const web: PortForward = { name: 'esp-web', protocol: 'tcp', entry: 'uplink', external_port: 8080, target: { device: ESP }, target_port: 80 };
const range: PortForward = { protocol: 'udp', entry: 'uplink', external_port: 8000, external_port_end: 8010, target: { address: '10.20.0.50' }, target_port: 9000 };
const ctx: ForwardContext = {
  others: [web, range],
  networks: [
    { name: 'IoT', address: '10.10.0.1/24' },
    { name: 'Test', address: '10.20.0.1/24' },
  ],
  twoPort: false,
};

function form(over: Partial<ForwardForm>): ForwardForm {
  return { ...emptyForwardForm(), external: '9090', device: ESP, ...over };
}

function acceptedBySpec(f: PortForward) {
  const configuration = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, port_forwards: { [WEB]: f } };
  return CompleteSetupBody.safeParse({ admin_password: 'correct horse battery', configuration }).error;
}

describe('the port forward form (M35a)', () => {
  it('reads a port or a range', () => {
    expect(parsePortRange('8080')).toEqual({ from: 8080, to: 8080 });
    expect(parsePortRange(' 8000 - 8010 ')).toEqual({ from: 8000, to: 8010 });
    expect(parsePortRange('8010-8000')).toEqual({ from: 8010, to: 8000 });
    expect(parsePortRange('0')).toBeUndefined();
    expect(parsePortRange('65536')).toBeUndefined();
    expect(parsePortRange('http')).toBeUndefined();
  });

  it('a forward to a device: no problem, and a forward the spec accepts with the default target port left out', () => {
    const f = form({ name: 'esp-ota', external: '9090' });
    expect(forwardFormErrors(f, ctx)).toEqual({});
    const p = forwardFromForm(f);
    expect(p).toEqual({ name: 'esp-ota', protocol: 'tcp', entry: 'uplink', external_port: 9090, target: { device: ESP } });
    expect(acceptedBySpec(p)).toBeUndefined();
  });

  it('a range to an address with a target range of the same length; one start port is shifted one to one', () => {
    const f = form({ protocol: 'udp', external: '7000-7010', targetKind: 'address', address: '10.20.0.60', targetPort: '9100-9110' });
    expect(forwardFormErrors(f, ctx)).toEqual({});
    const p = forwardFromForm(f);
    expect(p).toEqual({ protocol: 'udp', entry: 'uplink', external_port: 7000, external_port_end: 7010, target: { address: '10.20.0.60' }, target_port: 9100, target_port_end: 9110 });
    expect(acceptedBySpec(p)).toBeUndefined();
    const shifted = forwardFromForm({ ...f, targetPort: '9100' });
    expect(shifted.target_port_end).toBeUndefined();
    expect(targetRange(shifted)).toEqual({ from: 9100, to: 9110 });
  });

  it('the checks stand under their fields', () => {
    expect(forwardFormErrors(form({ external: '' }), ctx).external).toBe('The external port is required.');
    expect(forwardFormErrors(form({ external: '70000' }), ctx).external).toBe('A port from 1 to 65535, or a range such as 8000-8010.');
    expect(forwardFormErrors(form({ external: '8010-8000' }), ctx).external).toBe('The range ends before it starts (8010 to 8000).');
    // the same protocol and an overlapping port: refused; the other protocol: fine
    expect(forwardFormErrors(form({ external: '8080' }), ctx).external).toBe('TCP 8080 is forwarded already (esp-web).');
    expect(forwardFormErrors(form({ protocol: 'udp', external: '8005-8020' }), ctx).external).toBe('UDP 8000-8010 is forwarded already (UDP 8000-8010).');
    expect(forwardFormErrors(form({ protocol: 'udp', external: '8080' }), ctx).external).toBeUndefined();
    // ranges of different length, and a shifted range past 65535
    expect(forwardFormErrors(form({ external: '7000-7010', targetPort: '9000-9005' }), ctx).targetPort).toBe('The target range must be as long as the external one (11 ports).');
    expect(forwardFormErrors(form({ external: '7000-7010', targetPort: '65530' }), ctx).targetPort).toBe('The range shifted to 65530 ends after 65535.');
    expect(forwardFormErrors(form({ targetPort: '0' }), ctx).targetPort).toMatch(/^A port from 1 to 65535/);
    // the target: a device chosen, an address in a test network that is not the gateway's own
    expect(forwardFormErrors(form({ device: '' }), ctx).target).toBe('Choose the device the connections go to.');
    expect(forwardFormErrors(form({ targetKind: 'address', address: '' }), ctx).target).toBe('The target address is required.');
    expect(forwardFormErrors(form({ targetKind: 'address', address: '10.10.0.300' }), ctx).target).toBe('An IPv4 address such as 10.10.0.20.');
    expect(forwardFormErrors(form({ targetKind: 'address', address: '192.168.56.20' }), ctx).target).toBe('The target lies in a local test network.');
    expect(forwardFormErrors(form({ targetKind: 'address', address: '10.10.0.1' }), ctx).target).toBe("10.10.0.1 is the gateway's own address in IoT.");
    expect(forwardFormErrors(form({ targetKind: 'address', address: '10.10.0.255' }), ctx).target).toBe('10.10.0.255 is no host address of IoT (10.10.0.0/24).');
    // names: the pattern, unique among the forwards
    expect(forwardFormErrors(form({ name: '-x' }), ctx).name).toMatch(/^Letters, digits/);
    expect(forwardFormErrors(form({ name: 'ESP-WEB' }), ctx).name).toBe('A forward is already called ESP-WEB.');
    // the management side in the two-port topology
    expect(forwardFormErrors(form({ entry: 'management' }), { ...ctx, twoPort: true }).entry).toBe(
      'In the two-port topology the management network is the uplink: forward from the uplink.',
    );
    expect(forwardFormErrors(form({ entry: 'management' }), ctx).entry).toBeUndefined();
  });

  it('a forward edited keeps what the form does not edit; disabled and enabled again it leaves `enabled` out', () => {
    const before: PortForward = { ...web, description: 'OTA' };
    const f = formFromForward(before);
    expect(f).toEqual({ name: 'esp-web', description: 'OTA', protocol: 'tcp', entry: 'uplink', external: '8080', targetKind: 'device', device: ESP, address: '', targetPort: '80', enabled: true });
    expect(forwardFromForm({ ...f, enabled: false }, before)).toEqual({ ...before, enabled: false });
    expect(forwardFromForm(f, { ...before, enabled: false })).toEqual(before);
    // a forward that leaves the target port to its default keeps it so when the external port changes
    const def: PortForward = { protocol: 'tcp', entry: 'uplink', external_port: 8443, target: { address: '10.10.0.20' } };
    expect(formFromForward(def).targetPort).toBe('');
    expect(forwardFromForm({ ...formFromForward(def), external: '9443' }, def)).toEqual({ ...def, external_port: 9443 });
    // a range read back as the form shows it
    expect(formFromForward(range)).toMatchObject({ external: '8000-8010', targetKind: 'address', address: '10.20.0.50', targetPort: '9000' });
    expect(formFromForward({ ...range, target_port_end: 9010 }).targetPort).toBe('9000-9010');
  });

  it("the gateway's messages go to the field of their pointer", () => {
    expect(forwardFieldOf('external_port')).toBe('external');
    expect(forwardFieldOf('external_port_end')).toBe('external');
    expect(forwardFieldOf('target_port')).toBe('targetPort');
    expect(forwardFieldOf('target_port_end')).toBe('targetPort');
    expect(forwardFieldOf('target/device')).toBe('target');
    expect(forwardFieldOf('target/address')).toBe('target');
    expect(forwardFieldOf('entry')).toBe('entry');
    expect(forwardFieldOf('forward')).toBeUndefined();
  });

  it('a forward in words', () => {
    expect(forwardTitle(web)).toBe('esp-web');
    expect(forwardTitle(range)).toBe('UDP 8000-8010');
    expect(forwardSummary(web, names)).toBe('TCP 8080 → ESP32-42:80');
    expect(forwardSummary(range)).toBe('UDP 8000-8010 → 10.20.0.50:9000-9010');
    expect(forwardChanges(undefined, web, names)).toEqual([{ field: 'forward', from: '—', to: 'added (TCP 8080 → ESP32-42:80 from the uplink)' }]);
    expect(forwardChanges(web, null)).toEqual([{ field: 'forward', from: 'present', to: 'deleted' }]);
    expect(forwardChanges(web, { ...web, external_port: 8081, enabled: false, target: { address: '10.10.0.20' } }, names)).toEqual([
      { field: 'external ports', from: '8080', to: '8081' },
      { field: 'target', from: 'ESP32-42', to: '10.10.0.20' },
      { field: 'enabled', from: 'yes', to: 'no' },
    ]);
  });

  it('the rows: the forwards of the draft with the status and the counter the gateway reads for them', () => {
    const views: PortForwardView[] = [
      { id: WEB, config: web, status: { state: 'active', target_address: '10.10.0.42' }, counters: { packets: 12, bytes: 720, epoch: 1 } },
      { id: RANGE, config: range, status: { state: 'inactive', reason: 'device_without_address', message: 'inactive: the device has no address' } },
    ];
    const NEW = 'e1000000-0000-4000-8000-0000000000ff';
    const added: PortForward = { protocol: 'tcp', entry: 'management', external_port: 22022, target: { device: ESP } };
    const rows = forwardRows({ [WEB]: web, [RANGE]: range }, { [RANGE]: null, [NEW]: added, [WEB]: { ...web, enabled: false } }, views);
    expect(rows.map((r) => [r.id, r.mark])).toEqual([
      [NEW, 'NEW'],
      [WEB, 'CHANGED'],
      [RANGE, 'DELETED'],
    ]);
    expect(rowStatus(rows[0]!)).toEqual({ tone: 'offline', label: 'not applied yet' });
    expect(rowStatus(rows[1]!)).toEqual({ tone: 'offline', label: 'disabled · not applied yet' });
    expect(rowStatus(rows[2]!)).toEqual({ tone: 'fault', label: 'inactive: the device has no address' });
    expect(rowStatus({ id: WEB, p: web, view: views[0] })).toEqual({ tone: 'normal', label: 'active · to 10.10.0.42' });
    expect(counterText(rows[1]!)).toBe('12 hits');
    expect(counterText({ ...rows[1]!, view: { ...rows[1]!.view!, counters: { ...rows[1]!.view!.counters!, packets: 1 } } })).toBe('1 hit');
    expect(counterText(rows[0]!)).toBe('');
  });
});
