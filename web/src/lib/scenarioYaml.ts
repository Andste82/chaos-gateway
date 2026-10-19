// The YAML view of a scenario (plan §2.10: YAML is the import/export format, so scenarios can also be versioned
// in git; §2.17 "Scenarios", tab YAML). A scenario is shown as YAML with the names of the objects it refers to
// (the configuration stores their UUIDs), and read back from YAML in which a reference is a name or a UUID, the
// way `chaosctl run -f` takes a file. Reading checks the text as far as the UI can: the syntax, the fields
// the spec has, the schema of the spec (the generated Zod schema) and the problems `scenarioProblems` knows.
import { parseDocument, stringify } from 'yaml';
import type { Scenario } from '../api/generated/model';
import { GetScenarioResponse } from '../api/generated/zod/config/config';
import { cloneJSON } from './mergePatch';
import type { Topology } from './scenarioRules';
import { scenarioProblems, type ScenarioProblem } from './scenarios';

/** An object a reference can name: its UUID and its name. */
export interface NamedRef {
  id: string;
  name: string;
}

/** The objects of the gateway a scenario can refer to, by what the reference points at. */
export interface RefIndex {
  /** Devices, WireGuard clients and probes share one namespace (`Scope.device` can name all three). */
  devices: NamedRef[];
  groups: NamedRef[];
  /** Networks, WireGuard hubs and links. */
  networks: NamedRef[];
  /** Built-in and custom profiles. */
  profiles: NamedRef[];
}

type RefKind = 'device' | 'group' | 'network' | 'profile' | 'client' | 'link';

const listOf = (x: RefIndex, kind: RefKind): NamedRef[] =>
  kind === 'device' || kind === 'client' ? x.devices : kind === 'group' ? x.groups : kind === 'profile' ? x.profiles : x.networks;

const uuidLike = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);

/**
 * Calls `fn` for every reference of a scenario (a document that may be wrong in any way: whatever is
 * not where a reference stands is left alone) and puts its result in place; `path` is the pointer below
 * the scenario. The places are those of the gateway's own walk (`internal/domain/refs.go`).
 */
export function mapRefs(sc: unknown, fn: (kind: RefKind, value: string, path: string) => string): unknown {
  const ref = (o: unknown, key: string, kind: RefKind, path: string) => {
    if (isObj(o) && typeof o[key] === 'string') o[key] = fn(kind, o[key] as string, `${path}/${key}`);
  };
  const scope = (s: unknown, path: string) => {
    if (!isObj(s)) return;
    ref(s, 'device', 'device', path);
    ref(s, 'group', 'group', path);
    ref(s, 'network', 'network', path);
    if (isObj(s.remote_network)) {
      ref(s.remote_network, 'client', 'client', `${path}/remote_network`);
      ref(s.remote_network, 'link', 'link', `${path}/remote_network`);
    }
  };
  const destination = (d: unknown, path: string) => {
    if (isObj(d)) ref(d, 'network', 'network', path);
  };
  const match = (m: unknown, path: string) => {
    if (isObj(m)) destination(m.destination, `${path}/destination`);
  };
  const tunnel = (t: unknown, path: string) => {
    if (!isObj(t)) return;
    ref(t, 'client', 'client', path);
    ref(t, 'link', 'link', path);
  };
  // a copy of plain JSON: a reactive proxy (the draft's scenario) cannot be structured-cloned
  const out = cloneJSON(sc);
  if (!isObj(out)) return out;
  scope(out.target, '/target');
  if (Array.isArray(out.steps)) {
    out.steps.forEach((s: unknown, i: number) => {
      if (!isObj(s)) return;
      const p = `/steps/${i}`;
      scope(s.target, `${p}/target`);
      ref(s, 'profile', 'profile', p);
      if (isObj(s.fault)) {
        destination(s.fault.destination, `${p}/fault/destination`);
        tunnel(s.fault.tunnel, `${p}/fault/tunnel`);
      }
      match(s.rule, `${p}/rule`);
      match(s.tls, `${p}/tls`);
      match(s.wait, `${p}/wait`);
      tunnel(s.wireguard, `${p}/wireguard`);
    });
  }
  if (Array.isArray(out.checks)) {
    out.checks.forEach((c: unknown, i: number) => {
      if (!isObj(c)) return;
      const p = `/checks/${i}`;
      for (const k of ['reconnected', 'no_connection', 'traffic_seen', 'tls_rejected', 'tls_accepted']) match(c[k], `${p}/${k}`);
      if (isObj(c.traffic_not_seen)) {
        match(c.traffic_not_seen, `${p}/traffic_not_seen`);
        if (Array.isArray(c.traffic_not_seen.except)) c.traffic_not_seen.except.forEach((d: unknown, j: number) => destination(d, `${p}/traffic_not_seen/except/${j}`));
      }
    });
  }
  return out;
}

/** Puts the names of known objects where a scenario has their UUIDs (what the YAML view shows). */
export function withNames(sc: Scenario, index: RefIndex): Scenario {
  return mapRefs(sc, (kind, v) => {
    if (!uuidLike.test(v)) return v;
    const hit = listOf(index, kind).find((r) => r.id === v);
    // a name is shown only if it reads back as the same object
    return hit && listOf(index, kind).filter((r) => r.name.toLowerCase() === hit.name.toLowerCase()).length === 1 ? hit.name : v;
  }) as Scenario;
}

/** Puts the UUIDs of known objects where a scenario has their names; an unknown reference is reported. */
export function withIds(sc: unknown, index: RefIndex): { scenario: unknown; unknown: ScenarioProblem[] } {
  const unknown: ScenarioProblem[] = [];
  const scenario = mapRefs(sc, (kind, v, path) => {
    if (uuidLike.test(v)) return v;
    const hit = listOf(index, kind).find((r) => r.name.toLowerCase() === v.toLowerCase());
    if (hit) return hit.id;
    unknown.push({ path, message: `There is no ${kind === 'client' ? 'WireGuard client' : kind === 'link' ? 'link' : kind} "${v}".` });
    return v;
  });
  return { scenario, unknown };
}

// the order the spec and the plan's example give the members in
const scenarioOrder = ['name', 'description', 'target', 'capture', 'zero_hit', 'preconditions', 'steps', 'checks'];
const stepOrder = ['id', 'at', 'target', 'profile', 'fault', 'rule', 'dns', 'tls', 'dhcp', 'wireguard', 'capture', 'wait', 'remove', 'restore'];
const checkOrder = ['name', 'reconnected', 'no_connection', 'traffic_seen', 'traffic_not_seen', 'dns_query_seen', 'tls_rejected', 'tls_accepted', 'window'];

function ordered(o: unknown, keys: string[]): unknown {
  if (!isObj(o)) return o;
  const out: Obj = {};
  for (const k of keys) if (o[k] !== undefined) out[k] = o[k];
  for (const k of Object.keys(o)) if (!(k in out)) out[k] = o[k];
  return out;
}

/** The scenario as YAML, its members in the order of the spec; `index` puts names for the UUIDs. */
export function scenarioToYaml(sc: Scenario, index?: RefIndex): string {
  const shown = index ? withNames(sc, index) : sc;
  const o = ordered(shown, scenarioOrder) as Obj;
  if (Array.isArray(o.steps)) o.steps = o.steps.map((s) => ordered(s, stepOrder));
  if (Array.isArray(o.checks)) o.checks = o.checks.map((c) => ordered(c, checkOrder));
  return stringify(o, { lineWidth: 0 });
}

export interface YamlProblem {
  message: string;
  /** The pointer below the scenario, when the problem is about one member. */
  path?: string;
  /** The line of the text (1-based), for a syntax error. */
  line?: number;
}

export interface YamlResult {
  /** The scenario with the UUIDs of its references; undefined when the text has problems. */
  scenario?: Scenario;
  problems: YamlProblem[];
}

/** The members the input has that the parsed output does not: what the schema strips, which the API refuses (`unknown_field`). */
function unknownFields(input: unknown, output: unknown, path: string, out: ScenarioProblem[]) {
  if (Array.isArray(input)) {
    input.forEach((x, i) => unknownFields(x, Array.isArray(output) ? output[i] : undefined, `${path}/${i}`, out));
    return;
  }
  if (!isObj(input)) return;
  const o = isObj(output) ? output : {};
  for (const [k, v] of Object.entries(input)) {
    if (!(k in o)) out.push({ path: `${path}/${k}`, message: `${k} is not a field of the scenario format.` });
    else unknownFields(v, o[k], `${path}/${k}`, out);
  }
}

/** The pointer of a Zod issue's path: `['steps', 2, 'at']` → `/steps/2/at`. */
const pointer = (p: PropertyKey[]): string => p.map((s) => `/${String(s)}`).join('');

/**
 * Reads a scenario from YAML (a file as `chaosctl run -f` takes it, or what the YAML tab holds): the syntax, a
 * mapping at the top, the references as names or UUIDs, the schema, the fields the format has and the
 * problems `scenarioProblems` finds. `others` are the names of the other scenarios, `topology` what is known of the
 * configuration for the rules about the target.
 */
export function parseScenarioYaml(text: string, index: RefIndex, others: string[] = [], topology: Topology = {}): YamlResult {
  const doc = parseDocument(text, { prettyErrors: true, uniqueKeys: true });
  if (doc.errors.length) {
    return {
      problems: doc.errors.map((e) => ({
        // the library appends the position to its message; the problem carries it as `line`
        message: (e.message.split('\n')[0] ?? e.message).replace(/ at line \d+, column \d+:?$/, ''),
        ...(e.linePos?.[0] ? { line: e.linePos[0].line } : {}),
      })),
    };
  }
  const raw = doc.toJS({ maxAliasCount: 100 }) as unknown;
  if (!isObj(raw)) return { problems: [{ message: 'A scenario is a mapping: name, target, steps, checks.' }] };
  const { scenario, unknown } = withIds(raw, index);
  const problems: YamlProblem[] = [...unknown];
  const taken = new Set(problems.map((p) => p.path));
  const add = (p: ScenarioProblem) => {
    // one message per place, and none for a place below one that already has a problem
    if (taken.has(p.path) || [...taken].some((t) => t && p.path.startsWith(`${t}/`))) return;
    taken.add(p.path);
    problems.push(p);
  };
  const sc = scenario as Scenario;
  scenarioProblems(sc, others, topology).forEach(add);
  const parsed = GetScenarioResponse.shape.config.safeParse(scenario);
  if (parsed.success) {
    const stripped: ScenarioProblem[] = [];
    unknownFields(scenario, parsed.data, '', stripped);
    stripped.forEach(add);
  } else {
    for (const i of parsed.error.issues) add({ path: pointer(i.path), message: i.message });
  }
  return problems.length ? { problems } : { scenario: sc, problems };
}
