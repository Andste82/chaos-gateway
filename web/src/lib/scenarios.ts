// Scenarios in the UI (plan §2.10, §2.17 "Scenarios"; M19): a scenario is a timeline of steps and a list of
// checks, an object of the configuration (`scenarios`) that is made and changed through the draft and the
// preview like a profile. This module reads a scenario in the user's words (step and check types, the line
// each step and check gets, the length of the timeline), orders and edits its steps, finds the problems the
// gateway would refuse before the draft is sent, and says what changed between two versions of a scenario.
import type { Check, Scenario, Scope, Step } from '../api/generated/model';
import {
  createRunBodyTwoInlineOneNameRegExp as nameRegExp,
  createRunBodyTwoInlineOneStepsItemAtRegExp as durationRegExp,
  createRunBodyTwoInlineOneStepsItemIdRegExp as stepIdRegExp,
} from '../api/generated/zod/runs/runs';
import { accessLabel, describeDestination, resetError, type FieldChange } from './rules';
import { createsOverlay, messages, reservedStepId, scopeWithin, startsCapture, tunnelOutsideTarget, type Topology } from './scenarioRules';
import { describeMatch, describeScope, faultChips, type Names } from './faults';
import { durationMs, formatDuration } from './format';
import { jsonEqual } from './mergePatch';
import { profileTitle } from './profileNames';

export type StepType = 'profile' | 'fault' | 'rule' | 'dns' | 'tls' | 'dhcp' | 'wireguard' | 'capture' | 'wait' | 'remove' | 'restore';
export type CheckType = 'reconnected' | 'no_connection' | 'traffic_seen' | 'traffic_not_seen' | 'dns_query_seen' | 'tls_rejected' | 'tls_accepted';
export type Zero = 'warning' | 'error';

/** The step types in the order the editor offers them. */
export const stepTypes: StepType[] = ['profile', 'fault', 'rule', 'wireguard', 'capture', 'wait', 'remove', 'restore', 'dns', 'tls', 'dhcp'];

export const stepTypeLabels: Record<StepType, string> = {
  profile: 'Profile',
  fault: 'Fault',
  rule: 'Access rule',
  dns: 'DNS fault',
  tls: 'TLS case',
  dhcp: 'DHCP action',
  wireguard: 'WireGuard action',
  capture: 'Capture',
  wait: 'Wait',
  remove: 'Remove',
  restore: 'Restore',
};

/** The step types that impair or block traffic are orange or red, the others blue (plan §2.17 "Visual system"). */
export const stepTypeTone: Record<StepType, 'fault' | 'block' | 'normal'> = {
  profile: 'fault',
  fault: 'fault',
  rule: 'block',
  dns: 'fault',
  tls: 'fault',
  dhcp: 'fault',
  wireguard: 'fault',
  capture: 'normal',
  wait: 'normal',
  remove: 'normal',
  restore: 'normal',
};

export const checkTypes: CheckType[] = ['reconnected', 'no_connection', 'traffic_seen', 'traffic_not_seen', 'dns_query_seen', 'tls_rejected', 'tls_accepted'];

export const checkTypeLabels: Record<CheckType, string> = {
  reconnected: 'Reconnected',
  no_connection: 'No connection',
  traffic_seen: 'Traffic seen',
  traffic_not_seen: 'Traffic not seen',
  dns_query_seen: 'DNS query seen',
  tls_rejected: 'TLS rejected',
  tls_accepted: 'TLS accepted',
};

/** What a check type asserts, for the editor. */
export const checkTypeHints: Record<CheckType, string> = {
  reconnected: 'A new connection of the target is established inside the window. Passes at the first one.',
  no_connection: 'No connection of the target is established inside the window. Fails at the first one.',
  traffic_seen: 'A packet of a connection of the target is seen inside the window. Passes at the first one.',
  traffic_not_seen: 'No packet of a connection of the target is seen inside the window, except to the destinations allowed below.',
  dns_query_seen: 'A device of the target asks the gateway for the name inside the window.',
  tls_rejected: 'The device aborted the TLS handshake with the responder.',
  tls_accepted: 'The device completed the handshake.',
};

const stepKeys: StepType[] = ['profile', 'fault', 'rule', 'dns', 'tls', 'dhcp', 'wireguard', 'capture', 'wait', 'remove', 'restore'];

/** The type of a step: the one member it has besides `id`, `at` and `target`; undefined for a step that has none. */
export function stepType(s: Step): StepType | undefined {
  return stepKeys.find((k) => s[k] !== undefined);
}

/** The type of a check: the one member it has besides `name` and `window`. */
export function checkType(c: Check): CheckType | undefined {
  return checkTypes.find((k) => c[k] !== undefined);
}

/** The scenario's steps in the order the engine runs them: by `at`, the list order for equal `at`. */
export function timelineOrder(steps: Step[]): Step[] {
  return steps.map((s, i) => ({ s, i, at: durationMs(typeof s.at === 'string' ? s.at : undefined) ?? 0 })).sort((a, b) => a.at - b.at || a.i - b.i).map((x) => x.s);
}

/**
 * The length of the timeline in milliseconds: the last step, or the end of the last check window that
 * ends at a time (`within` after a step, or `until` a step). A `wait` step shifts the later steps by the
 * time it waited, which no one knows beforehand, so the length is the one without waiting.
 */
export function scenarioLength(sc: Pick<Scenario, 'steps' | 'checks'>): number {
  const at = new Map(sc.steps.map((s) => [s.id, durationMs(s.at) ?? 0]));
  let end = 0;
  for (const s of sc.steps) end = Math.max(end, durationMs(s.at) ?? 0);
  for (const c of sc.checks ?? []) {
    const from = c.window.from === 'start' ? 0 : (at.get(c.window.from) ?? 0);
    const to = c.window.until !== undefined ? (at.get(c.window.until) ?? from) : from + (durationMs(c.window.within) ?? 0);
    end = Math.max(end, to);
  }
  return end;
}

/** A length as the lists show it: "45 s", "2 min 30 s", "1 h 5 min"; "0 s" for no time. */
export function lengthText(ms: number): string {
  const total = Math.round(ms / 1000);
  if (total < 1) return ms > 0 ? `${Math.round(ms)} ms` : '0 s';
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  return [h ? `${h} h` : '', m ? `${m} min` : '', s ? `${s} s` : ''].filter(Boolean).join(' ');
}

/** The names a scenario's references are shown with: a profile by its (display) name, devices, groups and networks. */
export type ScenarioNames = Names & { profile?: (ref: string) => string | undefined };

/** The line of a step: what it does, in words ("↑↓ 200 ms ±50 → TCP 8883", "Drop · cut existing → TCP 8883"). */
export function stepText(s: Step, names: ScenarioNames = {}): string {
  const type = stepType(s);
  switch (type) {
    case 'profile': {
      const ref = s.profile ?? '';
      return `Profile ${profileTitle(names.profile?.(ref) ?? ref)}`;
    }
    case 'fault': {
      const f = s.fault!;
      const chips = f.family === 'mtu' || f.family === 'tunnel' || faultChips(f).length ? faultChips(f) : [];
      const tunnel = f.tunnel ? ` · tunnel of ${f.tunnel.client ? (names.device?.(f.tunnel.client) ?? f.tunnel.client) : `link ${names.network?.(f.tunnel.link ?? '') ?? f.tunnel.link}`}` : '';
      return `${chips.join(', ') || 'Fault'}${describeMatch(f, names) ? ` ${describeMatch(f, names)}` : ''}${tunnel}`;
    }
    case 'rule': {
      const r = s.rule!;
      return `${accessLabel(r)}${describeMatch(r, names) ? ` ${describeMatch(r, names)}` : ' · all traffic'}`;
    }
    case 'wireguard': {
      const w = s.wireguard!;
      const who = w.client ? `client ${names.device?.(w.client) ?? w.client}` : w.link ? `link ${names.network?.(w.link) ?? w.link}` : 'the tunnel';
      return `WireGuard ${w.action.replace(/_/g, ' ')} · ${who}`;
    }
    case 'capture':
      return s.capture === 'start' ? 'Start a capture of the target' : 'Stop the captures of the target';
    case 'wait': {
      const w = s.wait!;
      const t = `timeout ${formatDuration(w.timeout)}`;
      if (w.for === 'online') return `Wait until the target is online · ${t}`;
      if (w.for === 'dns_query') return `Wait for a DNS query for ${w.name ?? 'a name'} · ${t}`;
      return `Wait for a new connection${describeMatch(w, names) ? ` ${describeMatch(w, names)}` : ''} · ${t}`;
    }
    case 'remove':
      return `Remove the overlay of step ${s.remove}`;
    case 'restore':
      return 'Restore: remove all overlays of the run';
    case 'dns':
      return `DNS ${s.dns!.action.replace(/_/g, ' ')}${s.dns!.names?.length ? ` · ${s.dns!.names.join(', ')}` : ''}`;
    case 'tls':
      return `TLS ${s.tls!.case.replace(/_/g, ' ')}${describeMatch(s.tls, names) ? ` ${describeMatch(s.tls, names)}` : ''}`;
    case 'dhcp':
      return `DHCP ${s.dhcp!.action.replace(/_/g, ' ')}`;
    default:
      return 'Step without a type';
  }
}

/** The check's own selector (a `TrafficMatch`, or the name of a DNS query), in words. */
export function checkText(c: Check, names: ScenarioNames = {}): string {
  const type = checkType(c);
  switch (type) {
    case 'dns_query_seen':
      return `a query for ${c.dns_query_seen!.name}`;
    case 'traffic_not_seen': {
      const t = c.traffic_not_seen!;
      const except = (t.except ?? []).map((d) => describeDestination(d, names)).join(', ');
      return `${describeMatch(t, names).replace(/^→ /, '') || 'any traffic'}${except ? ` except ${except}` : ''}`;
    }
    case undefined:
      return 'a check without a type';
    default:
      return describeMatch(c[type], names).replace(/^→ /, '') || 'any connection';
  }
}

/** The window of a check in words: "within 30 s of restore", "from start until cut". */
export function windowText(w: Check['window']): string {
  const from = w.from === 'start' ? 'the start' : `step ${w.from}`;
  if (w.until) return `from ${from} until step ${w.until}`;
  return `within ${formatDuration(w.within)} of ${from}`;
}

/** The name of a check in the report: its own, else "reconnected 1" from the type and its position. */
export function checkName(c: Check, index: number): string {
  return c.name ?? `${checkType(c) ?? 'check'} ${index + 1}`;
}

/** The scope a step acts on in words, when it narrows the scenario's target. */
export function narrowText(s: Step, names: ScenarioNames = {}): string | undefined {
  return s.target ? describeScope(s.target, names) : undefined;
}

// ---- the problems of a scenario ----------------------------------------------------------------

export interface ScenarioProblem {
  /** The pointer below the scenario: `/name`, `/steps/2/at`, `/checks/0/window/from`. */
  path: string;
  message: string;
}

/** Exactly one of the members: the members a step or a check has. */
function present(o: object, keys: readonly string[]): string[] {
  return keys.filter((k) => (o as Record<string, unknown>)[k] !== undefined);
}

/**
 * What the gateway would refuse in a scenario that the UI can see without it (the checks of
 * `domain.ValidateScenario` that need nothing of the rest of the configuration): the name, one type per
 * step and check, the step ids (not `start`) and offsets, a `remove` of an earlier step that makes an overlay, a
 * `capture: stop` after the start of the same target, a step that stays inside the target, a tunnel inside it, a
 * reset of TCP, a timeout and a window longer than zero, and the windows of the checks. `others` are the names of
 * the other scenarios (names are unique, case-insensitive). What needs the configuration (is a device in the
 * group or the network of the target, is a client in the target's network) is checked as far as `topology` tells;
 * without it only the certain cases are problems.
 */
export function scenarioProblems(sc: Scenario, others: string[] = [], topology: Topology = {}): ScenarioProblem[] {
  // the scenario may come from a text (the YAML view, an import): whatever is not where it should be is a problem, not an error
  const out: ScenarioProblem[] = [];
  const add = (path: string, message: string) => out.push({ path, message });
  const o = (sc ?? {}) as unknown as Record<string, unknown>;
  const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined);
  const obj = (v: unknown): Record<string, unknown> | undefined => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined);
  const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
  const name = (str(o.name) ?? '').trim();
  if (!name) add('/name', 'Give the scenario a name.');
  else if (!nameRegExp.test(name) || uuidLike.test(name)) add('/name', 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.');
  else if (others.some((x) => x.toLowerCase() === name.toLowerCase())) add('/name', 'Another scenario has this name.');
  if ((str(o.description) ?? '').length > 500) add('/description', 'At most 500 characters.');
  const target = obj(o.target);
  if (!target || Object.keys(target).length === 0) add('/target', 'Choose what the scenario acts on.');
  else if (target.global) add('/target', 'A scenario acts on a device, a group or a network, not on everything.');
  else if (target.remote_network) add('/target', 'A scenario acts on a device, a group or a network, not on a remote network.');
  const steps = list(o.steps).map(obj);
  const checks = list(o.checks).map(obj);
  if (!steps.length) add('/steps', 'A scenario needs at least one step.');
  if (steps.length > 500) add('/steps', 'At most 500 steps.');
  if (checks.length > 100) add('/checks', 'At most 100 checks.');

  const ids = new Map<string, number>();
  // the position of each step on the timeline (by time, then by the list); a time that is none counts as 0
  const rank = new Map(
    steps
      .map((s, i) => ({ i, at: durationMs(str(s?.at)) ?? 0 }))
      .sort((a, b) => a.at - b.at || a.i - b.i)
      .map((x, k) => [x.i, k] as const),
  );
  steps.forEach((s, i) => {
    const p = `/steps/${i}`;
    if (!s) {
      add(p, 'A step is a mapping: id, at and its type.');
      return;
    }
    const id = str(s.id) ?? '';
    if (!stepIdRegExp.test(id)) add(`${p}/id`, 'Letters, digits, dash and underscore; starting with a lower-case letter; at most 32.');
    else {
      if (id === reservedStepId) add(`${p}/id`, messages.reservedId);
      else if (ids.has(id)) add(`${p}/id`, `The id ${id} is used by another step.`);
      if (!ids.has(id)) ids.set(id, i);
    }
    if (!durationRegExp.test(str(s.at) ?? '')) add(`${p}/at`, 'A time like 0s, 10s or 1m30s.');
    const types = present(s, stepKeys);
    if (types.length === 0) add(p, 'A step needs one of: ' + stepKeys.join(', ') + '.');
    else if (types.length > 1) add(p, `A step has exactly one of its types; this one has ${types.join(' and ')}.`);
  });
  // the rules about the target and what a step does (the gateway reports them for a step with exactly one type)
  const scenarioTarget = target && Object.keys(target).length === 1 ? (target as Scope) : undefined;
  steps.forEach((s, i) => {
    if (!s || present(s, stepKeys).length !== 1) return;
    const p = `/steps/${i}`;
    const own = obj(s.target) as Scope | undefined;
    if (own && scenarioTarget && !scopeWithin(own, scenarioTarget, topology)) add(`${p}/target`, messages.widened);
    const effective = own ?? scenarioTarget;
    const rule = obj(s.rule);
    const reset = rule ? resetError(str(rule.action), str(rule.protocol)) : undefined;
    if (reset) add(`${p}/rule/action`, reset);
    const wait = obj(s.wait);
    if (wait && durationRegExp.test(str(wait.timeout) ?? '') && durationMs(str(wait.timeout)) === 0) add(`${p}/wait/timeout`, messages.zeroTimeout);
    if (s.capture === 'stop' && effective) {
      const before = steps.flatMap((x, j) => (x && j !== i && (rank.get(j) ?? 0) < (rank.get(i) ?? 0) ? [x as unknown as Step] : []));
      if (!startsCapture(before, scenarioTarget!, effective)) add(`${p}/capture`, messages.captureStop);
    }
    const fault = obj(s.fault);
    const tunnel = fault?.family === 'tunnel' ? obj(fault.tunnel) : obj(s.wireguard);
    if (effective && tunnel && tunnelOutsideTarget({ client: str(tunnel.client), link: str(tunnel.link) }, effective, topology)) {
      add(fault?.family === 'tunnel' ? `${p}/fault/tunnel` : `${p}/wireguard`, messages.tunnel);
    }
  });
  steps.forEach((s, i) => {
    if (!s || s.remove === undefined || present(s, stepKeys).length !== 1) return;
    const named = str(s.remove) ?? '';
    const at = ids.get(named);
    if (at === undefined) add(`/steps/${i}/remove`, `There is no step ${named}.`);
    else if (named === s.id || (rank.get(at) ?? 0) >= (rank.get(i) ?? 0)) add(`/steps/${i}/remove`, `Step ${named} does not come before this one.`);
    else if (steps[at] && !createsOverlay(steps[at] as unknown as Step)) add(`/steps/${i}/remove`, messages.noOverlay(named));
  });
  checks.forEach((c, i) => {
    const p = `/checks/${i}`;
    if (!c) {
      add(p, 'A check is a mapping: its type and its window.');
      return;
    }
    const types = present(c, checkTypes);
    if (types.length === 0) add(p, 'A check needs one of: ' + checkTypes.join(', ') + '.');
    else if (types.length > 1) add(p, `A check has exactly one type; this one has ${types.join(' and ')}.`);
    if (c.name !== undefined && (str(c.name) ?? '').length > 100) add(`${p}/name`, 'At most 100 characters.');
    const w = obj(c.window);
    if (!w) {
      add(`${p}/window`, 'A check needs a window.');
      return;
    }
    const from = str(w.from) ?? '';
    if (from !== 'start' && !ids.has(from)) add(`${p}/window/from`, from ? `There is no step ${from}.` : 'Choose the step that opens the window, or the start.');
    if ((w.within === undefined) === (w.until === undefined)) add(`${p}/window`, 'A window has exactly one of within and until.');
    else if (w.within !== undefined && !durationRegExp.test(str(w.within) ?? '')) add(`${p}/window/within`, 'A time like 30s or 2m.');
    else if (w.within !== undefined && durationMs(str(w.within)) === 0) add(`${p}/window/within`, messages.zeroWindow);
    else if (w.until !== undefined) {
      const until = str(w.until) ?? '';
      const to = ids.get(until);
      if (to === undefined) add(`${p}/window/until`, `There is no step ${until}.`);
      else if (from !== 'start' && ids.has(from) && (rank.get(to) ?? 0) <= (rank.get(ids.get(from)!) ?? 0)) add(`${p}/window/until`, `Step ${until} does not come after step ${from}.`);
    }
  });
  return out;
}

const uuidLike = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * The ids of the steps that come before a step at the offset `at` on the timeline: those at an earlier time, and those
 * at the same time that stand before it in the list. A new step (`self` undefined) goes after the others at its time.
 */
export function stepsBefore(sc: Pick<Scenario, 'steps'>, at: string, self?: string): string[] {
  const t = durationMs(at) ?? 0;
  const own = self === undefined ? -1 : sc.steps.findIndex((s) => s.id === self);
  return sc.steps.flatMap((s, i) => {
    if (s.id === self) return [];
    const x = durationMs(s.at) ?? 0;
    return x < t || (x === t && (own < 0 || i < own)) ? [s.id] : [];
  });
}

/** The checks and steps that refer to a step: what stands in the way of deleting or renaming it. */
export function stepReferences(sc: Scenario, id: string): { steps: string[]; checks: string[] } {
  return {
    steps: (sc.steps ?? []).filter((s) => s.remove === id).map((s) => s.id),
    checks: (sc.checks ?? []).flatMap((c, i) => (c.window.from === id || c.window.until === id ? [checkName(c, i)] : [])),
  };
}

// ---- editing ------------------------------------------------------------------------------------

/** A scenario without steps yet. */
export function newScenario(name: string, target: Scenario['target']): Scenario {
  return { name, target, steps: [] };
}

/** The id for a new step: `step-1`, `step-2`, … not used yet. */
export function freeStepId(sc: Pick<Scenario, 'steps'>, base = 'step'): string {
  const used = new Set(sc.steps.map((s) => s.id));
  for (let i = 1; ; i++) {
    const id = `${base}-${i}`;
    if (!used.has(id)) return id;
  }
}

/**
 * The scenario with a step added (`was` undefined) or replaced (`was` is the id it had). A step that
 * changes its id takes the references to it along: a `remove` of it and the windows of the checks.
 */
export function withStep(sc: Scenario, step: Step, was?: string): Scenario {
  const steps = [...sc.steps];
  const i = was === undefined ? -1 : steps.findIndex((s) => s.id === was);
  if (i >= 0) steps[i] = step;
  else steps.push(step);
  if (was === undefined || was === step.id) return { ...sc, steps };
  return {
    ...sc,
    steps: steps.map((s) => (s.remove === was ? { ...s, remove: step.id } : s)),
    ...(sc.checks
      ? {
          checks: sc.checks.map((c) =>
            c.window.from === was || c.window.until === was
              ? { ...c, window: { ...c.window, ...(c.window.from === was ? { from: step.id } : {}), ...(c.window.until === was ? { until: step.id } : {}) } }
              : c,
          ),
        }
      : {}),
  };
}

/** The scenario without a step; the caller has checked `stepReferences`. */
export function withoutStep(sc: Scenario, id: string): Scenario {
  return { ...sc, steps: sc.steps.filter((s) => s.id !== id) };
}

/** The scenario with a check added (`at` undefined) or replaced (its position). */
export function withCheck(sc: Scenario, check: Check, at?: number): Scenario {
  const checks = [...(sc.checks ?? [])];
  if (at === undefined) checks.push(check);
  else checks[at] = check;
  return { ...sc, checks };
}

/** The scenario without a check; an empty list is left out. */
export function withoutCheck(sc: Scenario, at: number): Scenario {
  const checks = (sc.checks ?? []).filter((_, i) => i !== at);
  const { checks: _old, ...rest } = sc;
  return checks.length ? { ...rest, checks } : rest;
}

// ---- what changed -------------------------------------------------------------------------------

/** "mqtt-outage", the name the lists and the bar use for a scenario. */
export function scenarioTitle(sc: Pick<Scenario, 'name'>): string {
  return sc.name;
}

/** The scenario in one line for the lists: "5 steps · 45 s · 2 checks". */
export function scenarioMeta(sc: Scenario): string {
  const steps = sc.steps?.length ?? 0;
  const checks = sc.checks?.length ?? 0;
  return [`${steps} ${steps === 1 ? 'step' : 'steps'}`, lengthText(scenarioLength(sc)), `${checks} ${checks === 1 ? 'check' : 'checks'}`].join(' · ');
}

/**
 * The fields that differ between two versions of a scenario, in the words of the preview: its settings, and the
 * steps and checks as added, removed or changed (by step id, and by position for the checks).
 */
export function scenarioChanges(before: Scenario | undefined, after: Scenario | null, names: ScenarioNames = {}): FieldChange[] {
  if (after === null) return [{ field: 'scenario', from: 'present', to: 'deleted' }];
  if (!before) return [{ field: 'scenario', from: '—', to: `added (${scenarioMeta(after)})` }];
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => {
    if (a !== b) out.push({ field, from: a || '—', to: b || '—' });
  };
  add('name', before.name, after.name);
  add('description', before.description ?? '', after.description ?? '');
  if (!jsonEqual(before.target, after.target)) add('target', describeScope(before.target, names), describeScope(after.target, names));
  add('capture', before.capture ? 'yes' : 'no', after.capture ? 'yes' : 'no');
  add('zero hit', before.zero_hit ?? 'warning', after.zero_hit ?? 'warning');
  add('skipped preconditions', (before.preconditions?.skip ?? []).join(', '), (after.preconditions?.skip ?? []).join(', '));
  const was = new Map(before.steps.map((s) => [s.id, s]));
  const now = new Map(after.steps.map((s) => [s.id, s]));
  for (const [id, s] of now) {
    const old = was.get(id);
    if (!old) out.push({ field: `step ${id}`, from: '—', to: `added: ${s.at} ${stepText(s, names)}` });
    else if (!jsonEqual(old, s)) {
      const line = (x: Step) => `${x.at} ${stepText(x, names)}${x.target ? ` (${narrowText(x, names)})` : ''}`;
      // the words may not show the change (a setting the line leaves out)
      out.push(line(old) === line(s) ? { field: `step ${id}`, from: 'as before', to: 'settings changed' } : { field: `step ${id}`, from: line(old), to: line(s) });
    }
  }
  for (const [id, s] of was) if (!now.has(id)) out.push({ field: `step ${id}`, from: `${s.at} ${stepText(s, names)}`, to: 'deleted' });
  const n = Math.max(before.checks?.length ?? 0, after.checks?.length ?? 0);
  for (let i = 0; i < n; i++) {
    const a = before.checks?.[i];
    const b = after.checks?.[i];
    if (jsonEqual(a, b)) continue;
    const line = (c: Check) => `${checkTypeLabels[checkType(c) ?? 'reconnected']} ${checkText(c, names)} ${windowText(c.window)}${c.name ? ` (${c.name})` : ''}`;
    out.push({ field: `check ${i + 1}`, from: a ? line(a) : '—', to: b ? line(b) : 'deleted' });
  }
  // a change none of the words above shows (the settings of a step the line does not mention)
  if (out.length === 0 && !jsonEqual(before, after)) out.push({ field: 'settings', from: 'as before', to: 'changed' });
  return out;
}
