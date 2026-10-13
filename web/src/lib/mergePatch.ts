// JSON Merge Patch (RFC 7396) between two documents: what a candidate revision sends to change the
// base revision's configuration into the edited one. Objects are merged key by key, a key the
// edited document no longer has is `null`, anything else (arrays, strings, numbers) is replaced
// whole.

/** A deep copy of a JSON value (also of a reactive proxy, which structuredClone refuses). */
export function cloneJSON<T>(v: T): T {
  return v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T);
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** Deep equality of JSON values (key order does not matter, an undefined member is no member). */
export function jsonEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((x, i) => jsonEqual(x, b[i]));
  if (isObject(a) && isObject(b)) {
    const ka = Object.keys(a).filter((k) => a[k] !== undefined);
    const kb = Object.keys(b).filter((k) => b[k] !== undefined);
    return ka.length === kb.length && ka.every((k) => jsonEqual(a[k], b[k]));
  }
  return false;
}

/**
 * The merge patch that turns `base` into `target`, or undefined when they are equal. A `null` (or
 * undefined) target deletes the member.
 */
export function createMergePatch(base: unknown, target: unknown): unknown {
  if (target === null || target === undefined) return base === undefined || base === null ? undefined : null;
  if (isObject(base) && isObject(target)) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(base)) {
      if (base[k] !== undefined && target[k] === undefined) out[k] = null;
    }
    for (const k of Object.keys(target)) {
      if (target[k] === undefined) continue;
      const sub = createMergePatch(base[k], target[k]);
      if (sub !== undefined) out[k] = sub;
    }
    return Object.keys(out).length ? out : undefined;
  }
  return jsonEqual(base, target) ? undefined : cloneJSON(target);
}

/** Applies a merge patch (RFC 7396) to a document; the document itself is not changed. */
export function applyMergePatch<T>(doc: T, patch: unknown): T {
  if (!isObject(patch)) return cloneJSON(patch) as T;
  const out: Record<string, unknown> = isObject(doc) ? { ...doc } : {};
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k];
    else out[k] = applyMergePatch(out[k], v);
  }
  return out as T;
}
