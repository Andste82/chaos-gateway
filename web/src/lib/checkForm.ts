// The check editor (plan §2.10, §2.17 "Scenarios"; M19): the form of one check of a scenario — its type, the
// selector or name it observes, the destinations a "traffic not seen" check allows, and the window tied to a
// step — the checks under the fields, the check it becomes and the field a problem the gateway reported names.
import type { Check, Destination } from '../api/generated/model';
import { createOverlayBodyFaultOneDestinationHostnameOneRegExp as hostnameRegExp } from '../api/generated/zod/overlays/overlays';
import { createRunBodyTwoInlineOneStepsItemAtRegExp as durationRegExp } from '../api/generated/zod/runs/runs';
import { checkType, type CheckType } from './scenarios';
import {
  destinationOf,
  emptyTraffic,
  trafficErrors,
  trafficFieldOf,
  trafficFromDestination,
  trafficFromMatch,
  trafficMatch,
  type DestKind,
  type TrafficForm,
} from './trafficForm';
import { createOverlayBodyFaultOneDestinationCidrRegExp as cidrRegExp } from '../api/generated/zod/overlays/overlays';

export interface ExceptRow {
  destKind: Exclude<DestKind, 'any'>;
  destination: string;
}

export interface CheckForm {
  name: string;
  type: CheckType;
  traffic: TrafficForm;
  /** The name a DNS query check looks for. */
  dnsName: string;
  /** The destinations a "traffic not seen" check allows. */
  except: ExceptRow[];
  /** `start`, or the id of the step whose application opens the window. */
  from: string;
  windowKind: 'within' | 'until';
  within: string;
  /** The id of the step whose application closes the window. */
  until: string;
}

export type CheckField = 'name' | 'type' | 'ports' | 'destination' | 'dnsName' | 'except' | 'from' | 'window' | 'within' | 'until';

export function emptyCheckForm(type: CheckType = 'reconnected'): CheckForm {
  return { name: '', type, traffic: emptyTraffic(), dnsName: '', except: [], from: 'start', windowKind: 'within', within: '30s', until: '' };
}

const matchTypes = new Set<CheckType>(['reconnected', 'no_connection', 'traffic_seen', 'traffic_not_seen', 'tls_rejected', 'tls_accepted']);

/** True for the check types that observe a selector of traffic. */
export const observesTraffic = (t: CheckType): boolean => matchTypes.has(t);

/** The form of a check. */
export function checkToForm(c: Check): CheckForm {
  const type = checkType(c) ?? 'reconnected';
  const f = emptyCheckForm(type);
  f.name = c.name ?? '';
  f.from = c.window.from;
  f.windowKind = c.window.until !== undefined ? 'until' : 'within';
  f.within = c.window.within ?? '30s';
  f.until = c.window.until ?? '';
  if (type === 'dns_query_seen') f.dnsName = c.dns_query_seen?.name ?? '';
  else if (type === 'traffic_not_seen') {
    f.traffic = trafficFromMatch(c.traffic_not_seen);
    f.except = (c.traffic_not_seen?.except ?? []).map((d) => ({ ...trafficFromDestination(d) }) as ExceptRow);
  } else if (c[type]) f.traffic = trafficFromMatch(c[type]);
  return f;
}

/** The check of the form. */
export function formToCheck(f: CheckForm): Check {
  const window = f.windowKind === 'until' ? { from: f.from, until: f.until } : { from: f.from, within: f.within.trim() };
  const out: Check = { ...(f.name.trim() ? { name: f.name.trim() } : {}), window };
  switch (f.type) {
    case 'dns_query_seen':
      out.dns_query_seen = { name: f.dnsName.trim() };
      break;
    case 'traffic_not_seen': {
      const except = f.except.map((r) => destinationOf(r)).filter((d): d is Destination => d !== undefined);
      out.traffic_not_seen = { ...trafficMatch(f.traffic), ...(except.length ? { except } : {}) };
      break;
    }
    default:
      out[f.type] = trafficMatch(f.traffic);
  }
  return out;
}

export interface CheckContext {
  /** The ids of the steps, for the window. */
  steps: string[];
}

/** The problems of the form, by field; empty when the check can go into the scenario. */
export function checkFormErrors(f: CheckForm, ctx: CheckContext): Partial<Record<CheckField, string>> {
  const e: Partial<Record<CheckField, string>> = {};
  if (f.name.length > 100) e.name = 'At most 100 characters.';
  if (observesTraffic(f.type)) Object.assign(e, trafficErrors(f.traffic));
  if (f.type === 'dns_query_seen' && !hostnameRegExp.test(f.dnsName.trim())) e.dnsName = 'A host name like broker.example.com.';
  if (f.type === 'traffic_not_seen' && f.except.some((r) => (r.destKind === 'cidr' ? !cidrRegExp.test(r.destination.trim()) : r.destKind === 'hostname' ? !hostnameRegExp.test(r.destination.trim()) : r.destKind === 'network' ? !r.destination : false))) {
    e.except = 'Each allowed destination needs a network, an address or prefix like 203.0.113.0/24, or a host name.';
  }
  if (f.from !== 'start' && !ctx.steps.includes(f.from)) e.from = 'Choose the step that opens the window, or the start.';
  if (f.windowKind === 'within') {
    if (!durationRegExp.test(f.within.trim())) e.within = 'A time like 30s or 2m.';
  } else if (!ctx.steps.includes(f.until)) e.until = 'Choose the step that closes the window.';
  return e;
}

/** The field of the form a problem the gateway reported for the check names, by the pointer below the check. */
export function checkFieldOf(rest: string): CheckField | undefined {
  const segments = rest.split('/').filter(Boolean);
  if (segments[0] === 'name') return 'name';
  if (segments[0] === 'window') return segments[1] === 'from' ? 'from' : segments[1] === 'within' ? 'within' : segments[1] === 'until' ? 'until' : 'window';
  if (segments[0] === 'dns_query_seen') return 'dnsName';
  if (segments[1] === 'except') return 'except';
  return trafficFieldOf(`/${segments.slice(1).join('/')}`);
}
