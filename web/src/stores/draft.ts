import { defineStore } from 'pinia';
import { computed, ref, watch } from 'vue';
import type { AccessRule, ConfigFault, Configuration, ConfigurationPatch, Device, RevisionWithConfiguration } from '../api/generated/model';
import { faultChips, describeScope, type Names } from '../lib/faults';
import { applyMergePatch, cloneJSON, createMergePatch, jsonEqual } from '../lib/mergePatch';
import { ruleErrors, ruleFieldChanges, formatPorts, type FieldChange } from '../lib/rules';

/** One change of the draft as the unapplied-changes bar counts it and the rule list marks it. */
export interface DraftChange extends FieldChange {
  kind: 'rule' | 'fault' | 'device';
  id: string;
  /** The object's name (or its id when it has none). */
  name: string;
}

/**
 * The configuration changes made in the UI and not applied yet (plan §2.17, "Unapplied changes"):
 * access rules, their order, configured faults and adopted devices, edited against the revision that was active
 * when the first edit was made. Nothing reaches the gateway until "Preview & apply" turns the draft
 * into a candidate revision (a JSON Merge Patch against that base, `If-Match` the base); a base
 * that is not the active revision any more is the concurrent-change dialog's (reload and reapply).
 * The draft lives across the screens of the shell, so a rule and a fault can go into one revision.
 */
export const useDraftStore = defineStore('draft', () => {
  /** The revision the draft is based on and its configuration. */
  const base = ref<number>();
  const baseConfig = ref<Configuration>();
  /** Edited rules by id (the whole rule), `null` for a deleted one. A new rule has no base. */
  const rules = ref<Record<string, AccessRule | null>>({});
  /** The edited order of the rules, when it was touched (a new or deleted rule, a move). */
  const order = ref<string[]>();
  /** Edited configured faults by id, `null` for a deleted one. */
  const faults = ref<Record<string, ConfigFault | null>>({});
  /** Devices added to the configuration (adopted), by id. */
  const devices = ref<Record<string, Device | null>>({});
  /** The ports as typed, by rule (a text that is no list of ports yet keeps its error). */
  const portsText = ref<Record<string, string>>({});

  const dirty = computed(
    () => Object.keys(rules.value).length > 0 || order.value !== undefined || Object.keys(faults.value).length > 0 || Object.keys(devices.value).length > 0,
  );

  /** Takes the active revision as the base, unless the draft has edits on another one. */
  function track(rev: RevisionWithConfiguration) {
    latest = rev;
    if (dirty.value && base.value !== undefined) return;
    base.value = rev.id;
    baseConfig.value = rev.configuration;
  }
  /** The newest active revision seen; the base after a discard. */
  let latest: RevisionWithConfiguration | undefined;
  /** The newest active revision as the base (a clean draft, after a discard or the last edit undone). */
  function rebaseOnLatest() {
    if (!latest || base.value === latest.id) return;
    base.value = latest.id;
    baseConfig.value = latest.configuration;
  }
  // edits undone by hand leave a clean draft: it follows the active revision again, as after a
  // discard, so a revision applied meanwhile is shown and the next edit starts from it
  watch(
    dirty,
    (d) => {
      if (!d) rebaseOnLatest();
    },
    { flush: 'sync' },
  );

  const baseRules = computed(() => baseConfig.value?.access_rules ?? {});
  const baseOrder = computed(() => baseConfig.value?.access_rule_order ?? []);
  const baseFaults = computed(() => baseConfig.value?.faults ?? {});

  /** The rules as the draft has them: base with the edits. */
  const effectiveRules = computed<Record<string, AccessRule>>(() => {
    const out: Record<string, AccessRule> = { ...baseRules.value };
    for (const [id, r] of Object.entries(rules.value)) {
      if (r === null) delete out[id];
      else out[id] = r;
    }
    return out;
  });

  /** The ids of the rules in the order the draft has them. */
  const effectiveOrder = computed<string[]>(() => {
    const all = effectiveRules.value;
    const ids = (order.value ?? baseOrder.value).filter((id) => id in all);
    for (const id of Object.keys(all)) if (!ids.includes(id)) ids.push(id);
    return ids;
  });

  const effectiveFaults = computed<Record<string, ConfigFault>>(() => {
    const out: Record<string, ConfigFault> = { ...baseFaults.value };
    for (const [id, f] of Object.entries(faults.value)) {
      if (f === null) delete out[id];
      else out[id] = f;
    }
    return out;
  });

  function clearServerErrors(id: string) {
    if (!(id in serverErrors.value)) return;
    const { [id]: _drop, ...rest } = serverErrors.value;
    serverErrors.value = rest;
  }

  function setRule(id: string, rule: AccessRule | null) {
    clearServerErrors(id);
    const before = baseRules.value[id];
    if ((rule === null && before === undefined) || (rule !== null && before !== undefined && jsonEqual(before, rule))) {
      const { [id]: _drop, ...rest } = rules.value;
      rules.value = rest;
    } else {
      rules.value = { ...rules.value, [id]: rule === null ? null : cloneJSON(rule) };
    }
    if (rule === null || before === undefined) {
      // a new rule goes to the end, a deleted one out of the order
      const ids = effectiveOrder.value.filter((x) => x !== id);
      if (rule !== null) ids.push(id);
      setOrder(ids);
    }
  }

  function setOrder(ids: string[]) {
    order.value = jsonEqual(ids, baseOrder.value) ? undefined : [...ids];
  }

  function move(id: string, by: -1 | 1) {
    const ids = [...effectiveOrder.value];
    const i = ids.indexOf(id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j]!, ids[i]!];
    setOrder(ids);
  }

  function setFault(id: string, fault: ConfigFault | null) {
    clearServerErrors(id);
    const before = baseFaults.value[id];
    if ((fault === null && before === undefined) || (fault !== null && before !== undefined && jsonEqual(before, fault))) {
      const { [id]: _drop, ...rest } = faults.value;
      faults.value = rest;
    } else {
      faults.value = { ...faults.value, [id]: fault === null ? null : cloneJSON(fault) };
    }
  }

  /** Undoes the deletion of a rule of the base: it comes back as it was, at its place in the order. */
  function restoreRule(id: string) {
    const before = baseRules.value[id];
    if (!before || rules.value[id] !== null) return;
    setRule(id, before);
    const ids = effectiveOrder.value.filter((x) => x !== id);
    // after the nearest rule that stands before it in the base and is still there
    const prev = baseOrder.value.slice(0, baseOrder.value.indexOf(id)).reverse().find((x) => ids.includes(x));
    ids.splice(prev === undefined ? 0 : ids.indexOf(prev) + 1, 0, id);
    setOrder(ids);
  }

  /** Undoes the deletion of a configured fault of the base. */
  function restoreFault(id: string) {
    const before = baseFaults.value[id];
    if (before && faults.value[id] === null) setFault(id, before);
  }

  function setDevice(id: string, device: Device | null) {
    clearServerErrors(id);
    const before = baseConfig.value?.devices?.[id];
    if ((device === null && before === undefined) || (device !== null && before !== undefined && jsonEqual(before, device))) {
      const { [id]: _drop, ...rest } = devices.value;
      devices.value = rest;
    } else {
      devices.value = { ...devices.value, [id]: device === null ? null : cloneJSON(device) };
    }
  }

  function setPortsText(id: string, text: string | undefined) {
    const { [id]: _drop, ...rest } = portsText.value;
    portsText.value = text === undefined ? rest : { ...rest, [id]: text };
  }

  /** The changes in words, rule by rule and fault by fault (the bar's count, the CHANGED marks). */
  function changes(names: Names = {}): DraftChange[] {
    const out: DraftChange[] = [];
    for (const [id, r] of Object.entries(rules.value)) {
      const before = baseRules.value[id];
      const name = r?.name ?? before?.name ?? id;
      if (r === null) out.push({ kind: 'rule', id, name, field: 'rule', from: 'present', to: 'deleted' });
      else if (!before) out.push({ kind: 'rule', id, name, field: 'rule', from: '—', to: 'added' });
      else for (const c of ruleFieldChanges(before, r, names)) out.push({ kind: 'rule', id, name, ...c });
    }
    if (order.value) {
      const was = baseOrder.value.filter((id) => id in effectiveRules.value);
      const now = effectiveOrder.value.filter((id) => id in baseRules.value);
      now.forEach((id, i) => {
        if (was[i] !== id && !(id in rules.value && rules.value[id] === null)) {
          const r = effectiveRules.value[id];
          out.push({ kind: 'rule', id, name: r?.name ?? id, field: 'position', from: String(was.indexOf(id) + 1), to: String(i + 1) });
        }
      });
    }
    for (const [id, f] of Object.entries(faults.value)) {
      const before = baseFaults.value[id];
      const name = f?.name ?? before?.name ?? id;
      if (f === null) out.push({ kind: 'fault', id, name, field: 'fault', from: 'present', to: 'deleted' });
      else if (!before) out.push({ kind: 'fault', id, name, field: 'fault', from: '—', to: `added (${faultChips(f).join(', ')})` });
      else {
        const add = (field: string, a: string, b: string) => a !== b && out.push({ kind: 'fault', id, name, field, from: a || '—', to: b || '—' });
        add('name', before.name ?? '', f.name ?? '');
        add('target', describeScope(before.source, names), describeScope(f.source, names));
        add('traffic', `${before.protocol ?? 'any'} ${formatPorts(before)} ${before.destination?.cidr ?? ''}`.trim(), `${f.protocol ?? 'any'} ${formatPorts(f)} ${f.destination?.cidr ?? ''}`.trim());
        add('parameters', faultChips(before).join(', '), faultChips(f).join(', '));
        add('enabled', before.enabled === false ? 'no' : 'yes', f.enabled === false ? 'no' : 'yes');
      }
    }
    for (const [id, dv] of Object.entries(devices.value)) {
      const before = baseConfig.value?.devices?.[id];
      const name = dv?.name ?? before?.name ?? id;
      if (dv === null) out.push({ kind: 'device', id, name, field: 'device', from: 'configured', to: 'deleted' });
      else if (!before) {
        const ids = [...(dv.identifiers?.macs ?? []), ...(dv.identifiers?.ipv4 ?? [])].join(', ');
        out.push({ kind: 'device', id, name, field: 'device', from: 'discovered', to: `configured${ids ? ` (${ids})` : ''}` });
      } else if (before.name !== dv.name) out.push({ kind: 'device', id, name, field: 'name', from: before.name, to: dv.name });
    }
    return out;
  }

  /** "3 changes in 2 rules", "1 change in 1 fault", "4 changes in 2 rules and 1 fault". */
  function summary(names: Names = {}): string {
    const cs = changes(names);
    const ruleIds = new Set(cs.filter((c) => c.kind === 'rule').map((c) => c.id));
    const faultIds = new Set(cs.filter((c) => c.kind === 'fault').map((c) => c.id));
    const deviceIds = new Set(cs.filter((c) => c.kind === 'device').map((c) => c.id));
    const objects = [
      ruleIds.size ? `${ruleIds.size} ${ruleIds.size === 1 ? 'rule' : 'rules'}` : '',
      faultIds.size ? `${faultIds.size} ${faultIds.size === 1 ? 'fault' : 'faults'}` : '',
      deviceIds.size ? `${deviceIds.size} ${deviceIds.size === 1 ? 'device' : 'devices'}` : '',
    ].filter(Boolean);
    const list = objects.length > 1 ? `${objects.slice(0, -1).join(', ')} and ${objects.at(-1)}` : (objects[0] ?? '');
    return `${cs.length} ${cs.length === 1 ? 'change' : 'changes'} in ${list}`;
  }

  /** The rules of the draft that cannot be applied as they are, with their problems. */
  const invalid = computed(() => {
    const out: Record<string, ReturnType<typeof ruleErrors>> = {};
    for (const [id, r] of Object.entries(rules.value)) {
      if (!r) continue;
      const e = ruleErrors(r, portsText.value[id]);
      if (Object.keys(e).length) out[id] = e;
    }
    return out;
  });

  /** The JSON Merge Patch against the base: whole objects for what changed, null for what went. */
  function patch(): ConfigurationPatch {
    const out: ConfigurationPatch = {};
    const rp: Record<string, unknown> = {};
    for (const [id, r] of Object.entries(rules.value)) {
      const p = createMergePatch(baseRules.value[id], r);
      if (p !== undefined) rp[id] = p;
    }
    if (Object.keys(rp).length) out.access_rules = rp;
    if (!jsonEqual(effectiveOrder.value, baseOrder.value)) out.access_rule_order = [...effectiveOrder.value];
    const fp: Record<string, unknown> = {};
    for (const [id, f] of Object.entries(faults.value)) {
      const p = createMergePatch(baseFaults.value[id], f);
      if (p !== undefined) fp[id] = p;
    }
    if (Object.keys(fp).length) out.faults = fp;
    const dp: Record<string, unknown> = {};
    for (const [id, dv] of Object.entries(devices.value)) {
      const p = createMergePatch(baseConfig.value?.devices?.[id], dv);
      if (p !== undefined) dp[id] = p;
    }
    if (Object.keys(dp).length) out.devices = dp;
    return out;
  }

  /**
   * Reapplies the draft on another revision (the concurrent-change dialog's "reload and reapply"):
   * my change of an object (a merge patch against the old base) on their version of it, so a field
   * only they changed keeps their value and a field both changed takes mine; an object they removed
   * comes back as I have it; deletions of what is still there; and the order the draft had with the
   * rules the other revision added kept where it put them.
   */
  function rebase(rev: RevisionWithConfiguration) {
    const theirOrder = rev.configuration.access_rule_order ?? [];
    const theirRules = rev.configuration.access_rules ?? {};
    const theirFaults = rev.configuration.faults ?? {};
    const theirDevices = rev.configuration.devices ?? {};
    const mine = order.value;
    const old = baseConfig.value;
    function reapply<T>(edits: Record<string, T | null>, before: Record<string, T>, theirs: Record<string, T>): Record<string, T | null> {
      const out: Record<string, T | null> = {};
      for (const [id, m] of Object.entries(edits)) {
        if (m === null) {
          // a deletion of what is gone already drops out
          if (id in theirs) out[id] = null;
          continue;
        }
        const was = before[id];
        const now = theirs[id];
        if (was === undefined || now === undefined) {
          // new on my side, or removed on theirs: mine as a whole
          out[id] = m;
          continue;
        }
        const merged = applyMergePatch(now, createMergePatch(was, m));
        if (!jsonEqual(merged, now)) out[id] = merged;
      }
      return out;
    }
    base.value = rev.id;
    baseConfig.value = rev.configuration;
    rules.value = reapply(rules.value, old?.access_rules ?? {}, theirRules);
    faults.value = reapply(faults.value, old?.faults ?? {}, theirFaults);
    devices.value = reapply(devices.value, old?.devices ?? {}, theirDevices);
    if (mine) {
      const ids = mine.filter((id) => id in effectiveRules.value);
      theirOrder.forEach((id, i) => {
        if (!ids.includes(id) && id in effectiveRules.value) ids.splice(Math.min(i, ids.length), 0, id);
      });
      setOrder(ids);
    }
  }

  function discard() {
    rules.value = {};
    order.value = undefined;
    faults.value = {};
    devices.value = {};
    portsText.value = {};
    serverErrors.value = {};
    // a clean draft is based on the newest active revision seen
    rebaseOnLatest();
  }

  /**
   * What the gateway refused when the draft became a candidate (422), by object id and field
   * (`rule` for a rule or a device as a whole; for a fault the pointer below it, `fault` for the
   * fault as a whole); an edit of the object clears its messages.
   */
  const serverErrors = ref<Record<string, Record<string, string>>>({});

  function setServerErrors(errors: { path?: string; message?: string }[]) {
    const out: Record<string, Record<string, string>> = {};
    for (const e of errors) {
      const m = /^\/(access_rules|faults|devices)\/([^/]+)(?:\/(.+))?/.exec(e.path ?? '');
      if (!m) continue;
      // a fault keeps the rest of the pointer ("upload/latency", `fault` for the fault as a whole),
      // which the fault dialog maps to its fields; a rule and a device the field below the object
      const first = m[3]?.split('/')[0];
      const field = m[1] === 'faults' ? (m[3] ?? 'fault') : first === 'port_ranges' ? 'ports' : (first ?? 'rule');
      (out[m[2]!] ??= {})[field] = e.message ?? 'not valid';
    }
    serverErrors.value = out;
  }

  /** The preview-and-apply drawer and the concurrent-change dialog, opened from any screen. */
  const drawerOpen = ref(false);
  /** The active revision that was applied over the draft's base (the conflict dialog is open). */
  const conflict = ref<{ base: number; active?: number }>();

  return {
    base,
    baseConfig,
    rules,
    order,
    faults,
    devices,
    portsText,
    dirty,
    effectiveRules,
    effectiveOrder,
    effectiveFaults,
    invalid,
    track,
    setRule,
    setOrder,
    move,
    setFault,
    restoreRule,
    restoreFault,
    setDevice,
    setPortsText,
    changes,
    summary,
    patch,
    rebase,
    discard,
    serverErrors,
    setServerErrors,
    drawerOpen,
    conflict,
  };
});
