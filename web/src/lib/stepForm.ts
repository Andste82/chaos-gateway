// The step editor of the timeline (plan §2.10, §2.17 "Scenarios"; M19): the form of one step — its id, its
// time on the timeline, whether it narrows the scenario's target, and the fields of its type (a profile, a
// fault with the fault dialog's fields, a DNS fault, a TLS case, a DHCP action (M23b: the fields of the fault dialog
// and of the DHCP dialog), an access rule, a WireGuard action, a capture start or stop, a wait,
// the removal of an earlier step's overlay, or the restore) — the checks under the fields, the step it becomes
// and the field a problem the gateway reported names. A step of a type this build cannot edit (none is left; a
// type a later build adds) is kept as it is and edited in the YAML view.
import type { AccessAction, RejectType, Scope, Step, WaitCondition, WireGuardAction } from '../api/generated/model';
import {
  createRunBodyTwoInlineOneStepsItemIdRegExp as stepIdRegExp,
  createRunBodyTwoInlineOneStepsItemAtRegExp as durationRegExp,
} from '../api/generated/zod/runs/runs';
import { dhcpErrors, dhcpFieldOf, dhcpBody, dhcpToForm, emptyDhcp, type DhcpField, type DhcpForm } from './dhcpForm';
import { dnsBody, dnsErrors, dnsFieldOf, dnsToForm, emptyDns, type DnsField, type DnsForm } from './dnsForm';
import { emptyForm, faultFormErrors, faultToForm, fieldOf as faultFieldOf, formToFault, scopeOf, targetOf, tunnelOf, type FaultField, type FaultForm } from './faultForm';
import { durationMs } from './format';
import { resetError } from './rules';
import { remoteCidrError } from './scopes';
import { stepType, type StepType } from './scenarios';
import { createsOverlay, dhcpTargetProblem, messages, reservedStepId, scopeWithin, startsCapture, tunnelOutsideTarget, type Topology } from './scenarioRules';
import { emptyTls, tlsBody, tlsErrors, tlsFieldOf, tlsToForm, type TlsField, type TlsForm } from './tlsForm';
import { emptyTraffic, trafficErrors, trafficFieldOf, trafficFromMatch, trafficMatch, type TrafficForm } from './trafficForm';
import { createOverlayBodyFaultOneDestinationHostnameOneRegExp as hostnameRegExp } from '../api/generated/zod/overlays/overlays';

export type WireGuardWho = string;
export type WaitFor = WaitCondition['for'];

export interface StepForm {
  id: string;
  /** The offset on the timeline: a duration like `10s`. */
  at: string;
  /** `''`: the scenario's target; else a scope text of `faultForm.targetOf` that narrows it. */
  narrow: string;
  type: StepType;
  /** The profile (its id, or the name of a built-in one). */
  profile: string;
  fault: FaultForm;
  /** The fields of a `dns` step, a `tls` step and a `dhcp` step. */
  dns: DnsForm;
  tls: TlsForm;
  dhcp: DhcpForm;
  action: AccessAction;
  rejectWith: RejectType | '';
  cutExisting: boolean;
  /** The traffic a rule selects or a wait for a connection waits for. */
  traffic: TrafficForm;
  /** `client:<id>` or `link:<id>`. */
  who: WireGuardWho;
  wgAction: WireGuardAction['action'];
  capture: 'start' | 'stop';
  waitFor: WaitFor;
  timeout: string;
  /** The name a wait for a DNS query waits for. */
  name: string;
  /** The step whose overlay is removed. */
  remove: string;
  /** A step the editor does not make (a type a later build adds): kept as it is. */
  kept?: Step;
}

export type StepField =
  | 'id'
  | 'at'
  | 'narrow'
  | 'type'
  | 'profile'
  | FaultField
  | DnsField
  | TlsField
  | DhcpField
  | 'action'
  | 'rejectWith'
  | 'cutExisting'
  | 'ports'
  | 'destination'
  | 'who'
  | 'wgAction'
  | 'capture'
  | 'waitFor'
  | 'timeout'
  | 'name'
  | 'remove';

export function emptyStepForm(type: StepType, id: string, at = '0s'): StepForm {
  const fault = emptyForm('global');
  fault.latency = '200';
  fault.jitter = '50';
  return {
    id,
    at,
    narrow: '',
    type,
    profile: '',
    fault,
    dns: emptyDns(),
    tls: emptyTls(),
    dhcp: emptyDhcp(),
    action: 'drop',
    rejectWith: '',
    cutExisting: false,
    traffic: emptyTraffic(),
    who: '',
    wgAction: 'disable',
    capture: 'start',
    waitFor: 'online',
    timeout: '60s',
    name: '',
    remove: '',
  };
}

/** The step types the editor makes; a type it does not (none of the types of this build) is kept as it is and edited as YAML. */
export const editableTypes: ReadonlySet<StepType> = new Set(['profile', 'fault', 'rule', 'dns', 'tls', 'dhcp', 'wireguard', 'capture', 'wait', 'remove', 'restore']);

/** The form of a step. */
export function stepToForm(s: Step): StepForm {
  const type = stepType(s) ?? 'restore';
  const f = emptyStepForm(type, s.id, s.at);
  f.narrow = s.target ? targetOf(s.target) : '';
  if (!editableTypes.has(type)) f.kept = s;
  if (s.profile !== undefined) f.profile = s.profile;
  if (s.fault) f.fault = faultToForm(s.fault, undefined);
  if (s.dns) f.dns = dnsToForm(s.dns);
  if (s.tls) f.tls = tlsToForm(s.tls);
  if (s.dhcp) f.dhcp = dhcpToForm(s.dhcp);
  if (s.rule) {
    f.action = s.rule.action;
    f.rejectWith = s.rule.reject_with ?? '';
    f.cutExisting = s.rule.cut_existing === true;
    f.traffic = trafficFromMatch(s.rule);
  }
  if (s.wireguard) {
    f.who = s.wireguard.client ? `client:${s.wireguard.client}` : s.wireguard.link ? `link:${s.wireguard.link}` : '';
    f.wgAction = s.wireguard.action;
  }
  if (s.capture) f.capture = s.capture;
  if (s.wait) {
    f.waitFor = s.wait.for;
    f.timeout = s.wait.timeout;
    f.name = s.wait.name ?? '';
    f.traffic = trafficFromMatch(s.wait);
  }
  if (s.remove !== undefined) f.remove = s.remove;
  return f;
}

/** The scope a step narrows to; a tunnel fault and a WireGuard action have none (they act on the tunnel). */
function narrowOf(f: StepForm): Scope | undefined {
  if (!f.narrow || f.type === 'wireguard' || (f.type === 'fault' && f.fault.family === 'tunnel')) return undefined;
  return scopeOf(f.narrow);
}

/** The step of the form. */
export function formToStep(f: StepForm): Step {
  if (f.kept) return { ...f.kept, id: f.id.trim(), at: f.at.trim(), ...(narrowOf(f) ? { target: narrowOf(f)! } : {}) };
  const step: Step = { id: f.id.trim(), at: f.at.trim() };
  const target = narrowOf(f);
  if (target) step.target = target;
  switch (f.type) {
    case 'profile':
      step.profile = f.profile;
      break;
    case 'fault':
      step.fault = formToFault(f.fault);
      break;
    case 'dns':
      step.dns = dnsBody(f.dns);
      break;
    case 'tls':
      step.tls = tlsBody(f.tls);
      break;
    case 'dhcp':
      step.dhcp = dhcpBody(f.dhcp);
      break;
    case 'rule':
      step.rule = {
        action: f.action,
        ...trafficMatch(f.traffic),
        ...(f.action === 'reject' && f.rejectWith ? { reject_with: f.rejectWith } : {}),
        ...(f.cutExisting ? { cut_existing: true } : {}),
      };
      break;
    case 'wireguard': {
      const who = tunnelOf(f.who);
      step.wireguard = { ...(who ?? {}), action: f.wgAction } as WireGuardAction;
      break;
    }
    case 'capture':
      step.capture = f.capture;
      break;
    case 'wait':
      step.wait = {
        for: f.waitFor,
        timeout: f.timeout.trim(),
        ...(f.waitFor === 'connected' ? trafficMatch(f.traffic) : {}),
        ...(f.waitFor === 'dns_query' ? { name: f.name.trim() } : {}),
      };
      break;
    case 'remove':
      step.remove = f.remove;
      break;
    case 'restore':
      step.restore = true;
      break;
  }
  return step;
}

export interface StepContext {
  /** The ids of the other steps (the step edited is not among them). */
  others: string[];
  /** The ids of the steps that come before this one on the timeline: a `remove` names one of them. */
  earlier: string[];
  /**
   * For the rules that need the rest of the scenario (P4-AUDIT-36): its steps (the one edited among them), its target
   * and what is known of the configuration. Without them those rules are not checked here; the gateway checks them.
   */
  steps?: Step[];
  target?: Scope;
  topology?: Topology;
}

/** The problems of the form, by field; empty when the step can go into the scenario. */
export function stepFormErrors(f: StepForm, ctx: StepContext): Partial<Record<StepField, string>> {
  const e: Partial<Record<StepField, string>> = {};
  const id = f.id.trim();
  if (!id) e.id = 'Give the step an id.';
  else if (!stepIdRegExp.test(id)) e.id = 'Letters, digits, dash and underscore; starting with a lower-case letter; at most 32.';
  else if (id === reservedStepId) e.id = messages.reservedId;
  else if (ctx.others.includes(id)) e.id = 'Another step has this id.';
  if (!durationRegExp.test(f.at.trim())) e.at = 'A time like 0s, 10s or 1m30s.';
  if (f.narrow) {
    const remote = remoteCidrError(f.narrow);
    if (remote) e.narrow = remote;
    // a step may narrow the scenario's target, never widen it (`target_widened`)
    else if (ctx.target && !f.kept && narrowOf(f) && !scopeWithin(narrowOf(f)!, ctx.target, ctx.topology)) e.narrow = messages.widened;
  }
  if (f.kept) return e;
  // the scope the step acts on, for the rules about the target
  const acts = narrowOf(f) ?? ctx.target;
  switch (f.type) {
    case 'profile':
      if (!f.profile) e.profile = 'Choose a profile.';
      break;
    case 'fault':
      Object.assign(e, faultFormErrors(f.fault, 'config'));
      // the target and the name belong to the step, not to the fault
      delete e.target;
      delete e.name;
      if (f.fault.family === 'tunnel' && !e.tunnel && acts && tunnelOutsideTarget(tunnelOf(f.fault.tunnel), acts, ctx.topology)) e.tunnel = messages.tunnel;
      break;
    case 'dns':
      Object.assign(e, dnsErrors(f.dns));
      break;
    case 'tls':
      Object.assign(e, tlsErrors(f.tls));
      break;
    case 'dhcp': {
      Object.assign(e, dhcpErrors(f.dhcp));
      // a step acts on a device or a network (a force of a new IP on a device); where it acts is the step's narrowing or the scenario's target
      const problem = acts ? dhcpTargetProblem(f.dhcp.action, acts) : undefined;
      if (problem) e[acts?.network && !acts.device ? 'dhcpAction' : 'narrow'] = problem;
      break;
    }
    case 'rule': {
      Object.assign(e, trafficErrors(f.traffic));
      const reset = resetError(f.action, f.traffic.protocol);
      if (reset) e.action = reset;
      if (f.cutExisting && f.action === 'allow') e.cutExisting = 'An allow rule cuts nothing.';
      else if (f.cutExisting && f.traffic.protocol !== 'tcp' && f.traffic.protocol !== 'any') e.cutExisting = 'Cutting connections needs the protocol TCP or any.';
      break;
    }
    case 'wireguard':
      if (!tunnelOf(f.who)) e.who = 'Choose the WireGuard client or link.';
      else if (acts && tunnelOutsideTarget(tunnelOf(f.who), acts, ctx.topology)) e.who = messages.tunnel;
      break;
    case 'capture':
      if (f.capture === 'stop' && ctx.steps && ctx.target) {
        const before = ctx.steps.filter((s) => ctx.earlier.includes(s.id));
        if (!startsCapture(before, ctx.target, acts!)) e.capture = messages.captureStop;
      }
      break;
    case 'wait':
      if (!durationRegExp.test(f.timeout.trim())) e.timeout = 'A time like 30s or 2m.';
      else if (durationMs(f.timeout.trim()) === 0) e.timeout = messages.zeroTimeout;
      if (f.waitFor === 'connected') Object.assign(e, trafficErrors(f.traffic));
      if (f.waitFor === 'dns_query' && !hostnameRegExp.test(f.name.trim())) e.name = 'A host name like broker.example.com.';
      break;
    case 'remove':
      if (!f.remove) e.remove = 'Choose the step whose overlay is removed.';
      else if (!ctx.earlier.includes(f.remove)) e.remove = 'Choose a step that comes before this one.';
      else if (ctx.steps?.some((s) => s.id === f.remove && !createsOverlay(s))) e.remove = messages.noOverlay(f.remove);
      break;
  }
  return e;
}

/**
 * The field of the form a problem the gateway reported for the step names, by the pointer below the step
 * ("at", "fault/jitter", "rule/ports", "wait/timeout", "target/device"); undefined for the step as a whole.
 */
export function stepFieldOf(rest: string, each = false): StepField | undefined {
  const segments = rest.split('/').filter(Boolean);
  switch (segments[0]) {
    case 'id':
    case 'at':
      return segments[0];
    case 'target':
      return 'narrow';
    case 'profile':
      return 'profile';
    case 'fault':
      return faultFieldOf(`/${segments.slice(1).join('/')}`, each);
    case 'dns':
      return dnsFieldOf(`/${segments.slice(1).join('/')}`);
    case 'tls':
      return tlsFieldOf(`/${segments.slice(1).join('/')}`);
    case 'dhcp':
      return segments.length === 1 ? 'narrow' : dhcpFieldOf(`/${segments.slice(1).join('/')}`);
    case 'rule': {
      const last = segments[1];
      if (last === 'action') return 'action';
      if (last === 'reject_with') return 'rejectWith';
      if (last === 'cut_existing') return 'cutExisting';
      return trafficFieldOf(`/${segments.slice(1).join('/')}`);
    }
    case 'wireguard':
      return segments[1] === 'action' ? 'wgAction' : 'who';
    case 'capture':
      return 'capture';
    case 'wait': {
      if (segments[1] === 'timeout') return 'timeout';
      if (segments[1] === 'name') return 'name';
      if (segments[1] === 'for') return 'waitFor';
      return trafficFieldOf(`/${segments.slice(1).join('/')}`);
    }
    case 'remove':
      return 'remove';
    default:
      return undefined;
  }
}
