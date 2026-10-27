import { describe, expect, it } from 'vitest';
import type { DiagnosticResult } from '../api/generated/model';
import {
  describeTarget,
  diagnosticErrors,
  diagnosticFieldOf,
  emptyDiagnosticForm,
  fieldsOf,
  formatMs,
  formFromQuery,
  formToDiagnosticRequest,
  isValidHost,
  parseHttpUrl,
  pathLine,
  pathSteps,
  resultSentence,
  resultTone,
  type DiagnosticForm,
  type DiagnosticType,
} from './diagnostics';

const form = (type: DiagnosticType, over: Partial<DiagnosticForm> = {}): DiagnosticForm => ({ ...emptyDiagnosticForm(type), ...over });

describe('the host of a diagnostic', () => {
  it('is an IPv4 address or a host name', () => {
    for (const h of ['10.0.0.1', '203.0.113.10', 'broker', 'broker.example.com', 'a-b.c_d.example', 'host1.lab', '1abc.example']) expect(isValidHost(h), h).toBe(true);
  });

  it('is nothing with a port, a path, a wildcard, an IPv6 address or a malformed address', () => {
    for (const h of ['', '10.0.0.256', '1.2.3', '::1', '[::1]', 'a.b.', '-a.example', 'a..b', 'a b', 'a/b', '*.a.b', 'a:80', 'http://x', 'a'.repeat(254)]) expect(isValidHost(h), h).toBe(false);
  });
});

describe('the URL of an HTTP diagnostic', () => {
  it('is an http or https URL with a host and no credentials', () => {
    expect(parseHttpUrl('https://api.example.com:8443/health?x=1')?.hostname).toBe('api.example.com');
    expect(parseHttpUrl('http://10.0.0.1/')?.hostname).toBe('10.0.0.1');
    for (const u of ['ftp://x.test/', 'x.test', 'https://user:pw@x.test/', 'http:///path', 'http://[::1]/', 'http://x.test:99999/', '']) expect(parseHttpUrl(u), u).toBeUndefined();
  });
});

describe('the checks of the form', () => {
  it('has none for a valid form of each type', () => {
    expect(diagnosticErrors(form('path', { host: 'broker.example.com', port: '8883' }))).toEqual({});
    expect(diagnosticErrors(form('ping', { host: '10.0.0.1', count: '10', timeout: '2s' }))).toEqual({});
    expect(diagnosticErrors(form('tcp', { host: '10.0.0.1', port: '80', timeout: '500ms' }))).toEqual({});
    expect(diagnosticErrors(form('udp', { host: '10.0.0.1', port: '5683', payload: 'x' }))).toEqual({});
    expect(diagnosticErrors(form('dns', { host: 'broker.example.com' }))).toEqual({});
    expect(diagnosticErrors(form('dns', { host: 'broker.example.com', server: '10.0.0.1', port: '5353' }))).toEqual({});
    expect(diagnosticErrors(form('http', { url: 'https://api.example.com/health' }))).toEqual({});
    expect(diagnosticErrors(form('tls', { host: '10.0.0.1', sni: 'broker.example.com' }))).toEqual({});
  });

  it('asks for the host, the port where one is needed and the URL', () => {
    expect(diagnosticErrors(form('path'))).toMatchObject({ host: expect.any(String), port: expect.any(String) });
    expect(diagnosticErrors(form('tcp', { host: '10.0.0.1' }))).toEqual({ port: 'The port is needed.' });
    expect(diagnosticErrors(form('udp', { host: '10.0.0.1' }))).toEqual({ port: 'The port is needed.' });
    expect(diagnosticErrors(form('ping'))).toEqual({ host: 'The host is needed.' });
    expect(diagnosticErrors(form('dns'))).toEqual({ host: 'The name to resolve is needed.' });
    expect(diagnosticErrors(form('http'))).toEqual({ url: 'The URL is needed.' });
    expect(diagnosticErrors(form('tls', { host: '10.0.0.1' }))).toEqual({}); // 443
  });

  it('checks the port, the count, the timeout and the length of the whole run', () => {
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '0' })).port).toBeDefined();
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '65536' })).port).toBeDefined();
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '8o' })).port).toBeDefined();
    expect(diagnosticErrors(form('ping', { host: 'x', count: '0' })).count).toBeDefined();
    expect(diagnosticErrors(form('ping', { host: 'x', count: '101' })).count).toBeDefined();
    expect(diagnosticErrors(form('ping', { host: 'x', count: '2.5' })).count).toBeDefined();
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '1', timeout: '5' })).timeout).toBeDefined();
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '1', timeout: '1ms' })).timeout).toBe('At least 10ms.');
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '1', timeout: '61s' })).timeout).toBe('At most 60s.');
    expect(diagnosticErrors(form('tcp', { host: 'x', port: '1', timeout: '1m' })).timeout).toBeUndefined();
    // 99 gaps of 200 ms and 40 s are 59.8 s, 41 s is over the minute
    expect(diagnosticErrors(form('ping', { host: 'x', count: '100', timeout: '40s' }))).toEqual({});
    expect(diagnosticErrors(form('ping', { host: 'x', count: '100', timeout: '41s' })).count).toContain('60s');
  });

  it('leaves out what a type does not have', () => {
    // a stale port or URL of another type is not checked
    expect(diagnosticErrors(form('ping', { host: 'x', port: 'junk', url: 'junk' }))).toEqual({});
    expect(diagnosticErrors(form('http', { host: 'junk host', url: 'https://x.test/' }))).toEqual({});
  });
});

describe('the request of the form', () => {
  it('has only the fields of the type', () => {
    expect(formToDiagnosticRequest(form('path', { host: ' broker.example.com ', port: '8883' }))).toEqual({ type: 'path', host: 'broker.example.com', port: 8883 });
    expect(formToDiagnosticRequest(form('path', { host: '10.0.0.1', port: '1883', tls: false, sni: 'x.test' }))).toEqual({ type: 'path', host: '10.0.0.1', port: 1883, sni: 'x.test', tls: false });
    expect(formToDiagnosticRequest(form('ping', { host: '10.0.0.1', port: '80', count: '3', timeout: '2s', payload: 'x', sni: 'x.test' }))).toEqual({ type: 'ping', host: '10.0.0.1', count: 3, timeout: '2s' });
    expect(formToDiagnosticRequest(form('udp', { host: '10.0.0.1', port: '5683', payload: 'hello' }))).toEqual({ type: 'udp', host: '10.0.0.1', port: 5683, payload: 'hello' });
    expect(formToDiagnosticRequest(form('tls', { host: '10.0.0.1', port: '', sni: 'b.example.com' }))).toEqual({ type: 'tls', host: '10.0.0.1', sni: 'b.example.com' });
  });

  it('asks the proxy for a name, or the server for the name', () => {
    expect(formToDiagnosticRequest(form('dns', { host: 'broker.example.com', port: '5353' }))).toEqual({ type: 'dns', host: 'broker.example.com' });
    expect(formToDiagnosticRequest(form('dns', { host: 'broker.example.com', server: '10.0.0.1', port: '5353' }))).toEqual({ type: 'dns', host: '10.0.0.1', dns_name: 'broker.example.com', port: 5353 });
  });

  it('takes the host of an HTTP request from the URL', () => {
    expect(formToDiagnosticRequest(form('http', { host: 'ignored', url: ' https://api.example.com:8443/health ' }))).toEqual({
      type: 'http',
      host: 'api.example.com',
      url: 'https://api.example.com:8443/health',
    });
  });
});

describe('the form from the address', () => {
  it('takes a known type, a host and a port, and falls back to a path', () => {
    expect(formFromQuery({ type: 'ping', host: '10.10.0.42' })).toMatchObject({ type: 'ping', host: '10.10.0.42', port: '' });
    expect(formFromQuery({ type: 'tcp', host: 'a.test', port: '80' })).toMatchObject({ type: 'tcp', port: '80' });
    expect(formFromQuery({ type: 'traceroute', host: 'a.test' })).toMatchObject({ type: 'path', host: 'a.test' });
    expect(formFromQuery({})).toMatchObject({ type: 'path', host: '', port: '' });
    expect(formFromQuery({ host: ['a', 'b'] })).toMatchObject({ host: '' });
  });

  it('knows the fields each type shows', () => {
    expect(fieldsOf('tcp')).toMatchObject({ host: true, port: 'required', url: false });
    expect(fieldsOf('dns')).toMatchObject({ host: true, port: 'optional', server: true });
    expect(fieldsOf('http')).toMatchObject({ host: false, port: false, url: true });
    expect(fieldsOf('ping')).toMatchObject({ port: false, count: true });
  });
});

describe('the field a refusal names', () => {
  it('maps the paths of the request to the fields of the form', () => {
    expect(diagnosticFieldOf('/host')).toBe('host');
    expect(diagnosticFieldOf('/dns_name')).toBe('host');
    expect(diagnosticFieldOf('/port')).toBe('port');
    expect(diagnosticFieldOf('/timeout')).toBe('timeout');
    expect(diagnosticFieldOf('/url')).toBe('url');
    expect(diagnosticFieldOf('/type')).toBeUndefined();
    expect(diagnosticFieldOf(undefined)).toBeUndefined();
  });
});

const base = { from: 'gateway', ok: true, started_at: '2026-10-27T10:00:00Z', duration_ms: 5 };

describe('the result in words', () => {
  it('describes the target', () => {
    expect(describeTarget({ type: 'tcp', host: 'broker.example.com', port: 8883 })).toBe('broker.example.com:8883');
    expect(describeTarget({ type: 'ping', host: '10.0.0.1' })).toBe('10.0.0.1');
    expect(describeTarget({ type: 'http', host: 'x', url: 'https://x.test/a' })).toBe('https://x.test/a');
    expect(describeTarget({ type: 'dns', host: 'a.test' })).toBe('a.test @ the DNS proxy');
    expect(describeTarget({ type: 'dns', host: '10.0.0.1', dns_name: 'a.test', port: 5353 })).toBe('a.test @ 10.0.0.1:5353');
  });

  it('formats a time with the digits that mean something', () => {
    expect(formatMs(0.456)).toBe('0.46 ms');
    expect(formatMs(12.34)).toBe('12.3 ms');
    expect(formatMs(180.4)).toBe('180 ms');
    expect(formatMs(undefined)).toBe('—');
  });

  it('says succeeded or failed', () => {
    expect(resultTone({ ok: true })).toEqual({ tone: 'normal', label: 'Succeeded' });
    expect(resultTone({ ok: false })).toEqual({ tone: 'block', label: 'Failed' });
  });

  it('puts the error first when a diagnostic failed, and a sentence of the measurement when it did not', () => {
    expect(resultSentence({ ...base, type: 'tcp', ok: false, error: 'connection refused' })).toBe('connection refused');
    expect(resultSentence({ ...base, type: 'ping', ping: { sent: 10, received: 8, loss_percent: 20, rtt_avg_ms: 52.4 } })).toBe('8 of 10 replies, 20 % loss, average 52.4 ms');
    expect(resultSentence({ ...base, type: 'tcp', connect: { reachable: true, rtt_ms: 1.2 } })).toBe('Connected in 1.20 ms');
    expect(resultSentence({ ...base, type: 'udp', connect: { reachable: true, rtt_ms: 2, detail: 'reply of 4 bytes' } })).toBe('reply of 4 bytes in 2.00 ms');
    expect(resultSentence({ ...base, type: 'dns', dns: { rcode: 'NOERROR', answers: ['203.0.113.10', 'CNAME lb.example.com'], rtt_ms: 3 } })).toBe('203.0.113.10, CNAME lb.example.com · NOERROR in 3.00 ms');
    expect(resultSentence({ ...base, type: 'http', http: { status: 200, rtt_ms: 31 } })).toBe('HTTP 200 in 31.0 ms');
    expect(resultSentence({ ...base, type: 'tls', tls: { version: 'TLS 1.3', cipher: 'TLS_AES_128_GCM_SHA256', verified: true } })).toBe('TLS 1.3, TLS_AES_128_GCM_SHA256; the certificate verifies');
  });
});

describe('the steps of a path', () => {
  const ok = (step: 'dns' | 'route' | 'tcp' | 'tls', detail = ''): NonNullable<DiagnosticResult['path']>[number] => ({ step, ok: true, detail });

  it('lists the four, every one passed', () => {
    const r = { ok: true, path: [ok('dns'), ok('route'), ok('tcp'), ok('tls')] };
    expect(pathSteps(r).map((s) => s.state)).toEqual(['ok', 'ok', 'ok', 'ok']);
    expect(pathLine(r)).toBe('DNS ✓  Route ✓  TCP ✓  TLS ✓');
  });

  it('shows the example of the plan: the TLS step failed with the reason', () => {
    const r = { ok: false, path: [ok('dns'), ok('route'), ok('tcp'), { step: 'tls' as const, ok: false, detail: 'certificate has expired' }] };
    expect(pathLine(r)).toBe('DNS ✓  Route ✓  TCP ✓  TLS ✗ (certificate has expired)');
  });

  it('lists the steps after the failed one as not run', () => {
    const r = { ok: false, path: [{ step: 'dns' as const, ok: false, detail: 'NXDOMAIN' }] };
    expect(pathSteps(r).map((s) => s.state)).toEqual(['failed', 'not run', 'not run', 'not run']);
    expect(pathLine(r)).toBe('DNS ✗ (NXDOMAIN)  Route –  TCP –  TLS –');
  });

  it('leaves out the TLS step of a path that was asked without it', () => {
    const r = { ok: true, path: [ok('dns'), ok('route'), ok('tcp')] };
    expect(pathSteps(r).map((s) => s.step)).toEqual(['dns', 'route', 'tcp']);
    // but a path that failed at its third step has a TLS step that did not run
    expect(pathSteps({ ok: false, path: [ok('dns'), ok('route'), { step: 'tcp', ok: false }] }).map((s) => s.step)).toEqual(['dns', 'route', 'tcp', 'tls']);
  });
});
