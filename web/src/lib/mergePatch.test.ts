import { describe, expect, it } from 'vitest';
import { applyMergePatch, cloneJSON, createMergePatch, jsonEqual } from './mergePatch';

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
});
