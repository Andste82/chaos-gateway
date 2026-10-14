import { defineStore } from 'pinia';
import { computed, ref, watch } from 'vue';
import type {
  AccessRule,
  ConfigFault,
  Configuration,
  ConfigurationPatch,
  Device,
  MatrixEntry,
  NetworkView,
  RevisionWithConfiguration,
  WireGuardClient,
  WireGuardNetwork,
} from '../api/generated/model';
import { faultChips, describeScope, type Names } from '../lib/faults';
import { applyMergePatch, bothChanged, cloneJSON, createMergePatch, jsonEqual } from '../lib/mergePatch';
import { cellOf, entryKey, matrixChanges, toggleCell } from '../lib/networks';
import { accessLabels, describeDestination, ruleErrors, ruleFieldChanges, formatPorts, type FieldChange } from '../lib/rules';

/** The members compared as a whole when both sides changed an object: a scope or a destination is one choice. */
const atomicMembers = new Set(['source', 'target', 'destination']);

/** The value of a member in words, for the fields both sides changed: "Reject", "no", "80, 443", "Network IoT". */
function valueText(key: string, v: unknown, names: Names, other?: unknown): string {
  if (key === 'enabled') return v === false ? 'no' : 'yes';
  // a switch that is not set is off (the other side's value says it is a switch)
  if (v === undefined || v === null) return typeof other === 'boolean' ? 'no' : '—';
  if (typeof v === 'boolean') return v ? 'yes' : 'no';
  if (key === 'source' || key === 'target') return describeScope(v as Parameters<typeof describeScope>[0], names);
  if (key === 'destination') return describeDestination(v as Parameters<typeof describeDestination>[0], names);
  if (key === 'action') return accessLabels[v as keyof typeof accessLabels] ?? String(v);
  if (Array.isArray(v)) {
    return v
      .map((x) => (typeof x === 'object' && x !== null && 'from' in x && 'to' in x ? `${(x as { from: number }).from}-${(x as { to: number }).to}` : typeof x === 'object' ? JSON.stringify(x) : String(x)))
      .join(', ') || '—';
  }
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

/** One change of the draft as the unapplied-changes bar counts it and the rule list marks it. */
export interface DraftChange extends FieldChange {
  kind: 'rule' | 'fault' | 'device' | 'client' | 'matrix';
  id: string;
  /** The object's name (or its id when it has none). */
  name: string;
}

/**
 * The configuration changes made in the UI and not applied yet (plan §2.17, "Unapplied changes"):
 * access rules, their order, configured faults, adopted devices, WireGuard clients and the access matrix, edited
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
  /** Devices added to the configuration (adopted), by id. */
  const devices = ref<Record<string, Device | null>>({});
  /** WireGuard clients added, changed or deleted (`client: null`), by client id with their hub. */
  const clients = ref<Record<string, { network: string; client: WireGuardClient | null }>>({});
  /** The entries of the access matrix, when they were edited. */
  const matrix = ref<MatrixEntry[]>();
  /** The ports as typed, by rule (a text that is no list of ports yet keeps its error). */
  const portsText = ref<Record<string, string>>({});

  const dirty = computed(
    () =>
      Object.keys(rules.value).length > 0 ||
      order.value !== undefined ||
      Object.keys(faults.value).length > 0 ||
      Object.keys(devices.value).length > 0 ||
      Object.keys(clients.value).length > 0 ||
      matrix.value !== undefined,
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

  /** Flips the policy of one pair of the matrix (`toggleCell`); a matrix back at its base is no change. */
  function toggleMatrix(from: string, to: string) {
    const next = toggleCell(effectiveMatrix.value, from, to, baseNetworks.value);
    matrix.value = matrixChanges(baseMatrix.value, next, baseNetworks.value).length === 0 ? undefined : next;
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
    const netName = (id: string) => names.network?.(id) ?? (baseConfig.value?.networks?.[id] as { name?: string } | undefined)?.name;
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
        const rest = (c: WireGuardClient) => ({ ...c, name: '', address: '', client_networks: [], enabled: true });
        if (!jsonEqual(rest(before), rest(client))) out.push({ kind: 'client', id, name, field: 'settings', from: 'as before', to: 'changed' });
      }
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
    const deviceIds = new Set(cs.filter((c) => c.kind === 'device').map((c) => c.id));
    const clientIds = new Set(cs.filter((c) => c.kind === 'client').map((c) => c.id));
    const objects = [
      ruleIds.size ? `${ruleIds.size} ${ruleIds.size === 1 ? 'rule' : 'rules'}` : '',
      faultIds.size ? `${faultIds.size} ${faultIds.size === 1 ? 'fault' : 'faults'}` : '',
      deviceIds.size ? `${deviceIds.size} ${deviceIds.size === 1 ? 'device' : 'devices'}` : '',
      clientIds.size ? `${clientIds.size} WireGuard ${clientIds.size === 1 ? 'client' : 'clients'}` : '',
      cs.some((c) => c.kind === 'matrix') ? 'the access matrix' : '',
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
    const np: Record<string, { clients: Record<string, unknown> }> = {};
    for (const [id, { network, client }] of Object.entries(clients.value)) {
      const p = createMergePatch(clientsIn(baseConfig.value, network)[id], client);
      if (p !== undefined) (np[network] ??= { clients: {} }).clients[id] = p;
    }
    if (Object.keys(np).length) out.networks = np;
    // the entries are an array: the merge patch replaces it whole
    if (matrix.value && !jsonEqual(matrix.value, baseMatrix.value)) out.access_matrix = { entries: cloneJSON(matrix.value) };
    return out;
  }

  /**
   * What both the draft and another revision changed, as the concurrent-change dialog names it before
   * reapplying (P3-M13-07): per object the fields both changed to different values, `from` their
   * value and `to` mine (which `rebase` takes); an object they changed that I delete, and one I
   * changed that they removed (it comes back as mine). A pair of the access matrix has two
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
        for (const path of bothChanged(was, now, m, atomicMembers)) {
          const get = (o: unknown) => path.reduce<unknown>((v, k) => (typeof v === 'object' && v !== null ? (v as Record<string, unknown>)[k] : undefined), o);
          const key = path.at(-1) ?? '';
          const t = get(now);
          const mv = get(m);
          out.push({ kind, id, name, field: path.join(' ').replace(/_/g, ' '), from: valueText(key, t, names, mv), to: valueText(key, mv, names, t) });
        }
      }
    }
    const named = (o: { name?: string }) => o.name;
    compare('rule', rules.value, old?.access_rules ?? {}, theirs.access_rules ?? {}, named);
    compare('fault', faults.value, old?.faults ?? {}, theirs.faults ?? {}, named);
    compare('device', devices.value, old?.devices ?? {}, theirs.devices ?? {}, named);
    for (const net of new Set(Object.values(clients.value).map((c) => c.network))) {
      const edits = Object.fromEntries(
        Object.entries(clients.value)
          .filter(([, c]) => c.network === net)
          .map(([id, c]) => [id, c.client]),
      );
      compare('client', edits, clientsIn(old, net), clientsIn(theirs, net), named);
    }
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
    const oldNetworks = baseNetworks.value;
    const oldMatrix = baseMatrix.value;
    const myMatrix = matrix.value;
    const myClients = clients.value;
    // the revision just fetched is the newest seen: a draft this leaves clean stays on it
    if (!latest || latest.id < rev.id) latest = rev;
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
    // clients hub by hub, like the other objects
    const nextClients: typeof clients.value = {};
    for (const net of new Set(Object.values(myClients).map((c) => c.network))) {
      const edits = Object.fromEntries(
        Object.entries(myClients)
          .filter(([, c]) => c.network === net)
          .map(([id, c]) => [id, c.client]),
      );
      for (const [id, c] of Object.entries(reapply(edits, clientsIn(old, net), clientsIn(rev.configuration, net)))) nextClients[id] = { network: net, client: c };
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
    devices.value = {};
    clients.value = {};
    matrix.value = undefined;
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
      const c = /^\/networks\/[^/]+\/clients\/([^/]+)(?:\/([^/]+))?/.exec(e.path ?? '');
      if (c) {
        (out[c[1]!] ??= {})[c[2] ?? 'client'] = e.message ?? 'not valid';
        continue;
      }
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
    clients,
    matrix,
    portsText,
    dirty,
    effectiveRules,
    effectiveOrder,
    effectiveFaults,
    effectiveMatrix,
    effectiveClients,
    invalid,
    track,
    setRule,
    setOrder,
    move,
    setFault,
    restoreRule,
    restoreFault,
    setDevice,
    setClient,
    toggleMatrix,
    clientName,
    setPortsText,
    changes,
    summary,
    patch,
    overlaps,
    rebase,
    discard,
    serverErrors,
    setServerErrors,
    drawerOpen,
    conflict,
  };
});
