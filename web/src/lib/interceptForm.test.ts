import { describe, expect, it } from 'vitest';
import type { InterceptSettings } from '../api/generated/model';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import {
  actionText,
  emptyIntercept,
  emptyRule,
  httpActions,
  interceptBody,
  interceptErrors,
  interceptFieldOf,
  interceptToForm,
  matchText,
  maxRules,
  ruleBody,
  ruleErrors,
  ruleSummary,
  ruleToForm,
  unreachableRules,
  type HttpRuleForm,
} from './interceptForm';

// The form of an interception case (M23c): the settings and the HTTP rules, the checks the gateway makes, the settings and rules
// they become (checked against the spec) and the field a message of the gateway belongs to.

const rule = (over: Partial<HttpRuleForm>): HttpRuleForm => ({ ...emptyRule(), ...over });
const spec = (intercept: unknown) => CreateOverlayBody.safeParse({ target: { device: 'esp32-42' }, tls: { case: 'intercept', intercept } }).success;

describe('the interception settings', () => {
  it('send only what differs from the defaults, and nothing for a case that only watches', () => {
    expect(interceptBody(emptyIntercept())).toBeUndefined();
    expect(interceptBody({ ...emptyIntercept(), keylog: false })).toEqual({ keylog: false });
    expect(interceptBody({ ...emptyIntercept(), blockQuic: false })).toEqual({ block_quic: false });
    expect(interceptBody({ ...emptyIntercept(), upstream: 'own_ca' })).toEqual({ upstream: 'own_ca' });
    expect(interceptBody({ ...emptyIntercept(), keylog: false, blockQuic: false, upstream: 'insecure' })).toEqual({ keylog: false, block_quic: false, upstream: 'insecure' });
  });

  it('read back into the form: an omitted field is its default, and the policy of the installation is empty', () => {
    expect(interceptToForm(undefined)).toEqual(emptyIntercept());
    expect(interceptToForm({})).toEqual(emptyIntercept());
    expect(interceptToForm({ keylog: true, block_quic: true })).toEqual(emptyIntercept());
    expect(interceptToForm({ keylog: false, block_quic: false, upstream: 'verify' })).toEqual({ keylog: false, blockQuic: false, upstream: 'verify', rules: [] });
  });
});

describe('an HTTP rule', () => {
  it('becomes the rule of each action, with its match only when the match says something', () => {
    expect(ruleBody(rule({ action: 'block' }))).toEqual({ action: 'block' });
    expect(ruleBody(rule({ action: 'abort', host: ' api.example.com ' }))).toEqual({ action: 'abort', match: { host: 'api.example.com' } });
    expect(ruleBody(rule({ action: 'status', status: '503', path: '/v1/', method: 'GET' }))).toEqual({ action: 'status', status: 503, match: { path_prefix: '/v1/', method: 'GET' } });
    expect(ruleBody(rule({ action: 'delay', delay: '250ms' }))).toEqual({ action: 'delay', delay: '250ms' });
    // the rate is written the way the API writes it
    expect(ruleBody(rule({ action: 'throttle', rate: '64 kbit/s' }))).toEqual({ action: 'throttle', rate: '64kbit' });
    expect(ruleBody(rule({ action: 'throttle', rate: '2 mbit' }))).toEqual({ action: 'throttle', rate: '2Mbit' });
    // a field of another action is not sent
    expect(ruleBody(rule({ action: 'block', status: '503', delay: '9s', rate: '1bit' }))).toEqual({ action: 'block' });
  });

  it('makes a modify rule of its headers and texts: a header taken away is the empty value', () => {
    const r = rule({
      action: 'modify',
      requestHeaders: [{ name: 'X-Chaos', value: 'on', remove: false }, { name: ' Cookie ', value: 'ignored', remove: true }],
      responseHeaders: [{ name: 'Cache-Control', value: 'no-store', remove: false }],
      replaces: [{ search: 'ok', replace: 'fail' }, { search: 'x', replace: '' }],
    });
    const body = ruleBody(r);
    expect(body).toEqual({
      action: 'modify',
      modify: {
        request_headers: { 'X-Chaos': 'on', Cookie: '' },
        response_headers: { 'Cache-Control': 'no-store' },
        response_body_replace: [{ search: 'ok', replace: 'fail' }, { search: 'x', replace: '' }],
      },
    });
    expect(spec({ http_rules: [body] })).toBe(true);
    // and back
    expect(ruleBody(ruleToForm(body))).toEqual(body);
    expect(ruleToForm(body).requestHeaders).toEqual([{ name: 'X-Chaos', value: 'on', remove: false }, { name: 'Cookie', value: '', remove: true }]);
  });

  it('is read back and sent unchanged, each action and the match', () => {
    const settings: InterceptSettings = {
      keylog: false,
      upstream: 'own_ca',
      http_rules: [
        { action: 'block', match: { host: '*.example.com' } },
        { action: 'status', status: 429, match: { path_prefix: '/v1', method: 'POST' } },
        { action: 'delay', delay: '1.5s' },
        { action: 'throttle', rate: '500kbit', match: { host: 'cdn.example.net' } },
        { action: 'abort' },
        { action: 'modify', modify: { response_headers: { Server: '' } } },
      ],
    };
    expect(interceptBody(interceptToForm(settings))).toEqual(settings);
    expect(spec(settings)).toBe(true);
  });

  it('is checked as the gateway checks it', () => {
    expect(ruleErrors(rule({ action: 'block' }))).toEqual({});
    expect(ruleErrors(rule({ host: 'not a name!' })).host).toMatch(/is no host name/);
    expect(ruleErrors(rule({ host: '*.example.com' })).host).toBeUndefined();
    expect(ruleErrors(rule({ path: 'v1' })).path).toMatch(/starts with \//);
    expect(ruleErrors(rule({ path: '/' + 'a'.repeat(512) })).path).toMatch(/At most 512/);
    expect(ruleErrors(rule({ method: 'GET POST' })).method).toMatch(/is no HTTP method/);
    expect(ruleErrors(rule({ method: 'PROPFIND' })).method).toBeUndefined();
    for (const status of ['', 'x', '99', '600', '50.5', '-503']) expect(ruleErrors(rule({ action: 'status', status })).status, status).toMatch(/status code from 100 to 599/);
    for (const status of ['100', '503', '599']) expect(ruleErrors(rule({ action: 'status', status })).status, status).toBeUndefined();
    for (const delay of ['', '0s', '11m', 'soon', '-1s']) expect(ruleErrors(rule({ action: 'delay', delay })).delay, delay).toMatch(/longer than zero and at most 10m/);
    for (const delay of ['1ms', '2s', '10m', '1m30s']) expect(ruleErrors(rule({ action: 'delay', delay })).delay, delay).toBeUndefined();
    for (const rate of ['', 'fast', '64']) expect(ruleErrors(rule({ action: 'throttle', rate })).rate, rate).toMatch(/rate with a unit/);
    expect(ruleErrors(rule({ action: 'throttle', rate: '7bit' })).rate).toBe('At least 8 bit/s.');
    for (const rate of ['8bit', '64kbit', '2 Mbit/s']) expect(ruleErrors(rule({ action: 'throttle', rate })).rate, rate).toBeUndefined();
    // only the field of the action counts: a stale status of a block rule is not an error
    expect(ruleErrors(rule({ action: 'block', status: 'x', delay: 'x', rate: 'x' }))).toEqual({});
  });

  it('checks what a modify rule changes', () => {
    const header = (name: string, value = 'v', remove = false) => ({ name, value, remove });
    expect(ruleErrors(rule({ action: 'modify' })).modify).toMatch(/changes nothing/);
    expect(ruleErrors(rule({ action: 'modify', requestHeaders: [header('')] })).requestHeaders).toMatch(/Name the request header/);
    expect(ruleErrors(rule({ action: 'modify', responseHeaders: [header('Bad Name')] })).responseHeaders).toMatch(/is no header name/);
    expect(ruleErrors(rule({ action: 'modify', responseHeaders: [header('X-A', 'line\nbreak')] })).responseHeaders).toMatch(/line break/);
    expect(ruleErrors(rule({ action: 'modify', requestHeaders: [header('X-A', '')] })).requestHeaders).toMatch(/no value/);
    expect(ruleErrors(rule({ action: 'modify', requestHeaders: [header('X-A'), header('x-a', 'w')] })).requestHeaders).toBe('x-a is listed twice.');
    // the same header on the request and on the response are two headers
    expect(ruleErrors(rule({ action: 'modify', requestHeaders: [header('X-A')], responseHeaders: [header('X-A')] }))).toEqual({});
    // a header that is taken away needs no value
    expect(ruleErrors(rule({ action: 'modify', requestHeaders: [header('Cookie', '', true)] }))).toEqual({});
    expect(ruleErrors(rule({ action: 'modify', replaces: [{ search: '', replace: 'x' }] })).replaces).toMatch(/text to search for/);
    expect(ruleErrors(rule({ action: 'modify', replaces: [{ search: 'a\nb', replace: 'x' }] })).replaces).toMatch(/line break/);
    expect(ruleErrors(rule({ action: 'modify', replaces: [{ search: 'a', replace: '' }] }))).toEqual({});
  });
});

describe('the list of rules', () => {
  it('has at most 64 rules, and a problem is under the rule that has it', () => {
    const f = emptyIntercept();
    f.rules = Array.from({ length: maxRules }, () => rule({}));
    expect(interceptErrors(f)).toEqual({});
    f.rules.push(rule({ action: 'status', status: '700' }));
    expect(interceptErrors(f)).toEqual({ interceptRules: 'At most 64 rules.', 'rule.64.status': expect.stringMatching(/status code/) });
  });

  it('knows the rules that never act: those after one that ends the rules for every request', () => {
    expect([...unreachableRules([rule({ action: 'delay' }), rule({ action: 'block' }), rule({ action: 'status' }), rule({ action: 'abort' })])]).toEqual([2, 3]);
    // a terminal rule that selects only some requests ends nothing for the others
    expect(unreachableRules([rule({ action: 'block', host: 'a.example.com' }), rule({ action: 'delay' })]).size).toBe(0);
    expect(unreachableRules([rule({ action: 'block', method: 'POST' }), rule({ action: 'delay' })]).size).toBe(0);
    // a rule that goes on does not end them
    expect(unreachableRules([rule({ action: 'delay' }), rule({ action: 'modify' }), rule({ action: 'throttle' })]).size).toBe(0);
    expect(httpActions.filter((a) => a.terminal).map((a) => a.value)).toEqual(['block', 'status', 'abort']);
  });

  it('says in words what a rule selects and does', () => {
    expect(matchText(rule({}))).toBe('every request');
    expect(matchText(rule({ host: 'api.example.com', path: '/v1/', method: 'post' }))).toBe('POST api.example.com /v1/');
    expect(actionText(rule({ action: 'block' }))).toBe('block (403)');
    expect(actionText(rule({ action: 'status', status: ' 503 ' }))).toBe('answer 503');
    expect(actionText(rule({ action: 'delay', delay: '250ms' }))).toBe('delay 250ms');
    expect(actionText(rule({ action: 'throttle', rate: '64 kbit' }))).toBe('throttle 64kbit/s');
    expect(actionText(rule({ action: 'abort' }))).toBe('abort the connection');
    expect(actionText(rule({ action: 'modify' }))).toBe('change nothing');
    expect(
      actionText(rule({ action: 'modify', requestHeaders: [{ name: 'A', value: 'b', remove: false }], responseHeaders: [{ name: 'C', value: '', remove: true }], replaces: [{ search: 'x', replace: 'y' }] })),
    ).toBe('change 2 headers and 1 text in the body');
    expect(ruleSummary(rule({ action: 'status', status: '503', host: 'api.example.com' }))).toBe('api.example.com → answer 503');
  });
});

describe('a problem of the gateway', () => {
  it('belongs to the field it names', () => {
    const at = (path: string) => interceptFieldOf(path.split('/').filter(Boolean));
    expect(at('/upstream')).toBe('interceptUpstream');
    expect(at('/http_rules')).toBe('interceptRules');
    expect(at('/http_rules/3/status')).toBe('rule.3.status');
    expect(at('/http_rules/0/delay')).toBe('rule.0.delay');
    expect(at('/http_rules/1/rate')).toBe('rule.1.rate');
    expect(at('/http_rules/2/match/host')).toBe('rule.2.host');
    expect(at('/http_rules/2/match/path_prefix')).toBe('rule.2.path');
    expect(at('/http_rules/2/match/method')).toBe('rule.2.method');
    expect(at('/http_rules/2/modify/request_headers/X-A')).toBe('rule.2.requestHeaders');
    expect(at('/http_rules/2/modify/response_headers/Server')).toBe('rule.2.responseHeaders');
    expect(at('/http_rules/2/modify/response_body_replace/0/search')).toBe('rule.2.replaces');
    expect(at('/http_rules/2/modify')).toBe('rule.2.modify');
    expect(at('/http_rules/2')).toBeUndefined();
    expect(at('/keylog')).toBeUndefined();
  });
});
