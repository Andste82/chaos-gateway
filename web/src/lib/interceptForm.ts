// The form of an interception case (plan §2.8, §2.17 "Interception UI", M23c): the settings of the proxy for the case (the key log, QUIC,
// the policy for the servers' certificates) and the list of HTTP rules it applies, in the order written. Each rule has a match (host, path
// prefix, method), an action (block, status, delay, throttle, abort, modify) and the fields of its action; a `modify` rule has headers
// to set or remove on the request and on the response and texts to replace in the body of the response. The interception is the case
// `intercept` of a TLS case, so the form is part of `tlsForm`: the "Add fault" dialog and the step dialog share it. The checks are
// those of the gateway (`internal/domain`: at most 64 rules, a delay up to 10 minutes, a rate of at least 8 bit/s, header names as RFC
// 9110 writes them) and the request it becomes is checked against the spec's schema.
import type { HttpRule, InterceptSettings } from '../api/generated/model';
import {
  createOverlayBodyTlsTwoInterceptHttpRulesItemDelayOneRegExp as delayRegExp,
  createOverlayBodyTlsTwoInterceptHttpRulesItemMatchHostMax as hostMax,
  createOverlayBodyTlsTwoInterceptHttpRulesItemMatchHostRegExp as hostRegExp,
  createOverlayBodyTlsTwoInterceptHttpRulesItemMatchMethodMax as methodMax,
  createOverlayBodyTlsTwoInterceptHttpRulesItemMatchPathPrefixMax as pathMax,
  createOverlayBodyTlsTwoInterceptHttpRulesItemStatusMax as statusMax,
  createOverlayBodyTlsTwoInterceptHttpRulesItemStatusMin as statusMin,
} from '../api/generated/zod/overlays/overlays';
import { durationMs } from './format';
import { parseRate, rateBits } from './netemForm';

export type HttpAction = HttpRule['action'];
export type UpstreamPolicy = NonNullable<InterceptSettings['upstream']>;

/** The most rules a case may have (`internal/domain`: MaxHTTPRules). */
export const maxRules = 64;
/** The longest delay of a rule, in milliseconds (`internal/domain`: MaxHTTPDelay: ten minutes). */
export const maxDelayMs = 10 * 60_000;

/** A header of a `modify` rule: set to a value, or taken away (the API's empty value). */
export interface HeaderRow {
  name: string;
  value: string;
  remove: boolean;
}

export interface ReplaceRow {
  search: string;
  replace: string;
}

export interface HttpRuleForm {
  action: HttpAction;
  /** The match: a host name (exact or `*.suffix`), the start of the path and a method; empty fields select everything. */
  host: string;
  path: string;
  method: string;
  /** The field of the action: a status code, a delay, a rate. */
  status: string;
  delay: string;
  rate: string;
  /** What a `modify` rule changes. */
  requestHeaders: HeaderRow[];
  responseHeaders: HeaderRow[];
  replaces: ReplaceRow[];
}

export interface InterceptForm {
  /** Give the TLS secrets to the captures that run (the API's `keylog`, default on). */
  keylog: boolean;
  /** Drop UDP 443 so that a QUIC client falls back to TCP (the API's `block_quic`, default on). */
  blockQuic: boolean;
  /** `''`: the installation's default. */
  upstream: '' | UpstreamPolicy;
  rules: HttpRuleForm[];
}

export type RuleField = 'host' | 'path' | 'method' | 'status' | 'delay' | 'rate' | 'requestHeaders' | 'responseHeaders' | 'replaces' | 'modify';
export type InterceptField = 'interceptUpstream' | 'interceptRules' | `rule.${number}.${RuleField}`;

/** The key of a rule's field in the errors of the form: `rule.2.status`. */
export const ruleKey = (i: number, field: RuleField): `rule.${number}.${RuleField}` => `rule.${i}.${field}`;

export const httpActions: { value: HttpAction; label: string; hint: string; terminal?: boolean }[] = [
  { value: 'block', label: 'Block', terminal: true, hint: 'Answers 403 with a short text. The request never reaches the server, and no rule after this one acts.' },
  { value: 'status', label: 'Answer with a status code', terminal: true, hint: 'Answers with the status code and an empty body, without the server (500, 503, 429). No rule after this one acts.' },
  { value: 'delay', label: 'Delay the request', hint: 'Holds the request back before it goes on, so the response comes that much later. The rules after it still act. A connection that is no HTTP is held back once.' },
  { value: 'throttle', label: 'Throttle the response', hint: 'Paces the bytes sent to the device at the rate, for the response to the request this rule selects. The rules after it still act.' },
  { value: 'abort', label: 'Abort the connection', terminal: true, hint: 'Resets the connection to the device. No rule after this one acts.' },
  { value: 'modify', label: 'Modify headers or body', hint: 'Sets or removes headers of the request and of the response and replaces text in the body of the response. The rules after it still act.' },
];

export const upstreamPolicies: { value: '' | UpstreamPolicy; label: string; hint: string }[] = [
  { value: '', label: 'The installation\'s default', hint: 'Verify the server\'s certificate, unless the proxy\'s container is set to use its own CA file or to verify nothing.' },
  { value: 'verify', label: 'Verify against the default bundle', hint: 'The server\'s certificate must be valid for its name and chain to a public CA. A server with a private CA or a wrong certificate is refused: the device gets a 502 over the proxy\'s certificate and the exchange is logged with the error "TLS to the server failed".' },
  { value: 'own_ca', label: 'Verify against the CA file of the installation', hint: 'The certificate must chain to the CA file the proxy\'s container was given (a lab\'s private CA), and to no other. Without such a file the connection is refused.' },
  { value: 'insecure', label: 'Do not verify', hint: 'The server\'s certificate is not checked, for a lab with servers whose certificates are wrong on purpose or not yet right. The device is still shown a certificate of the test CA, valid for the name it asked for.' },
];

export function emptyRule(): HttpRuleForm {
  return { action: 'block', host: '', path: '', method: '', status: '503', delay: '1s', rate: '64kbit', requestHeaders: [], responseHeaders: [], replaces: [] };
}

export function emptyIntercept(): InterceptForm {
  return { keylog: true, blockQuic: true, upstream: '', rules: [] };
}

/** The HTTP method names the form offers beside typing one. */
export const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];

// the header names and values the gateway accepts (`internal/domain`: httpHeaderName, httpHeaders)
const headerName = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const httpMethod = /^[A-Za-z][A-Za-z0-9-]*$/;
const lineBreak = /[\r\n\0]/;

/** The rules that a rule above makes unreachable: one that stops a request (block, status, abort) and selects every request. */
export function unreachableRules(rules: HttpRuleForm[]): Set<number> {
  const out = new Set<number>();
  const stop = rules.findIndex((r) => httpActions.find((a) => a.value === r.action)?.terminal && !r.host.trim() && !r.path.trim() && !r.method.trim());
  if (stop >= 0) for (let i = stop + 1; i < rules.length; i++) out.add(i);
  return out;
}

function headerErrors(rows: HeaderRow[], what: string): string | undefined {
  const seen = new Set<string>();
  for (const r of rows) {
    const name = r.name.trim();
    if (!name) return `Name the ${what} header, or remove the row.`;
    if (!headerName.test(name)) return `${name} is no header name: letters, digits and - ! # $ % & ' * + . ^ _ \` | ~ only.`;
    if (!r.remove && lineBreak.test(r.value)) return `The value of ${name} has a line break.`;
    if (!r.remove && r.value === '') return `${name} has no value: give one, or tick Remove to take the header away.`;
    if (seen.has(name.toLowerCase())) return `${name} is listed twice.`;
    seen.add(name.toLowerCase());
  }
  return undefined;
}

/** The problems of one rule, by field. */
export function ruleErrors(r: HttpRuleForm): Partial<Record<RuleField, string>> {
  const e: Partial<Record<RuleField, string>> = {};
  const host = r.host.trim();
  if (host && (host.length > hostMax || !hostRegExp.test(host))) e.host = `${host} is no host name: a name like api.example.com, or *.example.com.`;
  const path = r.path.trim();
  if (path && !path.startsWith('/')) e.path = 'A path starts with /, such as /v1/ (the path of a request, without its query).';
  else if (path.length > pathMax) e.path = `At most ${pathMax} characters.`;
  const method = r.method.trim();
  if (method && (method.length > methodMax || !httpMethod.test(method))) e.method = `${method} is no HTTP method: letters, digits and - only, such as GET.`;
  switch (r.action) {
    case 'status': {
      const s = r.status.trim();
      const n = Number(s);
      if (!/^\d+$/.test(s) || n < statusMin || n > statusMax) e.status = `A status code from ${statusMin} to ${statusMax}, such as 503.`;
      break;
    }
    case 'delay': {
      const d = r.delay.trim();
      const ms = durationMs(d);
      if (!delayRegExp.test(d) || ms === undefined || ms <= 0 || ms > maxDelayMs) e.delay = 'A delay longer than zero and at most 10m, such as 500ms or 2s.';
      break;
    }
    case 'throttle': {
      const rate = parseRate(r.rate);
      if (!rate) e.rate = 'A rate with a unit, such as 64kbit or 2 Mbit/s.';
      else if (rateBits(rate) < 8) e.rate = 'At least 8 bit/s.';
      break;
    }
    case 'modify': {
      const req = headerErrors(r.requestHeaders, 'request');
      const res = headerErrors(r.responseHeaders, 'response');
      if (req) e.requestHeaders = req;
      if (res) e.responseHeaders = res;
      if (r.replaces.some((x) => x.search === '')) e.replaces = 'Give the text to search for, or remove the row.';
      else if (r.replaces.some((x) => lineBreak.test(x.search) || lineBreak.test(x.replace))) e.replaces = 'A line break cannot be typed here.';
      if (!r.requestHeaders.length && !r.responseHeaders.length && !r.replaces.length) e.modify = 'This rule changes nothing: add a header or a text to replace.';
      break;
    }
  }
  return e;
}

/** The problems of the form, by field; empty when the case's settings and rules can be sent. */
export function interceptErrors(f: InterceptForm): Partial<Record<InterceptField, string>> {
  const e: Partial<Record<InterceptField, string>> = {};
  if (f.rules.length > maxRules) e.interceptRules = `At most ${maxRules} rules.`;
  f.rules.forEach((r, i) => {
    for (const [field, message] of Object.entries(ruleErrors(r))) e[ruleKey(i, field as RuleField)] = message;
  });
  return e;
}

const trimmed = (s: string) => s.trim();

function headersBody(rows: HeaderRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) out[trimmed(r.name)] = r.remove ? '' : r.value;
  return out;
}

/** The rule of the form. */
export function ruleBody(r: HttpRuleForm): HttpRule {
  const out: HttpRule = { action: r.action };
  const match: NonNullable<HttpRule['match']> = {};
  if (trimmed(r.host)) match.host = trimmed(r.host);
  if (trimmed(r.path)) match.path_prefix = trimmed(r.path);
  if (trimmed(r.method)) match.method = trimmed(r.method);
  if (Object.keys(match).length) out.match = match;
  switch (r.action) {
    case 'status':
      out.status = Number(r.status.trim());
      break;
    case 'delay':
      out.delay = r.delay.trim();
      break;
    case 'throttle':
      out.rate = parseRate(r.rate) ?? r.rate.trim();
      break;
    case 'modify': {
      const modify: NonNullable<HttpRule['modify']> = {};
      if (r.requestHeaders.length) modify.request_headers = headersBody(r.requestHeaders);
      if (r.responseHeaders.length) modify.response_headers = headersBody(r.responseHeaders);
      if (r.replaces.length) modify.response_body_replace = r.replaces.map((x) => ({ search: x.search, replace: x.replace }));
      out.modify = modify;
      break;
    }
  }
  return out;
}

/**
 * The `intercept` part of the case: only what differs from the defaults (the key log and QUIC blocking are on unless said otherwise), and
 * nothing at all for a case that is only watching with them.
 */
export function interceptBody(f: InterceptForm): InterceptSettings | undefined {
  const out: InterceptSettings = {};
  if (!f.keylog) out.keylog = false;
  if (!f.blockQuic) out.block_quic = false;
  if (f.upstream) out.upstream = f.upstream;
  if (f.rules.length) out.http_rules = f.rules.map(ruleBody);
  return Object.keys(out).length ? out : undefined;
}

function headerRows(map: Record<string, string> | undefined): HeaderRow[] {
  return Object.entries(map ?? {}).map(([name, value]) => ({ name, value, remove: value === '' }));
}

/** The rule of the form, from a rule of the API. */
export function ruleToForm(r: HttpRule): HttpRuleForm {
  const f = emptyRule();
  f.action = r.action;
  f.host = r.match?.host ?? '';
  f.path = r.match?.path_prefix ?? '';
  f.method = r.match?.method ?? '';
  if (r.status !== undefined) f.status = String(r.status);
  if (r.delay !== undefined) f.delay = r.delay;
  if (r.rate !== undefined) f.rate = r.rate;
  f.requestHeaders = headerRows(r.modify?.request_headers);
  f.responseHeaders = headerRows(r.modify?.response_headers);
  f.replaces = (r.modify?.response_body_replace ?? []).map((x) => ({ search: x.search, replace: x.replace }));
  return f;
}

/** The form of the settings and rules of a case. */
export function interceptToForm(s: InterceptSettings | undefined): InterceptForm {
  const f = emptyIntercept();
  if (!s) return f;
  f.keylog = s.keylog !== false;
  f.blockQuic = s.block_quic !== false;
  f.upstream = s.upstream ?? '';
  f.rules = (s.http_rules ?? []).map(ruleToForm);
  return f;
}

/** What a rule selects, in words: "api.example.com /v1/ POST", "every request". */
export function matchText(r: Pick<HttpRuleForm, 'host' | 'path' | 'method'>): string {
  const parts = [trimmed(r.method).toUpperCase(), trimmed(r.host), trimmed(r.path)].filter(Boolean);
  return parts.length ? parts.join(' ') : 'every request';
}

/** What a rule does, in words: "answer 503", "delay 250ms", "throttle 64kbit", "change 2 headers and 1 text". */
export function actionText(r: HttpRuleForm): string {
  switch (r.action) {
    case 'block':
      return 'block (403)';
    case 'status':
      return `answer ${r.status.trim()}`;
    case 'delay':
      return `delay ${r.delay.trim()}`;
    case 'throttle':
      return `throttle ${(parseRate(r.rate) ?? r.rate.trim()).replace(/bit$/, 'bit/s')}`;
    case 'abort':
      return 'abort the connection';
    case 'modify': {
      const headers = r.requestHeaders.length + r.responseHeaders.length;
      const parts = [];
      if (headers) parts.push(`${headers} header${headers === 1 ? '' : 's'}`);
      if (r.replaces.length) parts.push(`${r.replaces.length} text${r.replaces.length === 1 ? '' : 's'} in the body`);
      return `change ${parts.join(' and ') || 'nothing'}`;
    }
  }
}

/** The one line of a rule: "api.example.com /v1/ → answer 503". */
export function ruleSummary(r: HttpRuleForm): string {
  return `${matchText(r)} → ${actionText(r)}`;
}

/** The field of the form a problem the gateway reported names, by its pointer below `intercept` ("upstream", "http_rules/2/status", "http_rules/0/modify/request_headers/X-A"). */
export function interceptFieldOf(segments: string[]): InterceptField | undefined {
  switch (segments[0]) {
    case 'upstream':
      return 'interceptUpstream';
    case 'http_rules': {
      if (segments[1] === undefined) return 'interceptRules';
      const i = Number(segments[1]);
      if (!Number.isInteger(i)) return 'interceptRules';
      const rest = segments.slice(2);
      switch (rest[0]) {
        case 'match':
          return rest[1] === 'host' ? ruleKey(i, 'host') : rest[1] === 'path_prefix' ? ruleKey(i, 'path') : rest[1] === 'method' ? ruleKey(i, 'method') : ruleKey(i, 'host');
        case 'status':
          return ruleKey(i, 'status');
        case 'delay':
          return ruleKey(i, 'delay');
        case 'rate':
          return ruleKey(i, 'rate');
        case 'modify': {
          if (rest[1] === 'request_headers') return ruleKey(i, 'requestHeaders');
          if (rest[1] === 'response_headers') return ruleKey(i, 'responseHeaders');
          if (rest[1] === 'response_body_replace') return ruleKey(i, 'replaces');
          return ruleKey(i, 'modify');
        }
        default:
          // the rule as a whole: the gateway names the field of its action ("the action status needs status"), so this is no field
          return undefined;
      }
    }
    default:
      return undefined;
  }
}
