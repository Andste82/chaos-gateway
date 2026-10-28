import { defineStore } from 'pinia';
import { computed, ref, watch } from 'vue';
import type {
  AccessRule,
  ConfigFault,
  Configuration,
  ConfigurationPatch,
  Device,
  Group,
  ImpairmentParams,
  MatrixEntry,
  Network,
  NetworkView,
  PortForward,
  Probe,
  Profile,
  RevisionWithConfiguration,
  Routing,
  Scenario,
  RoutingProtocol,
  WireGuardClient,
  WireGuardNetwork,
} from '../api/generated/model';
import { deviceReferences, groupWithMember, groupWithout } from '../lib/devices';
import { forwardChanges, forwardTitle } from '../lib/forwards';
import { faultChips, describeScope, describeTunnel, impairmentCombines, impairmentKeys, impairmentOf, withImpairment, type Names } from '../lib/faults';
import { bothChanged, cloneJSON, createMergePatch, jsonEqual, reapplyMergePatch } from '../lib/mergePatch';
import { networkChanges, protocolChanges, routingSettingsChanges, withoutClients } from '../lib/networkChanges';
import { cellOf, entryKey, keysText, matrixChanges, toggleCell } from '../lib/networks';
import { probeChanges, probeTitle } from '../lib/probes';
import { profileChips } from '../lib/profiles';
import { scenarioChanges, scenarioProblems, scenarioTitle, type ScenarioProblem } from '../lib/scenarios';
import { accessLabels, describeDestination, ruleErrors, ruleFieldChanges, formatPorts, type FieldChange } from '../lib/rules';

/**
 * The members compared and reapplied as a whole when both sides changed an object: a scope or a destination is
 * one choice, and the steps and the checks of a scenario are lists (a merge patch replaces a list whole).
 */
const atomicMembers = new Set(['source', 'target', 'destination', 'steps', 'checks']);

/**
 * The impairment parameter set of a configured fault (its flat netem parameters and its directions)
 * and of a custom profile (`parts.impairment`): where it is, how an object takes another one, and the
 * field the concurrent-change dialog names for it.
 */
const parameterSets: Partial<Record<string, { get(o: unknown): ImpairmentParams | undefined; set<T>(o: T, i: ImpairmentParams | undefined): T; inside(path: string[]): boolean; field: string }>> = {
  fault: {
    get: (o) => impairmentOf(o as ConfigFault),
    set: (o, i) => withImpairment(o as ConfigFault, cloneJSON(i)) as typeof o,
    inside: (path) => impairmentKeys.has(path[0] ?? ''),
    field: 'parameters',
  },
  profile: {
    get: (o) => (o as Profile).parts?.impairment,
    set: (o, i) => {
      const parts = { ...(o as Profile).parts, impairment: cloneJSON(i) };
      if (i === undefined) delete parts.impairment;
      return { ...(o as Profile), parts } as typeof o;
    },
    inside: (path) => path[0] === 'parts' && path[1] === 'impairment',
    field: 'parts impairment',
  },
};

/**
 * My edit of an object reapplied on their version (P3-M13-07): member by member, a scope or a
 * destination whole. An impairment parameter set merged member by member can become one the gateway
 * refuses where neither side's was (their flat loss with my upload/download, their burst loss with
 * my loss): then my parameter set is taken whole (`whole`), and the dialog names it.
 */
function reapplyObject<T>(kind: string, was: T, now: T, mine: unknown): { merged: T; whole: boolean } {
  const merged = reapplyMergePatch(was, now, mine, atomicMembers);
  const ps = parameterSets[kind];
  if (!ps || impairmentCombines(ps.get(merged))) return { merged, whole: false };
  return { merged: ps.set(merged, ps.get(mine)), whole: true };
}

/** An impairment parameter set as chips: "↑↓ 20 ms, 2 % loss". */
function parameterText(i: ImpairmentParams | undefined): string {
  return (i ? faultChips(i) : []).join(', ') || '—';
}

/** The value of a member in words, for the fields both sides changed: "Reject", "no", "80, 443", "Network IoT". */
function valueText(key: string, v: unknown, names: Names, other?: unknown): string {
  if (key === 'enabled') return v === false ? 'no' : 'yes';
  // a switch that is not set is off (the other side's value says it is a switch)
  if (v === undefined || v === null) return typeof other === 'boolean' ? 'no' : '—';
  if (typeof v === 'boolean') return v ? 'yes' : 'no';
  // a forward's target is a device or an address
  if (key === 'target' && typeof v === 'object' && 'address' in v) return String((v as { address: string }).address);
  // a probe's network
  if (key === 'network' && typeof v === 'string') return names.network?.(v) ?? v;
  if (key === 'source' || key === 'target') return describeScope(v as Parameters<typeof describeScope>[0], names);
  if (key === 'destination') return describeDestination(v as Parameters<typeof describeDestination>[0], names);
  if (key === 'action') return accessLabels[v as keyof typeof accessLabels] ?? String(v);
  // a scenario's steps by their ids, its checks by their number
  if (key === 'steps' && Array.isArray(v)) return v.map((x) => (x as { id?: string }).id ?? '?').join(', ') || '—';
  // a group's members by the names of the devices
  if (key === 'members' && Array.isArray(v)) return v.map((x) => names.device?.(String(x)) ?? String(x)).join(', ') || '—';
  if (key === 'checks' && Array.isArray(v)) return v.length ? `${v.length} ${v.length === 1 ? 'check' : 'checks'}` : '—';
  if (Array.isArray(v)) {
    return v
      .map((x) => (typeof x === 'object' && x !== null && 'from' in x && 'to' in x ? `${(x as { from: number }).from}-${(x as { to: number }).to}` : typeof x === 'object' ? JSON.stringify(x) : String(x)))
      .join(', ') || '—';
  }
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

// a rule's members by the words the rule editor and the changes in words use (ruleFieldChanges):
// the conflict dialog names a field both sides changed the same way as "your change" does
const ruleFieldLabels: Record<string, string> = { source: 'from', destination: 'to', action: 'access', cut_existing: 'cut existing', port_ranges: 'ports' };

/** The words for a member of an object, for the fields both sides changed: "access", "from", "dhcp lease time". */
function fieldLabel(kind: string, path: string[]): string {
  if (kind === 'rule' && path[0] && ruleFieldLabels[path[0]]) return ruleFieldLabels[path[0]]!;
  // a fault's source is its target in the changes in words
  if (kind === 'fault' && path.length === 1 && path[0] === 'source') return 'target';
  return path.join(' ').replace(/_/g, ' ');
}

/** The revision an editor's form was built on (`editOrigin`). */
export interface EditOrigin {
  base?: number;
  config?: Configuration;
}

/** One change of the draft as the unapplied-changes bar counts it and the rule list marks it. */
export interface DraftChange extends FieldChange {
  kind: 'rule' | 'fault' | 'device' | 'group' | 'client' | 'matrix' | 'profile' | 'network' | 'routing' | 'forward' | 'scenario' | 'probe';
  id: string;
  /** The object's name (or its id when it has none). */
  name: string;
}

/**
 * What the draft had before a device or a probe was removed together with the objects that name it
 * (#99, M26a): the device or the probe, the rules, faults and port forwards deleted along and the groups
 * it was taken out of (as the draft had them), and the order of the rules.
 */
interface Removal {
  device: Device | undefined;
  probe: Probe | undefined;
  rules: Record<string, AccessRule>;
  faults: Record<string, ConfigFault>;
  forwards: Record<string, PortForward>;
  groups: Record<string, Group>;
  order: string[];
}

/**
 * The configuration changes made in the UI and not applied yet (plan §2.17, "Unapplied changes"):
 * access rules, their order, configured faults, custom profiles, adopted devices, groups (#99), networks (P3-M14-01), WireGuard clients, the access
 * matrix, the routing (P3-M14-01), the port forwards (M35a), the scenarios (M19) and the probes (M26a), edited
 * against the revision that was active when the first edit was made. Nothing reaches the gateway until "Preview & apply" turns the draft
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
  /** Custom profiles added, changed or deleted (`null`), by id (P3-M13-03). */
  const profiles = ref<Record<string, Profile | null>>({});
  /** Devices added to the configuration (adopted), by id. */
  const devices = ref<Record<string, Device | null>>({});
  /**
   * Groups changed or deleted (`null`), by id: the UI has no group editor (groups are configured via YAML
   * or the API); a device removed together with the objects that name it takes itself out of their
   * members (#99).
   */
  const groups = ref<Record<string, Group | null>>({});
  /** What the draft had before each removal of a device or a probe together with the objects that name it, by its id: for its Undo (#99, M26a). */
  const removals = ref<Record<string, Removal>>({});
  /** WireGuard clients added, changed or deleted (`client: null`), by client id with their hub. */
  const clients = ref<Record<string, { network: string; client: WireGuardClient | null }>>({});
  /** The entries of the access matrix, when they were edited. */
  const matrix = ref<MatrixEntry[]>();
  /**
   * Networks added, changed or deleted (`null`), by id (P3-M14-01): the network's own settings, without
   * the clients of a hub, which `clients` holds.
   */
  const networks = ref<Record<string, Network | null>>({});
  /** The routing (plan §2.2.2: the BIRD settings and the protocols), when it was edited (P3-M14-01). */
  const routing = ref<Routing>();
  /** Port forwards added, changed or deleted (`null`), by id (M35a). */
  const forwards = ref<Record<string, PortForward | null>>({});
  /** Scenarios added, changed or deleted (`null`), by id (M19). */
  const scenarios = ref<Record<string, Scenario | null>>({});
  /** Probes added, changed or deleted (`null`), by id (M26a). */
  const probes = ref<Record<string, Probe | null>>({});
  /** The ports as typed, by rule (a text that is no list of ports yet keeps its error). */
  const portsText = ref<Record<string, string>>({});

  const dirty = computed(
    () =>
      Object.keys(rules.value).length > 0 ||
      order.value !== undefined ||
      Object.keys(faults.value).length > 0 ||
      Object.keys(profiles.value).length > 0 ||
      Object.keys(devices.value).length > 0 ||
      Object.keys(groups.value).length > 0 ||
      Object.keys(clients.value).length > 0 ||
      Object.keys(networks.value).length > 0 ||
      routing.value !== undefined ||
      Object.keys(forwards.value).length > 0 ||
      Object.keys(scenarios.value).length > 0 ||
      Object.keys(probes.value).length > 0 ||
      matrix.value !== undefined,
  );

  /**
   * The revision this UI applied that waits for confirmation, with the base the draft had then (the
   * revision before it, still the active one while it waits). Edits made meanwhile are based on that
   * one; when the own revision is confirmed and becomes active, the draft moves onto it silently: its
   * edits met nobody else's change, so there is nothing for the concurrent-change dialog (Phase 3 audit).
   */
  let ownPending: { revision: number; base: number } | undefined;
  /** The apply of `revision` waits for confirmation (the drawer, after its discard of the draft). */
  function notePending(revision: number) {
    ownPending = base.value !== undefined ? { revision, base: base.value } : undefined;
  }

  /** Takes the active revision as the base, unless the draft has edits on another one. */
  function track(rev: RevisionWithConfiguration) {
    const own = ownPending;
    // anything but the revision before it as the active one: the waiting is over (confirmed, or another)
    if (own && rev.id !== own.base) ownPending = undefined;
    if (own && rev.id === own.revision && dirty.value && base.value === own.base) {
      rebase(rev);
      return;
    }
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

  /**
   * Where an editor started: the base its form was built on (Phase 3 audit). While the draft is clean
   * its base follows the active revision, so a revision applied while an edit dialog is open would
   * otherwise become the base of a form built on the older one: every field the other revision changed
   * and the user left alone would be sent back as the user's, and If-Match would not see it.
   */
  function editOrigin(): EditOrigin {
    return { base: base.value, config: baseConfig.value };
  }
  /**
   * Saves an editor's result (`save` runs the `set…` of the object) against the revision the editor
   * was opened on: a clean draft whose base moved meanwhile goes back to that revision, so the edit is
   * what the user changed there, and the newer revision meets it in the concurrent-change dialog
   * (reload and reapply keeps their fields) instead of being overwritten unseen. An edit that changes
   * nothing leaves the draft clean, on the newest revision.
   */
  function editFrom(origin: EditOrigin | undefined, save: () => void) {
    if (origin?.base !== undefined && origin.config && !dirty.value && base.value !== origin.base) {
      base.value = origin.base;
      baseConfig.value = origin.config;
    }
    save();
    if (!dirty.value) rebaseOnLatest();
  }

  const baseRules = computed(() => baseConfig.value?.access_rules ?? {});
  const baseOrder = computed(() => baseConfig.value?.access_rule_order ?? []);
  const baseFaults = computed(() => baseConfig.value?.faults ?? {});
  const baseProfiles = computed(() => baseConfig.value?.profiles ?? {});

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

  /** The custom profiles as the draft has them: base with the edits. */
  const effectiveProfiles = computed<Record<string, Profile>>(() => {
    const out: Record<string, Profile> = { ...baseProfiles.value };
    for (const [id, p] of Object.entries(profiles.value)) {
      if (p === null) delete out[id];
      else out[id] = p;
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

  function setProfile(id: string, profile: Profile | null) {
    clearServerErrors(id);
    const before = baseProfiles.value[id];
    if ((profile === null && before === undefined) || (profile !== null && before !== undefined && jsonEqual(before, profile))) {
      const { [id]: _drop, ...rest } = profiles.value;
      profiles.value = rest;
    } else {
      profiles.value = { ...profiles.value, [id]: profile === null ? null : cloneJSON(profile) };
    }
  }

  /** Undoes the deletion of a custom profile of the base. */
  function restoreProfile(id: string) {
    const before = baseProfiles.value[id];
    if (before && profiles.value[id] === null) setProfile(id, before);
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

  const baseGroups = computed<Record<string, Group>>(() => baseConfig.value?.groups ?? {});
  /** The groups as the draft has them: base with the edits. */
  const effectiveGroups = computed<Record<string, Group>>(() => {
    const out: Record<string, Group> = { ...baseGroups.value };
    for (const [id, g] of Object.entries(groups.value)) {
      if (g === null) delete out[id];
      else out[id] = g;
    }
    return out;
  });

  /** A group changed or deleted (`null`); a group back at its base is no change. */
  function setGroup(id: string, group: Group | null) {
    clearServerErrors(id);
    const before = baseGroups.value[id];
    if ((group === null && before === undefined) || (group !== null && before !== undefined && jsonEqual(before, group))) {
      const { [id]: _drop, ...rest } = groups.value;
      groups.value = rest;
    } else {
      groups.value = { ...groups.value, [id]: group === null ? null : cloneJSON(group) };
    }
  }

  /**
   * Removes a configured device together with the objects of the draft that name it (#99): access
   * rules, configured faults and port forwards are deleted, a group loses the device as a member and
   * keeps the others. A scenario is not changed (the removal dialog waits while one names the device).
   * What the draft had before is kept for the Undo (`restoreDevice`).
   */
  function removeDevice(device: { id: string; name: string }) {
    const record = removeReferences(device, { device: devices.value[device.id] ?? baseConfig.value?.devices?.[device.id], probe: undefined });
    setDevice(device.id, null);
    removals.value = { ...removals.value, [device.id]: record };
  }

  /**
   * Removes a probe together with the objects of the draft that name it, as `removeDevice` does a device
   * (a probe is a device of the same namespace: `Scope.device` names it), M26a. Undo: `restoreProbe`.
   */
  function removeProbe(probe: { id: string; name: string }) {
    const record = removeReferences(probe, { device: undefined, probe: probes.value[probe.id] ?? baseConfig.value?.probes?.[probe.id] });
    setProbe(probe.id, null);
    removals.value = { ...removals.value, [probe.id]: record };
  }

  /** The objects of the draft that name the device or probe, deleted or changed; `start` is the device or probe as the draft had it, and the record returned is for the Undo. */
  function removeReferences(device: { id: string; name: string }, start: Pick<Removal, 'device' | 'probe'>): Removal {
    const record: Removal = { ...start, rules: {}, faults: {}, forwards: {}, groups: {}, order: [...effectiveOrder.value] };
    const refs = deviceReferences(device, { rules: effectiveRules.value, faults: effectiveFaults.value, groups: effectiveGroups.value, forwards: effectiveForwards.value });
    for (const r of refs) {
      if (r.kind === 'rule') {
        record.rules[r.id] = cloneJSON(effectiveRules.value[r.id]!);
        setRule(r.id, null);
      } else if (r.kind === 'fault') {
        record.faults[r.id] = cloneJSON(effectiveFaults.value[r.id]!);
        setFault(r.id, null);
      } else if (r.kind === 'forward') {
        record.forwards[r.id] = cloneJSON(effectiveForwards.value[r.id]!);
        setForward(r.id, null);
      } else if (r.kind === 'group') {
        const g = effectiveGroups.value[r.id]!;
        record.groups[r.id] = cloneJSON(g);
        setGroup(r.id, groupWithout(g, device));
      }
    }
    return record;
  }

  /**
   * Undoes the removal of a configured device (the device list's and the detail's Undo): the device
   * comes back as the draft had it, and so do the objects removed along with it (`removeDevice`):
   * rules, faults and port forwards as the draft had them (a rule at its place in the order), and the
   * device is put back into its groups at its old place, while the groups keep what changed since.
   * An object brought back meanwhile stays as it is. Without that record (the draft was reapplied on
   * another revision since), the device and the objects of the base that named it come back as the
   * base has them.
   */
  function restoreDevice(id: string) {
    if (devices.value[id] !== null) return;
    const record = removals.value[id];
    const { [id]: _drop, ...rest } = removals.value;
    removals.value = rest;
    if (!record) {
      restoreDeviceFromBase(id);
      return;
    }
    setDevice(id, record.device ?? null);
    restoreReferences(record, { id, name: record.device?.name ?? id });
  }

  /** The Undo of the removal of a probe (`removeProbe`), like `restoreDevice`: the probe and the objects removed along come back. */
  function restoreProbe(id: string) {
    if (probes.value[id] !== null) return;
    const record = removals.value[id];
    const { [id]: _drop, ...rest } = removals.value;
    removals.value = rest;
    if (!record) {
      const before = baseConfig.value?.probes?.[id];
      if (before) restoreReferencesFromBase(id, before, () => setProbe(id, before));
      return;
    }
    setProbe(id, record.probe ?? null);
    restoreReferences(record, { id, name: record.probe?.name ?? id });
  }

  /** What a removal took along comes back: the objects as the draft had them, the device in its groups (the Undo of `removeReferences`). */
  function restoreReferences(record: Removal, device: { id: string; name: string }) {
    const back = Object.keys(record.rules).filter((r) => !(r in effectiveRules.value));
    for (const r of back) setRule(r, record.rules[r]!);
    if (back.length) {
      // each rule after the nearest one that stood before it and is still there
      const ids = effectiveOrder.value.filter((x) => !back.includes(x));
      for (const r of record.order.filter((x) => back.includes(x))) {
        const prev = record.order.slice(0, record.order.indexOf(r)).reverse().find((x) => ids.includes(x));
        ids.splice(prev === undefined ? 0 : ids.indexOf(prev) + 1, 0, r);
      }
      setOrder(ids);
    }
    for (const [f, fault] of Object.entries(record.faults)) if (!(f in effectiveFaults.value)) setFault(f, fault);
    for (const [f, forward] of Object.entries(record.forwards)) if (!(f in effectiveForwards.value)) setForward(f, forward);
    for (const [g, group] of Object.entries(record.groups)) {
      const now = effectiveGroups.value[g];
      if (now) setGroup(g, groupWithMember(group, now, device));
    }
  }

  /** The Undo of a removal without its record: the device and the objects of the base that named it come back as the base has them. */
  function restoreDeviceFromBase(id: string) {
    const before = baseConfig.value?.devices?.[id];
    if (before) restoreReferencesFromBase(id, before, () => setDevice(id, before));
  }

  function restoreReferencesFromBase(id: string, before: { name: string }, restore: () => void) {
    restore();
    const device = { id, name: before.name };
    const refs = deviceReferences(device, { rules: baseRules.value, faults: baseFaults.value, groups: baseGroups.value, forwards: baseForwards.value });
    for (const r of refs) {
      if (r.kind === 'rule') restoreRule(r.id);
      else if (r.kind === 'fault') restoreFault(r.id);
      else if (r.kind === 'forward' && forwards.value[r.id] === null) restoreForward(r.id);
      else if (r.kind === 'group') {
        const now = effectiveGroups.value[r.id];
        if (now) setGroup(r.id, groupWithMember(baseGroups.value[r.id]!, now, device));
      }
    }
  }

  /** The networks of a configuration without the clients of the hubs (what network edits are compared with). */
  function networkSettings(c: Configuration | undefined): Record<string, Network> {
    return Object.fromEntries(Object.entries(c?.networks ?? {}).map(([id, n]) => [id, withoutClients(n)]));
  }

  /** The networks of the base as the matrix functions take them (they read the type and the name). */
  const baseNetworks = computed<NetworkView[]>(() =>
    Object.entries(baseConfig.value?.networks ?? {}).map(([id, config]) => ({ id, config, status: { state: 'ok' as const } })),
  );
  const baseMatrix = computed<MatrixEntry[]>(() => baseConfig.value?.access_matrix?.entries ?? []);
  /** The access matrix as the draft has it. */
  const effectiveMatrix = computed<MatrixEntry[]>(() => matrix.value ?? baseMatrix.value);

  function clientsIn(cfg: Configuration | undefined, network: string): Record<string, WireGuardClient> {
    const n = cfg?.networks?.[network];
    return n?.type === 'wireguard' ? ((n as WireGuardNetwork).clients ?? {}) : {};
  }

  /** The clients of a hub as the draft has them: the base's with the edits. */
  function effectiveClients(network: string): Record<string, WireGuardClient> {
    const out: Record<string, WireGuardClient> = { ...clientsIn(baseConfig.value, network) };
    for (const [id, e] of Object.entries(clients.value)) {
      if (e.network !== network) continue;
      if (e.client === null) delete out[id];
      else out[id] = e.client;
    }
    return out;
  }

  function setClient(network: string, id: string, client: WireGuardClient | null) {
    clearServerErrors(id);
    const before = clientsIn(baseConfig.value, network)[id];
    if ((client === null && before === undefined) || (client !== null && before !== undefined && jsonEqual(before, client))) {
      const { [id]: _drop, ...rest } = clients.value;
      clients.value = rest;
    } else {
      clients.value = { ...clients.value, [id]: { network, client: client === null ? null : cloneJSON(client) } };
    }
  }

  /** A network of the base revision without the clients of a hub (what a network edit is compared with). */
  function baseNetwork(id: string): Network | undefined {
    const n = baseConfig.value?.networks?.[id];
    return n ? withoutClients(n) : undefined;
  }

  /** The networks as the draft has them: the base's with the edits (a hub with the clients of the base). */
  const effectiveNetworks = computed<Record<string, Network>>(() => {
    const out: Record<string, Network> = { ...(baseConfig.value?.networks ?? {}) };
    for (const [id, n] of Object.entries(networks.value)) {
      if (n === null) {
        delete out[id];
        continue;
      }
      const clientsOf = (out[id] as WireGuardNetwork | undefined)?.clients;
      out[id] = clientsOf && n.type === 'wireguard' ? ({ ...n, clients: clientsOf } as WireGuardNetwork) : n;
    }
    return out;
  });

  /**
   * Adds, changes or deletes (`null`) a network (P3-M14-01). The clients of a hub are not part of it: they
   * stay as they are, and a network deleted takes the edits of its clients with it.
   */
  function setNetwork(id: string, network: Network | null) {
    clearServerErrors(id);
    const before = baseNetwork(id);
    const n = network === null ? null : withoutClients(network);
    if ((n === null && before === undefined) || (n !== null && before !== undefined && jsonEqual(before, n))) {
      const { [id]: _drop, ...rest } = networks.value;
      networks.value = rest;
    } else {
      networks.value = { ...networks.value, [id]: n === null ? null : cloneJSON(n) };
    }
    if (n === null) {
      clients.value = Object.fromEntries(Object.entries(clients.value).filter(([, c]) => c.network !== id));
    }
  }

  /** Undoes the edit or the deletion of a network of the base. */
  function restoreNetwork(id: string) {
    const before = baseNetwork(id);
    if (before) setNetwork(id, before);
  }

  const baseRouting = computed<Routing>(() => baseConfig.value?.routing ?? {});
  /** The routing as the draft has it. */
  const effectiveRouting = computed<Routing>(() => routing.value ?? baseRouting.value);

  /** The routing edited (P3-M14-01); a routing back at its base is no change. */
  function setRouting(r: Routing) {
    clearServerErrors('routing');
    for (const id of Object.keys(r.protocols ?? {})) clearServerErrors(id);
    for (const id of Object.keys(effectiveRouting.value.protocols ?? {})) clearServerErrors(id);
    const next = cloneJSON(r);
    // the last protocol deleted from a routing that had none in the base leaves no empty map behind
    if (next.protocols && Object.keys(next.protocols).length === 0 && !baseRouting.value.protocols) delete next.protocols;
    routing.value = jsonEqual(next, baseRouting.value) ? undefined : next;
  }

  /** A routing protocol added, changed or deleted (`null`). */
  function setProtocol(id: string, p: RoutingProtocol | null) {
    const protocols = { ...(effectiveRouting.value.protocols ?? {}) };
    if (p === null) delete protocols[id];
    else protocols[id] = p;
    setRouting({ ...effectiveRouting.value, protocols });
  }

  /** Undoes the edit or the deletion of a protocol of the base. */
  function restoreProtocol(id: string) {
    const before = baseRouting.value.protocols?.[id];
    if (before) setProtocol(id, before);
  }

  const baseForwards = computed<Record<string, PortForward>>(() => baseConfig.value?.port_forwards ?? {});
  /** The port forwards as the draft has them: base with the edits. */
  const effectiveForwards = computed<Record<string, PortForward>>(() => {
    const out: Record<string, PortForward> = { ...baseForwards.value };
    for (const [id, f] of Object.entries(forwards.value)) {
      if (f === null) delete out[id];
      else out[id] = f;
    }
    return out;
  });

  /** A port forward added, changed or deleted (`null`); a forward back at its base is no change. */
  function setForward(id: string, forward: PortForward | null) {
    clearServerErrors(id);
    const before = baseForwards.value[id];
    if ((forward === null && before === undefined) || (forward !== null && before !== undefined && jsonEqual(before, forward))) {
      const { [id]: _drop, ...rest } = forwards.value;
      forwards.value = rest;
    } else {
      forwards.value = { ...forwards.value, [id]: forward === null ? null : cloneJSON(forward) };
    }
  }

  /** Undoes the edit or the deletion of a forward of the base. */
  function restoreForward(id: string) {
    const before = baseForwards.value[id];
    if (before) setForward(id, before);
  }

  const baseProbes = computed<Record<string, Probe>>(() => baseConfig.value?.probes ?? {});
  /** The probes as the draft has them: base with the edits. */
  const effectiveProbes = computed<Record<string, Probe>>(() => {
    const out: Record<string, Probe> = { ...baseProbes.value };
    for (const [id, p] of Object.entries(probes.value)) {
      if (p === null) delete out[id];
      else out[id] = p;
    }
    return out;
  });

  /** A probe added, changed or deleted (`null`); a probe back at its base is no change (M26a). */
  function setProbe(id: string, probe: Probe | null) {
    clearServerErrors(id);
    const before = baseProbes.value[id];
    if ((probe === null && before === undefined) || (probe !== null && before !== undefined && jsonEqual(before, probe))) {
      const { [id]: _drop, ...rest } = probes.value;
      probes.value = rest;
    } else {
      probes.value = { ...probes.value, [id]: probe === null ? null : cloneJSON(probe) };
    }
  }

  /** Undoes the edit of a probe of the base, or its deletion (without what a removal took along: `restoreProbe` does that). */
  function revertProbe(id: string) {
    const before = baseProbes.value[id];
    if (before) setProbe(id, before);
  }

  const baseScenarios = computed<Record<string, Scenario>>(() => baseConfig.value?.scenarios ?? {});
  /** The scenarios as the draft has them: base with the edits. */
  const effectiveScenarios = computed<Record<string, Scenario>>(() => {
    const out: Record<string, Scenario> = { ...baseScenarios.value };
    for (const [id, sc] of Object.entries(scenarios.value)) {
      if (sc === null) delete out[id];
      else out[id] = sc;
    }
    return out;
  });

  /** A scenario added, changed or deleted (`null`); a scenario back at its base is no change. */
  function setScenario(id: string, scenario: Scenario | null) {
    clearServerErrors(id);
    const before = baseScenarios.value[id];
    if ((scenario === null && before === undefined) || (scenario !== null && before !== undefined && jsonEqual(before, scenario))) {
      const { [id]: _drop, ...rest } = scenarios.value;
      scenarios.value = rest;
    } else {
      scenarios.value = { ...scenarios.value, [id]: scenario === null ? null : cloneJSON(scenario) };
    }
  }

  /** Undoes the edit or the deletion of a scenario of the base. */
  function restoreScenario(id: string) {
    const before = baseScenarios.value[id];
    if (before) setScenario(id, before);
  }

  /** Flips the policy of one pair of the matrix (`toggleCell`); a matrix back at its base is no change. */
  function toggleMatrix(from: string, to: string) {
    const next = toggleCell(effectiveMatrix.value, from, to, baseNetworks.value);
    matrix.value = matrixChanges(baseMatrix.value, next, baseNetworks.value).length === 0 ? undefined : next;
  }

  /** A client of a hub as the base revision has it (what the client actions and Undo start from). */
  function baseClient(network: string, id: string): WireGuardClient | undefined {
    return clientsIn(baseConfig.value, network)[id];
  }

  /** The name of a client of the base, wherever its hub is. */
  function clientName(id: string): string | undefined {
    for (const net of Object.keys(baseConfig.value?.networks ?? {})) {
      const c = clientsIn(baseConfig.value, net)[id];
      if (c) return c.name;
    }
    return undefined;
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
        const count = out.length;
        const add = (field: string, a: string, b: string) => a !== b && out.push({ kind: 'fault', id, name, field, from: a || '—', to: b || '—' });
        const traffic = (x: ConfigFault) => `${x.protocol ?? 'any'} ${formatPorts(x)} ${x.destination ? describeDestination(x.destination, names) : ''}`.replace(/\s+/g, ' ').trim();
        add('name', before.name ?? '', f.name ?? '');
        add('description', before.description ?? '', f.description ?? '');
        add('target', describeScope(before.source, names), describeScope(f.source, names));
        if (before.tunnel || f.tunnel) add('tunnel', before.tunnel ? describeTunnel(before.tunnel, names) : '', f.tunnel ? describeTunnel(f.tunnel, names) : '');
        add('traffic', traffic(before), traffic(f));
        add('parameters', faultChips(before).join(', '), faultChips(f).join(', '));
        add('enabled', before.enabled === false ? 'no' : 'yes', f.enabled === false ? 'no' : 'yes');
        // a change the words above do not show (setFault keeps no edit that equals the base)
        if (out.length === count) out.push({ kind: 'fault', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
    }
    for (const [id, p] of Object.entries(profiles.value)) {
      const before = baseProfiles.value[id];
      const name = p?.name ?? before?.name ?? id;
      if (p === null) out.push({ kind: 'profile', id, name, field: 'profile', from: 'present', to: 'deleted' });
      else if (!before) out.push({ kind: 'profile', id, name, field: 'profile', from: '—', to: `added (${profileChips(p.parts).join(', ')})` });
      else {
        const count = out.length;
        const add = (field: string, a: string, b: string) => a !== b && out.push({ kind: 'profile', id, name, field, from: a || '—', to: b || '—' });
        add('name', before.name, p.name);
        add('description', before.description ?? '', p.description ?? '');
        add('parameters', profileChips(before.parts).join(', '), profileChips(p.parts).join(', '));
        // a change the words above do not show (setProfile keeps no edit that equals the base)
        if (out.length === count) out.push({ kind: 'profile', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
    }
    const netName = (id: string) => names.network?.(id) ?? (baseConfig.value?.networks?.[id] as { name?: string } | undefined)?.name;
    for (const [id, dv] of Object.entries(devices.value)) {
      const before = baseConfig.value?.devices?.[id];
      const name = dv?.name ?? before?.name ?? id;
      if (dv === null) out.push({ kind: 'device', id, name, field: 'device', from: 'configured', to: 'deleted' });
      else if (!before) {
        const ids = [...(dv.identifiers?.macs ?? []), ...(dv.identifiers?.ipv4 ?? [])].join(', ');
        out.push({ kind: 'device', id, name, field: 'device', from: 'discovered', to: `configured${ids ? ` (${ids})` : ''}` });
      } else {
        const count = out.length;
        const add = (field: string, a: string, b: string) => a !== b && out.push({ kind: 'device', id, name, field, from: a || '—', to: b || '—' });
        const net = (n: string | undefined) => (n ? (netName(n) ?? n) : '');
        add('name', before.name, dv.name);
        add('network', net(before.network), net(dv.network));
        add('fixed IP', before.fixed_ip ?? '', dv.fixed_ip ?? '');
        add('MACs', (before.identifiers?.macs ?? []).join(', '), (dv.identifiers?.macs ?? []).join(', '));
        // a change the words above do not show (setDevice keeps no edit that equals the base)
        if (out.length === count) out.push({ kind: 'device', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
    }
    const deviceName = (ref: string) => names.device?.(ref) ?? baseConfig.value?.devices?.[ref]?.name ?? ref;
    for (const [id, g] of Object.entries(groups.value)) {
      const before = baseGroups.value[id];
      const name = g?.name ?? before?.name ?? id;
      if (g === null) out.push({ kind: 'group', id, name, field: 'group', from: 'present', to: 'deleted' });
      else if (!before) out.push({ kind: 'group', id, name, field: 'group', from: '—', to: 'added' });
      else {
        const count = out.length;
        const members = (x: Group) => (x.members ?? []).map(deviceName).join(', ');
        if (members(before) !== members(g)) out.push({ kind: 'group', id, name, field: 'members', from: members(before) || '—', to: members(g) || '—' });
        if (out.length === count) out.push({ kind: 'group', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
    }
    for (const [id, { network, client }] of Object.entries(clients.value)) {
      const before = clientsIn(baseConfig.value, network)[id];
      const name = client?.name ?? before?.name ?? id;
      if (client === null) out.push({ kind: 'client', id, name, field: 'client', from: 'present', to: 'deleted' });
      else if (!before) out.push({ kind: 'client', id, name, field: 'client', from: '—', to: `added (${client.address} in ${netName(network) ?? network})` });
      else {
        const add = (field: string, a: string, b: string) => a !== b && out.push({ kind: 'client', id, name, field, from: a || '—', to: b || '—' });
        add('name', before.name, client.name);
        add('address', before.address, client.address);
        add('client networks', (before.client_networks ?? []).join(', '), (client.client_networks ?? []).join(', '));
        add('enabled', before.enabled === false ? 'no' : 'yes', client.enabled === false ? 'no' : 'yes');
        // a key rotation: the next generation of generated keys, or the client's new public key
        add('keys', keysText(before), keysText(client));
        const rest = (c: WireGuardClient) => ({
          ...c,
          name: '',
          address: '',
          client_networks: [],
          enabled: true,
          key: { ...c.key, generation: undefined, public_key: undefined },
        });
        if (!jsonEqual(rest(before), rest(client))) out.push({ kind: 'client', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
    }
    for (const [id, n] of Object.entries(networks.value)) {
      const before = baseNetwork(id);
      const name = n?.name ?? before?.name ?? id;
      for (const c of networkChanges(before, n)) out.push({ kind: 'network', id, name, ...c });
    }
    if (routing.value) {
      const was = baseRouting.value;
      const now = routing.value;
      const label = { network: (id: string) => effectiveNetworks.value[id]?.name ?? netName(id), client: (id: string) => clientName(id) };
      for (const c of routingSettingsChanges(was, now)) out.push({ kind: 'routing', id: 'routing', name: 'settings', ...c });
      for (const pid of new Set([...Object.keys(was.protocols ?? {}), ...Object.keys(now.protocols ?? {})])) {
        const a = was.protocols?.[pid];
        const b = now.protocols?.[pid];
        for (const c of protocolChanges(a, b, label)) out.push({ kind: 'routing', id: pid, name: `protocol ${b?.name ?? a?.name ?? pid}`, ...c });
      }
    }
    for (const [id, f] of Object.entries(forwards.value)) {
      const before = baseForwards.value[id];
      const name = forwardTitle(f ?? before!);
      for (const c of forwardChanges(before, f, names.device)) out.push({ kind: 'forward', id, name, ...c });
    }
    for (const [id, sc] of Object.entries(scenarios.value)) {
      const before = baseScenarios.value[id];
      const name = scenarioTitle(sc ?? before!);
      for (const c of scenarioChanges(before, sc, names)) out.push({ kind: 'scenario', id, name, ...c });
    }
    for (const [id, p] of Object.entries(probes.value)) {
      const before = baseProbes.value[id];
      const name = probeTitle(p ?? before!);
      for (const c of probeChanges(before, p, (n) => netName(n) ?? n)) out.push({ kind: 'probe', id, name, ...c });
    }
    if (matrix.value) {
      for (const c of matrixChanges(baseMatrix.value, matrix.value, baseNetworks.value, { network: netName, client: clientName })) {
        out.push({ kind: 'matrix', id: c.field, name: 'Access between networks', ...c });
      }
    }
    return out;
  }

  /** "3 changes in 2 rules", "1 change in 1 fault", "4 changes in 2 rules and 1 fault". */
  function summary(names: Names = {}): string {
    const cs = changes(names);
    const ruleIds = new Set(cs.filter((c) => c.kind === 'rule').map((c) => c.id));
    const faultIds = new Set(cs.filter((c) => c.kind === 'fault').map((c) => c.id));
    const profileIds = new Set(cs.filter((c) => c.kind === 'profile').map((c) => c.id));
    const deviceIds = new Set(cs.filter((c) => c.kind === 'device').map((c) => c.id));
    const groupIds = new Set(cs.filter((c) => c.kind === 'group').map((c) => c.id));
    const clientIds = new Set(cs.filter((c) => c.kind === 'client').map((c) => c.id));
    const networkIds = new Set(cs.filter((c) => c.kind === 'network').map((c) => c.id));
    const forwardIds = new Set(cs.filter((c) => c.kind === 'forward').map((c) => c.id));
    const scenarioIds = new Set(cs.filter((c) => c.kind === 'scenario').map((c) => c.id));
    const probeIds = new Set(cs.filter((c) => c.kind === 'probe').map((c) => c.id));
    const objects = [
      ruleIds.size ? `${ruleIds.size} ${ruleIds.size === 1 ? 'rule' : 'rules'}` : '',
      faultIds.size ? `${faultIds.size} ${faultIds.size === 1 ? 'fault' : 'faults'}` : '',
      profileIds.size ? `${profileIds.size} ${profileIds.size === 1 ? 'profile' : 'profiles'}` : '',
      deviceIds.size ? `${deviceIds.size} ${deviceIds.size === 1 ? 'device' : 'devices'}` : '',
      groupIds.size ? `${groupIds.size} ${groupIds.size === 1 ? 'group' : 'groups'}` : '',
      networkIds.size ? `${networkIds.size} ${networkIds.size === 1 ? 'network' : 'networks'}` : '',
      clientIds.size ? `${clientIds.size} WireGuard ${clientIds.size === 1 ? 'client' : 'clients'}` : '',
      forwardIds.size ? `${forwardIds.size} port ${forwardIds.size === 1 ? 'forward' : 'forwards'}` : '',
      scenarioIds.size ? `${scenarioIds.size} ${scenarioIds.size === 1 ? 'scenario' : 'scenarios'}` : '',
      probeIds.size ? `${probeIds.size} ${probeIds.size === 1 ? 'probe' : 'probes'}` : '',
      cs.some((c) => c.kind === 'matrix') ? 'the access matrix' : '',
      cs.some((c) => c.kind === 'routing') ? 'the routing' : '',
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

  /**
   * The scenarios of the draft that the gateway would refuse as they are, with their problems (a scenario
   * without steps, a check whose window names a step that is gone): Preview & apply waits until they are fixed.
   */
  const invalidScenarios = computed(() => {
    const out: Record<string, ScenarioProblem[]> = {};
    const all = effectiveScenarios.value;
    for (const [id, sc] of Object.entries(scenarios.value)) {
      if (!sc) continue;
      const others = Object.entries(all)
        .filter(([k]) => k !== id)
        .map(([, o]) => o.name);
      const p = scenarioProblems(sc, others);
      if (p.length) out[id] = p;
    }
    return out;
  });

  /** The JSON Merge Patch against the base: whole objects for what changed, null for what went. */
  /**
   * The merge patch the preview sends for one configured fault or custom profile edited into `value`
   * (the whole object when it is new; `{}` when nothing changed), for the editors' `</> API` panel.
   */
  function objectPatch(kind: 'fault' | 'profile' | 'scenario', id: string, value: ConfigFault | Profile | Scenario): unknown {
    const before = kind === 'fault' ? baseFaults.value[id] : kind === 'scenario' ? baseScenarios.value[id] : baseProfiles.value[id];
    return createMergePatch(before, value) ?? {};
  }

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
    const pp: Record<string, unknown> = {};
    for (const [id, p] of Object.entries(profiles.value)) {
      const mp = createMergePatch(baseProfiles.value[id], p);
      if (mp !== undefined) pp[id] = mp;
    }
    if (Object.keys(pp).length) out.profiles = pp;
    const dp: Record<string, unknown> = {};
    for (const [id, dv] of Object.entries(devices.value)) {
      const p = createMergePatch(baseConfig.value?.devices?.[id], dv);
      if (p !== undefined) dp[id] = p;
    }
    if (Object.keys(dp).length) out.devices = dp;
    // a group's members are an array: the merge patch replaces them whole
    const gp: Record<string, unknown> = {};
    for (const [id, g] of Object.entries(groups.value)) {
      const p = createMergePatch(baseGroups.value[id], g);
      if (p !== undefined) gp[id] = p;
    }
    if (Object.keys(gp).length) out.groups = gp as ConfigurationPatch['groups'];
    // a network's own settings, and the clients of a hub inside it (a network deleted takes its clients along)
    const np: Record<string, Record<string, unknown> | null> = {};
    for (const [id, n] of Object.entries(networks.value)) {
      const p = createMergePatch(baseNetwork(id), n);
      if (p !== undefined) np[id] = p as Record<string, unknown> | null;
    }
    for (const [id, { network, client }] of Object.entries(clients.value)) {
      const p = createMergePatch(clientsIn(baseConfig.value, network)[id], client);
      if (p === undefined || np[network] === null) continue;
      const n = (np[network] ??= {});
      ((n.clients ??= {}) as Record<string, unknown>)[id] = p;
    }
    if (Object.keys(np).length) out.networks = np;
    const wp: Record<string, unknown> = {};
    for (const [id, f] of Object.entries(forwards.value)) {
      const p = createMergePatch(baseForwards.value[id], f);
      if (p !== undefined) wp[id] = p;
    }
    if (Object.keys(wp).length) out.port_forwards = wp;
    const sp: Record<string, unknown> = {};
    for (const [id, sc] of Object.entries(scenarios.value)) {
      const p = createMergePatch(baseScenarios.value[id], sc);
      if (p !== undefined) sp[id] = p;
    }
    if (Object.keys(sp).length) out.scenarios = sp;
    const prp: Record<string, unknown> = {};
    for (const [id, pr] of Object.entries(probes.value)) {
      const p = createMergePatch(baseProbes.value[id], pr);
      if (p !== undefined) prp[id] = p;
    }
    if (Object.keys(prp).length) out.probes = prp;
    if (routing.value) {
      const p = createMergePatch(baseConfig.value?.routing, routing.value);
      if (p !== undefined) out.routing = p as ConfigurationPatch;
    }
    // the entries are an array: the merge patch replaces it whole
    if (matrix.value && !jsonEqual(matrix.value, baseMatrix.value)) out.access_matrix = { entries: cloneJSON(matrix.value) };
    return out;
  }

  /**
   * What both the draft and another revision changed, as the concurrent-change dialog names it before
   * reapplying (P3-M13-07): per object the fields both changed to different values, `from` their
   * value and `to` mine (which `rebase` takes); an object they changed that I delete, and one I
   * changed that they removed (it comes back as mine); the rule order when both moved rules to
   * different orders (mine wins, the rules they added stay where they put them). A pair of the access matrix has two
   * policies, so where both changed it they agree.
   */
  function overlaps(theirs: Configuration, names: Names = {}): DraftChange[] {
    const out: DraftChange[] = [];
    const old = baseConfig.value;
    function compare<T extends object>(kind: DraftChange['kind'], edits: Record<string, T | null>, before: Record<string, T>, after: Record<string, T>, nameOf: (o: T) => string | undefined) {
      for (const [id, m] of Object.entries(edits)) {
        const was = before[id];
        const now = after[id];
        if (was === undefined) continue; // new on my side: nothing of theirs to meet
        const name = (m && nameOf(m)) ?? nameOf(was) ?? id;
        if (m === null) {
          if (now !== undefined && !jsonEqual(was, now)) out.push({ kind, id, name, field: kind === 'client' ? 'client' : kind, from: 'changed', to: 'deleted' });
          continue;
        }
        if (now === undefined) {
          if (!jsonEqual(was, m)) out.push({ kind, id, name, field: kind === 'client' ? 'client' : kind, from: 'deleted', to: 'kept with my change' });
          continue;
        }
        const ps = parameterSets[kind];
        const whole = ps !== undefined && reapplyObject(kind, was, now, m).whole;
        for (const path of bothChanged(was, now, m, atomicMembers)) {
          // a parameter set taken whole is named once, as a whole
          if (whole && ps.inside(path)) continue;
          const get = (o: unknown) => path.reduce<unknown>((v, k) => (typeof v === 'object' && v !== null ? (v as Record<string, unknown>)[k] : undefined), o);
          const key = path.at(-1) ?? '';
          const t = get(now);
          const mv = get(m);
          out.push({ kind, id, name, field: fieldLabel(kind, path), from: valueText(key, t, names, mv), to: valueText(key, mv, names, t) });
        }
        if (whole) out.push({ kind, id, name, field: ps.field, from: parameterText(ps.get(now)), to: parameterText(ps.get(m)) });
      }
    }
    const named = (o: { name?: string }) => o.name;
    compare('rule', rules.value, old?.access_rules ?? {}, theirs.access_rules ?? {}, named);
    if (order.value) {
      // the rules all three have, in each one's order: a move of theirs and one of mine meet here
      const kept = (ids: string[]) => ids.filter((id) => id in (old?.access_rules ?? {}) && id in (theirs.access_rules ?? {}) && id in effectiveRules.value);
      const was = kept(baseOrder.value);
      const their = kept(theirs.access_rule_order ?? []);
      const my = kept(effectiveOrder.value);
      if (!jsonEqual(their, was) && !jsonEqual(my, was) && !jsonEqual(their, my)) {
        const nameOf = (id: string) => effectiveRules.value[id]?.name ?? id;
        out.push({ kind: 'rule', id: 'access_rule_order', name: 'order', field: '', from: their.map(nameOf).join(', '), to: my.map(nameOf).join(', ') });
      }
    }
    compare('fault', faults.value, old?.faults ?? {}, theirs.faults ?? {}, named);
    compare('profile', profiles.value, old?.profiles ?? {}, theirs.profiles ?? {}, named);
    compare('device', devices.value, old?.devices ?? {}, theirs.devices ?? {}, named);
    compare('group', groups.value, old?.groups ?? {}, theirs.groups ?? {}, named);
    compare('network', networks.value, networkSettings(old), networkSettings(theirs), named);
    compare('forward', forwards.value, old?.port_forwards ?? {}, theirs.port_forwards ?? {}, forwardTitle);
    compare('scenario', scenarios.value, old?.scenarios ?? {}, theirs.scenarios ?? {}, named);
    compare('probe', probes.value, old?.probes ?? {}, theirs.probes ?? {}, named);
    if (routing.value) {
      const count = out.length;
      compare('routing', { routing: routing.value }, { routing: old?.routing ?? {} }, { routing: theirs.routing ?? {} }, () => 'routing');
      // a protocol by its name, not its id
      const protocols = { ...(theirs.routing?.protocols ?? {}), ...(routing.value.protocols ?? {}) };
      for (const c of out.slice(count)) c.field = c.field.replace(/^protocols ([0-9a-f-]{36})/, (_m, id: string) => `protocol ${protocols[id]?.name ?? id}`);
    }
    for (const net of new Set(Object.values(clients.value).map((c) => c.network))) {
      const edits = Object.fromEntries(
        Object.entries(clients.value)
          .filter(([, c]) => c.network === net)
          .map(([id, c]) => [id, c.client]),
      );
      if (hubGone(net, theirs)) {
        // a client cannot be kept without its hub: the change of it is dropped (rebase)
        const hubName = old?.networks?.[net]?.name ?? names.network?.(net) ?? net;
        for (const [id, m] of Object.entries(edits)) {
          const name = m?.name ?? clientsIn(old, net)[id]?.name ?? id;
          out.push({ kind: 'client', id, name, field: 'client', from: `hub ${hubName} deleted`, to: 'my change dropped' });
        }
        continue;
      }
      compare('client', edits, clientsIn(old, net), clientsIn(theirs, net), named);
    }
    return out;
  }

  /**
   * The hub of client edits is gone in their revision and the draft does not bring it back (as a
   * network of its own edit): a client has no place without its hub, so the client edits drop out
   * instead of a merge patch that would make a network of clients only.
   */
  function hubGone(net: string, theirs: Configuration): boolean {
    const mine = networks.value[net];
    return theirs.networks?.[net] === undefined && (mine === undefined || mine === null);
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
    const oldNetworks = baseNetworks.value;
    const oldMatrix = baseMatrix.value;
    const myMatrix = matrix.value;
    const myClients = clients.value;
    // the revision just fetched is the newest seen: a draft this leaves clean stays on it
    if (!latest || latest.id < rev.id) latest = rev;
    function reapply<T>(kind: DraftChange['kind'], edits: Record<string, T | null>, before: Record<string, T>, theirs: Record<string, T>): Record<string, T | null> {
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
        // a scope or a destination both changed is mine as a whole, as the dialog says, and so is a
        // parameter set the member-by-member merge would make one the gateway refuses
        const { merged } = reapplyObject(kind, was, now, m);
        if (!jsonEqual(merged, now)) out[id] = merged;
      }
      return out;
    }
    base.value = rev.id;
    baseConfig.value = rev.configuration;
    rules.value = reapply('rule', rules.value, old?.access_rules ?? {}, theirRules);
    faults.value = reapply('fault', faults.value, old?.faults ?? {}, theirFaults);
    profiles.value = reapply('profile', profiles.value, old?.profiles ?? {}, rev.configuration.profiles ?? {});
    devices.value = reapply('device', devices.value, old?.devices ?? {}, theirDevices);
    groups.value = reapply('group', groups.value, old?.groups ?? {}, rev.configuration.groups ?? {});
    // what the draft had before a removal was based on the old revision: its Undo goes back to the base
    removals.value = {};
    networks.value = reapply('network', networks.value, networkSettings(old), networkSettings(rev.configuration));
    forwards.value = reapply('forward', forwards.value, old?.port_forwards ?? {}, rev.configuration.port_forwards ?? {});
    scenarios.value = reapply('scenario', scenarios.value, old?.scenarios ?? {}, rev.configuration.scenarios ?? {});
    probes.value = reapply('probe', probes.value, old?.probes ?? {}, rev.configuration.probes ?? {});
    if (routing.value) {
      const r = reapply('routing', { routing: routing.value }, { routing: old?.routing ?? {} }, { routing: rev.configuration.routing ?? {} }).routing;
      routing.value = r && !jsonEqual(r, rev.configuration.routing ?? {}) ? r : undefined;
    }
    // clients hub by hub, like the other objects
    const nextClients: typeof clients.value = {};
    for (const net of new Set(Object.values(myClients).map((c) => c.network))) {
      // a hub they deleted takes my client edits along (the network edits are reapplied above)
      if (hubGone(net, rev.configuration)) continue;
      const edits = Object.fromEntries(
        Object.entries(myClients)
          .filter(([, c]) => c.network === net)
          .map(([id, c]) => [id, c.client]),
      );
      for (const [id, c] of Object.entries(reapply('client', edits, clientsIn(old, net), clientsIn(rev.configuration, net)))) nextClients[id] = { network: net, client: c };
    }
    clients.value = nextClients;
    // the matrix pair by pair: each pair I changed gets my policy on their entries, the others stay theirs
    if (myMatrix) {
      let next = rev.configuration.access_matrix?.entries ?? [];
      const pairs = new Map<string, [string, string]>();
      for (const e of [...oldMatrix, ...myMatrix]) {
        const f = entryKey(e.from, oldNetworks);
        const t = entryKey(e.to, oldNetworks);
        pairs.set(`${f}|${t}`, [f, t]);
      }
      for (const [f, t] of pairs.values()) {
        const want = cellOf(myMatrix, f, t, oldNetworks).policy;
        if (want === cellOf(oldMatrix, f, t, oldNetworks).policy) continue;
        if (cellOf(next, f, t, baseNetworks.value).policy !== want) next = toggleCell(next, f, t, baseNetworks.value);
      }
      matrix.value = matrixChanges(baseMatrix.value, next, baseNetworks.value).length ? next : undefined;
    }
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
    profiles.value = {};
    devices.value = {};
    groups.value = {};
    removals.value = {};
    clients.value = {};
    networks.value = {};
    routing.value = undefined;
    forwards.value = {};
    scenarios.value = {};
    probes.value = {};
    matrix.value = undefined;
    portsText.value = {};
    serverErrors.value = {};
    // a clean draft is based on the newest active revision seen
    rebaseOnLatest();
  }

  /**
   * Drops the edits and everything the draft learned of the configuration (its base, the newest
   * revision seen, the open drawer and conflict): the admin logged out (Phase 3 audit), and the next
   * session starts from the active revision it reads itself.
   */
  function forget() {
    latest = undefined;
    ownPending = undefined;
    discard();
    base.value = undefined;
    baseConfig.value = undefined;
    drawerOpen.value = false;
    conflict.value = undefined;
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
      const c = /^\/networks\/[^/]+\/clients\/([^/]+)(?:\/([^/]+))?/.exec(e.path ?? '');
      if (c) {
        (out[c[1]!] ??= {})[c[2] ?? 'client'] = e.message ?? 'not valid';
        continue;
      }
      // a network and a routing protocol keep the pointer below them (their dialogs map it to their fields;
      // `network` and `protocol` for the object as a whole), the BIRD settings theirs under `routing`
      const n = /^\/networks\/([^/]+)(?:\/(.+))?$/.exec(e.path ?? '');
      if (n) {
        (out[n[1]!] ??= {})[n[2] ?? 'network'] = e.message ?? 'not valid';
        continue;
      }
      // a port forward keeps the pointer below it (its dialog maps it to its fields; `forward` for the forward as a whole)
      const f = /^\/port_forwards\/([^/]+)(?:\/(.+))?$/.exec(e.path ?? '');
      if (f) {
        (out[f[1]!] ??= {})[f[2] ?? 'forward'] = e.message ?? 'not valid';
        continue;
      }
      // a probe keeps the pointer below it (its dialog maps it to its fields; `probe` for the probe as a whole)
      const pr = /^\/probes\/([^/]+)(?:\/(.+))?$/.exec(e.path ?? '');
      if (pr) {
        (out[pr[1]!] ??= {})[pr[2] ?? 'probe'] = e.message ?? 'not valid';
        continue;
      }
      // a scenario keeps the pointer below it (its editors map it to steps and checks; `scenario` for the scenario as a whole)
      const sc = /^\/scenarios\/([^/]+)(?:\/(.+))?$/.exec(e.path ?? '');
      if (sc) {
        (out[sc[1]!] ??= {})[sc[2] ?? 'scenario'] = e.message ?? 'not valid';
        continue;
      }
      const r = /^\/routing(?:\/protocols\/([^/]+))?(?:\/(.+))?$/.exec(e.path ?? '');
      if (r) {
        (out[r[1] ?? 'routing'] ??= {})[r[2] ?? (r[1] ? 'protocol' : 'routing')] = e.message ?? 'not valid';
        continue;
      }
      const m = /^\/(access_rules|faults|profiles|devices)\/([^/]+)(?:\/(.+))?/.exec(e.path ?? '');
      if (!m) continue;
      // a fault and a profile keep the rest of the pointer ("upload/latency", "parts/mtu/size"; `fault`
      // or `profile` for the object as a whole), which their dialogs map to their fields; a rule and a
      // device the field below the object
      const first = m[3]?.split('/')[0];
      const field =
        m[1] === 'faults' ? (m[3] ?? 'fault') : m[1] === 'profiles' ? (m[3] ?? 'profile') : first === 'port_ranges' ? 'ports' : (first ?? 'rule');
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
    profiles,
    devices,
    groups,
    clients,
    networks,
    routing,
    forwards,
    scenarios,
    probes,
    matrix,
    portsText,
    dirty,
    effectiveRules,
    effectiveOrder,
    effectiveFaults,
    effectiveProfiles,
    effectiveMatrix,
    effectiveClients,
    effectiveNetworks,
    effectiveRouting,
    effectiveForwards,
    baseForwards,
    effectiveScenarios,
    baseScenarios,
    effectiveProbes,
    baseProbes,
    baseNetwork,
    baseRouting,
    baseClient,
    invalid,
    invalidScenarios,
    track,
    notePending,
    editOrigin,
    editFrom,
    setRule,
    setOrder,
    move,
    setFault,
    setProfile,
    restoreRule,
    restoreFault,
    restoreProfile,
    setDevice,
    removeDevice,
    restoreDevice,
    setProbe,
    revertProbe,
    removeProbe,
    restoreProbe,
    effectiveGroups,
    setGroup,
    setClient,
    setNetwork,
    restoreNetwork,
    setRouting,
    setProtocol,
    restoreProtocol,
    setForward,
    restoreForward,
    setScenario,
    restoreScenario,
    toggleMatrix,
    clientName,
    setPortsText,
    changes,
    summary,
    patch,
    objectPatch,
    overlaps,
    rebase,
    discard,
    forget,
    serverErrors,
    setServerErrors,
    drawerOpen,
    conflict,
  };
});
