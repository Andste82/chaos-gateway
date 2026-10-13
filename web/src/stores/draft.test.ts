import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import type { RevisionWithConfiguration } from '../api/generated/model';
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
});

describe('the API panel', () => {
  it('shows a request as HTTP and as curl with a token', () => {
    const call = { method: 'POST' as const, path: '/overlays', body: { profile: 'lte' }, headers: { 'If-Match': '"42"' } };
    expect(httpOf(call)).toBe('POST /api/v1/overlays\nIf-Match: "42"\n\n{\n  "profile": "lte"\n}');
    expect(curlOf(call, 'https://gw')).toBe(
      `curl -X POST 'https://gw/api/v1/overlays' \\\n  -H "Authorization: Bearer $CHAOSGW_TOKEN" \\\n  -H 'If-Match: "42"' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"profile":"lte"}'`,
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
