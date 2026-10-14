import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import { nextTick } from 'vue';
import type { AccessRule, RevisionWithConfiguration } from '../api/generated/model';
import { curlOf, httpOf, useApiPanelStore } from './apiPanel';
import { useDraftStore } from './draft';

const A = 'a1000000-0000-4000-8000-000000000001';
const B = 'a1000000-0000-4000-8000-000000000002';
const C = 'a1000000-0000-4000-8000-000000000003';

function revision(id: number, extra: Partial<RevisionWithConfiguration['configuration']> = {}): RevisionWithConfiguration {
  return {
    id,
    status: 'active',
    schema_version: 1,
    created_at: '',
    created_by: { type: 'user', id: 'admin' },
    configuration: {
      schema_version: 1,
      uplink: { interface: { name: 'eth0' } },
      management: { interface: { name: 'eth1' } },
      access_rules: {
        [A]: { name: 'a', source: { global: true }, action: 'drop' },
        [B]: { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow' },
      },
      access_rule_order: [A, B],
      faults: { f1: { name: 'slow', source: { global: true }, latency: '10ms' } },
      ...extra,
    },
  };
}

beforeEach(() => setActivePinia(createPinia()));

describe('the configuration draft', () => {
  it('follows the active revision until it has edits of its own', () => {
    const d = useDraftStore();
    d.track(revision(42));
    expect(d.base).toBe(42);
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    d.track(revision(43));
    expect(d.base).toBe(42); // the edits are against 42; the conflict dialog moves them
    d.discard();
    expect(d.base).toBe(43); // a clean draft is based on the newest revision seen
    expect(d.dirty).toBe(false);
  });

  it('edits undone by hand leave a clean draft on the newest active revision', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(B, { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow', enabled: false });
    d.track(revision(43, { faults: {} }));
    expect(d.base).toBe(42);
    expect(d.effectiveFaults).toHaveProperty('f1');
    // the switch back: no edit is left, so the draft shows what is active now
    d.setRule(B, { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow' });
    expect(d.dirty).toBe(false);
    expect(d.base).toBe(43);
    expect(d.effectiveFaults).toEqual({});
  });

  it('a deletion is undone: the rule comes back at its place, the fault as it was', () => {
    const d = useDraftStore();
    d.track(revision(42, { access_rules: { ...revision(42).configuration.access_rules, [C]: { name: 'c', source: { global: true }, action: 'drop' } }, access_rule_order: [A, B, C] }));
    d.setRule(B, null);
    d.setFault('f1', null);
    expect(d.effectiveOrder).toEqual([A, C]);
    expect(d.summary()).toBe('2 changes in 1 rule and 1 fault');
    d.restoreRule(B);
    expect(d.effectiveOrder).toEqual([A, B, C]);
    d.restoreFault('f1');
    expect(d.dirty).toBe(false);
    // the first rule goes back to the front, and another edit stays
    d.setRule(A, null);
    d.setRule(C, { name: 'c', source: { global: true }, action: 'reject' });
    d.restoreRule(A);
    expect(d.effectiveOrder).toEqual([A, B, C]);
    expect(d.summary()).toBe('1 change in 1 rule');
  });

  it('counts the changes per field and per rule, and edits back to the base are no change', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject', cut_existing: true });
    expect(d.summary()).toBe('2 changes in 1 rule');
    d.setRule(B, { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow', enabled: false });
    expect(d.summary()).toBe('3 changes in 2 rules');
    d.setRule(B, { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow' });
    expect(d.summary()).toBe('2 changes in 1 rule');
    d.setFault('f1', { name: 'slow', source: { global: true }, latency: '20ms' });
    expect(d.summary()).toBe('3 changes in 1 rule and 1 fault');
    expect(d.changes().find((c) => c.kind === 'fault')).toMatchObject({ field: 'parameters', from: '↑↓ 10 ms', to: '↑↓ 20 ms' });
  });

  it('builds the merge patch: changed fields, new rules whole, deleted ones null, the order when it changed', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    expect(d.patch()).toEqual({ access_rules: { [A]: { action: 'reject' } } });
    d.setRule(C, { name: 'c', source: { network: 'n1' }, action: 'drop' });
    d.move(C, -1);
    d.move(C, -1);
    d.setRule(B, null);
    d.setFault('f2', { name: 'new', source: { global: true }, loss: '1%' });
    d.setFault('f1', null);
    expect(d.effectiveOrder).toEqual([C, A]);
    expect(d.patch()).toEqual({
      access_rules: { [A]: { action: 'reject' }, [C]: { name: 'c', source: { network: 'n1' }, action: 'drop' }, [B]: null },
      access_rule_order: [C, A],
      faults: { f2: { name: 'new', source: { global: true }, loss: '1%' }, f1: null },
    });
    // A and B keep their order among themselves: no change of position of a base rule
    expect(d.changes().filter((c) => c.field === 'position')).toEqual([]);
  });

  it('an adopted device is a change of its own, with the rules and faults in one revision', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setDevice('d1', { name: 'thermostat', identifiers: { macs: ['02:00:00:00:00:01'] }, network: 'n1' });
    expect(d.summary()).toBe('1 change in 1 device');
    expect(d.changes()[0]).toMatchObject({ kind: 'device', from: 'discovered', to: 'configured (02:00:00:00:00:01)' });
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    d.setFault('f1', null);
    expect(d.summary()).toBe('3 changes in 1 rule, 1 fault and 1 device');
    expect(d.patch().devices).toEqual({ d1: { name: 'thermostat', identifiers: { macs: ['02:00:00:00:00:01'] }, network: 'n1' } });
    d.setServerErrors([{ path: '/devices/d1/name', message: 'taken' }]);
    expect(d.serverErrors).toEqual({ d1: { name: 'taken' } });
    // a fault keeps the pointer below it for the fault dialog's fields
    d.setServerErrors([
      { path: '/faults/f1/upload/latency', message: 'too long' },
      { path: '/faults/f1', message: 'the fault sets no parameter' },
    ]);
    expect(d.serverErrors).toEqual({ f1: { 'upload/latency': 'too long', fault: 'the fault sets no parameter' } });
  });

  it('a move of base rules is a change of their positions', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.move(B, -1);
    expect(d.summary()).toBe('2 changes in 2 rules');
    d.move(B, 1);
    expect(d.dirty).toBe(false);
  });

  it('marks invalid rules, also by the ports as typed, and the problems the gateway named', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'drop', protocol: 'any', ports: [53] });
    expect(Object.keys(d.invalid)).toEqual([A]);
    d.setRule(A, { name: 'a', source: { global: true }, action: 'drop', protocol: 'udp', ports: [53] });
    expect(d.invalid).toEqual({});
    d.setPortsText(A, '53, x');
    expect(d.invalid[A]?.ports).toMatch(/x is no port/);
    d.setServerErrors([{ path: `/access_rules/${A}/port_ranges/0`, message: 'too many' }, { path: '/networks/x', message: 'ignored' }]);
    expect(d.serverErrors).toEqual({ [A]: { ports: 'too many' } });
    d.setRule(A, { name: 'a', source: { global: true }, action: 'drop', protocol: 'udp', ports: [5353] });
    expect(d.serverErrors).toEqual({});
  });

  it('reapplies on the new revision: my objects win, their new rules keep their place, a deletion of a gone object drops out', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    d.setRule(C, { name: 'c', source: { global: true }, action: 'drop' }); // mine, at the end
    d.setFault('f1', null);
    // theirs: a new rule D at the front, rule B disabled, the fault f1 already gone
    const D = 'a1000000-0000-4000-8000-000000000004';
    const theirs = revision(43, {
      access_rules: {
        [D]: { name: 'd', source: { global: true }, action: 'drop' },
        [A]: { name: 'a', source: { global: true }, action: 'drop' },
        [B]: { name: 'b', source: { global: true }, protocol: 'tcp', ports: [80], action: 'allow', enabled: false },
      },
      access_rule_order: [D, A, B],
      faults: {},
    });
    d.rebase(theirs);
    expect(d.base).toBe(43);
    expect(d.effectiveOrder).toEqual([D, A, B, C]);
    expect(d.effectiveRules[B]?.enabled).toBe(false);
    expect(d.patch()).toEqual({
      access_rules: { [A]: { action: 'reject' }, [C]: { name: 'c', source: { global: true }, action: 'drop' } },
      access_rule_order: [D, A, B, C],
    });
  });

  it('reapplies field by field where both changed the same object: their fields stay, a field both changed takes mine', () => {
    const d = useDraftStore();
    d.track(revision(42));
    // mine: rule A rejects and is renamed, fault f1 gets 50ms, rule B goes
    d.setRule(A, { name: 'a2', source: { global: true }, action: 'reject' });
    d.setRule(B, null);
    d.setFault('f1', { name: 'slow', source: { global: true }, latency: '50ms' });
    // theirs: rule A disabled and renamed too, fault f1 disabled with loss, rule B's port changed
    const theirs = revision(43, {
      access_rules: {
        [A]: { name: 'theirs', source: { global: true }, action: 'drop', enabled: false },
        [B]: { name: 'b', source: { global: true }, protocol: 'tcp', ports: [8080], action: 'allow' },
      },
      faults: { f1: { name: 'slow', source: { global: true }, latency: '10ms', loss: '1%', enabled: false } },
    });
    d.rebase(theirs);
    expect(d.effectiveRules[A]).toEqual({ name: 'a2', source: { global: true }, action: 'reject', enabled: false });
    expect(d.effectiveFaults.f1).toEqual({ name: 'slow', source: { global: true }, latency: '50ms', loss: '1%', enabled: false });
    // the candidate sends only my fields: their enabled:false is not set back
    expect(d.patch()).toEqual({
      access_rules: { [A]: { name: 'a2', action: 'reject' }, [B]: null },
      access_rule_order: [A],
      faults: { f1: { latency: '50ms' } },
    });
  });

  it('names the fields both sides changed, their value and mine, before reapplying (P3-M13-07)', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject', cut_existing: true });
    d.setRule(B, null);
    d.setFault('f1', { name: 'slow', source: { global: true }, latency: '50ms' });
    const theirs = revision(43, {
      access_rules: {
        [A]: { name: 'a', source: { global: true }, action: 'reset', enabled: false },
        [B]: { name: 'b', source: { global: true }, protocol: 'tcp', ports: [8080], action: 'allow' },
      },
      faults: { f1: { name: 'slow', source: { global: true }, latency: '20ms', loss: '1%' } },
    }).configuration;
    expect(d.overlaps(theirs)).toEqual([
      { kind: 'rule', id: A, name: 'a', field: 'action', from: 'TCP reset', to: 'Reject' },
      { kind: 'rule', id: B, name: 'b', field: 'rule', from: 'changed', to: 'deleted' },
      { kind: 'fault', id: 'f1', name: 'slow', field: 'latency', from: '20ms', to: '50ms' },
    ]);
    // a rule they removed comes back as mine; one they left as it was is no conflict
    expect(d.overlaps(revision(43, { access_rules: {}, faults: {} }).configuration)).toEqual([
      { kind: 'rule', id: A, name: 'a', field: 'rule', from: 'deleted', to: 'kept with my change' },
      { kind: 'fault', id: 'f1', name: 'slow', field: 'fault', from: 'deleted', to: 'kept with my change' },
    ]);
    expect(d.overlaps(revision(43).configuration)).toEqual([]);
  });

  it('names the rule order when both moved rules to different orders (P3-M13-07)', () => {
    const C = 'c0000000-0000-4000-8000-00000000000c';
    const rule = (name: string): AccessRule => ({ name, source: { global: true }, action: 'drop' });
    const three = { [A]: rule('a'), [B]: rule('b'), [C]: rule('c') };
    const d = useDraftStore();
    d.track(revision(42, { access_rules: three, access_rule_order: [A, B, C] }));
    d.setOrder([C, A, B]);
    // they moved b to the top: the dialog says their order and mine, and reapplying takes mine
    const theirs = revision(43, { access_rules: three, access_rule_order: [B, A, C] });
    expect(d.overlaps(theirs.configuration)).toEqual([{ kind: 'rule', id: 'access_rule_order', name: 'order', field: '', from: 'b, a, c', to: 'c, a, b' }]);
    // the same order on both sides, or theirs untouched (a rule they only added does not count), is no conflict
    expect(d.overlaps(revision(43, { access_rules: three, access_rule_order: [C, A, B] }).configuration)).toEqual([]);
    expect(d.overlaps(revision(43, { access_rules: { ...three, d: rule('d') }, access_rule_order: [A, 'd', B, C] }).configuration)).toEqual([]);
    d.rebase(theirs);
    expect(d.effectiveOrder).toEqual([C, A, B]);
  });

  it('reapplying drops an edit that their revision made already, and keeps a rule they removed as mine', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    d.setRule(B, { name: 'b', source: { global: true }, protocol: 'tcp', ports: [81], action: 'allow' });
    d.rebase(
      revision(43, {
        access_rules: { [A]: { name: 'a', source: { global: true }, action: 'reject' } },
        access_rule_order: [A],
      }),
    );
    expect(d.rules[A]).toBeUndefined();
    expect(d.patch().access_rules).toEqual({ [B]: { name: 'b', source: { global: true }, protocol: 'tcp', ports: [81], action: 'allow' } });
  });

  it('reapplying onto a revision that holds all my edits leaves a clean draft on that revision', async () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    // revision 44 was fetched for the reapply; the active-revision query has not seen it yet
    d.rebase(revision(44, { access_rules: { ...revision(42).configuration.access_rules, [A]: { name: 'a', source: { global: true }, action: 'reject' } } }));
    await nextTick();
    expect(d.dirty).toBe(false);
    expect(d.base).toBe(44);
    expect(d.effectiveRules[A]?.action).toBe('reject');
  });

  it('reapplies a renamed device on theirs: the identifiers they added stay', () => {
    const d = useDraftStore();
    const dev = { name: 'esp', network: 'iot', identifiers: { macs: ['aa:bb:cc:dd:ee:01'] } };
    d.track(revision(42, { devices: { d1: dev } }));
    d.setDevice('d1', { ...dev, name: 'esp-kitchen' });
    const theirs = { ...dev, identifiers: { macs: ['aa:bb:cc:dd:ee:01'], ipv4: ['10.0.0.9'] } };
    d.rebase(revision(43, { devices: { d1: theirs } }));
    expect(d.patch()).toEqual({ devices: { d1: { name: 'esp-kitchen' } } });
    expect(d.devices.d1).toEqual({ ...theirs, name: 'esp-kitchen' });
  });
});

describe('WireGuard clients and the access matrix in the draft (M14)', () => {
  const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
  const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';
  const RA = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
  const RC = 'c1000000-0000-4000-8000-000000000003';
  const networks = (): RevisionWithConfiguration['configuration']['networks'] => ({
    [IOT]: { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24' },
    [HUB]: { type: 'wireguard', kind: 'hub', name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820, clients: { [RA]: { name: 'rA', address: '10.99.0.2' } } },
  });

  it('a new client is a change in words and a merge patch below its hub', () => {
    const d = useDraftStore();
    d.track(revision(42, { networks: networks() }));
    d.setClient(HUB, RC, { name: 'rC', address: '10.99.0.3', key: { mode: 'generated' } });
    expect(d.dirty).toBe(true);
    expect(d.changes()).toEqual([{ kind: 'client', id: RC, name: 'rC', field: 'client', from: '—', to: 'added (10.99.0.3 in lab-hub)' }]);
    expect(d.summary()).toBe('1 change in 1 WireGuard client');
    expect(d.patch()).toEqual({ networks: { [HUB]: { clients: { [RC]: { name: 'rC', address: '10.99.0.3', key: { mode: 'generated' } } } } } });
    expect(Object.keys(d.effectiveClients(HUB))).toEqual([RA, RC]);
    // the gateway's refusal of the client is kept for its row
    d.setServerErrors([{ path: `/networks/${HUB}/clients/${RC}/address`, message: 'address taken' }]);
    expect(d.serverErrors[RC]).toEqual({ address: 'address taken' });
    d.setClient(HUB, RC, null);
    expect(d.dirty).toBe(false);
  });

  it('the client actions are changes in words and a merge patch of their fields (P3-M14-01)', () => {
    const d = useDraftStore();
    d.track(revision(42, { networks: networks() }));
    d.setClient(HUB, RA, { name: 'rA', address: '10.99.0.2', enabled: false, key: { generation: 2 } });
    expect(d.changes()).toEqual([
      { kind: 'client', id: RA, name: 'rA', field: 'enabled', from: 'yes', to: 'no' },
      { kind: 'client', id: RA, name: 'rA', field: 'keys', from: 'generation 1', to: 'generation 2' },
    ]);
    expect(d.patch()).toEqual({ networks: { [HUB]: { clients: { [RA]: { enabled: false, key: { generation: 2 } } } } } });
    d.setClient(HUB, RA, null);
    expect(d.changes()).toEqual([{ kind: 'client', id: RA, name: 'rA', field: 'client', from: 'present', to: 'deleted' }]);
    expect(d.patch()).toEqual({ networks: { [HUB]: { clients: { [RA]: null } } } });
    // undone: the client as it is
    d.setClient(HUB, RA, { name: 'rA', address: '10.99.0.2' });
    expect(d.dirty).toBe(false);
  });

  it('a matrix cell flipped is a change; flipped back it is none; the patch replaces the entries', () => {
    const d = useDraftStore();
    d.track(revision(42, { networks: networks(), access_matrix: { entries: [{ from: { network: 'IoT' }, to: { network: 'lab-hub' }, policy: 'allow' }] } }));
    d.toggleMatrix(`network:${IOT}`, 'uplink');
    expect(d.changes()).toEqual([{ kind: 'matrix', id: 'IoT → Internet', name: 'Access between networks', field: 'IoT → Internet', from: 'allowed', to: 'blocked' }]);
    expect(d.summary()).toBe('1 change in the access matrix');
    expect(d.patch()).toEqual({
      access_matrix: {
        entries: [
          { from: { network: 'IoT' }, to: { network: 'lab-hub' }, policy: 'allow' },
          { from: { network: IOT }, to: { uplink: true }, policy: 'deny' },
        ],
      },
    });
    d.toggleMatrix(`network:${IOT}`, 'uplink');
    expect(d.dirty).toBe(false);
    expect(d.matrix).toBeUndefined();
  });

  it('reapplies a client and a matrix cell on their revision, keeping their entries and clients', () => {
    const d = useDraftStore();
    d.track(revision(42, { networks: networks() }));
    d.setClient(HUB, RC, { name: 'rC', address: '10.99.0.3' });
    d.toggleMatrix(`network:${IOT}`, 'uplink');
    // their revision: another client on the hub and an entry of their own
    const theirs = networks()!;
    (theirs[HUB] as { clients: Record<string, unknown> }).clients['d1000000-0000-4000-8000-000000000004'] = { name: 'rD', address: '10.99.0.4' };
    d.rebase(revision(43, { networks: theirs, access_matrix: { entries: [{ from: { network: IOT }, to: { network: HUB }, policy: 'allow' }] } }));
    expect(d.base).toBe(43);
    expect(Object.values(d.effectiveClients(HUB)).map((c) => c.name)).toEqual(['rA', 'rD', 'rC']);
    expect(d.effectiveMatrix).toEqual([
      { from: { network: IOT }, to: { network: HUB }, policy: 'allow' },
      { from: { network: IOT }, to: { uplink: true }, policy: 'deny' },
    ]);
    // their revision already blocks IoT → Internet: nothing of mine is left for the matrix
    d.discard();
    d.track(revision(44, { networks: networks() }));
    d.toggleMatrix(`network:${IOT}`, 'uplink');
    d.rebase(revision(45, { networks: networks(), access_matrix: { entries: [{ from: { network: IOT }, to: { uplink: true }, policy: 'deny' }] } }));
    expect(d.matrix).toBeUndefined();
    expect(d.dirty).toBe(false);
  });

  it('names a client field both sides changed; a matrix pair both changed agrees (P3-M13-07)', () => {
    const d = useDraftStore();
    d.track(revision(42, { networks: networks() }));
    d.setClient(HUB, RA, { name: 'rA', address: '10.99.0.2', enabled: false });
    d.toggleMatrix(`network:${IOT}`, 'uplink');
    const theirs = networks()!;
    (theirs[HUB] as { clients: Record<string, unknown> }).clients[RA] = { name: 'rA', address: '10.99.0.2', enabled: true, keepalive: '5s' };
    // they switched it on explicitly, I switched it off; their keepalive stays theirs
    expect(d.overlaps(revision(43, { networks: theirs }).configuration)).toEqual([
      { kind: 'client', id: RA, name: 'rA', field: 'enabled', from: 'yes', to: 'no' },
    ]);
    const theirMatrix = { entries: [{ from: { network: IOT }, to: { uplink: true as const }, policy: 'deny' as const }] };
    expect(d.overlaps(revision(43, { networks: networks(), access_matrix: theirMatrix }).configuration)).toEqual([]);
  });
});

describe('the API panel', () => {
  it('shows a request as HTTP and as curl with a token', () => {
    const call = { method: 'POST' as const, path: '/overlays', body: { profile: 'lte' }, headers: { 'If-Match': '"42"' } };
    expect(httpOf(call)).toBe('POST /api/v1/overlays\nIf-Match: "42"\n\n{\n  "profile": "lte"\n}');
    expect(curlOf(call, 'https://gw')).toBe(
      `curl -X POST 'https://gw/api/v1/overlays' \\\n  -H "Authorization: Bearer $CHAOSGW_TOKEN" \\\n  -H 'If-Match: "42"' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"profile":"lte"}'`,
    );
  });

  it('quotes a body and a header with an apostrophe for the shell', () => {
    const call = { method: 'POST' as const, path: '/overlays', body: { description: "Bob's phone" }, headers: { 'X-Note': "it's" } };
    expect(curlOf(call, 'https://gw')).toBe(
      `curl -X POST 'https://gw/api/v1/overlays' \\\n  -H "Authorization: Bearer $CHAOSGW_TOKEN" \\\n  -H 'X-Note: it'\\''s' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"description":"Bob'\\''s phone"}'`,
    );
  });

  it('a new screen forgets the last action of the one before', () => {
    const p = useApiPanelStore();
    p.screen('a', [{ method: 'GET', path: '/a' }]);
    p.record('did', [{ method: 'DELETE', path: '/x' }]);
    expect(p.last?.title).toBe('did');
    p.screen('b', [{ method: 'GET', path: '/b' }]);
    expect(p.last).toBeUndefined();
    expect(p.context?.title).toBe('b');
  });
});
