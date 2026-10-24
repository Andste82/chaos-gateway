import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import { nextTick } from 'vue';
import type { AccessRule, ConfigFault, RevisionWithConfiguration } from '../api/generated/model';
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

  it('forgets the edits and the configuration at a logout (Phase 3 audit)', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setRule(A, { name: 'a', source: { global: true }, action: 'reject' });
    d.drawerOpen = true;
    d.forget();
    expect(d.dirty).toBe(false);
    expect(d.base).toBeUndefined();
    expect(d.baseConfig).toBeUndefined();
    expect(d.drawerOpen).toBe(false);
    // the next session starts from the revision it reads, not from one the last one saw
    d.discard();
    expect(d.base).toBeUndefined();
    d.track(revision(44));
    expect(d.base).toBe(44);
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

  it('names every fault field the dialog edits: burst loss values, keep order, any destination and the tunnel (P3-M13-04)', () => {
    const d = useDraftStore();
    const NET = 'b0000000-0000-4000-8000-0000000000b1';
    const CL = 'b0000000-0000-4000-8000-0000000000c1';
    const LK = 'b0000000-0000-4000-8000-0000000000d1';
    const burst = { name: 'bursty', source: { global: true as const }, burst_loss: { p: '5%', r: '30%' } };
    const host = { name: 'host', source: { global: true as const }, protocol: 'tcp' as const, ports: [443], destination: { hostname: 'a.example.com' }, latency: '10ms' };
    const tun = { name: 'tun', family: 'tunnel' as const, tunnel: { client: CL }, latency: '50ms' };
    d.track(revision(42, { faults: { burst, host, tun } }));
    const names = { device: (id: string) => (id === CL ? 'rA' : undefined), network: (id: string) => ({ [NET]: 'IoT', [LK]: 'site-b' })[id] };

    d.setFault('burst', { ...burst, burst_loss: { p: '10%', r: '30%' } });
    expect(d.changes(names)).toEqual([{ kind: 'fault', id: 'burst', name: 'bursty', field: 'parameters', from: 'burst loss 5 %/30 %', to: 'burst loss 10 %/30 %' }]);
    expect(d.summary(names)).toBe('1 change in 1 fault');
    d.setFault('burst', { ...burst, burst_loss: { p: '5%', r: '30%', h: '10%', k: '90%' }, keep_order: true });
    expect(d.changes(names)).toMatchObject([{ field: 'parameters', from: 'burst loss 5 %/30 %', to: 'keep order, burst loss 5 %/30 % h 10 % k 90 %' }]);
    d.setFault('burst', burst);
    expect(d.dirty).toBe(false);

    d.setFault('host', { ...host, destination: { hostname: 'b.example.com' } });
    expect(d.changes(names)).toEqual([{ kind: 'fault', id: 'host', name: 'host', field: 'traffic', from: 'tcp 443 a.example.com', to: 'tcp 443 b.example.com' }]);
    d.setFault('host', { ...host, destination: { network: NET } });
    expect(d.changes(names)).toMatchObject([{ field: 'traffic', to: 'tcp 443 network IoT' }]);
    d.setFault('host', { ...host, destination: { uplink: true } });
    expect(d.changes(names)).toMatchObject([{ field: 'traffic', to: 'tcp 443 uplink' }]);
    d.setFault('host', host);

    d.setFault('tun', { ...tun, tunnel: { link: LK } });
    expect(d.changes(names)).toEqual([{ kind: 'fault', id: 'tun', name: 'tun', field: 'tunnel', from: 'Tunnel of rA', to: 'Tunnel of link site-b' }]);
    expect(d.summary(names)).toBe('1 change in 1 fault');
    d.setFault('tun', tun);

    // a change the words do not show is still one: settings as before → changed
    d.setFault('host', { ...host, latency: '10ms', created_at: '2026-10-14T00:00:00Z' });
    expect(d.changes(names)).toEqual([{ kind: 'fault', id: 'host', name: 'host', field: 'settings', from: 'as before', to: 'changed' }]);
    expect(d.summary(names)).toBe('1 change in 1 fault');
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

  it('an edited device lists its name, network, fixed IP and MACs; clearing the fixed IP removes it in the patch (#73)', () => {
    const d = useDraftStore();
    const dev = { name: 'esp', identifiers: { macs: ['02:00:00:00:00:01', '02:00:00:00:00:02'] }, network: 'n1', fixed_ip: '10.10.0.50' };
    d.track(revision(42, { devices: { d1: dev }, networks: { n1: { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24' } } }));
    d.setDevice('d1', { ...dev, name: 'esp-1', fixed_ip: '10.10.0.60', identifiers: { macs: ['02:00:00:00:00:02', '02:00:00:00:00:01'] } });
    expect(d.changes().map((c) => [c.field, c.from, c.to])).toEqual([
      ['name', 'esp', 'esp-1'],
      ['fixed IP', '10.10.0.50', '10.10.0.60'],
      ['MACs', '02:00:00:00:00:01, 02:00:00:00:00:02', '02:00:00:00:00:02, 02:00:00:00:00:01'],
    ]);
    const { fixed_ip: _f, network: _n, ...rest } = dev;
    d.setDevice('d1', rest);
    expect(d.changes().map((c) => [c.field, c.from, c.to])).toEqual([
      ['network', 'IoT', '—'],
      ['fixed IP', '10.10.0.50', '—'],
    ]);
    expect(d.patch().devices).toEqual({ d1: { network: null, fixed_ip: null } });
    d.setServerErrors([{ path: '/devices/d1/fixed_ip', message: 'outside' }]);
    expect(d.serverErrors).toEqual({ d1: { fixed_ip: 'outside' } });
    d.setDevice('d1', { ...dev, labels: ['x'] });
    expect(d.changes()).toMatchObject([{ field: 'settings', from: 'as before', to: 'changed' }]);
    expect(d.serverErrors).toEqual({});
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
    d.setServerErrors([{ path: `/access_rules/${A}/port_ranges/0`, message: 'too many' }, { path: '/settings/x', message: 'ignored' }]);
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
      { kind: 'rule', id: A, name: 'a', field: 'access', from: 'TCP reset', to: 'Reject' },
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

  it('a scope or a destination both changed: the dialog names it, and reapplying takes mine whole (P3-M13-07)', () => {
    const d = useDraftStore();
    d.track(revision(42));
    // mine: rule A from a group, to a prefix; fault f1 on a client's networks
    d.setRule(A, { name: 'a', source: { group: 'g1' }, destination: { cidr: '10.9.0.0/24' }, action: 'drop' });
    d.setFault('f1', { name: 'slow', source: { remote_network: { client: 'c1' } }, latency: '10ms' });
    // theirs: rule A from a device, to the uplink; fault f1 on a link's networks
    const theirs = revision(43, {
      access_rules: {
        [A]: { name: 'a', source: { device: 'd2' }, destination: { uplink: true }, action: 'drop' },
        [B]: revision(42).configuration.access_rules![B]!,
      },
      faults: { f1: { name: 'slow', source: { remote_network: { link: 'l1' } }, latency: '10ms' } },
    });
    const names = { device: (id: string) => ({ d1: 'D1', d2: 'D2' })[id], group: (id: string) => ({ g1: 'G' })[id] };
    expect(d.overlaps(theirs.configuration, names).map((c) => [c.kind, c.field, c.from, c.to])).toEqual([
      ['rule', 'from', 'D2', 'Group G'],
      ['rule', 'to', 'uplink', '10.9.0.0/24'],
      ['fault', 'target', 'Remote networks of link l1', 'Client networks of c1'],
    ]);
    d.rebase(theirs);
    // exactly mine: no scope with two keys, and the candidate is valid
    expect(d.effectiveRules[A]).toEqual({ name: 'a', source: { group: 'g1' }, destination: { cidr: '10.9.0.0/24' }, action: 'drop' });
    expect(d.effectiveFaults.f1?.source).toEqual({ remote_network: { client: 'c1' } });
    expect(d.invalid).toEqual({});
    expect(d.patch()).toEqual({
      access_rules: { [A]: { source: { device: null, group: 'g1' }, destination: { uplink: null, cidr: '10.9.0.0/24' } } },
      faults: { f1: { source: { remote_network: { link: null, client: 'c1' } } } },
    });
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

describe('a device removed together with the objects that name it (#99)', () => {
  const DEV = 'd0000000-0000-4000-8000-000000000001';
  const OTHER = 'd0000000-0000-4000-8000-000000000002';
  const G = 'c0000000-0000-4000-8000-000000000001';
  const F = 'e0000000-0000-4000-8000-000000000001';
  function lab(): RevisionWithConfiguration {
    return revision(42, {
      devices: { [DEV]: { name: 'esp' }, [OTHER]: { name: 'phone' } },
      access_rules: { ...revision(42).configuration.access_rules, [C]: { name: 'esp-out', source: { device: DEV }, action: 'drop' } },
      access_rule_order: [A, B, C],
      groups: { [G]: { name: 'lab', members: [DEV, OTHER] } },
      port_forwards: { [F]: { name: 'esp-web', protocol: 'tcp', entry: 'uplink', external_port: 8080, target: { device: DEV }, target_port: 80 } },
    });
  }
  function removeAll(d: ReturnType<typeof useDraftStore>) {
    d.removeDevice({ id: DEV, name: 'esp' });
  }

  it('puts everything into one patch and names it in the changes', () => {
    const d = useDraftStore();
    d.track(lab());
    removeAll(d);
    expect(d.patch()).toEqual({
      access_rules: { [C]: null },
      access_rule_order: [A, B],
      port_forwards: { [F]: null },
      groups: { [G]: { members: [OTHER] } },
      devices: { [DEV]: null },
    });
    const names = { device: (id: string) => ({ [DEV]: 'esp', [OTHER]: 'phone' })[id] };
    expect(d.changes(names).filter((c) => c.kind === 'group')).toEqual([{ kind: 'group', id: G, name: 'lab', field: 'members', from: 'esp, phone', to: 'phone' }]);
    expect(d.summary(names)).toBe('4 changes in 1 rule, 1 device, 1 group and 1 port forward');
    expect(d.effectiveGroups[G]!.members).toEqual([OTHER]);
  });

  it('a group back at its base is no change', () => {
    const d = useDraftStore();
    d.track(lab());
    d.setGroup(G, { name: 'lab', members: [OTHER] });
    expect(d.dirty).toBe(true);
    d.setGroup(G, { name: 'lab', members: [DEV, OTHER] });
    expect(d.dirty).toBe(false);
  });

  it('Undo of the removal brings the device back with the objects removed along', () => {
    const d = useDraftStore();
    d.track(lab());
    removeAll(d);
    d.restoreDevice(DEV);
    expect(d.dirty).toBe(false);
    expect(d.patch()).toEqual({});
  });

  it('Undo keeps the other edits of a group the device was taken out of', () => {
    const d = useDraftStore();
    d.track(lab());
    removeAll(d);
    d.setGroup(G, { name: 'lab', members: [OTHER, 'x'] });
    d.restoreDevice(DEV);
    expect(d.groups[G]).toEqual({ name: 'lab', members: [DEV, OTHER, 'x'] });
  });

  it('Undo brings back what the draft had: an edit of a rule removed along, a new rule at its place, not what was deleted before', () => {
    const d = useDraftStore();
    d.track(lab());
    const D = 'a1000000-0000-4000-8000-000000000004';
    d.setRule(C, { name: 'esp-out', source: { device: DEV }, action: 'reject' });
    d.setRule(D, { name: 'esp-in', source: { device: 'ESP' }, action: 'drop' });
    d.move(D, -1);
    d.move(D, -1);
    expect(d.effectiveOrder).toEqual([A, D, B, C]);
    // a forward the user deleted before the removal stays deleted after its Undo
    d.setForward(F, null);
    removeAll(d);
    expect(d.effectiveOrder).toEqual([A, B]);
    expect(d.rules[D]).toBeUndefined();
    d.restoreDevice(DEV);
    expect(d.devices[DEV]).toBeUndefined();
    expect(d.effectiveRules[C]?.action).toBe('reject');
    expect(d.effectiveRules[D]?.name).toBe('esp-in');
    expect(d.effectiveOrder).toEqual([A, D, B, C]);
    expect(d.forwards[F]).toBeNull();
    expect(d.groups).toEqual({});
  });

  it('Undo of one removal leaves the other device out of the group', () => {
    const d = useDraftStore();
    d.track(lab());
    removeAll(d);
    d.removeDevice({ id: OTHER, name: 'phone' });
    expect(d.effectiveGroups[G]!.members).toEqual([]);
    d.restoreDevice(DEV);
    expect(d.effectiveGroups[G]!.members).toEqual([DEV]);
    expect(d.devices[OTHER]).toBeNull();
  });

  it('reapplies the group change on theirs; a group both changed is named in the overlaps', () => {
    const d = useDraftStore();
    d.track(lab());
    removeAll(d);
    const theirs = lab();
    theirs.id = 43;
    theirs.configuration.groups = { [G]: { name: 'lab', description: 'theirs', members: [DEV, OTHER, 'z'] } };
    const names = { device: (id: string) => ({ [DEV]: 'esp', [OTHER]: 'phone' })[id] };
    expect(d.overlaps(theirs.configuration, names).filter((c) => c.kind === 'group')).toEqual([
      { kind: 'group', id: G, name: 'lab', field: 'members', from: 'esp, phone, z', to: 'phone' },
    ]);
    d.rebase(theirs);
    expect(d.groups[G]).toEqual({ name: 'lab', description: 'theirs', members: [OTHER] });
    // the Undo after the reapply brings the device back as the new base has it, into the group again
    d.restoreDevice(DEV);
    expect(d.devices[DEV]).toBeUndefined();
    expect(d.rules[C]).toBeUndefined();
    expect(d.effectiveGroups[G]!.members).toEqual([DEV, OTHER]);
    d.removeDevice({ id: DEV, name: 'esp' });
    d.discard();
    expect(d.groups).toEqual({});
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

  it('a custom profile is added, changed and deleted through the draft (P3-M13-03)', () => {
    const d = useDraftStore();
    const P = 'c0000000-0000-4000-8000-0000000000c1';
    const slow = { name: 'slow-lab', parts: { impairment: { latency: '20ms' }, mtu: { size: 1280 } } };
    d.track(revision(42, { profiles: { [P]: slow } }));
    const NEW = 'c0000000-0000-4000-8000-0000000000c2';
    d.setProfile(NEW, { name: 'flaky', parts: { impairment: { loss: '1%' } } });
    d.setProfile(P, { ...slow, parts: { ...slow.parts, impairment: { latency: '30ms' } } });
    expect(d.changes()).toEqual([
      { kind: 'profile', id: NEW, name: 'flaky', field: 'profile', from: '—', to: 'added (1 % loss)' },
      { kind: 'profile', id: P, name: 'slow-lab', field: 'parameters', from: '↑↓ 20 ms, MTU 1280', to: '↑↓ 30 ms, MTU 1280' },
    ]);
    expect(d.summary()).toBe('2 changes in 2 profiles');
    expect(d.patch()).toEqual({ profiles: { [NEW]: { name: 'flaky', parts: { impairment: { loss: '1%' } } }, [P]: { parts: { impairment: { latency: '30ms' } } } } });
    expect(Object.keys(d.effectiveProfiles)).toEqual([P, NEW]);
    // deleted, then undone: back to the base, no change
    d.setProfile(P, null);
    expect(d.patch().profiles).toEqual({ [NEW]: { name: 'flaky', parts: { impairment: { loss: '1%' } } }, [P]: null });
    d.restoreProfile(P);
    expect(d.profiles).not.toHaveProperty(P);
    // the gateway's problems of a profile keep the pointer below it, for the editor's fields
    d.setServerErrors([{ path: `/profiles/${NEW}/parts/impairment/loss`, message: 'too much' }, { path: `/profiles/${NEW}`, message: 'whole' }]);
    expect(d.serverErrors[NEW]).toEqual({ 'parts/impairment/loss': 'too much', profile: 'whole' });
    d.setProfile(NEW, { name: 'flaky', parts: { impairment: { loss: '2%' } } });
    expect(d.serverErrors[NEW]).toBeUndefined();
    d.discard();
    expect(d.dirty).toBe(false);
  });

  it('a profile edit of burst loss values or of a part the chips do not show is a change (P3-M13-03)', () => {
    const d = useDraftStore();
    const P = 'c0000000-0000-4000-8000-0000000000c1';
    const lossy = { name: 'lossy', parts: { impairment: { burst_loss: { p: '5%', r: '30%' } } } };
    d.track(revision(42, { profiles: { [P]: lossy } }));
    d.setProfile(P, { name: 'lossy', parts: { impairment: { burst_loss: { p: '9%', r: '30%' } } } });
    expect(d.changes()).toEqual([{ kind: 'profile', id: P, name: 'lossy', field: 'parameters', from: 'burst loss 5 %/30 %', to: 'burst loss 9 %/30 %' }]);
    expect(d.summary()).toBe('1 change in 1 profile');
    d.setProfile(P, { name: 'lossy', parts: { impairment: { burst_loss: { p: '5%', r: '30%' }, distribution: 'uniform' } } });
    expect(d.changes()).toEqual([{ kind: 'profile', id: P, name: 'lossy', field: 'settings', from: 'as before', to: 'changed' }]);
    expect(d.summary()).toBe('1 change in 1 profile');
  });

  it('reapplies a profile on their revision and names a field both changed (P3-M13-03)', () => {
    const d = useDraftStore();
    const P = 'c0000000-0000-4000-8000-0000000000c1';
    const slow = { name: 'slow-lab', description: 'old', parts: { impairment: { latency: '20ms' } } };
    d.track(revision(42, { profiles: { [P]: slow } }));
    d.setProfile(P, { ...slow, parts: { impairment: { latency: '30ms' } } });
    const theirs = revision(43, { profiles: { [P]: { ...slow, description: 'theirs', parts: { impairment: { latency: '25ms' } } } } });
    expect(d.overlaps(theirs.configuration)).toEqual([{ kind: 'profile', id: P, name: 'slow-lab', field: 'parts impairment latency', from: '25ms', to: '30ms' }]);
    d.rebase(theirs);
    expect(d.effectiveProfiles[P]).toEqual({ name: 'slow-lab', description: 'theirs', parts: { impairment: { latency: '30ms' } } });
  });

  it('a fault made "each its own" on mine, a flat loss added on theirs: the dialog names the parameters, reapplying takes mine whole (P3-M13-04)', () => {
    const d = useDraftStore();
    const was: ConfigFault = { name: 'slow', source: { global: true }, latency: '20ms' };
    d.track(revision(42, { faults: { f1: was } }));
    d.setFault('f1', { name: 'slow', source: { global: true }, upload: { latency: '20ms' }, download: { loss: '1%' } });
    // theirs: a flat loss and a new description; no member both changed
    const theirs = revision(43, { faults: { f1: { ...was, description: 'theirs', loss: '2%' } } });
    expect(d.overlaps(theirs.configuration)).toEqual([{ kind: 'fault', id: 'f1', name: 'slow', field: 'parameters', from: '↑↓ 20 ms, 2 % loss', to: '↑ 20 ms, 1 % loss' }]);
    d.rebase(theirs);
    // no flat parameter next to upload/download (mixed_directions); their description stays
    expect(d.effectiveFaults.f1).toEqual({ name: 'slow', description: 'theirs', source: { global: true }, upload: { latency: '20ms' }, download: { loss: '1%' } });
  });

  it('a profile loss changed on mine, burst loss on theirs: the dialog names the parameters, reapplying takes mine whole (P3-M13-03)', () => {
    const d = useDraftStore();
    const P = 'c0000000-0000-4000-8000-0000000000c2';
    const lossy = { name: 'lossy', parts: { impairment: { latency: '20ms', loss: '1%' }, mtu: { size: 1280 } } };
    d.track(revision(42, { profiles: { [P]: lossy } }));
    d.setProfile(P, { ...lossy, parts: { ...lossy.parts, impairment: { latency: '20ms', loss: '3%' } } });
    const theirs = revision(43, { profiles: { [P]: { ...lossy, parts: { impairment: { latency: '20ms', burst_loss: { p: '5%', r: '30%' } }, mtu: { size: 1400 } } } } });
    expect(d.overlaps(theirs.configuration)).toEqual([
      { kind: 'profile', id: P, name: 'lossy', field: 'parts impairment', from: '↑↓ 20 ms, burst loss 5 %/30 %', to: '↑↓ 20 ms, 3 % loss' },
    ]);
    d.rebase(theirs);
    // not loss and burst loss together (mutually_exclusive); their MTU part stays
    expect(d.effectiveProfiles[P]).toEqual({ name: 'lossy', parts: { impairment: { latency: '20ms', loss: '3%' }, mtu: { size: 1400 } } });
  });

  it('blackout on mine, flapping on theirs: the merged parameters would be refused, so mine is taken whole', () => {
    const d = useDraftStore();
    const was: ConfigFault = { name: 'down', source: { global: true }, latency: '20ms' };
    d.track(revision(42, { faults: { f1: was } }));
    d.setFault('f1', { ...was, blackout: true });
    const theirs = revision(43, { faults: { f1: { ...was, flapping: { up: '20s', down: '10s' } } } });
    expect(d.overlaps(theirs.configuration).map((c) => [c.field, c.from, c.to])).toEqual([['parameters', '↑↓ 20 ms, ↑↓ flapping 20s up / 10s down', '↑↓ blackout, ↑↓ 20 ms']]);
    d.rebase(theirs);
    expect(d.effectiveFaults.f1).toEqual({ ...was, blackout: true });
  });

  it('parameters that combine are still merged member by member: their loss and my latency', () => {
    const d = useDraftStore();
    const was: ConfigFault = { name: 'slow', source: { global: true }, latency: '20ms' };
    d.track(revision(42, { faults: { f1: was } }));
    d.setFault('f1', { ...was, latency: '30ms' });
    const theirs = revision(43, { faults: { f1: { ...was, loss: '1%' } } });
    expect(d.overlaps(theirs.configuration)).toEqual([]);
    d.rebase(theirs);
    expect(d.effectiveFaults.f1).toEqual({ ...was, latency: '30ms', loss: '1%' });
  });

  it('the merge patch of one fault or profile for the editors\' API panel: what changed against the base revision, a new one whole', () => {
    const d = useDraftStore();
    const P = 'c0000000-0000-4000-8000-0000000000c3';
    d.track(revision(42, { faults: { f1: { name: 'slow', source: { global: true }, latency: '20ms', loss: '1%' } }, profiles: { [P]: { name: 'p', parts: { impairment: { loss: '1%' } } } } }));
    expect(d.objectPatch('fault', 'f1', { name: 'slow', source: { global: true }, upload: { latency: '20ms', loss: '1%' }, download: { latency: '20ms', loss: '1%' } })).toEqual({
      latency: null,
      loss: null,
      upload: { latency: '20ms', loss: '1%' },
      download: { latency: '20ms', loss: '1%' },
    });
    expect(d.objectPatch('profile', P, { name: 'p', parts: { impairment: { burst_loss: { p: '5%', r: '30%' } } } })).toEqual({ parts: { impairment: { loss: null, burst_loss: { p: '5%', r: '30%' } } } });
    expect(d.objectPatch('profile', P, { name: 'p', parts: { impairment: { loss: '1%' } } })).toEqual({});
    expect(d.objectPatch('fault', 'new', { name: 'n', source: { global: true }, loss: '2%' })).toEqual({ name: 'n', source: { global: true }, loss: '2%' });
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
