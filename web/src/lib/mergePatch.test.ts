import { describe, expect, it } from 'vitest';
import { applyMergePatch, bothChanged, cloneJSON, createMergePatch, jsonEqual, reapplyMergePatch } from './mergePatch';

describe('merge patch (RFC 7396)', () => {
  it('is undefined between equal documents, key order and undefined members aside', () => {
    expect(createMergePatch({ a: 1, b: { c: [1, 2] } }, { b: { c: [1, 2] }, a: 1, d: undefined })).toBeUndefined();
    expect(jsonEqual({ a: 1, b: undefined }, { a: 1 })).toBe(true);
    expect(jsonEqual([1, 2], [2, 1])).toBe(false);
  });

  it('changes what changed, deletes what went with null, replaces arrays whole', () => {
    const base = { name: 'no-dns', ports: [53], protocol: 'udp', action: 'drop', cut_existing: true };
    const target = { name: 'no-dns', ports: [53, 5353], protocol: 'udp', action: 'reject' };
    expect(createMergePatch(base, target)).toEqual({ ports: [53, 5353], action: 'reject', cut_existing: null });
  });

  it('a new object is the whole object, a deleted one null, a nested change only the nested part', () => {
    expect(createMergePatch(undefined, { a: 1 })).toEqual({ a: 1 });
    expect(createMergePatch({ a: 1 }, null)).toBeNull();
    expect(createMergePatch(undefined, null)).toBeUndefined();
    expect(createMergePatch({ source: { network: 'x' }, d: { cidr: '1.2.3.4' } }, { source: { group: 'g' }, d: { cidr: '1.2.3.4' } })).toEqual({
      source: { network: null, group: 'g' },
    });
  });

  it('applying the patch to the base gives the target', () => {
    const base = { a: 1, b: { c: 2, d: 3 }, e: [1] };
    const target = { a: 1, b: { c: 5 }, e: [1, 2], f: 'new' };
    expect(applyMergePatch(base, createMergePatch(base, target))).toEqual(target);
    expect(base).toEqual({ a: 1, b: { c: 2, d: 3 }, e: [1] }); // unchanged
  });

  it('copies a value deeply', () => {
    const v = { a: [{ b: 1 }] };
    const c = cloneJSON(v);
    c.a[0]!.b = 2;
    expect(v.a[0]!.b).toBe(1);
  });

  it('names the members both edits changed to different values, a scope as a whole', () => {
    const base = { name: 'a', action: 'drop', enabled: true, source: { network: 'n1' }, upload: { latency: '10ms', loss: '1%' } };
    const theirs = { name: 'b', action: 'reset', source: { group: 'g' }, upload: { latency: '20ms', loss: '1%' } };
    const mine = { name: 'b', action: 'reject', enabled: true, source: { network: 'n2' }, upload: { latency: '10ms', loss: '2%' } };
    // the name both set to b is no conflict; the loss only I changed and the removed enabled only they did
    expect(bothChanged(base, theirs, mine, new Set(['source']))).toEqual([['action'], ['source']]);
    expect(bothChanged(base, { ...base, upload: { latency: '30ms' } }, { ...base, upload: { latency: '40ms', loss: '1%' } })).toEqual([['upload', 'latency']]);
    expect(bothChanged(base, base, mine)).toEqual([]);
  });

  it('reapplies my edit on theirs: their fields stay, a scope I changed is mine as a whole', () => {
    const atomic = new Set(['source', 'target']);
    const base = { name: 'a', action: 'drop', source: { group: 'g1' }, upload: { latency: '10ms' } };
    const theirs = { name: 'a', action: 'drop', enabled: false, source: { device: 'd2' }, upload: { latency: '20ms' } };
    const mine = { name: 'a', action: 'reject', source: { device: 'd1' }, upload: { latency: '10ms', loss: '1%' } };
    // merged key by key the scope would be { group: deleted, device: d1 } on { device: d2 }; with a
    // network of mine it would carry two keys
    expect(reapplyMergePatch(base, theirs, mine, atomic)).toEqual({ name: 'a', action: 'reject', enabled: false, source: { device: 'd1' }, upload: { latency: '20ms', loss: '1%' } });
    const mineNet = { ...base, source: { network: 'n1' } };
    expect(applyMergePatch(theirs, createMergePatch(base, mineNet)).source).toEqual({ device: 'd2', network: 'n1' });
    expect(reapplyMergePatch(base, theirs, mineNet, atomic).source).toEqual({ network: 'n1' });
    // a scope inside a scope (remote_network client vs link) goes with it
    const t2 = { ...base, target: { remote_network: { link: 'l1' } } };
    expect(reapplyMergePatch({ ...base, target: { global: true } }, t2, { ...base, target: { remote_network: { client: 'c1' } } }, atomic).target).toEqual({ remote_network: { client: 'c1' } });
    // a scope only they changed stays theirs; no edit of mine leaves theirs
    expect(reapplyMergePatch(base, theirs, { ...base, action: 'reject' }, atomic).source).toEqual({ device: 'd2' });
    expect(reapplyMergePatch(base, theirs, base, atomic)).toEqual(theirs);
  });
});
